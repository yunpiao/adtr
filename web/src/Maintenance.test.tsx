import { revealNavigation } from "./test-navigation";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import TaskWorkspace, { discardTaskIntent } from "./TaskWorkspace";
import { PermissionEditor } from "./access-common";
import {
  labels,
  marks,
  type Permission,
  type PermissionInput,
} from "./access-api";
import type { Profile } from "./api";
import { maintenanceAPI, utcSecond, type Schedule } from "./maintenance-api";
import {
  discardMaintenanceIntent,
  maintenanceIntent,
} from "./maintenance-intent";
import { taskAPI, taskOperations, type Task } from "./task-api";

const sid = "00000000-0000-4000-8000-000000000010",
  tid = "00000000-0000-4000-8000-000000000011";
const profile: Profile = {
  ID: 1,
  username: "synthetic",
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
  pwdUpdateTm: "2026-10-07T00:00:00Z",
  csrfToken: "synthetic-csrf",
};
const kind = {
  taskName: "infrastructure.health",
  payloadVersion: 1,
  scope: "platform",
  maxAttempts: 5,
  timeoutSeconds: 10,
};
const plan = (state: Schedule["state"] = "paused"): Schedule => ({
  scheduleUUID: sid,
  label: "Synthetic health",
  taskName: kind.taskName,
  domainId: "platform",
  payloadVersion: 1,
  startAt: utcSecond(Date.now() + 120000),
  intervalSeconds: 60,
  state,
  controlVersion: 1,
  lastTaskUUID: "",
  error: "",
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:00Z",
  ...(state === "enabled" ? { nextAt: utcSecond(Date.now() + 120000) } : {}),
});
const task = (archived = false): Task => ({
  taskUUID: tid,
  taskName: kind.taskName,
  domainId: "platform",
  payloadVersion: 1,
  state: "failed",
  sourceState: "FAILURE",
  error: "synthetic",
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:02Z",
  terminalAt: "2026-10-07T00:00:02Z",
  archived,
  visibilityVersion: archived ? 1 : 0,
  attempt: 1,
  maxAttempts: 5,
  progress: 0,
  resultVersion: 1,
  result: { database: "ready" },
  cursor: {},
  parentTaskUUID: "",
});
const page = { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 };
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
const response = (v: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: async () => v,
  } as Response);
