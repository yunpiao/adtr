import { randomBytes, randomInt } from "node:crypto";
import { revealNavigation } from "./test-navigation";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
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
  isAssetExportTask,
  isDirectoryTask,
  requiresDedicatedTaskControls,
  requiresDedicatedTaskRecovery,
  taskAPI,
  taskError,
  taskOperations,
  taskStates,
  terminal,
  type Task,
  type TaskState,
} from "./task-api";
import {
  clearExportRecovery as clearUserExportRecovery,
  exportRecoveryStorage as userExportRecoveryStorage,
  writeExportRecovery as writeUserExportRecovery,
} from "./user-asset-export-v3-recovery";
import {
  clearExportRecovery as clearGroupExportRecovery,
  exportRecoveryStorage as groupExportRecoveryStorage,
  writeExportRecovery as writeGroupExportRecovery,
} from "./group-asset-export-v3-recovery";
import {
  clearExportRecovery as clearComputerExportRecovery,
  exportRecoveryStorage as computerExportRecoveryStorage,
  writeExportRecovery as writeComputerExportRecovery,
} from "./computer-asset-export-v3-recovery";

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
// Ephemeral inputs for fetch-mocked DOM tests, never real account credentials.
let mockPassword: string;
let mockCode: string;
let replayCode: string;
beforeEach(() => {
  mockPassword = randomBytes(24).toString("base64url");
  const code = randomInt(1_000_000);
  mockCode = code.toString().padStart(6, "0");
  replayCode = ((code + 1) % 1_000_000).toString().padStart(6, "0");
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
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
const proof = (code = mockCode) => {
  fill("操作者当前密码", mockPassword);
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
  await screen.findByRole("list", { name: "持久化任务事件" });
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
      { actorPassword: mockPassword, totpCode: mockCode },
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
        { actorPassword: mockPassword, totpCode: mockCode },
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
    await revealNavigation("后台任务");
    click("后台任务");
    await screen.findByRole("table", { name: "任务列表" });
    fireEvent.click(screen.getByText("任务范围与结果说明"));
    expect(screen.getByText(/域连接检测请使用域连接页面/)).toBeVisible();
    expect(
      JSON.parse(
        fetcher.mock.calls.find(([url]) => url === "/api/access/check")![1]
          .body as string,
      ).paths,
    ).toEqual(taskOperations);
    await revealNavigation("账户概览");
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
      screen.queryByRole("list", { name: "持久化任务事件" }),
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
    proof(replayCode);
    click("使用原幂等键确认提交");
    await screen.findByText(/服务器确认幂等重放/);
    const bodies = calls("/submit").map(([, init]) =>
      JSON.parse(init.body as string),
    );
    expect(bodies).toHaveLength(2);
    expect(bodies[0].idempotencyKey).toBe(bodies[1].idempotencyKey);
    expect(bodies[0].totpCode).toBe(mockCode);
    expect(bodies[1].totpCode).toBe(replayCode);
    expect(replayCode).not.toBe(mockCode);
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
    await screen.findByRole("list", { name: "持久化任务事件" });
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
  it.each(taskStates)(
    "links a loaded exact audit.export task in %s to its original detail without creating or downloading",
    async (state) => {
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response({
              task: { ...task(state), taskName: "audit.export" },
              events: [],
            })
          : undefined;
      await detail();
      expect(
        screen.getByRole("link", { name: "查看原审计导出" }),
      ).toHaveAttribute("href", `#audit/detail/${task().taskUUID}`);
      expect(
        screen.getByText(/能否下载以原页面的当前授权和文件校验为准/),
      ).toBeVisible();
      expect(
        screen.getByText(/失败后通过该页面重新创建导出，生成新的受保护控制记录/),
      ).toBeVisible();
      expect(
        screen.queryByRole("button", { name: "下载 XLSX" }),
      ).not.toBeInTheDocument();
      expect(
        fetcher.mock.calls.filter(
          ([url, init]) =>
            (init.method === "POST" && url !== "/api/access/check") ||
            url.startsWith("/api/audit/"),
        ),
      ).toHaveLength(0);
    },
  );

  it.each(["infrastructure.health", "audit.export.other", "Audit.export"])(
    "does not link a non-audit kind %s",
    async (taskName) => {
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response({ task: { ...task("succeeded"), taskName }, events: [] })
          : undefined;
      await detail();
      expect(
        screen.queryByRole("link", { name: "查看原审计导出" }),
      ).not.toBeInTheDocument();
    },
  );

  it.each([
    "not-a-uuid",
    "../audit/export",
    "00000000-0000-4000-8000-000000000001?download=1",
    "00000000-0000-4000-8000-000000000002",
  ])("does not link a malformed or mismatched detail ID %s", async (id) => {
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response({
            task: { ...task("succeeded", id), taskName: "audit.export" },
            events: [],
          })
        : undefined;
    await start();
    click(`详情 ${task().taskUUID}`);
    await screen.findByRole("alert");
    expect(
      screen.queryByRole("link", { name: "查看原审计导出" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByTestId("task-detail-identity")).toBeNull();
  });

  it("removes the original link while refreshing and after a denied detail read", async () => {
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response({
            task: { ...task("succeeded"), taskName: "audit.export" },
            events: [],
          })
        : undefined;
    await detail();
    expect(screen.getByRole("link", { name: "查看原审计导出" })).toBeVisible();
    let release!: (value: Response) => void;
    const held = new Promise<Response>((resolve) => {
      release = resolve;
    });
    override = (url) =>
      url.startsWith("/api/tasks/detail") ? held : undefined;
    click("刷新任务详情");
    await screen.findByText("正在读取任务详情…");
    expect(
      screen.queryByRole("link", { name: "查看原审计导出" }),
    ).not.toBeInTheDocument();
    await act(async () =>
      release(await response({ error: "forbidden" }, 403)),
    );
    await screen.findByRole("alert");
    expect(
      screen.queryByRole("link", { name: "查看原审计导出" }),
    ).not.toBeInTheDocument();
  });

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
    // The warning is rendered before TaskDetails' passive effect reconciles
    // the saved intent. Wait for that cleanup itself, not only its warning.
    await waitFor(() =>
      expect(
        sessionStorage.getItem(
          `adtr.pending-task:${profile.ID}:${encodeURIComponent(profile.username)}`,
        ),
      ).toBeNull(),
    );
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
      // Clearing a stale intent is a post-render effect; wait for that effect,
      // not merely the detail text that can appear before it under worker load.
      await waitFor(() =>
        expect(
          sessionStorage.getItem(
            `adtr.pending-task:${profile.ID}:${encodeURIComponent(profile.username)}`,
          ),
        ).toBeNull(),
      );
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

// Synthetic entry/lifecycle contracts only. The original producer, dedicated
// authorization, cross-page pending record and ZIP are covered by log suites.
describe("original operational bundle task navigation", () => {
  const linkName = "查看原运行日志诊断包";
  const firstID = task().taskUUID;
  const secondID = "00000000-0000-4000-8000-000000000002";
  const bundleTask = (state: TaskState = "succeeded", id = firstID): Task => ({
    ...task(state, id),
    taskName: "system.logs_bundle",
  });
  const bundleDetail = (current = bundleTask()) => ({
    task: current,
    events: [],
  });
  function deferredResponse() {
    let resolve!: (value: Response) => void;
    const promise = new Promise<Response>((done) => {
      resolve = done;
    });
    return { promise, resolve };
  }
  function expectNoLink() {
    expect(screen.queryByRole("link", { name: linkName })).toBeNull();
  }
  function expectReadOnlyEntry() {
    expect(
      fetcher.mock.calls.filter(
        ([url, init]) =>
          url.startsWith("/api/system/logs") ||
          (init.method === "POST" && url !== "/api/access/check"),
      ),
    ).toHaveLength(0);
  }
  function expectNoBundleAction() {
    for (const name of [
      "请求取消任务",
      "创建恢复任务",
      "确认请求取消",
      "确认恢复任务",
      "下载 ZIP",
    ])
      expect(screen.queryByRole("button", { name })).toBeNull();
    expect(screen.queryByRole("link", { name: "下载 ZIP" })).toBeNull();
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    expect(screen.queryByLabelText("未使用的认证器验证码")).toBeNull();
    expectReadOnlyEntry();
  }

  it.each(taskStates)(
    "links only the original active %s task without fetching logs or offering generic mutations",
    async (state) => {
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response(bundleDetail(bundleTask(state)))
          : undefined;
      await detail();
      expect(screen.getAllByRole("link", { name: linkName })).toHaveLength(1);
      expect(screen.getByRole("link", { name: linkName })).toHaveAttribute(
        "href",
        `#operational-logs/detail/${firstID}`,
      );
      expect(
        screen.getByText(
          /此入口仅核对原任务对应的诊断包；能否查看或下载，以原页面的当前授权和文件校验为准。/,
        ),
      ).toBeVisible();
      expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(
        firstID,
      );
      expect(
        fetcher.mock.calls
          .filter(([url]) => url === "/api/access/check")
          .map(([, init]) => JSON.parse(init.body as string).paths),
      ).toEqual([taskOperations]);
      expectNoBundleAction();
    },
  );

  it.each([
    ["different kind", { taskName: "infrastructure.health" }],
    ["near kind", { taskName: "system.logs_bundle.other" }],
    ["case changed kind", { taskName: "System.logs_bundle" }],
    ["different domain", { domainId: "synthetic-domain-id" }],
    ["case changed domain", { domainId: "Platform" }],
    ["archived task", { archived: true }],
  ] as const)("does not link a %s", async (_label, change) => {
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response(bundleDetail({ ...bundleTask(), ...change }))
        : undefined;
    await detail();
    expectNoLink();
    expectReadOnlyEntry();
  });

  it.each([
    "not-a-uuid",
    "../operational-logs/detail",
    `${firstID}?download=1`,
    `${firstID}/download`,
    secondID,
  ])("rejects a malformed or mismatched detail ID %s", async (id) => {
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response(bundleDetail(bundleTask("succeeded", id)))
        : undefined;
    await start();
    click(`详情 ${firstID}`);
    await screen.findByRole("alert");
    expectNoLink();
    expect(screen.queryByTestId("task-detail-identity")).toBeNull();
    expectNoBundleAction();
  });

  it("does not derive a link from the route while the first detail is pending or lacks a task", async () => {
    const held = deferredResponse();
    override = (url) =>
      url.startsWith("/api/tasks/detail") ? held.promise : undefined;
    await start();
    click(`详情 ${firstID}`);
    await screen.findByText("正在读取任务详情…");
    expectNoLink();
    expectNoBundleAction();
    await act(async () =>
      held.resolve(await response({ task: null, events: [] })),
    );
    await screen.findByRole("alert");
    expectNoLink();
    expectNoBundleAction();
  });

  it.each([
    [403, "forbidden"],
    [500, "internal"],
  ] as const)(
    "hides the old link throughout refresh and %s failure, then restores only a successful current read",
    async (status, error) => {
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response(bundleDetail())
          : undefined;
      await detail();
      expect(screen.getByRole("link", { name: linkName })).toBeVisible();
      const held = deferredResponse();
      override = (url) =>
        url.startsWith("/api/tasks/detail") ? held.promise : undefined;
      click("刷新任务详情");
      await screen.findByText("正在读取任务详情…");
      expectNoLink();
      await act(async () => held.resolve(await response({ error }, status)));
      await screen.findByRole("alert");
      expectNoLink();
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response(bundleDetail())
          : undefined;
      click("刷新任务详情");
      expect(
        await screen.findByRole("link", { name: linkName }),
      ).toHaveAttribute(
        "href",
        `#operational-logs/detail/${firstID}`,
      );
      expectNoBundleAction();
    },
  );

  it.each([
    "unauthenticated",
    "password_change_required",
    "credential_use_identity_changed",
    "domain_source_identity_changed",
  ])(
    "clears the loaded entry when the current read reports %s",
    async (error) => {
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response(bundleDetail())
          : undefined;
      const sessionChanged = vi.fn();
      render(
        <TaskWorkspace profile={profile} sessionChanged={sessionChanged} />,
      );
      await screen.findByRole("table", { name: "任务列表" });
      click(`详情 ${firstID}`);
      await screen.findByRole("link", { name: linkName });
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response({ error }, 401)
          : undefined;
      click("刷新任务详情");
      await waitFor(() => expect(sessionChanged).toHaveBeenCalledTimes(1));
      expectNoLink();
      expect(screen.queryByTestId("task-detail-identity")).toBeNull();
      expectNoBundleAction();
    },
  );

  it.each(["/api/access/menu", "/api/access/check"])(
    "waits for %s before reading or exposing a task entry",
    async (path) => {
      const held = deferredResponse();
      override = (url) =>
        url === path
          ? held.promise
          : url.startsWith("/api/tasks/detail")
            ? response(bundleDetail())
            : undefined;
      render(<TaskWorkspace profile={profile} sessionChanged={vi.fn()} />);
      await screen.findByText("正在确认任务权限…");
      expectNoLink();
      expect(calls("")).toHaveLength(0);
      expect(calls("/detail")).toHaveLength(0);
      expectReadOnlyEntry();
      await act(async () =>
        held.resolve(
          await response(
            path.endsWith("/menu")
              ? { menu: metadata() }
              : { results: taskOperations.map(() => true) },
          ),
        ),
      );
      await screen.findByRole("table", { name: "任务列表" });
      click(`详情 ${firstID}`);
      await screen.findByRole("link", { name: linkName });
      expectNoBundleAction();
    },
  );

  it.each([
    ["non-array menu", "/api/access/menu", { menu: {} }],
    ["non-array checks", "/api/access/check", { results: {} }],
    ["short checks", "/api/access/check", { results: [true] }],
    [
      "non-boolean check",
      "/api/access/check",
      { results: taskOperations.map((_, index) => (index === 0 ? 1 : true)) },
    ],
  ] as const)(
    "rejects %s without exposing the entry",
    async (_label, path, body) => {
      override = (url) => (url === path ? response(body) : undefined);
      render(<TaskWorkspace profile={profile} sessionChanged={vi.fn()} />);
      await screen.findByRole("alert");
      expect(calls("")).toHaveLength(0);
      expect(calls("/detail")).toHaveLength(0);
      expectNoLink();
      expectReadOnlyEntry();
    },
  );

  it.each(["menu", "GET /api/tasks", "GET /api/tasks/detail"])(
    "does not expose the entry when %s alone denies task access",
    async (denied) => {
      override = (url) => {
        if (url === "/api/access/menu")
          return response({
            menu: metadata().map((node) =>
              denied === "menu" && node.mark === "tasks"
                ? { ...node, auth: { ...node.auth, readable: false } }
                : node,
            ),
          });
        if (url === "/api/access/check")
          return response({
            results: taskOperations.map((operation) => operation !== denied),
          });
        return undefined;
      };
      render(<TaskWorkspace profile={profile} sessionChanged={vi.fn()} />);
      if (denied === "GET /api/tasks/detail") {
        await screen.findByRole("table", { name: "任务列表" });
        expect(
          screen.queryByRole("button", { name: `详情 ${firstID}` }),
        ).toBeNull();
      } else {
        await screen.findByText("当前账户没有后台任务读取权限。");
        expect(calls("")).toHaveLength(0);
      }
      expect(calls("/detail")).toHaveLength(0);
      expectNoLink();
      expectReadOnlyEntry();
    },
  );

  it("does not infer dedicated log access or add checks when that menu is denied", async () => {
    override = (url) =>
      url === "/api/access/menu"
        ? response({
            menu: metadata().map((node) =>
              ["system", "system_logs"].includes(node.mark)
                ? { ...node, auth: { readable: false, writeable: false } }
                : node,
            ),
          })
        : url.startsWith("/api/tasks/detail")
          ? response(bundleDetail())
          : undefined;
    await detail();
    expect(screen.getByRole("link", { name: linkName })).toBeVisible();
    expect(
      fetcher.mock.calls
        .filter(([url]) => url === "/api/access/check")
        .map(([, init]) => JSON.parse(init.body as string).paths),
    ).toEqual([taskOperations]);
    expectNoBundleAction();
  });

  it("ignores an aborted old bundle response after selecting another task", async () => {
    const stale = deferredResponse();
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? url.includes(firstID)
          ? stale.promise
          : response(bundleDetail(bundleTask("succeeded", secondID)))
        : url.split("?")[0] === "/api/tasks"
          ? response(list([bundleTask(), bundleTask("succeeded", secondID)]))
          : undefined;
    await start();
    click(`详情 ${firstID}`);
    await waitFor(() => expect(calls("/detail")).toHaveLength(1));
    const signal = calls("/detail")[0][1].signal;
    expectNoLink();
    click("返回任务列表");
    await screen.findByRole("table", { name: "任务列表" });
    click(`详情 ${secondID}`);
    await screen.findByRole("link", { name: linkName });
    expect(signal?.aborted).toBe(true);
    // This mock deliberately ignores AbortSignal and still completes.
    await act(async () => stale.resolve(await response(bundleDetail())));
    expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(
      secondID,
    );
    expect(screen.getAllByRole("link", { name: linkName })).toHaveLength(1);
    expect(screen.getByRole("link", { name: linkName })).toHaveAttribute(
      "href",
      `#operational-logs/detail/${secondID}`,
    );
    expectNoBundleAction();
  });

  it.each(["account change", "unmount"])(
    "cannot restore the old entry after %s even if transport ignores abort",
    async (transition) => {
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response(bundleDetail())
          : undefined;
      const mounted = render(
        <TaskWorkspace profile={profile} sessionChanged={vi.fn()} />,
      );
      await screen.findByRole("table", { name: "任务列表" });
      click(`详情 ${firstID}`);
      await screen.findByRole("link", { name: linkName });
      const stale = deferredResponse();
      override = (url) =>
        url.startsWith("/api/tasks/detail") ? stale.promise : undefined;
      click("刷新任务详情");
      await waitFor(() => expect(calls("/detail")).toHaveLength(2));
      const signal = calls("/detail")[1][1].signal;
      expectNoLink();
      if (transition === "account change") {
        mounted.rerender(
          <TaskWorkspace
            profile={{
              ...profile,
              ID: 2,
              username: "other_admin",
              csrfToken: "other-session",
            }}
            sessionChanged={vi.fn()}
          />,
        );
        await screen.findByRole("table", { name: "任务列表" });
      } else mounted.unmount();
      expect(signal?.aborted).toBe(true);
      await act(async () => stale.resolve(await response(bundleDetail())));
      expectNoLink();
      expect(screen.queryByTestId("task-detail-identity")).toBeNull();
      expectNoBundleAction();
    },
  );

  it("preserves a valid actor-bound bundle intent byte for byte while rendering and refreshing details", async () => {
    const key = `adtr.pending-operational-bundle.${profile.ID}.${encodeURIComponent(profile.username)}`;
    const raw = JSON.stringify({
      owner: profile.ID,
      username: profile.username,
      input: {
        startTm: "2026-10-06T01:00:00.000001Z",
        endTm: "2026-10-07T01:00:00.000001Z",
        systemType: ["api", "worker"],
        idempotencyKey: "00000000-0000-4000-8000-000000000039",
      },
    });
    sessionStorage.setItem(key, raw);
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response(bundleDetail())
        : undefined;
    await detail();
    expect(screen.getByRole("link", { name: linkName })).toBeVisible();
    expect(sessionStorage.getItem(key)).toBe(raw);
    const held = deferredResponse();
    override = (url) =>
      url.startsWith("/api/tasks/detail") ? held.promise : undefined;
    click("刷新任务详情");
    await screen.findByText("正在读取任务详情…");
    expectNoLink();
    expect(sessionStorage.getItem(key)).toBe(raw);
    await act(async () => held.resolve(await response(bundleDetail())));
    await screen.findByRole("link", { name: linkName });
    expect(sessionStorage.getItem(key)).toBe(raw);
    expectNoBundleAction();
  });
});

describe("dedicated dictionary task guards", () => {
  it.each([
    ["domain.directory_read", "running"],
    ["domain.directory_read", "failed"],
    ["domain.directory_read.v2", "running"],
    ["domain.directory_read.v2", "failed"],
  ] as const)(
    "keeps %s %s mutation and private result inspection on its dedicated route",
    async (kind, state) => {
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response({
              task: {
                ...task(state),
                taskName: kind,
                domainId: "synthetic-domain-id",
                maxAttempts: 1,
                result: { unsafe: "DIRECTORY_PRIVATE_RESULT" },
                cursor: { unsafe: "DIRECTORY_PRIVATE_CURSOR" },
              },
              events: [],
            })
          : undefined;
      await detail();
      expect(screen.getByText(/目录任务请在/)).toHaveTextContent(
        kind.endsWith(".v2") ? "补充目录资产" : "目录资产",
      );
      expect(screen.queryByRole("button", { name: "创建恢复任务" })).toBeNull();
      expect(screen.queryByRole("button", { name: "请求取消任务" })).toBeNull();
      expect(screen.queryByText(/DIRECTORY_PRIVATE/)).toBeNull();
      expect(calls("/recover")).toHaveLength(0);
      expect(calls("/cancel")).toHaveLength(0);
    },
  );
});

// Synthetic DOM/transport regressions only. Real browser/API/Worker evidence
// belongs to the dedicated integration suites and is not implied by these tests.
describe("specialized dictionary and asset export task controls", () => {
  const specialKinds = [
    { taskName: "domain.directory_read.v3", href: "#directory-v3" },
    { taskName: "domain.directory_read.v4", href: "#directory-v4" },
    { taskName: "user_assets.v3.export", href: "#user-assets-v3" },
    { taskName: "group_assets.v3.export", href: "#group-assets-v3" },
    { taskName: "computer_assets.v3.export", href: "#computer-assets-v3" },
  ] as const;
  const firstID = task().taskUUID;
  const secondID = "00000000-0000-4000-8000-000000000002";
  const recoveryKey = "00000000-0000-4000-8000-000000000039";
  const otherActor = { ID: 2, username: "other_admin" };
  const ownerStorage = (actor = { ID: profile.ID, username: profile.username }) =>
    `adtr.pending-task:${actor.ID}:${encodeURIComponent(actor.username)}`;
  const savedRecover = (taskUUID = firstID, actor = profile) =>
    JSON.stringify({
      owner: actor.ID,
      username: actor.username,
      action: "recover",
      taskUUID,
      key: recoveryKey,
    });
  const specialTask = (
    taskName: string,
    state: TaskState = "failed",
    id = firstID,
  ): Task => ({
    ...task(state, id),
    taskName,
    domainId: "synthetic-domain-id",
    progress: 37,
    attempt: 1,
    resultVersion: 1,
    error: "SYNTHETIC_SPECIALIZED_ERROR",
    result: {
      private: "SPECIALIZED_PRIVATE_RESULT",
      nested: { objectDN: "SPECIALIZED_PRIVATE_DIRECTORY_DN" },
    },
    cursor: { private: "SPECIALIZED_PRIVATE_CURSOR" },
  });
  const taskDetail = (current: Task) => ({
    task: current,
    events: [
      {
        id: 17,
        action: "progress",
        state: current.state,
        attempt: 1,
        resultVersion: 1,
        createdAt: current.updatedAt,
      },
    ],
  });
  function deferredResponse() {
    let resolve!: (value: Response) => void;
    const promise = new Promise<Response>((done) => {
      resolve = done;
    });
    return { promise, resolve };
  }
  function expectNoGenericAction() {
    for (const name of [
      "请求取消任务",
      "创建恢复任务",
      "确认请求取消",
      "确认恢复任务",
    ])
      expect(screen.queryByRole("button", { name })).toBeNull();
    expect(screen.queryByLabelText("恢复幂等键")).toBeNull();
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    expect(screen.queryByLabelText("未使用的认证器验证码")).toBeNull();
    expect(screen.queryByLabelText("任务实际结果")).toBeNull();
    expect(screen.queryByText("查看已提交的执行游标")).toBeNull();
    // Include closed details and other hidden DOM, not just visible text.
    expect(document.body.textContent).not.toContain("SPECIALIZED_PRIVATE_");
    expect(calls("/cancel")).toHaveLength(0);
    expect(calls("/recover")).toHaveLength(0);
  }
  function expectDedicatedLink(href: string) {
    const detail = screen.getByRole("region", { name: "任务执行详情" });
    const links = within(detail).getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveAttribute("href", href);
    expect(links[0]).toBeVisible();
    // The link selects a workspace; it must not encode an unsupported task-ID
    // lookup or import a generic pending recovery key into that workspace.
    expect(links[0].getAttribute("href")).not.toContain(firstID);
    expect(links[0].getAttribute("href")).not.toContain(recoveryKey);
    const guidance = links[0].closest("p");
    if (href === "#directory-v4") {
      expect(guidance).toHaveTextContent("通用工作区");
      expect(guidance).toHaveTextContent("不会重新打开当前选中的历史任务");
      expect(guidance).toHaveTextContent("本标签页同一身份保存的原操作记录");
      expect(guidance).toHaveTextContent("组原始成员页面仍保持只读");
    } else if (href === "#user-assets-v3" || href === "#computer-assets-v3" || href === "#group-assets-v3") {
      expect(guidance).toHaveTextContent("查看原导出结果");
      expect(guidance).toHaveTextContent("重新核验专用权限");
      expect(guidance).toHaveTextContent(href === "#user-assets-v3" ? "未知创建提交恢复仍在" : "未知提交恢复仍在");
      if (href === "#user-assets-v3") expect(guidance).toHaveTextContent("核对原用户导出取消");
    } else if (href.endsWith("-assets-v3")) {
      expect(guidance).toHaveTextContent(/不会按此任务 ID\s*自动恢复/);
      expect(guidance).toHaveTextContent("请保留原导出操作记录");
      expect(guidance).toHaveTextContent("重新核验当前权限");
    } else {
      expect(guidance).toHaveTextContent("观察、回执及专用任务操作");
      expect(guidance).toHaveTextContent("重新核验当前权限");
    }
    return links[0];
  }
  afterEach(() => {
    discardTaskIntent(otherActor);
    clearUserExportRecovery();
    clearGroupExportRecovery();
    clearComputerExportRecovery();
  });

  it.each(specialKinds)(
    "classifies the exact dedicated kind $taskName consistently",
    ({ taskName }) => {
      expect(isDirectoryTask(taskName)).toBe(taskName.startsWith("domain."));
      expect(isAssetExportTask(taskName)).toBe(!taskName.startsWith("domain."));
      expect(requiresDedicatedTaskControls(taskName)).toBe(true);
      expect(requiresDedicatedTaskRecovery(taskName)).toBe(true);
    },
  );

  it.each([
    { name: undefined },
    { name: null },
    { name: 3 },
    { name: {} },
    { name: "" },
    { name: "domain.directory_read.v30" },
    { name: "domain.directory_read.v4.extra" },
    { name: "prefix.user_assets.v3.export" },
    { name: "user_assets.v3.export.extra" },
    { name: "group_assets.v3.export-preview" },
    { name: "computer_assets.v3.exported" },
    { name: "system.logs_bundle.extra" },
    { name: "toString" },
    { name: "__proto__" },
  ])("does not broaden dedicated behavior to $name", ({ name }) => {
    expect(isDirectoryTask(name)).toBe(false);
    expect(isAssetExportTask(name)).toBe(false);
    expect(requiresDedicatedTaskControls(name)).toBe(false);
    expect(requiresDedicatedTaskRecovery(name)).toBe(false);
  });

  it.each([
    ["infrastructure.health", false, false],
    ["audit.export", false, true],
    ["domain.connection_test", false, true],
    ["domain.account_connection_test", false, true],
    ["domain.directory_read", true, true],
    ["domain.directory_read.v2", true, true],
    ["system.logs_bundle", true, true],
  ] as const)(
    "preserves the existing control/recovery distinction for %s",
    (name, dedicatedControls, dedicatedRecovery) => {
      expect(requiresDedicatedTaskControls(name)).toBe(dedicatedControls);
      expect(requiresDedicatedTaskRecovery(name)).toBe(dedicatedRecovery);
    },
  );

  it.each([
    ["directory_route_required", "目录任务"],
    ["user_asset_export_route_required", "用户资产"],
    ["group_asset_export_route_required", "组资产"],
    ["computer_asset_export_route_required", "计算机资产"],
  ] as const)(
    "explains the dedicated API error %s without suggesting a generic retry",
    async (code, destination) => {
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response({ error: code }, 400)
          : undefined;
      await start();
      click(`详情 ${firstID}`);
      await screen.findByRole("alert");
      const message = taskError(new ApiError(code, 400));
      expect(screen.getByRole("alert")).toHaveTextContent(message);
      expect(message).toContain(destination);
      expect(message).toContain("专用");
      expect(message).not.toContain(code);
      if (code === "directory_route_required") {
        expect(message).toContain("字典 3");
        expect(message).toContain("字典 4");
        expect(message).toContain("仅查看已保存结果");
        expect(message).toContain("采集和取消仍需专用 API");
      } else {
        expect(message).toContain("不会按此任务 ID 自动恢复");
        expect(message).toContain("请保留原导出操作记录");
      }
      expectNoGenericAction();
    },
  );

  it.each(
    specialKinds.flatMap((kind) =>
      taskStates.map((state) => ({ ...kind, state })),
    ),
  )(
    "keeps $taskName / $state read-only with a dedicated route and fresh status",
    async ({ taskName, href, state }) => {
      let current = specialTask(taskName, state);
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response(taskDetail(current))
          : url.split("?")[0] === "/api/tasks"
            ? response(list([current]))
            : undefined;
      await detail();
      expectDedicatedLink(href);
      expectNoGenericAction();
      expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(
        firstID,
      );
      expect(document.querySelector(`[data-state="${state}"]`)).toBeVisible();
      expect(screen.getByLabelText("任务持久化进度")).toHaveValue(37);
      expect(screen.getByText("服务器错误代码：")).toHaveTextContent(
        "SYNTHETIC_SPECIALIZED_ERROR",
      );
      const events = screen.getByRole("list", { name: "持久化任务事件" });
      expect(within(events).getByText("事件 #17")).toBeVisible();
      expect(within(events).getByText("进度已持久化")).toBeVisible();
      expect(within(events).getByText("尝试 1 · 结果版本 1")).toBeVisible();
      if (["failed", "partial_failed", "dead_letter"].includes(state)) {
        expect(screen.getByText(/下方专用入口/)).toBeVisible();
        expect(screen.queryByText(/当前健康检查界面不支持/)).toBeNull();
      }
      current = { ...current, progress: 63 };
      click("刷新任务详情");
      await waitFor(() =>
        expect(screen.getByLabelText("任务持久化进度")).toHaveValue(63),
      );
      expect(calls("/detail").length).toBeGreaterThanOrEqual(2);
      expectDedicatedLink(href);
      expectNoGenericAction();
    },
  );

  it.each(
    specialKinds.flatMap((kind) =>
      (["legacy", "scoped"] as const).map((storage) => ({
        ...kind,
        storage,
      })),
    ),
  )(
    "clears only a confirmed $storage generic recovery for $taskName",
    async ({ taskName, href, storage }) => {
      const raw = savedRecover();
      sessionStorage.setItem(
        storage === "legacy" ? "adtr.pending-task" : ownerStorage(),
        raw,
      );
      const foreign = savedRecover(firstID, { ...profile, ...otherActor });
      sessionStorage.setItem(ownerStorage(otherActor), foreign);
      const locator = {
        version: 1 as const,
        actorID: profile.ID,
        domainId: "synthetic-domain-id",
        idempotencyKey: recoveryKey,
      };
      writeUserExportRecovery({ ...locator, kind: "user_assets.v3.export" });
      writeGroupExportRecovery({ ...locator, kind: "group_assets.v3.export" });
      writeComputerExportRecovery({
        ...locator,
        kind: "computer_assets.v3.export",
      });
      const exportRecords = [
        userExportRecoveryStorage,
        groupExportRecoveryStorage,
        computerExportRecoveryStorage,
      ].map((key) => ({ key, value: sessionStorage.getItem(key) }));
      const pendingDetail = deferredResponse();
      override = (url) =>
        url.startsWith("/api/tasks/detail") ? pendingDetail.promise : undefined;
      await start();
      click("继续核对未确认的提交");
      await waitFor(() => expect(calls("/detail")).toHaveLength(1));
      expect(sessionStorage.getItem(ownerStorage())).not.toBeNull();
      expectNoGenericAction();
      await act(async () =>
        pendingDetail.resolve(
          await response(taskDetail(specialTask(taskName))),
        ),
      );
      await screen.findByRole("list", { name: "持久化任务事件" });
      expectDedicatedLink(href);
      await waitFor(() =>
        expect(sessionStorage.getItem(ownerStorage())).toBeNull(),
      );
      expect(sessionStorage.getItem("adtr.pending-task")).toBeNull();
      expect(sessionStorage.getItem(ownerStorage(otherActor))).toBe(foreign);
      for (const { key, value } of exportRecords)
        expect(sessionStorage.getItem(key)).toBe(value);
      expectNoGenericAction();
      click("返回任务列表");
      await screen.findByRole("table", { name: "任务列表" });
      expect(screen.getByRole("button", { name: "提交健康检查" })).toBeEnabled();
      expect(
        screen.queryByRole("button", { name: "继续核对未确认的提交" }),
      ).toBeNull();
    },
  );

  it.each([
    "other task",
    "submit",
    "other actor",
    "other username",
    "foreign legacy",
    "foreign legacy username",
  ] as const)(
    "preserves an unrelated %s intent when reading a specialized task",
    async (source) => {
      const foreign = source !== "other task" && source !== "submit";
      const actor = {
        ...profile,
        ...otherActor,
        ...(source.includes("username") ? { ID: profile.ID } : {}),
      };
      const raw =
        source === "submit"
          ? JSON.stringify({
              owner: profile.ID,
              username: profile.username,
              action: "submit",
              key: recoveryKey,
            })
          : savedRecover(
              source === "other task" ? secondID : firstID,
              foreign ? actor : profile,
            );
      const storage =
        source.startsWith("foreign legacy")
          ? "adtr.pending-task"
          : ownerStorage(foreign ? actor : profile);
      sessionStorage.setItem(storage, raw);
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response(taskDetail(specialTask("user_assets.v3.export")))
          : undefined;
      await detail();
      expectDedicatedLink("#user-assets-v3");
      expectNoGenericAction();
      expect(sessionStorage.getItem(storage)).toBe(raw);
      click("返回任务列表");
      await screen.findByRole("table", { name: "任务列表" });
      if (source === "other task" || source === "submit") {
        expect(screen.getByRole("button", { name: "提交健康检查" })).toBeDisabled();
        expect(
          screen.getByRole("button", { name: "继续核对未确认的提交" }),
        ).toBeVisible();
      } else {
        expect(screen.getByRole("button", { name: "提交健康检查" })).toBeEnabled();
      }
      expect(sessionStorage.getItem(storage)).toBe(raw);
    },
  );

  it.each([403, 404])(
    "preserves a recovery intent when its kind cannot be confirmed (%s)",
    async (status) => {
      const raw = savedRecover();
      sessionStorage.setItem(ownerStorage(), raw);
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response(
              { error: status === 403 ? "forbidden" : "not_found" },
              status,
            )
          : undefined;
      await start();
      click("继续核对未确认的提交");
      await screen.findByRole("alert");
      expect(sessionStorage.getItem(ownerStorage())).toBe(raw);
      expectNoGenericAction();
    },
  );

  it("preserves a saved recovery when the detail response identifies another task", async () => {
    const raw = savedRecover();
    sessionStorage.setItem(ownerStorage(), raw);
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response(
            taskDetail(
              specialTask("user_assets.v3.export", "failed", secondID),
            ),
          )
        : undefined;
    await start();
    click("继续核对未确认的提交");
    await screen.findByRole("alert");
    expect(sessionStorage.getItem(ownerStorage())).toBe(raw);
    expect(screen.queryByTestId("task-detail-identity")).toBeNull();
    expectNoGenericAction();
  });

  it.each(specialKinds)(
    "removes $taskName detail after current permissions are revoked",
    async ({ taskName, href }) => {
      let revoked = false;
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? revoked
            ? response({ error: "forbidden" }, 403)
            : response(taskDetail(specialTask(taskName)))
          : undefined;
      await detail();
      expectDedicatedLink(href);
      revoked = true;
      click("刷新任务详情");
      await screen.findByRole("alert");
      expect(screen.queryByTestId("task-detail-identity")).toBeNull();
      expect(screen.queryByRole("link")).toBeNull();
      expect(screen.queryByLabelText("任务持久化进度")).toBeNull();
      expect(screen.queryByText("SYNTHETIC_SPECIALIZED_ERROR")).toBeNull();
      expectNoGenericAction();
    },
  );

  it.each([
    ["running", "请求取消任务", "确认请求取消"],
    ["failed", "创建恢复任务", "确认恢复任务"],
  ] as const)(
    "removes the old generic %s action form after a refreshed specialized kind",
    async (state, openAction, confirmAction) => {
      let current = task(state);
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response(taskDetail(current))
          : undefined;
      await detail();
      click(openAction);
      proof();
      expect(screen.getByRole("button", { name: confirmAction })).toBeEnabled();
      current = specialTask("computer_assets.v3.export", state);
      click("刷新任务详情");
      await screen.findByRole("link");
      expectDedicatedLink("#computer-assets-v3");
      expectNoGenericAction();
    },
  );

  it("ignores an older generic response after selecting a specialized task", async () => {
    const stale = deferredResponse();
    const current = specialTask("group_assets.v3.export", "failed", secondID);
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? url.includes(firstID)
          ? stale.promise
          : response(taskDetail(current))
        : url.split("?")[0] === "/api/tasks"
          ? response(list([task(), current]))
          : undefined;
    await start();
    click(`详情 ${firstID}`);
    await waitFor(() => expect(calls("/detail")).toHaveLength(1));
    const staleSignal = calls("/detail")[0][1].signal;
    click("返回任务列表");
    await screen.findByRole("table", { name: "任务列表" });
    click(`详情 ${secondID}`);
    await screen.findByRole("list", { name: "持久化任务事件" });
    expect(staleSignal?.aborted).toBe(true);
    await act(async () =>
      stale.resolve(
        await response(
          taskDetail({
            ...task("failed"),
            result: { private: "SPECIALIZED_PRIVATE_STALE_RESPONSE" },
          }),
        ),
      ),
    );
    expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(
      secondID,
    );
    expectDedicatedLink("#group-assets-v3");
    expectNoGenericAction();
  });

  it("fences a specialized response and cleanup when the signed-in actor changes", async () => {
    const raw = savedRecover();
    const originalStorage = ownerStorage();
    sessionStorage.setItem(originalStorage, raw);
    const stale = deferredResponse();
    override = (url) =>
      url.startsWith("/api/tasks/detail") ? stale.promise : undefined;
    const mounted = render(
      <TaskWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByRole("table", { name: "任务列表" });
    click("继续核对未确认的提交");
    await waitFor(() => expect(calls("/detail")).toHaveLength(1));
    const staleSignal = calls("/detail")[0][1].signal;
    const nextActor = {
      ...profile,
      ...otherActor,
      csrfToken: "next-actor-session",
    };
    mounted.rerender(
      <TaskWorkspace profile={nextActor} sessionChanged={vi.fn()} />,
    );
    await screen.findByRole("table", { name: "任务列表" });
    expect(staleSignal?.aborted).toBe(true);
    await act(async () =>
      stale.resolve(
        await response(taskDetail(specialTask("domain.directory_read.v4"))),
      ),
    );
    expect(screen.queryByTestId("task-detail-identity")).toBeNull();
    expect(screen.queryByRole("link")).toBeNull();
    expect(sessionStorage.getItem(originalStorage)).toBe(raw);
    expect(sessionStorage.getItem(ownerStorage(otherActor))).toBeNull();
    expect(screen.getByRole("button", { name: "提交健康检查" })).toBeEnabled();
    expectNoGenericAction();
  });

  it.each(["popstate", "hashchange"] as const)(
    "revalidates a specialized detail after %s without restoring generic controls",
    async (eventName) => {
      window.history.replaceState({}, "", "#tasks");
      let current = specialTask("domain.directory_read.v4", "failed");
      override = (url) =>
        url.startsWith("/api/tasks/detail")
          ? response(taskDetail(current))
          : url.split("?")[0] === "/api/tasks"
            ? response(list([current]))
            : undefined;
      render(<App />);
      await screen.findByRole("table", { name: "任务列表" });
      click(`详情 ${firstID}`);
      await screen.findByRole("list", { name: "持久化任务事件" });
      const saved = JSON.parse(JSON.stringify(window.history.state));
      const hash = window.location.hash;
      click("返回任务列表");
      await screen.findByRole("table", { name: "任务列表" });
      current = { ...current, progress: 91 };
      const before = fetcher.mock.calls.length;
      act(() => {
        window.history.replaceState(saved, "", hash);
        window.dispatchEvent(
          eventName === "popstate"
            ? new PopStateEvent("popstate", { state: saved })
            : new HashChangeEvent("hashchange"),
        );
      });
      await waitFor(() =>
        expect(screen.getByLabelText("任务持久化进度")).toHaveValue(91),
      );
      const refreshed = fetcher.mock.calls.slice(before).map(([url]) => url);
      expect(refreshed).toEqual(
        expect.arrayContaining([
          "/api/auth/me",
          "/api/access/menu",
          "/api/access/check",
          expect.stringContaining(`/api/tasks/detail?taskUUID=${firstID}`),
        ]),
      );
      expectDedicatedLink("#directory-v4");
      expectNoGenericAction();
    },
  );
});

describe("original user export dedicated cancellation entry", () => {
  it("keeps generic controls forbidden and the new dedicated control collapsed", async () => {
    override = (url) => url.startsWith("/api/tasks/detail") ? response({ task: { ...task("running"), taskName: "user_assets.v3.export", domainId: "synthetic-domain-id", maxAttempts: 3 }, events: [] }) : undefined;
    await detail();
    expect(screen.getByRole("button", { name: "核对原用户导出取消" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "请求取消任务" })).toBeNull();
    expect(screen.queryByRole("button", { name: "创建恢复任务" })).toBeNull();
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    expect(calls("/cancel")).toHaveLength(0); expect(calls("/recover")).toHaveLength(0);
  });
});
