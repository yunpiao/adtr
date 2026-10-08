import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import TaskWorkspace, { discardTaskIntent } from "./TaskWorkspace";
import { PermissionEditor } from "./access-common";
import {
  labels,
  marks,
  permissionInputs,
  type Permission,
  type PermissionInput,
} from "./access-api";
import { ApiError, type Profile } from "./api";
import {
  taskAPI,
  taskOperations,
  taskStates,
  terminal,
  type Task,
  type TaskState,
} from "./task-api";

let profile: Profile;
const kind = {
  taskName: "infrastructure.health",
  payloadVersion: 1,
  scope: "platform",
  maxAttempts: 5,
  timeoutSeconds: 30,
};
const task = (
  state: TaskState = "queued",
  id = "00000000-0000-4000-8000-000000000001",
): Task => ({
  taskUUID: id,
  taskName: kind.taskName,
  domainId: "platform",
  payloadVersion: 1,
  state,
  sourceState: state === "succeeded" ? "SUCCESS" : "PENDING",
  error: "",
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:01Z",
  attempt: 0,
  maxAttempts: 5,
  progress: 0,
  resultVersion: 0,
  result: null,
  cursor: null,
  parentTaskUUID: "",
  terminalAt: null,
  archived: false,
  visibilityVersion: 0,
});
const list = (tasks = [task()]) => ({
  page: { pageIdx: 1, pageSize: 20, total: tasks.length, totalPage: 1 },
  tasks,
  exhausted: true,
});
const metadata = (): Permission[] =>
  marks.map((mark) => ({
    mark,
    name: labels[mark],
    auth: { readable: true, writeable: true },
    allow_auth: { readable: true, writeable: true },
    paths: [],
    children: [],
    checked: true,
    icon: mark,
  }));