let override: (url: string, init: RequestInit) => Promise<Response> | undefined;
let fetcher: ReturnType<typeof vi.fn>;
beforeEach(() => {
  discardTaskIntent(profile);
  discardMaintenanceIntent(profile);
  sessionStorage.clear();
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
    if (path === "/api/tasks")
      return response({ page, tasks: [task()], exhausted: true });
    if (path === "/api/tasks/detail")
      return response({ task: task(), events: [] });
    if (path === "/api/tasks/kinds") return response({ kinds: [kind] });
    if (path === "/api/tasks/schedules")
      return response({ page, schedules: [plan()], exhausted: true });
    if (path === "/api/tasks/schedules/detail")
      return response({ schedule: plan(), page, events: [], exhausted: true });
    if (path === "/api/tasks/schedules/create")
      return response({ schedule: plan(), replayed: false });
    if (path === "/api/tasks/archive-candidates")
      return response({
        before: "2026-10-07T00:01:00Z",
        page,
        tasks: [task()],
        exhausted: true,
      });
    throw new Error(`Unexpected ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  discardMaintenanceIntent(profile);
  vi.useRealTimers();
});
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
const proof = (code = "123456") => {
  fill("操作者当前密码", "Synthetic Password 123");
  fill("未使用的认证器验证码", code);
};
const calls = (path: string) =>
  fetcher.mock.calls.filter(([u]) => u.split("?")[0] === `/api/tasks/${path}`);
async function start() {
  render(<TaskWorkspace profile={profile} sessionChanged={vi.fn()} />);
  await screen.findByRole("table", { name: "任务列表" });
}
async function createReview() {
  await start();
  click("周期计划");
  await screen.findByRole("table", { name: "周期计划列表" });
  click("新建周期计划");
  await screen.findByLabelText("计划名称");
  await waitFor(() => expect(screen.getByLabelText("计划名称")).toBeEnabled());
  fill("计划名称", "Synthetic health");
  click("核对暂停计划");
  await screen.findByLabelText("维护操作幂等键");
}
async function scheduleDetail() {
  await start();
  click("周期计划");
  await screen.findByRole("table", { name: "周期计划列表" });
  click(`计划详情 ${sid}`);
  await screen.findByRole("table", { name: "计划发生记录" });
}

describe("maintenance permission and contracts", () => {
  it("requires each server menu grant as well as every registered path check", async () => {
    override = (url) =>
      url === "/api/access/menu"
        ? response({
            menu: metadata().filter(
              (p) => !["schedules", "task_archive"].includes(p.mark),
            ),
          })
        : undefined;
    await start();
    expect(
      screen.queryByRole("button", { name: "周期计划" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "任务归档" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("任务可见性筛选")).not.toBeInTheDocument();
    expect(
      JSON.parse(
        fetcher.mock.calls.find(([u]) => u === "/api/access/check")![1]
          .body as string,
      ).paths,
    ).toEqual(taskOperations);
  });
  it("preserves both new grants when another permission changes", () => {
    const input: PermissionInput[] = metadata().map((p) => ({
        mark: p.mark,
        auth: p.auth,
      })),
      change = vi.fn();
    render(
      <PermissionEditor
        value={input}
        metadata={metadata()}
        onChange={change}
      />,
    );
    fireEvent.click(screen.getByRole("checkbox", { name: "用户管理：写入" }));
    for (const mark of ["schedules", "task_archive"])
      expect(
        change.mock.calls[0][0].find((p: PermissionInput) => p.mark === mark),
      ).toEqual(input.find((p) => p.mark === mark));
  });
  it("rejects wrong schedule identity, cursor types and malformed receipts", async () => {
    override = () =>
      response({
        schedule: { ...plan(), scheduleUUID: "foreign" },
        page,
        events: [],
        exhausted: true,
      });
    await expect(
      maintenanceAPI.detail(sid, 1, new AbortController().signal),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () =>
      response({
        schedule: plan(),
        page,
        events: [
          {
            id: 1,
            action: "admitted",
            taskUUID: tid,
            controlVersion: 1,
            createdAt: plan().createdAt,
            count: "1",
          },
        ],
        exhausted: true,
      });
    await expect(
      maintenanceAPI.detail(sid, 1, new AbortController().signal),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () =>
      response({
        receipt: {
          operationUUID: sid,
          action: "archive",
          reason: "x",
          occurredAt: plan().createdAt,
          targets: [
            { taskUUID: "foreign", archived: true, visibilityVersion: 1 },
          ],
        },
        replayed: false,
      });
    await expect(
      maintenanceAPI.archive(
        "archive",
        {
          targets: [{ taskUUID: tid, visibilityVersion: 0 }],
          before: plan().createdAt,
          reason: "x",
        },
        "key",
        { actorPassword: "password", totpCode: "123456" },
        "csrf",
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("requires explicit archived detail and rejects missing visibility version", async () => {
    override = () => response({ task: task(true), events: [] });
    await taskAPI.detail(tid, new AbortController().signal, "all");
    expect(fetcher.mock.calls[0][0]).toContain("visibility=all");
    override = () =>
      response({
        task: { ...task(), visibilityVersion: undefined },
        events: [],
      });
    await expect(
      taskAPI.detail(tid, new AbortController().signal),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
});
describe("periodic maintenance UI", () => {
  it("validates UTC whole-second future bounds and interval limits", async () => {
    await start();
    click("周期计划");
    await screen.findByRole("table", { name: "周期计划列表" });
    click("新建周期计划");
    await waitFor(() =>
      expect(screen.getByLabelText("计划名称")).toBeEnabled(),
    );
    fill("计划名称", "Synthetic health");
    fill("首次计划时间（UTC）", utcSecond(Date.now() + 30000));
    click("核对暂停计划");
    await screen.findByText(/首次时间须为 UTC 整秒/);
    fill("首次计划时间（UTC）", "2028-01-01T00:00:00+08:00");
    click("核对暂停计划");
    expect(calls("schedules/create")).toHaveLength(0);
    fill("首次计划时间（UTC）", utcSecond(Date.now() + 120000));
    fill("执行间隔（秒）", "59");
    fireEvent.submit(screen.getByLabelText("计划名称").closest("form")!);
    await screen.findByText(/间隔须为 60–86400/);
  });
  it("does not invent a schedule editor for an unknown registered task kind", async () => {
    override = (url) =>
      url === "/api/tasks/kinds"
        ? response({
            kinds: [{ ...kind, taskName: "ad.unknown", scope: "domain" }],
          })
        : undefined;
    await start();
    click("周期计划");
    await screen.findByRole("table", { name: "周期计划列表" });
    click("新建周期计划");
    await screen.findByText(/服务器未登记受支持的健康检查种类/);
    expect(screen.getByRole("button", { name: "核对暂停计划" })).toBeDisabled();
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    expect(calls("schedules/create")).toHaveLength(0);
  });
  it("persists identical create key and immutable inputs across network failure and fresh-proof retry", async () => {
    let attempts = 0;
    override = (url) =>
      url === "/api/tasks/schedules/create"
        ? ++attempts === 1
          ? Promise.reject(new TypeError("offline"))
          : response({ schedule: plan(), replayed: true })
        : undefined;
    await createReview();
    const key = (screen.getByLabelText("维护操作幂等键") as HTMLInputElement)
      .value;
    proof();
    click("确认创建暂停计划");
    await screen.findByRole("alert");
    await screen.findByText(/已重新读取 1 项计划/);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    const stored = sessionStorage.getItem(
      `adtr.pending-task-maintenance:${profile.ID}:${encodeURIComponent(profile.username)}`,
    )!;
    expect(stored).toContain(key);
    for (const secret of [
      "actorPassword",
      "totpCode",
      profile.csrfToken,
      "Synthetic Password 123",
    ])
      expect(stored).not.toContain(secret);
    proof("654321");
    click("使用原幂等键核对维护操作");
    await screen.findByText(/已确认同一计划的创建记录/);
    const bodies = calls("schedules/create").map(([, i]) =>
      JSON.parse(i.body as string),
    );
    expect(bodies[0].idempotencyKey).toBe(key);
    expect(bodies[1]).toEqual({ ...bodies[0], totpCode: "654321" });
    expect(sessionStorage.length).toBe(0);
  });
  it("prevents duplicate writes and ignores a late response after leaving the review", async () => {
    let resolve!: (v: Response) => void;
    override = (url) =>
      url === "/api/tasks/schedules/create"
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    await createReview();
    proof();
    click("确认创建暂停计划");
    fireEvent.submit(screen.getByLabelText("操作者当前密码").closest("form")!);
    expect(calls("schedules/create")).toHaveLength(1);
    click("取消");
    await screen.findByRole("table", { name: "周期计划列表" });
    await act(async () =>
      resolve(await response({ schedule: plan(), replayed: false })),
    );
    expect(
      screen.queryByRole("table", { name: "计划发生记录" }),
    ).not.toBeInTheDocument();
    click("继续核对维护操作");
    await screen.findByLabelText("维护操作幂等键");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
  });
  it("renders current paused state when an earlier enable receipt is replayed", async () => {
    override = (url) =>
      url === "/api/tasks/schedules/enable"
        ? response({
            schedule: { ...plan(), controlVersion: 3 },
            replayed: true,
            receipt: {
              operationUUID: tid,
              scheduleUUID: sid,
              action: "enable",
              state: "enabled",
              controlVersion: 2,
              createdAt: plan().createdAt,
            },
          })
        : undefined;
    await scheduleDetail();
    click("启用计划");
    proof();
    click("确认启用计划");
    await screen.findByText(/服务器当前状态：已暂停/);
    expect(
      JSON.parse(calls("schedules/enable")[0][1].body as string)
        .expectedControlVersion,
    ).toBe(1);
    expect(screen.getByText(/暂停不会取消已经生成的任务/)).toBeVisible();
  });
  it("stops stale CAS retry and requires new review after an explicit conflict", async () => {
    override = (url) =>
      url === "/api/tasks/schedules/enable"
        ? response({ error: "control_version_conflict" }, 409)
        : undefined;
    await scheduleDetail();
    click("启用计划");
    proof();
    click("确认启用计划");
    await screen.findByRole("button", { name: "重新读取并核对" });
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    click("重新读取并核对");
    await screen.findByRole("table", { name: "计划发生记录" });
    expect(maintenanceIntent(profile)).toBeUndefined();
  });
  it("does not offer reactivation after authorization invalidation", async () => {
    override = (url) =>
      url.startsWith("/api/tasks/schedules/detail")
        ? response({
            schedule: plan("authorization_blocked"),
            page,
            events: [],
            exhausted: true,
          })
        : undefined;
    await scheduleDetail();
    expect(
      screen.queryByRole("button", { name: "启用计划" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText(/此计划不能重新启用/)).toBeVisible();
  });
  it("clears schedule contents on permission revocation", async () => {
    let revoked = false;
    override = (url) =>
      url.startsWith("/api/tasks/schedules/detail") && revoked
        ? response({ error: "forbidden" }, 403)
        : undefined;
    await scheduleDetail();
    revoked = true;
    click("刷新计划详情");
    await screen.findByRole("alert");
    expect(
      screen.queryByRole("table", { name: "计划发生记录" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "启用计划" }),
    ).not.toBeInTheDocument();
  });
});
describe("reversible archive UI", () => {
  it("previews exact terminal targets and submits frozen cutoff/version/reason, then rereads current visibility", async () => {
    let archived = false;
    override = (url) =>
      url === "/api/tasks/archive"
        ? ((archived = true),
          response({
            receipt: {
              operationUUID: sid,
              action: "archive",
              reason: "Synthetic retention",
              before: "2026-10-07T00:01:00Z",
              targets: [
                { taskUUID: tid, visibilityVersion: 1, archived: true },
              ],
              occurredAt: plan().createdAt,
            },
            replayed: false,
          }))
        : url.startsWith("/api/tasks/detail")
          ? response({ task: task(archived), events: [] })
          : undefined;
    await start();
    click("任务归档");
    await screen.findByRole("heading", { name: "预览任务归档" });
    expect(calls("archive-candidates")).toHaveLength(0);
    fill("归档截止时间（UTC）", "2026-10-07T00:01:00Z");
    click("预览归档候选");
    await screen.findByRole("table", { name: "归档候选任务" });
    fireEvent.click(screen.getByRole("checkbox", { name: `选择归档 ${tid}` }));
    fill("归档原因", "Synthetic retention");
    click("核对归档所选任务");
    proof();
    click("确认归档所选任务");
    await screen.findByText(/任务可见性操作已登记/);
    expect(JSON.parse(calls("archive")[0][1].body as string)).toMatchObject({
      targets: [{ taskUUID: tid, visibilityVersion: 0 }],
      before: "2026-10-07T00:01:00Z",
      reason: "Synthetic retention",
    });
    await screen.findByText(/任务已归档，原执行结果/);
    expect(calls("detail").at(-1)![0]).toContain("visibility=all");
    expect(
      screen.queryByRole("button", { name: "创建恢复任务" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "核对恢复可见性" }),
    ).toBeEnabled();
  });
  it("archive read-only users can preview but cannot select or write", async () => {
    override = (url, init) =>
      url === "/api/access/check"
        ? response({
            results: JSON.parse(init.body as string).paths.map(
              (p: string) => !p.startsWith("POST /api/tasks/"),
            ),
          })
        : undefined;
    await start();
    click("任务归档");
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "预览归档候选" }),
      ).toBeEnabled(),
    );
    click("预览归档候选");
    await screen.findByRole("table", { name: "归档候选任务" });
    expect(
      screen.getByRole("checkbox", { name: `选择归档 ${tid}` }),
    ).toBeDisabled();
    expect(screen.queryByLabelText("归档原因")).not.toBeInTheDocument();
  });
  it("retains actor-isolated recovery through logout while Back dismisses secrets", async () => {
    sessionStorage.setItem(
      "adtr.pending-task-maintenance",
      JSON.stringify({
        owner: 1,
        username: profile.username,
        key: sid,
        action: "restore",
        input: {
          targets: [
            { taskUUID: tid, visibilityVersion: 1, actorPassword: "injected" },
          ],
          reason: "Synthetic restore",
          actorPassword: "injected",
          totpCode: "123456",
        },
      }),
    );
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    await revealNavigation("后台任务");
    click("后台任务");
    await screen.findByRole("table", { name: "任务列表" });
    click("继续核对维护操作");
    await screen.findByLabelText("维护操作幂等键");
    expect(screen.getByLabelText("待确认的维护内容")).not.toHaveTextContent(
      "injected",
    );
    proof();
    act(() => window.history.back());
    await waitFor(() => expect(window.location.hash).toBe("#tasks"));
    await screen.findByRole("table", { name: "任务列表" });
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    override = (url) =>
      url === "/api/auth/logout" ? response({ result: "SUCCESS" }) : undefined;
    await revealNavigation("退出登录");
    click("退出登录");
    await screen.findByRole("heading", { name: "登录账户" });
    expect(
      sessionStorage.getItem(
        `adtr.pending-task-maintenance:${profile.ID}:${encodeURIComponent(profile.username)}`,
      ),
    ).toContain(sid);
    expect(maintenanceIntent({ ...profile, ID: 2 })).toBeUndefined();
    expect(maintenanceIntent({ ...profile, csrfToken: "rotated" })?.key).toBe(
      sid,
    );
    expect(calls("/api/tasks/restore")).toHaveLength(0);
  });
});

describe("archived replay and candidate paging", () => {
  it("identifies an archived original on generic submit replay without claiming a new execution", async () => {
    override = (url) =>
      url === "/api/tasks/submit"
        ? response({ task: task(true), replayed: true })
        : url.startsWith("/api/tasks/detail")
          ? response({ task: task(true), events: [] })
          : undefined;
    await start();
    click("提交健康检查");
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "确认提交健康检查" }),
      ).toBeEnabled(),
    );
    proof();
    click("确认提交健康检查");
    await screen.findByText(/同一个任务（已归档）。未创建新的执行/);
    await screen.findByText(/任务已归档，原执行结果/);
    expect(calls("detail").at(-1)![0]).toContain("visibility=all");
    expect(
      screen.queryByRole("button", { name: "创建恢复任务" }),
    ).not.toBeInTheDocument();
  });
  it("treats an old archive receipt as history and displays the freshly read restored state", async () => {
    sessionStorage.setItem(
      "adtr.pending-task-maintenance",
      JSON.stringify({
        owner: profile.ID,
        username: profile.username,
        key: sid,
        action: "archive",
        input: {
          targets: [{ taskUUID: tid, visibilityVersion: 0 }],
          before: "2026-10-07T00:01:00Z",
          reason: "Earlier archive",
        },
      }),
    );
    override = (url) =>
      url === "/api/tasks/archive"
        ? response({
            receipt: {
              operationUUID: sid,
              action: "archive",
              reason: "Earlier archive",
              occurredAt: plan().createdAt,
              targets: [
                { taskUUID: tid, archived: true, visibilityVersion: 1 },
              ],
            },
            replayed: true,
          })
        : url.startsWith("/api/tasks/detail")
          ? response({ task: { ...task(), visibilityVersion: 2 }, events: [] })
          : undefined;
    await start();
    click("继续核对维护操作");
    proof();
    click("使用原幂等键核对维护操作");
    await screen.findByText(/原操作收据，当前可见性可能已经变化/);
    await screen.findByText("默认可见");
    expect(
      screen.queryByText(/任务已归档，原执行结果/),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "创建恢复任务" })).toBeEnabled();
  });
  it("clears candidate selections when moving to a new server page", async () => {
    const other = "00000000-0000-4000-8000-000000000012";
    override = (url) => {
      if (!url.startsWith("/api/tasks/archive-candidates")) return;
      const second =
        new URLSearchParams(url.split("?")[1]).get("pageIdx") === "2";
      return response({
        before: "2026-10-07T00:01:00Z",
        page: { ...page, total: 21, totalPage: 2, pageIdx: second ? 2 : 1 },
        tasks: [{ ...task(), taskUUID: second ? other : tid }],
        exhausted: second,
      });
    };
    await start();
    click("任务归档");
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "预览归档候选" }),
      ).toBeEnabled(),
    );
    click("预览归档候选");
    await screen.findByRole("checkbox", { name: `选择归档 ${tid}` });
    fireEvent.click(screen.getByRole("checkbox", { name: `选择归档 ${tid}` }));
    fill("归档原因", "Synthetic page selection");
    expect(
      screen.getByRole("button", { name: "核对归档所选任务" }),
    ).toBeEnabled();
    click("下一页");
    await screen.findByRole("checkbox", { name: `选择归档 ${other}` });
    expect(
      screen.getByRole("checkbox", { name: `选择归档 ${other}` }),
    ).not.toBeChecked();
    expect(
      screen.getByRole("button", { name: "核对归档所选任务" }),
    ).toBeDisabled();
    expect(calls("archive")).toHaveLength(0);
  });
});

describe("maintenance refresh and legacy terminal evidence", () => {
  it("repeats the same archive preview against current server versions and clears the old selection", async () => {
    let version = 0;
    override = (url) =>
      url.startsWith("/api/tasks/archive-candidates")
        ? response({
            before: "2026-10-07T00:01:00Z",
            page,
            tasks: [{ ...task(), visibilityVersion: version }],
            exhausted: true,
          })
        : undefined;
    await start();
    click("任务归档");
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "预览归档候选" }),
      ).toBeEnabled(),
    );
    fill("归档截止时间（UTC）", "2026-10-07T00:01:00Z");
    click("预览归档候选");
    await screen.findByRole("checkbox", { name: `选择归档 ${tid}` });
    fireEvent.click(screen.getByRole("checkbox", { name: `选择归档 ${tid}` }));
    version = 2;
    click("预览归档候选");
    await waitFor(() => expect(calls("archive-candidates")).toHaveLength(2));
    await waitFor(() =>
      expect(
        screen.getByRole("checkbox", { name: `选择归档 ${tid}` }),
      ).not.toBeChecked(),
    );
    expect(
      screen.getByRole("button", { name: "核对归档所选任务" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("table", { name: "归档候选任务" }),
    ).toHaveTextContent("2");
  });
  it("keeps legacy terminal timestamps unknown instead of reporting an unfinished task", async () => {
    override = (url) =>
      url.startsWith("/api/tasks/detail")
        ? response({ task: { ...task(), terminalAt: null }, events: [] })
        : undefined;
    await start();
    click(`详情 ${tid}`);
    await screen.findByText("终态时间未知");
    expect(screen.queryByText("尚未结束")).not.toBeInTheDocument();
  });
});

describe("uncertain recovery whose parent was archived", () => {
  it("retains the original recovery key while restoring parent visibility first", async () => {
    sessionStorage.setItem(
      "adtr.pending-task",
      JSON.stringify({
        owner: profile.ID,
        username: profile.username,
        key: sid,
        action: "recover",
        taskUUID: tid,
      }),
    );
    let archived = true;
    override = (url) => {
      if (url.startsWith("/api/tasks/detail"))
        return response({
          task: { ...task(archived), visibilityVersion: archived ? 1 : 2 },
          events: [],
        });
      if (url === "/api/tasks/restore") {
        archived = false;
        return response({
          receipt: {
            operationUUID: sid,
            action: "restore",
            reason: "Restore recovery parent",
            targets: [{ taskUUID: tid, archived: false, visibilityVersion: 2 }],
            occurredAt: plan().createdAt,
          },
          replayed: false,
        });
      }
    };
    await start();
    click("继续核对未确认的提交");
    await screen.findByText(/任务已归档，原执行结果/);
    expect(calls("detail").at(-1)![0]).toContain("visibility=all");
    expect(
      screen.queryByRole("button", { name: "确认恢复任务" }),
    ).not.toBeInTheDocument();
    fill("恢复可见性原因", "Restore recovery parent");
    click("核对恢复可见性");
    proof();
    click("确认恢复任务可见性");
    await screen.findByLabelText("恢复幂等键");
    expect(screen.getByLabelText("恢复幂等键")).toHaveValue(sid);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(calls("recover")).toHaveLength(0);
    expect(
      sessionStorage.getItem(
        `adtr.pending-task:${profile.ID}:${encodeURIComponent(profile.username)}`,
      ),
    ).toContain(sid);
  });
});
