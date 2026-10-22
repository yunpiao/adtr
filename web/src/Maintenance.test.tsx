import { createHash } from "node:crypto";
import { StrictMode } from "react";
import { MaintenanceContent } from "./TaskMaintenance";
import type { TaskContext } from "./task-common";
import { revealNavigation } from "./test-navigation";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import TaskWorkspace, { attemptTaskIntent, beginTaskIntent, discardTaskIntent } from "./TaskWorkspace";
import { PermissionEditor } from "./access-common";
import {
  labels,
  marks,
  type Permission,
  type PermissionInput,
} from "./access-api";
import { ApiError, type Profile } from "./api";
import { maintenanceAPI, utcSecond, type Schedule } from "./maintenance-api";
import {
  beginMaintenanceIntent,
  attemptMaintenanceIntent,
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
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
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
    await waitFor(() => expect(window.location.hash).toBe("#tasks/list"));
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

describe("exact original archive target observations", () => {
  const other = "00000000-0000-4000-8000-000000000012";
  const targets = [
    { taskUUID: tid, visibilityVersion: 0 },
    { taskUUID: other, visibilityVersion: 4 },
  ];
  const warning = "这里只核对该目标当前状态，不确认原操作是否提交。确认原操作仍须使用原幂等键、相同内容和新的验证码。";
  function setup(options: {
    action?: "archive" | "restore"; attempted?: boolean;
    targets?: typeof targets; canRead?: boolean; canWrite?: boolean;
  } = {}) {
    const intent = beginMaintenanceIntent(profile, {
      action: options.action ?? "archive",
      input: {
        reason: "Synthetic original reason",
        ...(options.action === "restore" ? {} : { before: "2026-10-07T00:01:00Z" }),
        targets: options.targets ?? targets,
      },
    });
    if (options.attempted !== false) attemptMaintenanceIntent(profile, intent);
    const context: TaskContext = {
      profile: { ...profile }, canArchiveView: options.canRead !== false,
      can: (operation) => operation.startsWith("GET") ? options.canRead !== false : options.canWrite !== false,
      sessionChanged: vi.fn(),
    };
    const open = vi.fn();
    const view = render(<MaintenanceContent view={{ type: "maintenance-confirm" }} context={context} open={open} />);
    const rerender = () => view.rerender(<MaintenanceContent view={{ type: "maintenance-confirm" }} context={context} open={open} />);
    return { intent, context, open, ...view, rerender };
  }
  const select = (id = tid) => fill("选择待核对目标", id);
  const observe = () => click("读取所选目标当前状态");
  const observed = () => screen.findByLabelText("归档目标当前状态");
  const deferred = () => {
    let resolve!: (response: Response) => void;
    const promise = new Promise<Response>((r) => { resolve = r; });
    return { promise, resolve };
  };
  async function release(pending: ReturnType<typeof deferred>, data: unknown) {
    await act(async () => { pending.resolve(await response(data)); });
  }
  const digest = (intent: ReturnType<typeof maintenanceIntent>) => {
    if (!intent || (intent.action !== "archive" && intent.action !== "restore")) throw new Error("Expected archive input");
    const originalTargets = intent.input.targets.map(({ taskUUID, visibilityVersion }) => ({ taskUUID, visibilityVersion }))
      .sort((a, b) => a.taskUUID < b.taskUUID ? -1 : a.taskUUID > b.taskUUID ? 1 : 0);
    if (new Set(originalTargets.map((t) => t.taskUUID)).size !== originalTargets.length) throw new Error("Duplicate targets");
    const hash = (value: unknown) => createHash("sha256").update(JSON.stringify(value), "utf8").digest("hex");
    return {
      originalIntentSHA256: hash({ action: intent.action, idempotencyKey: intent.key,
        ...(intent.action === "archive" ? { before: intent.input.before } : {}),
        reason: intent.input.reason, targets: originalTargets }),
      originalTargetsSHA256: hash(originalTargets), originalTargetCount: originalTargets.length,
    };
  };
  it.each(["archive", "restore"] as const)("does no automatic reads for attempted %s, then exactly three one-shot GETs", async (action) => {
    const { intent } = setup({ action });
    expect(screen.getByText("尚未核对当前目标状态")).toBeInTheDocument();
    expect(screen.getByRole("combobox")).toHaveValue("");
    expect(screen.getAllByRole("option")).toHaveLength(3);
    expect(fetcher).not.toHaveBeenCalled();
    const before = JSON.stringify(intent), evidence = digest(intent);
    select();
    expect(fetcher).not.toHaveBeenCalled();
    observe();
    observe();
    await observed();
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual([
      "/api/auth/me", `/api/tasks/detail?taskUUID=${tid}&visibility=all`, "/api/auth/me",
    ]);
    expect(fetcher.mock.calls.every(([, init]) => init.method === "GET")).toBe(true);
    expect(screen.getByText(warning)).toBeInTheDocument();
    expect(JSON.stringify(intent)).toBe(before);
    expect(digest(maintenanceIntent(profile))).toEqual(evidence);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
  });
  it("does not expose an observer for an unattempted operation", () => {
    setup({ attempted: false });
    expect(screen.queryByLabelText("选择待核对目标")).not.toBeInTheDocument();
    expect(fetcher).not.toHaveBeenCalled();
  });
  it.each([21, 100])("selects target %i directly without list scans", async (count) => {
    const many = Array.from({ length: count }, (_, i) => ({ taskUUID: `00000000-0000-4000-8000-${String(i + 100).padStart(12, "0")}`, visibilityVersion: i }));
    const last = many[count - 1];
    override = (url) => url.startsWith("/api/tasks/detail?") ? response({ task: { ...task(true), taskUUID: last.taskUUID, visibilityVersion: count + 10 }, events: [] }) : undefined;
    const { intent } = setup({ targets: many });
    select(last.taskUUID); observe();
    expect(await observed()).toHaveTextContent(last.taskUUID);
    expect(calls("detail")).toHaveLength(1);
    expect(calls("detail")[0][0]).toContain(`taskUUID=${last.taskUUID}&visibility=all`);
    expect(intent.input).toHaveProperty("targets", many);
  });
  it.each(["duplicate", "too-many", "negative", "unsafe", "uuid"])("rejects a malformed in-memory target list: %s", (mode) => {
    const { intent, rerender } = setup();
    if (intent.action !== "archive") throw new Error("Expected archive");
    if (mode === "duplicate") intent.input.targets.push({ ...targets[0] });
    if (mode === "too-many") intent.input.targets = Array.from({ length: 101 }, () => ({ ...targets[0] }));
    if (mode === "negative") intent.input.targets[0].visibilityVersion = -1;
    if (mode === "unsafe") intent.input.targets[0].visibilityVersion = Number.MAX_SAFE_INTEGER + 1;
    if (mode === "uuid") intent.input.targets[0].taskUUID = "foreign";
    rerender();
    expect(screen.getByText("原目标清单无效，不能读取当前状态。")).toBeInTheDocument();
    expect(screen.queryByLabelText("选择待核对目标")).not.toBeInTheDocument();
    expect(fetcher).not.toHaveBeenCalled();
  });
  it("requires read permission without requiring mutation permission", async () => {
    const { context, rerender } = setup({ canRead: false, canWrite: false });
    expect(screen.getByRole("button", { name: "读取所选目标当前状态" })).toBeDisabled();
    expect(fetcher).not.toHaveBeenCalled();
    context.canArchiveView = true;
    context.can = (operation) => operation.startsWith("GET");
    rerender(); select(); observe(); await observed();
    expect(screen.getByRole("button", { name: "使用原幂等键核对维护操作" })).toBeDisabled();
    context.canArchiveView = false; rerender();
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
  });
  it.each([
    [403, "forbidden", "当前读取权限被拒绝"],
    [404, "not_found", "当前无法读取所选目标"],
    [422, "result_too_large", "完整任务详情超出现有限制"],
    [500, "internal", "读取未能核实当前状态"],
  ])("clears old values on detail error %i and never retries", async (status, code, label) => {
    const { intent } = setup();
    select(); observe(); await observed();
    const original = JSON.stringify(intent);
    override = (url) => url.startsWith("/api/tasks/detail?") ? response({ error: code }, status as number) : undefined;
    observe();
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    await screen.findByText(new RegExp(label as string));
    vi.useFakeTimers();
    await act(async () => { vi.advanceTimersByTime(32000); });
    expect(fetcher).toHaveBeenCalledTimes(5);
    expect(JSON.stringify(maintenanceIntent(profile))).toBe(original);
  });
  it.each(["wrong-id", "wrong-kind", "wrong-scope", "bad-version", "bad-archived", "network", "malformed"])("rejects unverified detail: %s", async (mode) => {
    setup();
    override = (url) => {
      if (!url.startsWith("/api/tasks/detail?")) return;
      if (mode === "network") return Promise.reject(new Error("Synthetic private transport detail"));
      const current: Record<string, unknown> = { ...task() };
      if (mode === "wrong-id") current.taskUUID = other;
      if (mode === "wrong-kind") current.taskName = "other.kind";
      if (mode === "wrong-scope") current.domainId = "foreign";
      if (mode === "bad-version") current.visibilityVersion = -1;
      if (mode === "bad-archived") current.archived = "yes";
      return response(mode === "malformed" ? {} : { task: current, events: [] });
    };
    select(); observe();
    await screen.findByText("读取未能核实当前状态；请稍后手动重试。");
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("Synthetic private transport detail");
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(maintenanceIntent(profile)?.key).toBeDefined();
  });
  it.each(["ID", "username", "role", "priv", "csrfToken", "needChangePwd", "isExpired"] as const)("invalidates on valid auth identity mismatch in %s", async (field) => {
    const { context } = setup();
    const changed = { ...profile, [field]: typeof profile[field] === "boolean" ? true : typeof profile[field] === "number" ? 2 : "changed" };
    override = (url) => url === "/api/auth/me" ? response(changed) : undefined;
    select(); observe();
    await waitFor(() => expect(context.sessionChanged).toHaveBeenCalledTimes(1));
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    expect(maintenanceIntent(profile)?.attempted).toBe(true);
  });
  it.each([null, {}, { ...profile, ID: 0 }, { ...profile, csrfToken: "" }, { ...profile, priv: "1" }, { ...profile, isExpired: 0 }])("rejects malformed identity without adopting it", async (identity) => {
    const { context } = setup();
    override = (url) => url === "/api/auth/me" ? response(identity) : undefined;
    select(); observe();
    await screen.findByText("读取未能核实当前状态；请稍后手动重试。");
    expect(context.sessionChanged).not.toHaveBeenCalled();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it.each([1, 3])("handles 401 during auth stage %i without displaying task data", async (stage) => {
    const { context } = setup(); let count = 0;
    override = () => ++count === stage ? response({ error: "unauthenticated" }, 401) : undefined;
    select(); observe();
    await waitFor(() => expect(context.sessionChanged).toHaveBeenCalledTimes(1));
    expect(fetcher).toHaveBeenCalledTimes(stage);
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
  });
  it.each([1, 2, 3])("ignores late stage %i after same-user session rotation", async (stage) => {
    const { context, rerender } = setup(); let count = 0; const pending = deferred();
    override = () => ++count === stage ? pending.promise : undefined;
    select(); observe();
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(stage));
    context.profile = { ...profile, csrfToken: "new-session" }; rerender();
    expect((fetcher.mock.calls[stage - 1][1].signal as AbortSignal).aborted).toBe(true);
    await release(pending, stage === 2 ? { task: task(true), events: [] } : profile);
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(stage);
  });
  it("invalidates a postflight mismatch instead of displaying the detail", async () => {
    const { context } = setup(); let auth = 0;
    override = (url) => url === "/api/auth/me" && ++auth === 2 ? response({ ...profile, ID: 2 }) : undefined;
    select(); observe();
    await waitFor(() => expect(context.sessionChanged).toHaveBeenCalledTimes(1));
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
  });
  it.each(["unselected-version", "unselected-id", "action", "key", "before", "reason"])("binds every original input field, including %s", async (field) => {
    const { intent, rerender } = setup(); const pending = deferred();
    override = (url) => url.startsWith("/api/tasks/detail?") ? pending.promise : undefined;
    select(); observe(); await waitFor(() => expect(calls("detail")).toHaveLength(1));
    if (intent.action !== "archive") throw new Error("Expected archive");
    if (field === "unselected-version") intent.input.targets[1].visibilityVersion++;
    if (field === "unselected-id") intent.input.targets[1].taskUUID = sid;
    if (field === "action") {
      const mutableAction: { action: "archive" | "restore" } = intent;
      mutableAction.action = "restore";
    }
    if (field === "key") intent.key = tid;
    if (field === "before") intent.input.before = "2026-10-06T00:01:00Z";
    if (field === "reason") intent.input.reason = "Changed reason";
    rerender(); await release(pending, { task: task(true), events: [] });
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
  it("cancels selection A before reading B and ignores a late abort-insensitive A", async () => {
    setup(); const pending = deferred();
    override = (url) => url.includes(`taskUUID=${tid}`) ? pending.promise : url.includes(`taskUUID=${other}`) ? response({ task: { ...task(true), taskUUID: other, visibilityVersion: 9 }, events: [] }) : undefined;
    select(); observe(); await waitFor(() => expect(calls("detail")).toHaveLength(1));
    select(other);
    expect((calls("detail")[0][1].signal as AbortSignal).aborted).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(2);
    observe(); expect(await observed()).toHaveTextContent(other);
    await release(pending, { task: task(true), events: [] });
    expect(screen.getByLabelText("归档目标当前状态")).toHaveTextContent(other);
    expect(screen.getByLabelText("归档目标当前状态")).not.toHaveTextContent(tid);
  });
  it("bounds the full sequence at 30 seconds, then permits only an explicit new read", async () => {
    setup(); const pending = deferred();
    override = (url) => url === "/api/auth/me" ? pending.promise : undefined;
    vi.useFakeTimers(); select(); observe();
    await act(async () => { vi.advanceTimersByTime(29999); });
    expect(screen.getByText("正在核对所选目标当前状态…")).toBeInTheDocument();
    await act(async () => { vi.advanceTimersByTime(1); });
    expect(screen.getByText("读取超时，当前状态尚未核实；请稍后手动重试。")).toBeInTheDocument();
    await act(async () => { vi.advanceTimersByTime(32000); });
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect((fetcher.mock.calls[0][1].signal as AbortSignal).aborted).toBe(true);
    await release(pending, profile);
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    override = () => undefined; vi.useRealTimers(); observe(); await observed();
    expect(fetcher).toHaveBeenCalledTimes(4);
  });
  it("clears observations on focus and on unmount/reentry without persisting values", async () => {
    const view = setup(); select(); observe(); await observed();
    fireEvent.focus(window);
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    observe(); await observed(); view.unmount();
    expect(sessionStorage.getItem(`adtr.pending-task-maintenance:${profile.ID}:${profile.username}`)).not.toContain("completed");
    render(<MaintenanceContent view={{ type: "maintenance-confirm" }} context={view.context} open={view.open} />);
    expect(screen.getByText("尚未核对当前目标状态")).toBeInTheDocument();
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(6);
  });
  it("aborts unmounted work even if the transport ignores abort", async () => {
    const view = setup(); const pending = deferred();
    override = () => pending.promise;
    select(); observe(); view.unmount();
    expect((fetcher.mock.calls[0][1].signal as AbortSignal).aborted).toBe(true);
    await release(pending, profile);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it("retains complete sorted evidence and the exact original replay body with a fresh proof", async () => {
    const { intent, open } = setup(); const evidence = digest(intent);
    const reordered = { ...intent, input: { ...intent.input, targets: [...targets].reverse() } } as typeof intent;
    expect(digest(reordered)).toEqual(evidence);
    expect(digest({ ...intent, input: { ...intent.input, targets: [targets[0]] } } as typeof intent).originalTargetsSHA256).not.toBe(evidence.originalTargetsSHA256);
    expect(() => digest({ ...intent, input: { ...intent.input, targets: [targets[0], targets[0]] } } as typeof intent)).toThrow("Duplicate targets");
    override = (url, init) => {
      if (url.startsWith("/api/tasks/detail?")) {
        const id = new URL(url, "https://synthetic.invalid").searchParams.get("taskUUID")!;
        return response({ task: { ...task(true), taskUUID: id, visibilityVersion: id === tid ? 1 : 5 }, events: [] });
      }
      if (url === "/api/tasks/archive") {
        const body = JSON.parse(init.body as string);
        expect(digest({ ...intent, key: body.idempotencyKey, input: body })).toEqual(evidence);
        return response({ receipt: { operationUUID: sid, occurredAt: plan().createdAt, action: "archive", reason: body.reason, before: body.before,
          targets: targets.map((t) => ({ ...t, visibilityVersion: t.visibilityVersion + 1, archived: true })),
        }, replayed: true });
      }
    };
    select(); observe(); await observed(); select(other); observe(); await observed();
    expect(maintenanceIntent(profile)?.key).toBe(intent.key);
    expect(screen.getByText(warning)).toBeInTheDocument();
    proof("654321"); click("使用原幂等键核对维护操作");
    await waitFor(() => expect(open).toHaveBeenCalledTimes(1));
    expect(calls("archive")).toHaveLength(1);
    const body = JSON.parse(calls("archive")[0][1].body as string);
    expect(body).toMatchObject({ idempotencyKey: intent.key, targets, totpCode: "654321" });
    expect(maintenanceIntent(profile)).toBeUndefined();
  });
  it.each([1, 2, 3])("synchronously cancels observation stage %i when a proof mutation starts", async (stage) => {
    const { intent } = setup();
    const original = JSON.stringify(intent), pending = deferred(), mutation = deferred();
    let reads = 0;
    override = (url, init) => {
      if (url === "/api/tasks/archive") return mutation.promise;
      if (init.method === "GET" && ++reads === stage) return pending.promise;
    };
    select(); observe();
    await waitFor(() => expect(reads).toBe(stage));
    proof("654321"); click("使用原幂等键核对维护操作");
    expect((fetcher.mock.calls[stage - 1][1].signal as AbortSignal).aborted).toBe(true);
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "读取所选目标当前状态" })).toBeDisabled();
    observe();
    await release(pending, stage === 2 ? { task: task(true), events: [] } : profile);
    expect(reads).toBe(stage);
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    await act(async () => { mutation.resolve(await response({ error: "internal" }, 500)); });
    expect(JSON.stringify(maintenanceIntent(profile))).toBe(original);
    expect(calls("archive")).toHaveLength(1);
    expect(reads).toBe(stage);
  });
  it("counts preflight and detail time against the same postflight deadline", async () => {
    setup();
    const pre = deferred(), detail = deferred(), post = deferred();
    let auth = 0;
    override = (url) => url === "/api/auth/me" ? (++auth === 1 ? pre.promise : post.promise)
      : url.startsWith("/api/tasks/detail?") ? detail.promise : undefined;
    vi.useFakeTimers(); select(); observe();
    await act(async () => { vi.advanceTimersByTime(20000); });
    await release(pre, profile);
    expect(fetcher).toHaveBeenCalledTimes(2);
    await act(async () => { vi.advanceTimersByTime(9000); });
    await release(detail, { task: task(true), events: [] });
    expect(fetcher).toHaveBeenCalledTimes(3);
    await act(async () => { vi.advanceTimersByTime(999); });
    expect(screen.getByText("正在核对所选目标当前状态…")).toBeInTheDocument();
    await act(async () => { vi.advanceTimersByTime(1); });
    expect(screen.getByText("读取超时，当前状态尚未核实；请稍后手动重试。")).toBeInTheDocument();
    expect((fetcher.mock.calls[2][1].signal as AbortSignal).aborted).toBe(true);
    await release(post, profile);
    await act(async () => { vi.advanceTimersByTime(32000); });
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    expect(maintenanceIntent(profile)?.attempted).toBe(true);
  });
  it.each(["ID", "username"] as const)("clears a late result when the mounted owner changes %s", async (field) => {
    const { context, rerender } = setup();
    const pending = deferred();
    override = (url) => url.startsWith("/api/tasks/detail?") ? pending.promise : undefined;
    select(); observe(); await waitFor(() => expect(calls("detail")).toHaveLength(1));
    context.profile = { ...profile, [field]: field === "ID" ? 2 : "other-owner" };
    rerender();
    expect((calls("detail")[0][1].signal as AbortSignal).aborted).toBe(true);
    await release(pending, { task: task(true), events: [] });
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
  it.each(["archive-view", "detail"])("requires and revokes the independent %s read gate", async (gate) => {
    const { context, rerender } = setup();
    if (gate === "archive-view") context.canArchiveView = false;
    else context.can = (operation) => operation !== "GET /api/tasks/detail";
    rerender();
    expect(screen.getByRole("button", { name: "读取所选目标当前状态" })).toBeDisabled();
    expect(fetcher).not.toHaveBeenCalled();
    context.canArchiveView = true; context.can = () => true; rerender();
    select(); observe(); await observed();
    if (gate === "archive-view") context.canArchiveView = false;
    else context.can = (operation) => operation !== "GET /api/tasks/detail";
    rerender();
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "读取所选目标当前状态" })).toBeDisabled();
    expect(fetcher).toHaveBeenCalledTimes(3);
  });
  it.each(["ID", "username", "role", "priv", "csrfToken", "needChangePwd", "isExpired"] as const)("rejects postflight mismatch in %s", async (field) => {
    const { context } = setup(); let auth = 0;
    const changed = { ...profile, [field]: typeof profile[field] === "boolean" ? true : typeof profile[field] === "number" ? 2 : "changed" };
    override = (url) => url === "/api/auth/me" && ++auth === 2 ? response(changed) : undefined;
    select(); observe();
    await waitFor(() => expect(context.sessionChanged).toHaveBeenCalledTimes(1));
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(screen.queryByLabelText("归档目标当前状态")).not.toBeInTheDocument();
    expect(maintenanceIntent(profile)?.attempted).toBe(true);
  });

});

describe("schedule history original-task navigation", () => {
  const older = "00000000-0000-4000-8000-000000000012";
  const latest = "00000000-0000-4000-8000-000000000013";
  type Detail = Awaited<ReturnType<typeof maintenanceAPI.detail>>;
  const event = (taskUUID = tid, id = 2) => ({
    id, action: "admitted", taskUUID, controlVersion: 1,
    createdAt: "2026-10-07T00:00:00Z",
  });
  const history = (taskUUID = tid, pageIdx = 1): Detail => ({
    schedule: { ...plan(), lastTaskUUID: latest },
    page: { ...page, pageIdx, total: 21, totalPage: 2 },
    events: [event(taskUUID)], exhausted: pageIdx === 2,
  });
  const name = (id = tid) => `查看该次实际任务 ${id}`;
  const businessWrites = () => fetcher.mock.calls.filter(([url, init]) =>
    url.startsWith("/api/tasks") && init.method === "POST",
  );
  const deferred = () => {
    let resolve!: (value: Detail) => void;
    let reject!: (error: unknown) => void;
    const promise = new Promise<Detail>((yes, no) => { resolve = yes; reject = no; });
    return { promise, resolve, reject };
  };
  async function release(pending: ReturnType<typeof deferred>, data: Detail) {
    await act(async () => { pending.resolve(data); });
  }
  // Capture the actual React closure, not a new DOM dispatch that would honor
  // disabled or fetch newer props. This exercises pre-effect stale handlers.
  function capture(button: HTMLElement): () => void {
    const key = Object.keys(button).find((value) => value.startsWith("__reactProps$"));
    if (!key) throw new Error("Missing rendered button props");
    const props = (button as unknown as Record<string, { onClick?: () => void }>)[key];
    if (typeof props.onClick !== "function") throw new Error("Missing rendered click handler");
    return props.onClick;
  }
  async function ready(id = tid) {
    const button = await screen.findByRole("button", { name: name(id) });
    await waitFor(() => expect(button).toBeEnabled());
    return button;
  }
  function setup(strict = false) {
    const context: TaskContext = {
      profile: { ...profile }, canArchiveView: true,
      can: (operation) => operation.startsWith("GET"), sessionChanged: vi.fn(),
    };
    const open = vi.fn();
    const content = (nextContext = context, id = sid) => {
      const node = <MaintenanceContent view={{ type: "schedule-detail", id }} context={nextContext} open={open} />;
      return strict ? <StrictMode>{node}</StrictMode> : node;
    };
    const view = render(content());
    return { ...view, context, open, rerender: (nextContext = context, id = sid) => view.rerender(content(nextContext, id)) };
  }
  function serve(data: Detail) {
    override = (url) => {
      if (url.startsWith("/api/tasks/schedules/detail?")) return response(data);
      if (url.startsWith("/api/tasks/detail?")) {
        const id = new URL(url, "https://synthetic.invalid").searchParams.get("taskUUID")!;
        return response({ task: { ...task(), taskUUID: id }, events: [] });
      }
    };
  }
  it("opens each admitted original A/B, including the older row, while latest still opens C", async () => {
    const data = history(); data.events.push(event(older, 1)); serve(data);
    await scheduleDetail();
    const table = screen.getByRole("table", { name: "计划发生记录" });
    const rows = within(table).getAllByRole("row");
    expect(within(rows[2]).getByRole("button", { name: name(older) })).toBeEnabled();
    expect(within(rows[2]).getByText(older, { exact: true })).toBeVisible();
    const original = await ready();
    expect(original).toHaveAttribute("type", "button");
    expect(original).toHaveTextContent("查看该次实际任务");
    for (const [label, id] of [[name(), tid], [name(older), older], ["查看最近实际任务", latest]]) {
      click(label);
      expect(await screen.findByTestId("task-detail-identity")).toHaveTextContent(id);
      expect(calls("detail").at(-1)![0]).toBe(`/api/tasks/detail?taskUUID=${id}&visibility=all`);
      click("周期计划");
      await screen.findByRole("table", { name: "周期计划列表" });
      click(`计划详情 ${sid}`); await ready();
    }
    expect(calls("detail")).toHaveLength(3);
    expect(businessWrites()).toHaveLength(0);
  });
  it.each(["paused", "authorization_blocked"] as const)("allows readable %s history without any write grant", async (state) => {
    const data = history(); data.schedule.state = state;
    vi.spyOn(maintenanceAPI, "detail").mockResolvedValue(data);
    const view = setup(); await ready();
    click(name()); expect(view.open).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: tid, archived: true });
    expect(businessWrites()).toHaveLength(0);
  });
  it("retains non-admitted and malformed task text through the real source validator without a link", async () => {
    const data = history();
    data.events = [
      { ...event(tid, 1), action: "skipped_overlap" },
      { ...event(older, 2), action: "future_action" },
      event("ABCDEFAB-0000-4000-8000-000000000011", 3),
      event(` ${tid}`, 4), event("", 5),
    ];
    serve(data); await scheduleDetail();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^查看该次实际任务 / })).not.toBeInTheDocument();
    expect(screen.getByRole("table", { name: "计划发生记录" })).toHaveTextContent("future_action");
    expect(calls("detail")).toHaveLength(0);
    expect(businessWrites()).toHaveLength(0);
  });
  it.each([
    "unknown-action", "skip-action", "control-action", "empty-id", "uppercase-id",
    "whitespace-id", "noncanonical-id", "zero-event", "unsafe-event", "duplicate-event",
    "wrong-page", "wrong-page-size", "wrong-schedule", "wrong-kind", "wrong-scope", "wrong-version",
  ])("does not enable an untrusted history source: %s", async (mode) => {
    const data = history();
    if (mode === "unknown-action") data.events[0].action = "future_action";
    if (mode === "skip-action") data.events[0].action = "skipped_overlap";
    if (mode === "control-action") data.events[0].action = "paused";
    if (mode === "empty-id") data.events[0].taskUUID = "";
    if (mode === "uppercase-id") data.events[0].taskUUID = "ABCDEFAB-0000-4000-8000-000000000011";
    if (mode === "whitespace-id") data.events[0].taskUUID = ` ${tid}`;
    if (mode === "noncanonical-id") data.events[0].taskUUID = tid.replaceAll("-", "");
    if (mode === "zero-event") data.events[0].id = 0;
    if (mode === "unsafe-event") data.events[0].id = Number.MAX_SAFE_INTEGER + 1;
    if (mode === "duplicate-event") data.events.push(event(older, data.events[0].id));
    if (mode === "wrong-page") data.page.pageIdx = 2;
    if (mode === "wrong-page-size") data.page.pageSize = 10;
    if (mode === "wrong-schedule") data.schedule.scheduleUUID = older;
    if (mode === "wrong-kind") data.schedule.taskName = "unknown.kind";
    if (mode === "wrong-scope") data.schedule.domainId = "foreign";
    if (mode === "wrong-version") data.schedule.payloadVersion = 2;
    // Deliberately bypass API validation to test the local guard as well.
    vi.spyOn(maintenanceAPI, "detail").mockResolvedValue(data);
    const view = setup();
    await screen.findByRole("table", { name: "计划发生记录" });
    for (const button of screen.queryAllByRole("button", { name: /^查看该次实际任务 / })) {
      expect(button).toBeDisabled(); act(() => capture(button)());
    }
    expect(view.open).not.toHaveBeenCalled();
    expect(calls("detail")).toHaveLength(0);
    expect(businessWrites()).toHaveLength(0);
  });
  it.each(["GET /api/tasks/schedules/detail", "GET /api/tasks/detail"] as const)("checks the current %s gate even in an older click closure", async (denied) => {
    vi.spyOn(maintenanceAPI, "detail").mockResolvedValue(history());
    const view = setup();
    const previous = capture(await ready());
    const forbidden = { ...view.context, can: (operation: Parameters<TaskContext["can"]>[0]) => operation.startsWith("GET") && operation !== denied };
    view.rerender(forbidden);
    expect(screen.getByRole("button", { name: name() })).toBeDisabled();
    act(previous); expect(view.open).not.toHaveBeenCalled();
    view.rerender(view.context); await ready();
    click(name()); expect(view.open).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: tid, archived: true });
  });
  it("takes archive visibility from the current context rather than a captured grant", async () => {
    vi.spyOn(maintenanceAPI, "detail").mockResolvedValue(history());
    const view = setup(); const previous = capture(await ready());
    view.rerender({ ...view.context, canArchiveView: false });
    act(previous);
    expect(view.open).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: tid, archived: false });
  });
  it.each([
    { archive: true, status: 200 }, { archive: false, status: 200 },
    { archive: false, status: 403 }, { archive: false, status: 404 },
    { archive: true, status: 403 },
  ])("uses fresh destination authorization without restore: archive=$archive status=$status", async ({ archive, status }) => {
    override = (url, init) => {
      if (url === "/api/access/menu") return response({ menu: metadata().map((item) => ({ ...item, auth: { ...item.auth, writeable: archive && item.mark === "tasks" } })) });
      if (url === "/api/access/check") return response({ results: JSON.parse(init.body as string).paths.map((path: string) => path.startsWith("GET")) });
      if (url.startsWith("/api/tasks/schedules/detail?")) return response(history());
      if (url.startsWith("/api/tasks/detail?")) return status === 200
        ? response({ task: task(archive), events: [] })
        : response({ error: status === 403 ? "forbidden" : "not_found" }, status);
    };
    await scheduleDetail(); await ready();
    expect(screen.queryByRole("button", { name: "启用计划" })).not.toBeInTheDocument();
    click(name());
    if (status === 200) {
      expect(await screen.findByTestId("task-detail-identity")).toHaveTextContent(tid);
      if (archive) await screen.findByText(/任务已归档，原执行结果/);
    } else {
      await screen.findByRole("alert");
      expect(screen.queryByTestId("task-detail-identity")).not.toBeInTheDocument();
    }
    expect(calls("detail")).toHaveLength(1);
    expect(calls("detail")[0][0]).toBe(`/api/tasks/detail?taskUUID=${tid}${archive ? "&visibility=all" : ""}`);
    expect(businessWrites()).toHaveLength(0);
  });
  it("keeps the original target when destination identity validation fails", async () => {
    serve(history()); const prior = override;
    override = (url, init) => url.startsWith("/api/tasks/detail?")
      ? response({ task: { ...task(), taskUUID: latest }, events: [] }) : prior(url, init);
    await scheduleDetail(); await ready(); click(name());
    await screen.findByRole("alert");
    expect(screen.queryByTestId("task-detail-identity")).not.toBeInTheDocument();
    expect(calls("detail")[0][0]).toContain(`taskUUID=${tid}`);
    expect(calls("detail").every(([url]) => !url.includes(`taskUUID=${latest}`))).toBe(true);
    expect(businessWrites()).toHaveLength(0);
  });
  it("invalidates synchronously on refresh and never revives an older closure even for the same DTO", async () => {
    const data = history(), pending = deferred();
    const api = vi.spyOn(maintenanceAPI, "detail").mockResolvedValueOnce(data).mockReturnValueOnce(pending.promise);
    const view = setup(); const previous = capture(await ready());
    const refresh = capture(screen.getByRole("button", { name: "刷新计划详情" }));
    act(() => {
      refresh(); previous();
      expect(api).toHaveBeenCalledTimes(1);
      expect(view.open).not.toHaveBeenCalled();
    });
    expect(screen.getByRole("button", { name: name() })).toBeDisabled();
    await release(pending, data); await ready();
    act(previous); expect(view.open).not.toHaveBeenCalled();
    click(name()); expect(view.open).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: tid, archived: true });
  });
  it("invalidates before pagination effects and rejects wrong-page metadata until a current retry", async () => {
    const pending = deferred();
    const api = vi.spyOn(maintenanceAPI, "detail").mockResolvedValueOnce(history()).mockReturnValueOnce(pending.promise).mockResolvedValueOnce(history(older, 2));
    const view = setup(); const previous = capture(await ready());
    const next = capture(screen.getByRole("button", { name: "下一页" }));
    act(() => {
      next(); previous();
      expect(api).toHaveBeenCalledTimes(1);
      expect(view.open).not.toHaveBeenCalled();
    });
    expect(api.mock.calls[1][1]).toBe(2);
    expect(api.mock.calls[0][2].aborted).toBe(true);
    await release(pending, history(older, 1));
    expect(screen.getByRole("button", { name: name(older) })).toBeDisabled();
    act(() => { previous(); capture(screen.getByRole("button", { name: name(older) }))(); });
    expect(view.open).not.toHaveBeenCalled();
    click("刷新计划详情"); await ready(older);
    expect(api.mock.calls[2][1]).toBe(2);
    act(previous); expect(view.open).not.toHaveBeenCalled();
    click(name(older)); expect(view.open).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: older, archived: true });
  });
  it.each([
    { stale: "success", order: "old-first" }, { stale: "failure", order: "old-first" },
    { stale: "success", order: "new-first" }, { stale: "failure", order: "new-first" },
  ])("keeps newer pending/accepted state through an abort-insensitive $stale ($order)", async ({ stale, order }) => {
    const old = deferred(), current = deferred();
    const api = vi.spyOn(maintenanceAPI, "detail").mockResolvedValueOnce(history()).mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
    const view = setup(); const previous = capture(await ready());
    click("刷新计划详情"); click("刷新计划详情");
    expect(api).toHaveBeenCalledTimes(3);
    expect(api.mock.calls[1][2].aborted).toBe(true);
    const finishOld = () => act(async () => {
      if (stale === "success") old.resolve(history());
      else old.reject(new ApiError("network"));
    });
    if (order === "old-first") {
      await finishOld();
      expect(screen.getByRole("button", { name: name() })).toBeDisabled();
      act(previous); expect(view.open).not.toHaveBeenCalled();
      await release(current, history(older));
    } else {
      await release(current, history(older)); await ready(older);
      await finishOld();
    }
    await ready(older);
    expect(screen.queryByRole("button", { name: name() })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    act(previous); expect(view.open).not.toHaveBeenCalled();
    click(name(older)); expect(view.open).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: older, archived: true });
  });
  it("fences same-key background polling and restores only the current automatic retry", async () => {
    const first = history(); first.schedule.state = "enabled";
    const poll = deferred(), retry = deferred();
    const api = vi.spyOn(maintenanceAPI, "detail").mockResolvedValueOnce(first).mockReturnValueOnce(poll.promise).mockReturnValueOnce(retry.promise);
    vi.useFakeTimers();
    const view = setup(); await act(async () => {});
    const button = screen.getByRole("button", { name: name() });
    expect(button).toBeEnabled(); const previous = capture(button);
    await act(async () => { vi.advanceTimersByTime(1000); });
    expect(api).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("button", { name: name() })).toBeDisabled();
    expect(screen.queryByText("正在读取计划详情…")).not.toBeInTheDocument();
    act(previous); expect(view.open).not.toHaveBeenCalled();
    await act(async () => { poll.reject(new ApiError("network")); });
    expect(screen.queryByRole("table", { name: "计划发生记录" })).not.toBeInTheDocument();
    expect(screen.getByRole("alert")).toBeInTheDocument();
    await act(async () => { vi.advanceTimersByTime(1999); });
    expect(api).toHaveBeenCalledTimes(2);
    await act(async () => { vi.advanceTimersByTime(1); });
    expect(api).toHaveBeenCalledTimes(3);
    act(previous); expect(view.open).not.toHaveBeenCalled();
    await release(retry, history(older));
    expect(screen.getByRole("button", { name: name(older) })).toBeEnabled();
    act(previous); expect(view.open).not.toHaveBeenCalled();
    click(name(older)); expect(view.open).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: older, archived: true });
  });
  it("propagates a current session failure once instead of retaining an enabled old row", async () => {
    const api = vi.spyOn(maintenanceAPI, "detail").mockResolvedValueOnce(history()).mockRejectedValueOnce(new ApiError("unauthenticated", 401));
    const view = setup(); const previous = capture(await ready());
    click("刷新计划详情");
    await waitFor(() => expect(view.context.sessionChanged).toHaveBeenCalledTimes(1));
    act(previous);
    expect(view.open).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: name() })).not.toBeInTheDocument();
    expect(api).toHaveBeenCalledTimes(2);
  });
  it.each(["ID", "username", "csrfToken"] as const)("invalidates both accepted and in-flight history when the current %s changes", async (field) => {
    const pending = deferred();
    vi.spyOn(maintenanceAPI, "detail").mockResolvedValueOnce(history()).mockReturnValueOnce(pending.promise).mockResolvedValueOnce(history(older));
    const view = setup(); const previous = capture(await ready());
    click("刷新计划详情");
    const next = { ...view.context, profile: { ...profile, [field]: field === "ID" ? 2 : "changed" } };
    view.rerender(next);
    act(previous); expect(view.open).not.toHaveBeenCalled();
    await release(pending, history());
    for (const button of screen.queryAllByRole("button", { name: /^查看该次实际任务 / })) expect(button).toBeDisabled();
    act(previous); expect(view.open).not.toHaveBeenCalled();
    click("刷新计划详情"); await ready(older);
    act(previous); expect(view.open).not.toHaveBeenCalled();
    click(name(older)); expect(view.open).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: older, archived: true });
  });
  it.each(["ID", "username", "csrfToken"] as const)("rejects an accepted old callback on current %s replacement without another read", async (field) => {
    vi.spyOn(maintenanceAPI, "detail").mockResolvedValue(history());
    const view = setup(); const previous = capture(await ready());
    view.rerender({ ...view.context, profile: { ...profile, [field]: field === "ID" ? 2 : "changed" } });
    expect(screen.getByRole("button", { name: name() })).toBeDisabled();
    act(previous); expect(view.open).not.toHaveBeenCalled();
  });
  it.each(["membership", "target", "action", "duplicate"])("rechecks exact captured row %s at activation", async (mode) => {
    const data = history();
    vi.spyOn(maintenanceAPI, "detail").mockResolvedValue(data);
    const view = setup(); const previous = capture(await ready());
    if (mode === "membership") data.events = [event()];
    if (mode === "target") data.events[0].taskUUID = older;
    if (mode === "action") data.events[0].action = "skipped_overlap";
    if (mode === "duplicate") data.events.push(event(older, data.events[0].id));
    act(previous); expect(view.open).not.toHaveBeenCalled();
  });
  it("leaves a current failure unavailable even when an older successful response arrives later", async () => {
    const old = deferred(), current = deferred();
    vi.spyOn(maintenanceAPI, "detail").mockResolvedValueOnce(history()).mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise).mockResolvedValueOnce(history(older));
    const view = setup(); const previous = capture(await ready());
    click("刷新计划详情"); click("刷新计划详情");
    await act(async () => { current.reject(new ApiError("forbidden", 403)); });
    await screen.findByRole("alert");
    await release(old, history());
    expect(screen.queryByRole("table", { name: "计划发生记录" })).not.toBeInTheDocument();
    act(previous); expect(view.open).not.toHaveBeenCalled();
    click("刷新计划详情"); await ready(older);
    act(previous); expect(view.open).not.toHaveBeenCalled();
  });
  it("invalidates a captured accepted row immediately when the schedule changes", async () => {
    const pending = deferred();
    vi.spyOn(maintenanceAPI, "detail").mockResolvedValueOnce(history()).mockReturnValueOnce(pending.promise);
    const view = setup(); const previous = capture(await ready());
    view.rerender(view.context, older); act(previous);
    expect(view.open).not.toHaveBeenCalled();
    const current = history(latest); current.schedule.scheduleUUID = older;
    await release(pending, current); await ready(latest);
    act(previous); expect(view.open).not.toHaveBeenCalled();
    click(name(latest)); expect(view.open).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: latest, archived: true });
  });
  it("does not revive callbacks or late results after unmount and reentry", async () => {
    const pending = deferred();
    const api = vi.spyOn(maintenanceAPI, "detail").mockResolvedValueOnce(history()).mockReturnValueOnce(pending.promise).mockResolvedValueOnce(history(older));
    const view = setup(); const previous = capture(await ready());
    click("刷新计划详情"); view.unmount();
    expect(api.mock.calls[1][2].aborted).toBe(true);
    act(previous); expect(view.open).not.toHaveBeenCalled();
    const replacement = setup(); await ready(older);
    await release(pending, history());
    act(previous);
    expect(view.open).not.toHaveBeenCalled(); expect(replacement.open).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: name(older) })).toBeEnabled();
    expect(screen.queryByRole("button", { name: name() })).not.toBeInTheDocument();
  });
  it("restores mounted readiness after StrictMode cleanup while ignoring the first mount result", async () => {
    const old = deferred(), current = deferred();
    const api = vi.spyOn(maintenanceAPI, "detail").mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
    const view = setup(true);
    expect(api).toHaveBeenCalledTimes(2);
    expect(api.mock.calls[0][2].aborted).toBe(true);
    await release(current, history(older)); await ready(older);
    await release(old, history());
    expect(screen.getByRole("button", { name: name(older) })).toBeEnabled();
    expect(screen.queryByRole("button", { name: name() })).not.toBeInTheDocument();
    click(name(older)); expect(view.open).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: older, archived: true });
  });
  it("keeps attempted maintenance and recovery bytes through real actor-bound Back/Forward", async () => {
    const maintenance = beginMaintenanceIntent(profile, {
      action: "enable", input: { scheduleUUID: sid, expectedControlVersion: 1 },
    });
    attemptMaintenanceIntent(profile, maintenance);
    const recovery = beginTaskIntent(profile, "recover", tid);
    attemptTaskIntent(profile, recovery);
    const maintenanceKey = `adtr.pending-task-maintenance:${profile.ID}:${encodeURIComponent(profile.username)}`;
    const recoveryKey = `adtr.pending-task:${profile.ID}:${encodeURIComponent(profile.username)}`;
    const savedMaintenance = sessionStorage.getItem(maintenanceKey);
    const savedRecovery = sessionStorage.getItem(recoveryKey);
    const originalMaintenance = JSON.stringify(maintenance);
    const originalRecovery = JSON.stringify(recovery);
    serve(history()); render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    await revealNavigation("后台任务"); click("后台任务");
    await screen.findByRole("table", { name: "任务列表" });
    click("周期计划"); await screen.findByRole("table", { name: "周期计划列表" });
    click(`计划详情 ${sid}`); await ready();
    expect(screen.getByRole("button", { name: "启用计划" })).toBeDisabled();
    click(name());
    expect(await screen.findByTestId("task-detail-identity")).toHaveTextContent(tid);
    await screen.findByLabelText("恢复幂等键");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
    act(() => window.history.back());
    await screen.findByRole("table", { name: "计划发生记录" }); await ready();
    act(() => window.history.forward());
    expect(await screen.findByTestId("task-detail-identity")).toHaveTextContent(tid);
    expect(sessionStorage.getItem(maintenanceKey)).toBe(savedMaintenance);
    expect(sessionStorage.getItem(recoveryKey)).toBe(savedRecovery);
    expect(JSON.stringify(maintenanceIntent(profile))).toBe(originalMaintenance);
    expect(JSON.stringify(recovery)).toBe(originalRecovery);
    expect(calls("detail").every(([url]) => url === `/api/tasks/detail?taskUUID=${tid}&visibility=all`)).toBe(true);
    expect(businessWrites()).toHaveLength(0);
  });
});