const response = (body: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response);
let override: (url: string, init: RequestInit) => Promise<Response> | undefined;
let fetcher: ReturnType<typeof vi.fn>;
let counter = 0;
beforeEach(() => {
  discardTaskIntent({ ID: 1, username: "admin" });
  sessionStorage.clear();
  profile = {
    ID: 1,
    username: "admin",
    role: "platform_admin",
    priv: 1,
    mobile: "",
    email: "",
    remark: "",
    passStrength: "high",
    hasMfa: true,
    needChangePwd: false,
    isExpired: false,
    passwordNotUpdatedDays: 0,
    pwdUpdateTm: "2026-10-01T00:00:00Z",
    csrfToken: `task-test-${counter++}`,
  };
  window.history.replaceState({}, "", "#account");
  override = () => undefined;
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const custom = override(url, init);
    if (custom) return custom;
    const path = url.split("?")[0];
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/menu") return response({ menu: metadata() });
    if (path === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map(() => true),
      });
    if (path === "/api/tasks") return response(list());
    if (path === "/api/tasks/kinds") return response({ kinds: [kind] });
    if (path === "/api/tasks/detail")
      return response({ task: task(), events: [] });
    if (path === "/api/tasks/submit")
      return response({ task: task(), replayed: false });
    if (path === "/api/tasks/cancel")
      return response({ task: task("cancel_requested") });
    throw new Error(`Unexpected request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => vi.useRealTimers());
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
const proof = (code = "123456") => {
  fill("操作者当前密码", "Synthetic Actor Password 123");
  fill("未使用的认证器验证码", code);
};
const calls = (path: string) =>
  fetcher.mock.calls.filter(
    ([url]) => url.split("?")[0] === `/api/tasks${path}`,
  );
async function start() {
  render(<TaskWorkspace profile={profile} sessionChanged={vi.fn()} />);
  await screen.findByRole("table", { name: "任务列表" });
}
async function detail() {
  await start();
  click(`详情 ${task().taskUUID}`);
  await screen.findByRole("table", { name: "持久化任务事件" });
}

describe("task transport", () => {
  it("keeps canonical local states distinct from legacy source labels", () => {
    expect(taskStates.filter(terminal)).toEqual([
      "succeeded",
      "failed",
      "partial_failed",
      "dead_letter",
      "cancelled",
    ]);
    expect(terminal("retry_wait")).toBe(false);
    expect(terminal("cancel_requested")).toBe(false);
  });
  it("uses cookie/CSRF contract, no actor identity or credential storage", async () => {
    const signal = new AbortController().signal;
    await taskAPI.submit(
      {
        taskName: kind.taskName,
        domainId: "platform",
        payloadVersion: 1,
        payload: {},
        idempotencyKey: "stable-key",
      },
      { actorPassword: "Synthetic Password 123", totpCode: "123456" },
      profile.csrfToken,
      signal,
    );
    const init = calls("/submit")[0][1];
    expect(init).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      signal,
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": profile.csrfToken,
      },
    });
    expect(init.headers).not.toHaveProperty("Origin");
    expect(JSON.parse(init.body as string)).not.toHaveProperty("actorId");
    expect(localStorage.length + sessionStorage.length).toBe(0);
  });
  it("rejects malformed state/progress, missing event fields, and task identity mismatch", async () => {
    for (const invalid of [
      { ...task(), state: "SUCCESS" },
      { ...task(), progress: 101 },
      { ...task(), taskUUID: "other" },
    ]) {
      override = () => response({ task: invalid, events: [] });
      await expect(
        taskAPI.detail(task().taskUUID, new AbortController().signal),
      ).rejects.toMatchObject({ code: "invalid_response" });
    }
    override = () =>
      response({ task: task(), events: [{ action: "submitted" }] });
    await expect(
      taskAPI.detail(task().taskUUID, new AbortController().signal),
    ).rejects.toBeInstanceOf(ApiError);
  });
  it("rejects out of contract pagination, unknown responses, and wrong recovery parent", async () => {
    override = () =>
      response({ ...list(), page: { ...list().page, pageSize: -1 } });
    await expect(
      taskAPI.list("", new AbortController().signal),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () => response({ task: task(), replayed: false });
    await expect(
      taskAPI.recover(
        task().taskUUID,
        "key",
        { actorPassword: "Password 1234", totpCode: "123456" },
        "csrf",
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () => response({ error: "forbidden" }, 403);
    await expect(
      taskAPI.kinds(new AbortController().signal),
    ).rejects.toMatchObject({ code: "forbidden", status: 403 });
  });
});
describe("task workspace", () => {
  it("requires server menu AND checks, and never derives privilege from platform_admin", async () => {
    override = (url) =>
      url === "/api/access/check"
        ? response({ results: taskOperations.map(() => false) })
        : undefined;
    render(<TaskWorkspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText("当前账户没有后台任务读取权限。");
    expect(calls("")).toHaveLength(0);
    expect(
      screen.queryByRole("button", { name: "提交健康检查" }),
    ).not.toBeInTheDocument();
  });
  it("uses the task nav and clears the page after navigation and browser Back", async () => {
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    click("后台任务");
    await screen.findByRole("table", { name: "任务列表" });
    expect(screen.getByText(/域连接检测请使用域连接页面/)).toBeVisible();
    expect(
      JSON.parse(
        fetcher.mock.calls.find(([url]) => url === "/api/access/check")![1]
          .body as string,
      ).paths,
    ).toEqual(taskOperations);
    click("账户概览");
    await screen.findByRole("heading", { name: "账户概览" });
    act(() => window.dispatchEvent(new PopStateEvent("popstate")));
    await screen.findByRole("heading", { name: "账户概览" });
    expect(
      screen.queryByRole("table", { name: "任务列表" }),
    ).not.toBeInTheDocument();
  });
  it("filters with bounded page sizes, resets to page one and paginates actual metadata", async () => {
    override = (url) =>
      url.startsWith("/api/tasks?")
        ? response({
            ...list([task("succeeded")]),
            page: {
              pageIdx: Number(
                new URLSearchParams(url.split("?")[1]).get("pageIdx"),
              ),
              pageSize: 10,
              total: 11,
              totalPage: 2,
            },
            exhausted: url.includes("pageIdx=2"),
          })
        : undefined;
    await start();
    fill("任务种类筛选", "infrastructure.health");
    fill("域 ID 筛选", "platform");
    fill("任务状态筛选", "succeeded");
    fill("每页任务数", "10");
    click("筛选任务");
    await waitFor(() =>
      expect(calls("").at(-1)![0]).toContain("state=succeeded"),
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "下一页" })).toBeEnabled(),
    );
    click("下一页");
    await waitFor(() => expect(calls("").at(-1)![0]).toContain("pageIdx=2"));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled(),
    );
    click("清除任务筛选");
    await waitFor(() =>
      expect(calls("").at(-1)![0]).toBe("/api/tasks?pageIdx=1&pageSize=20"),
    );
    expect(
      screen.queryByRole("option", { name: /全部/ }),
    ).not.toBeInTheDocument();
  });
  it("renders actual result and cancellation race, then stops polling at terminal", async () => {
    let state: TaskState = "cancel_requested";
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response({
            task: {
              ...task(state),
              progress: state === "succeeded" ? 100 : 50,
              result: { database: "ok", queue: "ok" },
            },
            events: [
              {
                id: 1,
                action: "completed-with-cancel-race",
                state: "succeeded",
                attempt: 1,
                resultVersion: 1,
                createdAt: task().createdAt,
              },
            ],
          })
        : undefined;
    await detail();
    expect(screen.getByText(/执行器尚未确认停止/)).toBeVisible();
    expect(
      screen.getByText("取消与完成发生竞争，终态反映实际结果"),
    ).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "请求取消任务" }),
    ).not.toBeInTheDocument();
    state = "succeeded";
    await waitFor(
      () => expect(screen.getByLabelText("任务持久化进度")).toHaveValue(100),
      { timeout: 2500 },
    );
    const count = calls("/detail").length;
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 1200));
    });
    expect(calls("/detail")).toHaveLength(count);
    expect(screen.getByLabelText("任务实际结果")).toHaveTextContent(
      '"database": "ok"',
    );
  });
  it("shows retry and partial failure truthfully and does not offer whole-task partial replay", async () => {
    let state: TaskState = "retry_wait";
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response({
            task: { ...task(state), nextAttemptAt: "2026-10-07T00:00:10Z" },
            events: [],
          })
        : undefined;
    await detail();
    expect(screen.getByText(/尚未成功/)).toBeVisible();
    state = "partial_failed";
    click("刷新任务详情");
    await screen.findByText(/不能整体自动重放/);
    expect(
      screen.queryByRole("button", { name: "创建恢复任务" }),
    ).not.toBeInTheDocument();
  });
  it("prevents repeated mutation clicks and ignores late success after stopping wait", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url === "/api/tasks/submit"
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    await start();
    click("提交健康检查");
    await screen.findByLabelText("提交幂等键");
    const key = (screen.getByLabelText("提交幂等键") as HTMLInputElement).value;
    proof();
    click("确认提交健康检查");
    fireEvent.submit(screen.getByLabelText("操作者当前密码").closest("form")!);
    expect(calls("/submit")).toHaveLength(1);
    click("取消");
    await screen.findByRole("table", { name: "任务列表" });
    await act(async () =>
      resolve(await response({ task: task("succeeded"), replayed: false })),
    );
    expect(
      screen.queryByRole("table", { name: "持久化任务事件" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交健康检查" })).toBeDisabled();
    click("继续核对未确认的提交");
    await screen.findByLabelText("提交幂等键");
    expect(screen.getByLabelText("提交幂等键")).toHaveValue(key);
    expect(await screen.findByLabelText("操作者当前密码")).toHaveValue("");
  });
  it("retains one idempotency key across network failure, refetch, and fresh-proof replay", async () => {
    let submissions = 0;
    override = (url) =>
      url === "/api/tasks/submit"
        ? ++submissions === 1
          ? Promise.reject(new TypeError("offline"))
          : response({ task: task("succeeded"), replayed: true })
        : undefined;
    await start();
    click("提交健康检查");
    await screen.findByLabelText("提交幂等键");
    proof();
    click("确认提交健康检查");
    await screen.findByRole("alert");
    await screen.findByText(/已重新读取最近/);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    proof("654321");
    click("使用原幂等键确认提交");
    await screen.findByText(/服务器确认幂等重放/);
    const bodies = calls("/submit").map(([, init]) =>
      JSON.parse(init.body as string),
    );
    expect(bodies).toHaveLength(2);
    expect(bodies[0].idempotencyKey).toBe(bodies[1].idempotencyKey);
    expect(bodies[1].totpCode).toBe("654321");
    expect(localStorage.length + sessionStorage.length).toBe(0);
  });
  it("preserves an uncertain key after leaving and reopening the entire workspace", async () => {
    override = (url) =>
      url === "/api/tasks/submit" ? new Promise(() => {}) : undefined;
    const mounted = render(
      <TaskWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByRole("table", { name: "任务列表" });
    click("提交健康检查");
    await screen.findByLabelText("提交幂等键");
    const key = (screen.getByLabelText("提交幂等键") as HTMLInputElement).value;
    proof();
    click("确认提交健康检查");
    mounted.unmount();
    await start();
    click("继续核对未确认的提交");
    await screen.findByLabelText("提交幂等键");
    expect(screen.getByLabelText("提交幂等键")).toHaveValue(key);
  });
  it("refuses an unregistered or unsupported task payload editor", async () => {
    override = (url) =>
      url === "/api/tasks/kinds"
        ? response({
            kinds: [{ ...kind, taskName: "ad.write", scope: "domain" }],
          })
        : undefined;
    await start();
    click("提交健康检查");
    await screen.findByText(/服务器未登记受支持/);
    expect(calls("/submit")).toHaveLength(0);
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
  });
  it("confirms cancellation and refetches a real cancel_requested state without claiming cancelled", async () => {
    let state: TaskState = "running";
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response({ task: task(state), events: [] })
        : url === "/api/tasks/cancel"
          ? ((state = "cancel_requested"), response({ task: task(state) }))
          : undefined;
    await detail();
    click("请求取消任务");
    proof();
    click("确认请求取消");
    await screen.findByText(/服务器返回：已请求取消，待确认/);
    // Wait for the independently refetched detail warning, not the mutation
    // notice, which also truthfully says the executor has not stopped yet.
    expect(
      await screen.findByText(
        "取消请求已登记，执行器尚未确认停止。取消可能与完成竞争，请等待真实终态。",
        { exact: true },
      ),
    ).toBeVisible();
    expect(calls("/detail").length).toBeGreaterThanOrEqual(2);
    expect(calls("/cancel")).toHaveLength(1);
  });
  it("creates a separate recovery task, retains original parent, and acknowledges idempotent replay", async () => {
    const recovered = {
      ...task("queued", "00000000-0000-4000-8000-000000000002"),
      parentTaskUUID: task().taskUUID,
    };
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response({
            task: url.includes(recovered.taskUUID)
              ? recovered
              : task("dead_letter"),
            events: [],
          })
        : url === "/api/tasks/recover"
          ? response({ task: recovered, replayed: true })
          : undefined;
    await detail();
    click("创建恢复任务");
    await screen.findByLabelText("恢复幂等键");
    proof();
    click("确认恢复任务");
    await screen.findByText(/已找到同一个恢复任务/);
    await waitFor(() =>
      expect(screen.getByText(recovered.taskUUID)).toBeVisible(),
    );
    expect(screen.getByText(task().taskUUID)).toBeVisible();
  });
  it("clears detail after permission revocation", async () => {
    const changed = vi.fn();
    let denied = false;
    override = (url) =>
      url.startsWith("/api/tasks/detail") && denied
        ? response({ error: "forbidden" }, 403)
        : undefined;
    render(<TaskWorkspace profile={profile} sessionChanged={changed} />);
    await screen.findByRole("table", { name: "任务列表" });
    click(`详情 ${task().taskUUID}`);
    await screen.findByRole("table", { name: "持久化任务事件" });
    denied = true;
    click("刷新任务详情");
    await screen.findByRole("alert");
    expect(screen.queryByLabelText("任务实际结果")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "请求取消任务" }),
    ).not.toBeInTheDocument();
  });
});
describe("task interruption persistence", () => {
  it("restores a non-secret pending key after reload and clears it only on explicit discard", async () => {
    const key = "10000000-0000-4000-8000-000000000001";
    sessionStorage.setItem(
      "adtr.pending-task",
      JSON.stringify({
        owner: profile.ID,
        username: profile.username,
        key,
        action: "submit",
      }),
    );
    await start();
    click("继续核对未确认的提交");
    await screen.findByLabelText("提交幂等键");
    expect(screen.getByLabelText("提交幂等键")).toHaveValue(key);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(
      sessionStorage.getItem(
        `adtr.pending-task:${profile.ID}:${encodeURIComponent(profile.username)}`,
      ),
    ).not.toContain(profile.csrfToken);
    expect(
      sessionStorage.getItem(
        `adtr.pending-task:${profile.ID}:${encodeURIComponent(profile.username)}`,
      ),
    ).not.toContain("actorPassword");
    discardTaskIntent();
    expect(sessionStorage.length).toBe(0);
  });
});
describe("expanded permission catalogue", () => {
  it("preserves task and server-supplied grants when changing a management checkbox", () => {
    const value = permissionInputs(metadata());
    const unknown = {
      mark: "future-feature",
      auth: { readable: true, writeable: false },
    } as PermissionInput;
    const onChange = vi.fn();
    render(
      <PermissionEditor
        value={[...value, unknown]}
        metadata={metadata()}
        onChange={onChange}
      />,
    );
    fireEvent.click(screen.getByRole("checkbox", { name: "用户管理：写入" }));
    const updated = onChange.mock.calls[0][0] as PermissionInput[];
    expect(updated.find((p) => p.mark === "tasks")).toEqual(
      value.find((p) => p.mark === "tasks"),
    );
    expect(updated).toContainEqual(unknown);
  });
});

describe("audit export tasks use their dedicated creation route", () => {
  it("does not expose generic recovery for a failed audit export", async () => {
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response({
            task: {
              ...task("dead_letter"),
              taskName: "audit.export",
              result: {},
              cursor: {},
            },
            events: [],
          })
        : undefined;
    await detail();
    expect(
      screen.queryByRole("button", { name: "创建恢复任务" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/审计导出文件请在「操作审计」/),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("任务实际结果")).toHaveTextContent("{}");
  });
  it("clears an obsolete saved recovery intent once its audit kind is confirmed", async () => {
    sessionStorage.setItem(
      "adtr.pending-task",
      JSON.stringify({
        owner: profile.ID,
        username: profile.username,
        action: "recover",
        taskUUID: task().taskUUID,
        key: "00000000-0000-4000-8000-000000000039",
      }),
    );
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response({
            task: {
              ...task("failed"),
              taskName: "audit.export",
              result: {},
              cursor: {},
            },
            events: [],
          })
        : undefined;
    await start();
    click("继续核对未确认的提交");
    await screen.findByText(/审计导出文件请在「操作审计」/);
    expect(
      screen.queryByRole("button", { name: "确认恢复任务" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("恢复幂等键")).not.toBeInTheDocument();
    expect(
      sessionStorage.getItem(
        `adtr.pending-task:${profile.ID}:${encodeURIComponent(profile.username)}`,
      ),
    ).toBeNull();
    expect(calls("/recover")).toHaveLength(0);
    click("返回任务列表");
    await screen.findByRole("table", { name: "任务列表" });
    expect(screen.getByRole("button", { name: "提交健康检查" })).toBeEnabled();
  });
  it("retains explicit cancellation for a running audit export", async () => {
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response({
            task: {
              ...task("running"),
              taskName: "audit.export",
              result: {},
              cursor: {},
            },
            events: [],
          })
        : undefined;
    await detail();
    expect(screen.getByRole("button", { name: "请求取消任务" })).toBeEnabled();
  });
});

describe("dedicated domain connection task guard", () => {
  it.each(["domain.connection_test", "domain.account_connection_test"])(
    "hides generic recovery and all raw results/cursors for %s",
    async (kind) => {
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response({
              task: {
                ...task("failed"),
                taskName: kind,
                domainId: "synthetic-domain-id",
                maxAttempts: 1,
                result: { unsafe: "DO_NOT_RENDER" },
                cursor: { unsafe: "DO_NOT_RENDER_CURSOR" },
              },
              events: [],
            })
          : undefined;
      await detail();
      expect(
        screen.getByText(/域连接诊断请在「域连接」页面查看/),
      ).toBeVisible();
      expect(screen.queryByRole("button", { name: "创建恢复任务" })).toBeNull();
      expect(screen.queryByText(/DO_NOT_RENDER/)).toBeNull();
      expect(calls("/recover")).toHaveLength(0);
    },
  );
  it.each(["domain.connection_test", "domain.account_connection_test"])(
    "clears a stale generic recovery intent for %s",
    async (kind) => {
      sessionStorage.setItem(
        "adtr.pending-task",
        JSON.stringify({
          owner: profile.ID,
          username: profile.username,
          action: "recover",
          taskUUID: task().taskUUID,
          key: "00000000-0000-4000-8000-000000000039",
        }),
      );
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response({
              task: {
                ...task("failed"),
                taskName: kind,
                domainId: "synthetic-domain-id",
                maxAttempts: 1,
              },
              events: [],
            })
          : undefined;
      await start();
      click("继续核对未确认的提交");
      await screen.findByText(/域连接诊断请在「域连接」页面查看/);
      expect(screen.queryByRole("button", { name: "确认恢复任务" })).toBeNull();
      expect(
        sessionStorage.getItem(
          `adtr.pending-task:${profile.ID}:${encodeURIComponent(profile.username)}`,
        ),
      ).toBeNull();
      expect(calls("/recover")).toHaveLength(0);
    },
  );
});

describe("dedicated operational bundle guard", () => {
  it.each(["running", "failed"] as const)(
    "hides generic mutation and private results for %s bundles",
    async (state) => {
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response({
              task: {
                ...task(state),
                taskName: "system.logs_bundle",
                result: { unsafe: "PRIVATE_BUNDLE_RESULT" },
                cursor: { unsafe: "PRIVATE_BUNDLE_CURSOR" },
              },
              events: [],
            })
          : undefined;
      await detail();
      expect(screen.getByText(/运行日志诊断包的结果和文件请在/)).toBeVisible();
      expect(screen.queryByRole("button", { name: "请求取消任务" })).toBeNull();
      expect(screen.queryByRole("button", { name: "创建恢复任务" })).toBeNull();
      expect(screen.queryByText(/PRIVATE_BUNDLE/)).toBeNull();
      expect(calls("/cancel")).toHaveLength(0);
      expect(calls("/recover")).toHaveLength(0);
    },
  );
});
