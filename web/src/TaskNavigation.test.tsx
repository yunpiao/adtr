import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StrictMode } from "react";
import App from "./App";
import TaskWorkspace, {
  TaskDetails,
  attemptTaskIntent,
  beginTaskIntent,
  discardTaskIntent,
  taskIntent,
} from "./TaskWorkspace";
import { labels, marks, type Permission } from "./access-api";
import type { Profile } from "./api";
import { taskOperations, type Task } from "./task-api";
import {
  defaultTaskQuery,
  writeTaskNavigation,
  type TaskListQuery,
  type TaskNavigationSnapshot,
} from "./task-view-state";
import { revealNavigation } from "./test-navigation";
import type { TaskContext } from "./task-common";
import { discardMaintenanceIntent } from "./maintenance-intent";

// App/history integration in jsdom with an isolated synthetic transport. These
// assertions are DOM unit evidence, not real browser/API acceptance evidence.
const firstID = "10000000-0000-4000-8000-000000000001";
const secondID = "10000000-0000-4000-8000-000000000002";
const namespace = "adtr.task-navigation";
const task = (id = firstID, extra: Partial<Task> = {}): Task => ({
  taskUUID: id,
  taskName: "infrastructure.health",
  domainId: "platform",
  payloadVersion: 1,
  state: "succeeded",
  sourceState: "SUCCESS",
  error: "",
  createdAt: "2026-10-10T00:00:00Z",
  updatedAt: "2026-10-10T00:00:01Z",
  attempt: 1,
  maxAttempts: 5,
  progress: 100,
  resultVersion: 1,
  result: { check: "synthetic current response" },
  cursor: null,
  parentTaskUUID: "",
  terminalAt: "2026-10-10T00:00:01Z",
  archived: false,
  visibilityVersion: 0,
  ...extra,
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
function deferred() {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
let profile: Profile;
let menu: Permission[];
let denied: Set<string>;
let override: (url: string, init: RequestInit) => Promise<Response> | undefined;
let fetcher: ReturnType<typeof vi.fn>;
let session = 0;
const calls = (path: string) =>
  fetcher.mock.calls.filter(([url]) => url.split("?")[0] === path);
const queryOf = (url: string) => new URLSearchParams(url.split("?")[1]);
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (name: string, value: string) =>
  fireEvent.change(screen.getByLabelText(name, { exact: true }), {
    target: { value },
  });
function openFilters() {
  const summary = screen.getByText(/^筛选条件/, { selector: "summary" });
  if (!(summary.parentElement as HTMLDetailsElement).open)
    fireEvent.click(summary);
  expect(summary.parentElement).toHaveAttribute("open");
}
function expectInputs(query: TaskListQuery) {
  openFilters();
  expect(screen.getByLabelText("任务种类筛选")).toHaveValue(query.taskName);
  expect(screen.getByLabelText("域 ID 筛选")).toHaveValue(query.domainId);
  expect(screen.getByLabelText("任务状态筛选")).toHaveValue(query.state);
  expect(screen.getByLabelText("任务可见性筛选")).toHaveValue(query.visibility);
  expect(screen.getByLabelText("每页任务数")).toHaveValue(
    String(query.pageSize),
  );
}
const applied: TaskListQuery = {
  pageIdx: 2,
  pageSize: 10,
  domainId: "platform",
  state: "succeeded",
  taskName: "infrastructure.health",
  visibility: "all",
};
async function filterPageTwo() {
  openFilters();
  fill("任务种类筛选", applied.taskName);
  fill("域 ID 筛选", applied.domainId);
  fill("任务状态筛选", applied.state);
  fill("任务可见性筛选", applied.visibility);
  fill("每页任务数", String(applied.pageSize));
  click("筛选任务");
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "下一页" })).toBeEnabled(),
  );
  click("下一页");
  await screen.findByRole("button", { name: `详情 ${secondID}` });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled(),
  );
}
async function openDetail(id = firstID) {
  click(`详情 ${id}`);
  await screen.findByRole("list", { name: "持久化任务事件" });
  expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(id);
}
function currentEntry() {
  return {
    state: JSON.parse(JSON.stringify(window.history.state)),
    hash: window.location.hash,
  };
}
function dispatchEntry(
  entry: ReturnType<typeof currentEntry>,
  event: "popstate" | "hashchange",
) {
  act(() => {
    window.history.replaceState(entry.state, "", entry.hash);
    window.dispatchEvent(
      event === "popstate"
        ? new PopStateEvent("popstate", { state: entry.state })
        : new HashChangeEvent("hashchange"),
    );
  });
}
function save(snapshot: Partial<TaskNavigationSnapshot> = {}) {
  writeTaskNavigation(
    profile,
    {
      view: { type: "list" },
      query: defaultTaskQuery(),
      listReturn: { focusTaskId: "", scrollY: 0 },
      ...snapshot,
    },
    "replace",
  );
}
function expectNoMutation() {
  expect(
    fetcher.mock.calls.filter(
      ([url, init]) => url.startsWith("/api/tasks") && init.method === "POST",
    ),
  ).toHaveLength(0);
}

beforeEach(() => {
  discardTaskIntent();
  localStorage.clear();
  sessionStorage.clear();
  profile = {
    ID: 101,
    username: "navigation_admin",
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
    csrfToken: `navigation-session-${session++}`,
  };
  discardTaskIntent(profile);
  menu = metadata();
  denied = new Set();
  override = () => undefined;
  window.history.replaceState({}, "", "#tasks");
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  vi.spyOn(window, "scrollY", "get").mockReturnValue(144);
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const custom = override(url, init);
    if (custom) return custom;
    const path = url.split("?")[0];
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/menu") return response({ menu });
    if (path === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map(
          (path: string) => !denied.has(path),
        ),
      });
    if (path === "/api/tasks") {
      const query = queryOf(url);
      const pageIdx = Number(query.get("pageIdx") || 1);
      const pageSize = Number(query.get("pageSize") || 20);
      return response({
        page: { pageIdx, pageSize, total: pageSize + 1, totalPage: 2 },
        tasks: [task(pageIdx === 1 ? firstID : secondID)],
        exhausted: pageIdx === 2,
      });
    }
    if (path === "/api/tasks/detail")
      return response({
        task: task(queryOf(url).get("taskUUID")!),
        events: [],
      });
    if (path === "/api/tasks/kinds")
      return response({
        kinds: [
          {
            taskName: "infrastructure.health",
            payloadVersion: 1,
            scope: "platform",
            maxAttempts: 5,
            timeoutSeconds: 30,
          },
        ],
      });
    throw new Error(`Unexpected synthetic request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  discardTaskIntent(profile);
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("task list navigation in the real App component", () => {
  it("opens the original audit detail and preserves the task query and selection across history and remount", async () => {
    const auditTask = task(secondID, { taskName: "audit.export" });
    override = (url) => {
      if (url.startsWith("/api/tasks/detail?"))
        return response({ task: auditTask, events: [] });
      if (url.startsWith("/api/audit/exports/detail?"))
        return Promise.resolve({
          ok: true,
          status: 200,
          headers: new Headers({ "X-ADTR-User-ID": String(profile.ID) }),
          json: async () => ({
            task: auditTask,
            downloadReady: true,
            rowCount: 0,
            snapshotAt: "2026-10-10T00:00:01Z",
            downloadPath: `/api/audit/exports/download?taskUUID=${secondID}`,
          }),
        } as Response);
      return undefined;
    };
    const mounted = render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    await filterPageTwo();
    await openDetail(secondID);
    const saved = currentEntry();
    const authReads = calls("/api/auth/me").length;
    fireEvent.click(screen.getByRole("link", { name: "查看原审计导出" }));
    await screen.findByRole("heading", { name: "审计导出任务" });
    expect(window.location.hash).toBe(`#audit/detail/${secondID}`);
    expect(calls("/api/auth/me").length).toBeGreaterThan(authReads);
    expect(calls("/api/audit/exports/detail").at(-1)![0]).toBe(
      `/api/audit/exports/detail?taskUUID=${secondID}`,
    );
    await screen.findByText(/XLSX 仅包含所选列的表头/);
    act(() => window.history.back());
    await screen.findByTestId("task-detail-identity");
    expect(currentEntry()).toEqual(saved);
    act(() => window.history.forward());
    await screen.findByRole("heading", { name: "审计导出任务" });
    mounted.unmount();
    render(<App />);
    await screen.findByRole("button", { name: "下载 XLSX" });
    expect(window.location.hash).toBe(`#audit/detail/${secondID}`);
    act(() => window.history.back());
    await screen.findByTestId("task-detail-identity");
    click("返回任务列表");
    const selected = await screen.findByRole("button", {
      name: `详情 ${secondID}`,
    });
    expect(selected).toHaveFocus();
    expectInputs(applied);
    expect(calls("/api/audit/exports/download")).toHaveLength(0);
    expect(
      fetcher.mock.calls.filter(
        ([url, init]) =>
          init.method === "POST" && url !== "/api/access/check",
      ),
    ).toHaveLength(0);
  });

  it("uses the original audit endpoint's current denial after following the task link", async () => {
    override = (url) => {
      if (url.startsWith("/api/tasks/detail?"))
        return response({
          task: task(firstID, { taskName: "audit.export" }),
          events: [],
        });
      if (url.startsWith("/api/audit/exports/detail?"))
        return response({ error: "forbidden" }, 403);
      return undefined;
    };
    render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    await openDetail();
    fireEvent.click(screen.getByRole("link", { name: "查看原审计导出" }));
    await screen.findByRole("heading", { name: "审计导出任务" });
    await screen.findByRole("alert");
    expect(screen.queryByRole("button", { name: "下载 XLSX" })).toBeNull();
    expect(calls("/api/audit/exports/detail").length).toBeGreaterThan(0);
    expect(calls("/api/audit/exports/download")).toHaveLength(0);
    expectNoMutation();
  });

  it("never restores an old audit link after a pending task read changes actor", async () => {
    save({ view: { type: "detail", id: firstID } });
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/tasks/detail?") ? held.promise : undefined;
    const mounted = render(
      <TaskWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await waitFor(() => expect(calls("/api/tasks/detail")).toHaveLength(1));
    expect(screen.queryByRole("link", { name: "查看原审计导出" })).toBeNull();
    const signal = calls("/api/tasks/detail")[0][1].signal;
    mounted.rerender(
      <TaskWorkspace
        profile={{
          ...profile,
          ID: 102,
          username: "navigation_other",
          csrfToken: "navigation-replaced-session",
        }}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByRole("table", { name: "任务列表" });
    expect(signal?.aborted).toBe(true);
    await act(async () =>
      held.resolve(
        await response({
          task: task(firstID, { taskName: "audit.export" }),
          events: [],
        }),
      ),
    );
    expect(screen.queryByRole("link", { name: "查看原审计导出" })).toBeNull();
    expect(screen.queryByTestId("task-detail-identity")).toBeNull();
    expect(calls("/api/audit/exports/detail")).toHaveLength(0);
    expectNoMutation();
  });

  it("preserves applied page two filters and row focus, but drops input drafts on detail return", async () => {
    window.history.replaceState({}, "", "#account");
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    await revealNavigation("后台任务");
    click("后台任务");
    await screen.findByRole("table", { name: "任务列表" });
    const panel = screen.getByText(/^筛选条件/, { selector: "summary" });
    expect(panel.parentElement).not.toHaveAttribute("open");
    expect(screen.getByRole("button", { name: "筛选任务" })).not.toBeVisible();
    await filterPageTwo();
    openFilters();
    fill("任务种类筛选", "unapplied.private.draft");
    fill("域 ID 筛选", "unapplied-domain");
    await openDetail(secondID);
    expect(screen.getByRole("region", { name: "任务执行详情" })).toBeVisible();
    expect(window.history.state[namespace].query).toEqual(applied);
    expect(window.history.state[namespace].listReturn).toEqual({
      focusTaskId: secondID,
      scrollY: 144,
    });
    click("返回任务列表");
    const row = await screen.findByRole("button", { name: `详情 ${secondID}` });
    expect(row).toHaveFocus();
    expect(window.scrollTo).toHaveBeenLastCalledWith(0, 144);
    expectInputs(applied);
    expect(Object.fromEntries(queryOf(calls("/api/tasks").at(-1)![0]))).toEqual(
      {
        ...applied,
        pageIdx: "2",
        pageSize: "10",
      },
    );
    expect(JSON.stringify(window.history.state)).not.toContain("unapplied");
    expect(localStorage.length + sessionStorage.length).toBe(0);
    expectNoMutation();
  });

  it("clears every applied filter and page size before reading an actual empty default list", async () => {
    render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    await filterPageTwo();
    override = (url) =>
      url === "/api/tasks?pageIdx=1&pageSize=20"
        ? response({
            page: { pageIdx: 1, pageSize: 20, total: 0, totalPage: 0 },
            tasks: [],
            exhausted: true,
          })
        : undefined;
    openFilters();
    click("清除任务筛选");
    await screen.findByText("没有符合条件的任务。");
    expectInputs(defaultTaskQuery());
    expect(window.history.state[namespace].query).toEqual(defaultTaskQuery());
    expect(calls("/api/tasks").at(-1)![0]).toBe(
      "/api/tasks?pageIdx=1&pageSize=20",
    );
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
  });

  it("clears unapplied drafts even when the applied query is already empty", async () => {
    render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    openFilters();
    fill("任务种类筛选", "draft-only");
    fill("域 ID 筛选", "draft-domain");
    fill("任务状态筛选", "failed");
    fill("任务可见性筛选", "archived");
    fill("每页任务数", "100");
    click("清除任务筛选");
    expectInputs(defaultTaskQuery());
    expect(window.history.state[namespace].query).toEqual(defaultTaskQuery());
    expect(calls("/api/tasks").every(([url]) => !url.includes("draft"))).toBe(
      true,
    );
  });

  it("restores an applied list on App remount without restoring unsent filter drafts", async () => {
    const mounted = render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    await filterPageTwo();
    openFilters();
    fill("任务种类筛选", "unsubmitted-list-draft");
    fill("域 ID 筛选", "unsubmitted-domain");
    fill("每页任务数", "100");
    mounted.unmount();
    fetcher.mockClear();
    render(<App />);
    await screen.findByRole("button", { name: `详情 ${secondID}` });
    expectInputs(applied);
    expect(calls("/api/auth/me")).toHaveLength(1);
    expect(calls("/api/access/menu")).toHaveLength(1);
    expect(calls("/api/access/check")).toHaveLength(1);
    expect(calls("/api/tasks")).toHaveLength(1);
    expect(queryOf(calls("/api/tasks")[0][0]).get("pageIdx")).toBe("2");
    expect(JSON.stringify(window.history.state)).not.toContain("unsubmitted");
  });

  it.each(["popstate", "hashchange"] as const)(
    "restores only the matching history entry after %s and runs fresh auth, permission and read requests",
    async (event) => {
      render(<App />);
      await screen.findByRole("table", { name: "任务列表" });
      await filterPageTwo();
      const listEntry = currentEntry();
      await openDetail(secondID);
      const detailEntry = currentEntry();
      const before = fetcher.mock.calls.length;
      dispatchEntry(listEntry, event);
      await screen.findByRole("button", { name: `详情 ${secondID}` });
      expectInputs(applied);
      expect(screen.queryByRole("region", { name: "任务执行详情" })).toBeNull();
      const refreshed = fetcher.mock.calls.slice(before).map(([url]) => url);
      expect(refreshed).toEqual(
        expect.arrayContaining([
          "/api/auth/me",
          "/api/access/menu",
          "/api/access/check",
          expect.stringContaining("/api/tasks?pageIdx=2&pageSize=10"),
        ]),
      );
      dispatchEntry(detailEntry, event);
      await screen.findByTestId("task-detail-identity");
      expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(
        secondID,
      );
      expect(calls("/api/tasks/detail")).toHaveLength(2);
      expect(window.history.state[namespace].query).toEqual(applied);
      expectNoMutation();
    },
  );

  it("uses actual history back and forward through App remounts without losing the selected page", async () => {
    render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    await filterPageTwo();
    await openDetail(secondID);
    act(() => window.history.back());
    await screen.findByRole("button", { name: `详情 ${secondID}` });
    expectInputs(applied);
    act(() => window.history.forward());
    await screen.findByTestId("task-detail-identity");
    expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(
      secondID,
    );
    expect(window.history.state[namespace].query).toEqual(applied);
  });

  it.each([`#tasks/detail/${firstID}`, "#tasks/unknown"])(
    "falls back to the default list for a direct unowned route %s",
    async (hash) => {
      window.history.replaceState({}, "", hash);
      render(<App />);
      await screen.findByRole("table", { name: "任务列表" });
      expectInputs(defaultTaskQuery());
      expect(calls("/api/tasks/detail")).toHaveLength(0);
      expect(window.location.hash).toBe("#tasks/list");
      expectNoMutation();
    },
  );

  it("rejects a stale history entry whose current URL identifies a different task", async () => {
    save({ view: { type: "detail", id: firstID }, query: applied });
    window.history.replaceState(
      window.history.state,
      "",
      `#tasks/detail/${secondID}`,
    );
    render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    expectInputs(defaultTaskQuery());
    expect(calls("/api/tasks/detail")).toHaveLength(0);
  });
});

describe("fresh reads and permission gates on restoration", () => {
  it("restores detail on a complete App remount only after auth and both permission responses", async () => {
    const first = render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    await filterPageTwo();
    await openDetail(secondID);
    first.unmount();
    fetcher.mockClear();
    const auth = deferred();
    const checks = deferred();
    const details = deferred();
    override = (url) => {
      if (url === "/api/auth/me") return auth.promise;
      if (url === "/api/access/check") return checks.promise;
      if (url.startsWith("/api/tasks/detail?")) return details.promise;
      return undefined;
    };
    render(<App />);
    expect(screen.queryByTestId("task-detail-identity")).toBeNull();
    expect(calls("/api/access/check")).toHaveLength(0);
    await act(async () => auth.resolve(await response(profile)));
    await waitFor(() => expect(calls("/api/access/check")).toHaveLength(1));
    expect(calls("/api/tasks/detail")).toHaveLength(0);
    await act(async () =>
      checks.resolve(
        await response({ results: taskOperations.map(() => true) }),
      ),
    );
    await waitFor(() => expect(calls("/api/tasks/detail")).toHaveLength(1));
    expect(calls("/api/tasks")).toHaveLength(0);
    expect(screen.queryByTestId("task-detail-identity")).toBeNull();
    await act(async () =>
      details.resolve(
        await response({
          task: task(secondID, { result: { fresh: "after-remount" } }),
          events: [],
        }),
      ),
    );
    expect(await screen.findByTestId("task-detail-identity")).toHaveTextContent(
      secondID,
    );
    expect(screen.getByLabelText("任务实际结果")).toHaveTextContent(
      "after-remount",
    );
    expect(window.history.state[namespace].query).toEqual(applied);
    expect(calls("/api/access/check")[0][1].headers).toMatchObject({
      "X-CSRF-Token": profile.csrfToken,
    });
    expectNoMutation();
  });

  it.each(["detail operation", "list operation", "task menu"])(
    "blocks saved detail when current %s permission is missing",
    async (permission) => {
      save({ view: { type: "detail", id: firstID } });
      if (permission === "detail operation")
        denied.add("GET /api/tasks/detail");
      if (permission === "list operation") denied.add("GET /api/tasks");
      if (permission === "task menu")
        menu.find((node) => node.mark === "tasks")!.auth.readable = false;
      render(<App />);
      await screen.findByText(
        permission === "detail operation"
          ? "当前账户没有此任务详情的读取权限，请返回执行任务列表。"
          : "当前账户没有后台任务读取权限。",
      );
      expect(calls("/api/tasks/detail")).toHaveLength(0);
      expect(calls("/api/tasks")).toHaveLength(0);
      expect(screen.queryByTestId("task-detail-identity")).toBeNull();
      expectNoMutation();
    },
  );

  it.each(["archive menu", "task write"])(
    "does not broaden or silently downgrade a saved archived detail after losing %s permission",
    async (permission) => {
      save({
        view: { type: "detail", id: firstID, archived: true },
        query: applied,
      });
      menu.find(
        (node) =>
          node.mark ===
          (permission === "archive menu" ? "task_archive" : "tasks"),
      )!.auth[permission === "archive menu" ? "readable" : "writeable"] = false;
      render(<App />);
      await screen.findByText(
        "当前账户没有此任务详情的读取权限，请返回执行任务列表。",
      );
      expect(calls("/api/tasks/detail")).toHaveLength(0);
      expect(calls("/api/tasks")).toHaveLength(0);
      expectNoMutation();
    },
  );

  it("requires explicit default-list selection when archived filter permission has been lost", async () => {
    save({ query: applied });
    menu.find((node) => node.mark === "task_archive")!.auth.readable = false;
    render(<App />);
    await screen.findByText("当前账户没有已保存可见性筛选的读取权限。");
    expect(calls("/api/tasks")).toHaveLength(0);
    click("返回默认可见任务");
    await screen.findByRole("table", { name: "任务列表" });
    expect(calls("/api/tasks")).toHaveLength(1);
    expect(calls("/api/tasks")[0][0]).toBe("/api/tasks?pageIdx=1&pageSize=20");
    openFilters();
    expect(screen.queryByLabelText("任务可见性筛选")).toBeNull();
    expect(window.history.state[namespace].query).toEqual(defaultTaskQuery());
  });

  it.each([false, true])(
    "reads the restored task through the current API with archived=%s and clears denied results",
    async (archived) => {
      save({ view: { type: "detail", id: firstID, archived } });
      override = (url) =>
        url.startsWith("/api/tasks/detail?")
          ? response({ error: "forbidden" }, 403)
          : undefined;
      render(<App />);
      await screen.findByRole("alert");
      expect(calls("/api/tasks/detail")).toHaveLength(1);
      expect(queryOf(calls("/api/tasks/detail")[0][0]).get("visibility")).toBe(
        archived ? "all" : null,
      );
      expect(screen.queryByTestId("task-detail-identity")).toBeNull();
      expect(screen.queryByLabelText("任务实际结果")).toBeNull();
      expect(screen.queryByRole("list", { name: "持久化任务事件" })).toBeNull();
      expectNoMutation();
    },
  );
});

describe("identity and outstanding response isolation", () => {
  it("clears an old task view during App session replacement and ignores its late detail", async () => {
    const late = deferred();
    override = (url) =>
      url.startsWith("/api/tasks/detail?") ? late.promise : undefined;
    render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    await filterPageTwo();
    click(`详情 ${secondID}`);
    await waitFor(() => expect(calls("/api/tasks/detail")).toHaveLength(1));
    const oldSignal = calls("/api/tasks/detail")[0][1].signal as AbortSignal;
    profile = { ...profile, csrfToken: `${profile.csrfToken}-rotated` };
    act(() => window.dispatchEvent(new Event("focus")));
    await screen.findByRole("heading", { name: "账户概览" });
    expect(oldSignal.aborted).toBe(true);
    await act(async () =>
      late.resolve(
        await response({
          task: task(secondID, {
            result: { stale: "old-app-session-response" },
          }),
          events: [],
        }),
      ),
    );
    expect(screen.queryByRole("region", { name: "任务执行详情" })).toBeNull();
    expect(screen.queryByText(/old-app-session-response/)).toBeNull();
    await revealNavigation("后台任务");
    click("后台任务");
    await screen.findByRole("table", { name: "任务列表" });
    expectInputs(defaultTaskQuery());
    expect(calls("/api/access/check").at(-1)![1].headers).toMatchObject({
      "X-CSRF-Token": profile.csrfToken,
    });
    expect(calls("/api/tasks/detail")).toHaveLength(1);
    expectNoMutation();
  });

  it("ignores an old permission grant when a replacement session is currently denied", async () => {
    const oldChecks = deferred();
    let checks = 0;
    override = (url) =>
      url === "/api/access/check" && checks++ === 0
        ? oldChecks.promise
        : undefined;
    save({ view: { type: "detail", id: firstID } });
    const mounted = render(
      <TaskWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await waitFor(() => expect(calls("/api/access/check")).toHaveLength(1));
    const oldSignal = calls("/api/access/check")[0][1].signal as AbortSignal;
    denied.add("GET /api/tasks/detail");
    profile = { ...profile, csrfToken: `${profile.csrfToken}-replacement` };
    mounted.rerender(
      <TaskWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByText(
      "当前账户没有此任务详情的读取权限，请返回执行任务列表。",
    );
    await act(async () =>
      oldChecks.resolve(
        await response({ results: taskOperations.map(() => true) }),
      ),
    );
    expect(oldSignal.aborted).toBe(true);
    expect(calls("/api/tasks/detail")).toHaveLength(0);
    expect(calls("/api/tasks")).toHaveLength(0);
    expect(
      screen.getByText(
        "当前账户没有此任务详情的读取权限，请返回执行任务列表。",
      ),
    ).toBeVisible();
    expectNoMutation();
  });

  it("ignores an old list response after a newer applied filter read finishes", async () => {
    const late = deferred();
    override = (url) =>
      url === "/api/tasks?pageIdx=1&pageSize=20" ? late.promise : undefined;
    render(<App />);
    await waitFor(() => expect(calls("/api/tasks")).toHaveLength(1));
    const oldSignal = calls("/api/tasks")[0][1].signal as AbortSignal;
    openFilters();
    fill("域 ID 筛选", "platform");
    click("筛选任务");
    await screen.findByRole("button", { name: `详情 ${firstID}` });
    await act(async () =>
      late.resolve(
        await response({
          page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
          tasks: [task(secondID, { domainId: "stale-domain" })],
          exhausted: true,
        }),
      ),
    );
    expect(oldSignal.aborted).toBe(true);
    expect(
      screen.getByRole("button", { name: `详情 ${firstID}` }),
    ).toBeVisible();
    expect(
      screen.queryByRole("button", { name: `详情 ${secondID}` }),
    ).toBeNull();
    expect(screen.queryByText("stale-domain")).toBeNull();
    expect(window.history.state[namespace].query.domainId).toBe("platform");
  });

  it.each(["ID", "username"] as const)(
    "discards saved selection/query when the current account changes only its %s",
    async (field) => {
      save({ view: { type: "detail", id: firstID }, query: applied });
      profile =
        field === "ID"
          ? { ...profile, ID: profile.ID + 1 }
          : { ...profile, username: "replacement_admin" };
      render(<App />);
      await screen.findByRole("table", { name: "任务列表" });
      expectInputs(defaultTaskQuery());
      expect(calls("/api/tasks/detail")).toHaveLength(0);
      expect(window.history.state[namespace].owner).toEqual({
        ID: profile.ID,
        username: profile.username,
      });
    },
  );

  it.each(["ID", "username", "csrfToken"] as const)(
    "ignores an abort-insensitive old detail when the workspace profile replaces %s",
    async (field) => {
      const late = deferred();
      let first = true;
      override = (url) => {
        if (!url.startsWith("/api/tasks/detail?")) return undefined;
        if (first) {
          first = false;
          return late.promise;
        }
        return response({
          task: task(firstID, { result: { fresh: "replacement-session" } }),
          events: [],
        });
      };
      const mounted = render(
        <TaskWorkspace profile={profile} sessionChanged={vi.fn()} />,
      );
      await screen.findByRole("table", { name: "任务列表" });
      click(`详情 ${firstID}`);
      await waitFor(() => expect(calls("/api/tasks/detail")).toHaveLength(1));
      const oldSignal = calls("/api/tasks/detail")[0][1].signal as AbortSignal;
      profile =
        field === "ID"
          ? { ...profile, ID: profile.ID + 1 }
          : { ...profile, [field]: `${profile[field]}-replacement` };
      mounted.rerender(
        <TaskWorkspace profile={profile} sessionChanged={vi.fn()} />,
      );
      if (field === "csrfToken") {
        await screen.findByTestId("task-detail-identity");
        expect(screen.getByLabelText("任务实际结果")).toHaveTextContent(
          "replacement-session",
        );
        expect(calls("/api/tasks/detail")).toHaveLength(2);
      } else {
        await screen.findByRole("table", { name: "任务列表" });
        expect(calls("/api/tasks/detail")).toHaveLength(1);
      }
      expect(oldSignal.aborted).toBe(true);
      await act(async () =>
        late.resolve(
          await response({
            task: task(firstID, { result: { stale: "old-account-response" } }),
            events: [],
          }),
        ),
      );
      expect(screen.queryByText(/old-account-response/)).toBeNull();
      if (field !== "csrfToken")
        expect(screen.queryByTestId("task-detail-identity")).toBeNull();
      expect(calls("/api/access/check")).toHaveLength(2);
      expectNoMutation();
    },
  );

  it("ignores a late detail after navigating to another task without waiting for AbortSignal support", async () => {
    const late = deferred();
    override = (url) =>
      url.startsWith("/api/tasks/detail?") &&
      queryOf(url).get("taskUUID") === firstID
        ? late.promise
        : undefined;
    render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    click(`详情 ${firstID}`);
    await waitFor(() => expect(calls("/api/tasks/detail")).toHaveLength(1));
    const oldSignal = calls("/api/tasks/detail")[0][1].signal as AbortSignal;
    click("返回任务列表");
    await screen.findByRole("table", { name: "任务列表" });
    click("下一页");
    await screen.findByRole("button", { name: `详情 ${secondID}` });
    await openDetail(secondID);
    await act(async () =>
      late.resolve(
        await response({
          task: task(firstID, {
            result: { stale: "superseded-task-response" },
          }),
          events: [],
        }),
      ),
    );
    expect(oldSignal.aborted).toBe(true);
    expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(
      secondID,
    );
    expect(screen.queryByText(/superseded-task-response/)).toBeNull();
    expect(window.history.state[namespace].view.id).toBe(secondID);
  });
});

describe("safe navigation state excludes forms and private response bytes", () => {
  it("never writes result, cursor, timeline, proof or session bytes and drops an unsubmitted cancellation form on App remount", async () => {
    override = (url) =>
      url.startsWith("/api/tasks/detail?")
        ? response({
            task: task(firstID, {
              state: "running",
              sourceState: "RUNNING",
              terminalAt: null,
              result: { marker: "private-response-marker" },
              cursor: { marker: "private-cursor-marker" },
            }),
            events: [
              {
                id: 1,
                action: "private-event-marker",
                state: "running",
                attempt: 1,
                resultVersion: 1,
                createdAt: "2026-10-10T00:00:01Z",
              },
            ],
          })
        : undefined;
    const mounted = render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    await openDetail();
    expect(
      within(screen.getByRole("list", { name: "持久化任务事件" })).getByText(
        "private-event-marker",
        { selector: "strong" },
      ),
    ).toBeVisible();
    click("请求取消任务");
    fill("操作者当前密码", "Synthetic Private Actor Password 123");
    fill("未使用的认证器验证码", "654321");
    const saved = JSON.stringify(window.history.state);
    for (const forbidden of [
      "private-response-marker",
      "private-cursor-marker",
      "private-event-marker",
      "Synthetic Private Actor Password 123",
      "654321",
      profile.csrfToken,
      "actorPassword",
      "totpCode",
      "payload",
      "result",
    ])
      expect(saved).not.toContain(forbidden);
    expect(Object.keys(window.history.state)).toEqual([namespace]);
    expect(Object.keys(window.history.state[namespace]).sort()).toEqual([
      "hash",
      "listReturn",
      "owner",
      "query",
      "version",
      "view",
    ]);
    expect(localStorage.length + sessionStorage.length).toBe(0);
    mounted.unmount();
    const fresh = deferred();
    override = (url) =>
      url.startsWith("/api/tasks/detail?") ? fresh.promise : undefined;
    render(<App />);
    await screen.findByRole("region", { name: "任务执行详情" });
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    expect(screen.queryByText(/private-response-marker/)).toBeNull();
    expect(screen.queryByLabelText("任务实际结果")).toBeNull();
    await act(async () =>
      fresh.resolve(await response({ task: task(), events: [] })),
    );
    await screen.findByTestId("task-detail-identity");
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    expect(screen.queryByText(/private-response-marker/)).toBeNull();
    expectNoMutation();
  });

  it("restores an unsubmitted submit route as a safe list without proof fields or an automatic submission", async () => {
    const mounted = render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    click("提交健康检查");
    await screen.findByLabelText("操作者当前密码");
    fill("操作者当前密码", "Synthetic Submit Password 123");
    fill("未使用的认证器验证码", "123456");
    expect(window.location.hash).toBe("#tasks/submit");
    expect(JSON.stringify(window.history.state)).not.toContain(
      "Synthetic Submit Password 123",
    );
    mounted.unmount();
    render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    expect(window.location.hash).toBe("#tasks/list");
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    expect(screen.queryByLabelText("提交幂等键")).toBeNull();
    expectNoMutation();
  });

  it("rejects history containing a payload/response object instead of importing it into App", async () => {
    save({ view: { type: "detail", id: firstID }, query: applied });
    const unsafe = currentEntry();
    unsafe.state[namespace].payload = {
      actorPassword: "synthetic-injected-secret",
      result: "injected-response",
    };
    window.history.replaceState(unsafe.state, "", unsafe.hash);
    render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    expectInputs(defaultTaskQuery());
    expect(calls("/api/tasks/detail")).toHaveLength(0);
    expect(screen.queryByText(/injected-response/)).toBeNull();
    expect(JSON.stringify(window.history.state)).not.toContain(
      "synthetic-injected-secret",
    );
    expectNoMutation();
  });
});

describe("task visibility remains in the fixed identity", () => {
  it.each([false, true])(
    "shows archived=%s while metadata stays collapsed",
    async (archived) => {
      writeTaskNavigation(
        profile,
        {
          view: { type: "detail", id: firstID, archived },
          query: defaultTaskQuery(),
        },
        "replace",
      );
      override = (url) =>
        url.startsWith("/api/tasks/detail?")
          ? response({
              task: task(firstID, {
                archived,
                visibilityVersion: archived ? 1 : 2,
              }),
              events: [],
            })
          : undefined;
      render(<TaskWorkspace profile={profile} sessionChanged={vi.fn()} />);
      await screen.findByTestId("task-detail-identity");
      const value = archived ? "已归档" : "默认可见";
      expect(screen.getAllByText(value, { exact: true })).toHaveLength(1);
      expect(screen.getByText(value, { exact: true })).toBeVisible();
      expect(
        document.querySelector(".task-metadata-panel"),
      ).not.toHaveAttribute("open");
      expect(document.querySelector(".task-detail-identity")).toHaveTextContent(
        `可见性：${value}`,
      );
      expectNoMutation();
    },
  );
});

// Direct component tests exercise real useTaskRead and taskAPI transport. Only
// synthetic server responses are supplied; App cases below cover actual routing.
describe("current direct-parent detail action", () => {
  afterEach(() => discardMaintenanceIntent(profile));
  const parentID = secondID;
  const ancestorID = "aaaaaaaa-0000-4000-8000-000000000003";
  const name = (id = parentID) => `查看父任务 ${id}`;
  const child = (extra: Partial<Task> = {}) =>
    task(firstID, { parentTaskUUID: parentID, ...extra });
  function serve(extra: Partial<Task> = {}) {
    override = (url) => {
      if (!url.startsWith("/api/tasks/detail?")) return undefined;
      const id = queryOf(url).get("taskUUID");
      return response({
        task:
          id === firstID
            ? child(extra)
            : task(id!, { parentTaskUUID: id === parentID ? ancestorID : "" }),
        events: [],
      });
    };
  }
  // Retain the actual rendered React callback so disabled DOM dispatch and new
  // props cannot conceal a stale-closure bug in the event-time fence.
  function capture(button: HTMLElement): () => void {
    const key = Object.keys(button).find((value) =>
      value.startsWith("__reactProps$"),
    );
    if (!key) throw new Error("Missing rendered button props");
    const props = (
      button as unknown as Record<string, { onClick?: () => void }>
    )[key];
    if (typeof props.onClick !== "function")
      throw new Error("Missing rendered click handler");
    return props.onClick;
  }
  // Reconcile has no standalone DOM control. Read its actual committed
  // TaskAction prop without mocking the shared hook or replacing TaskAction.
  function actionReconcile(form: HTMLElement): () => void {
    type Fiber = {
      return?: Fiber;
      memoizedProps?: { reconcile?: () => void };
    };
    const key = Object.keys(form).find((value) =>
      value.startsWith("__reactFiber$"),
    );
    if (!key) throw new Error("Missing rendered form fiber");
    let fiber: Fiber | undefined = (
      form as unknown as Record<string, Fiber>
    )[key];
    while (fiber) {
      if (typeof fiber.memoizedProps?.reconcile === "function")
        return fiber.memoizedProps.reconcile;
      fiber = fiber.return;
    }
    throw new Error("Missing committed reconcile callback");
  }
  function setup(options: { archived?: boolean; strict?: boolean } = {}) {
    let context: TaskContext = {
      profile: { ...profile },
      canArchiveView: true,
      can: (operation) => operation.startsWith("GET"),
      sessionChanged: vi.fn(),
    };
    let id = firstID;
    let archived = options.archived ?? false;
    let done = vi.fn();
    const content = () => {
      const node = (
        <TaskDetails context={context} id={id} archived={archived} done={done} />
      );
      return options.strict ? <StrictMode>{node}</StrictMode> : node;
    };
    const mounted = render(content());
    return {
      ...mounted,
      get context() {
        return context;
      },
      get done() {
        return done;
      },
      change(next: {
        context?: TaskContext;
        id?: string;
        archived?: boolean;
        done?: ReturnType<typeof vi.fn>;
      }) {
        context = next.context ?? context;
        id = next.id ?? id;
        archived = next.archived ?? archived;
        done = next.done ?? done;
        mounted.rerender(content());
      },
    };
  }
  const ready = (id = parentID) =>
    screen.findByRole("button", { name: name(id) });
  async function release(
    pending: ReturnType<typeof deferred>,
    data: Task,
    status = 200,
  ) {
    await act(async () => {
      pending.resolve(
        await response(status === 200 ? { task: data, events: [] } : { error: "forbidden" }, status),
      );
    });
  }

  it("opens the exact immediate parent, never the child or an ancestor, using read-only authority", async () => {
    serve();
    const view = setup();
    view.change({ context: { ...view.context, canArchiveView: false } });
    const button = await ready();
    expect(button).toHaveAttribute("type", "button");
    expect(button).toHaveTextContent("查看父任务");
    expect(document.querySelector(".task-parent-identity span")).toHaveTextContent(parentID);
    fireEvent.click(button);
    expect(view.done).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: parentID, archived: false });
    expect(screen.queryByRole("button", { name: "创建恢复任务" })).toBeNull();
    expectNoMutation();
  });

  it.each(["", "not-a-uuid", "AAAAAAAA-0000-4000-8000-000000000003", firstID])(
    "preserves available parent text but rejects unusable parent %s",
    async (parentTaskUUID) => {
      serve({ parentTaskUUID });
      const view = setup();
      await screen.findByTestId("task-detail-identity");
      expect(screen.queryByRole("button", { name: /^查看父任务 / })).toBeNull();
      if (parentTaskUUID)
        expect(document.querySelector(".task-parent-identity")).toHaveTextContent(parentTaskUUID);
      expect(view.done).not.toHaveBeenCalled();
      expect(calls("/api/tasks/detail")).toHaveLength(1);
      expectNoMutation();
    },
  );

  it.each(["wrong child", "denied read"])(
    "does not create a parent action after %s",
    async (failure) => {
      override = (url) => url.startsWith("/api/tasks/detail?")
        ? failure === "wrong child"
          ? response({ task: task(parentID, { parentTaskUUID: ancestorID }), events: [] })
          : response({ error: "forbidden" }, 403)
        : undefined;
      const view = setup();
      await screen.findByRole("alert");
      expect(screen.queryByRole("button", { name: /^查看父任务 / })).toBeNull();
      expect(screen.queryByTestId("task-detail-identity")).toBeNull();
      expect(view.done).not.toHaveBeenCalled();
      expectNoMutation();
    },
  );

  it("has no parent action before the initial response and synchronously fences toolbar refresh", async () => {
    const first = deferred();
    const refreshed = deferred();
    let reads = 0;
    override = (url) => url.startsWith("/api/tasks/detail?")
      ? ++reads === 1 ? first.promise : refreshed.promise
      : undefined;
    const view = setup();
    expect(screen.queryByRole("button", { name: /^查看父任务 / })).toBeNull();
    await release(first, child());
    const retained = capture(await ready());
    const refresh = capture(screen.getByRole("button", { name: "刷新任务详情" }));
    act(() => {
      refresh();
      retained();
      expect(view.done).not.toHaveBeenCalled();
      expect(calls("/api/tasks/detail")).toHaveLength(1);
    });
    expect(calls("/api/tasks/detail")).toHaveLength(2);
    expect(screen.queryByRole("button", { name: /^查看父任务 / })).toBeNull();
    await release(refreshed, child());
    act(retained);
    expect(view.done).not.toHaveBeenCalled();
    fireEvent.click(await ready());
    expect(view.done).toHaveBeenCalledOnce();
    expectNoMutation();
  });

  it.each(["closed", "reconcile"])(
    "synchronously fences the existing TaskAction %s refresh without changing its read count",
    async (seam) => {
      serve({ state: "running", terminalAt: null });
      const view = setup();
      view.change({ context: { ...view.context, can: () => true } });
      await ready();
      click("请求取消任务");
      const form = screen.getByLabelText("操作者当前密码").closest("form")!;
      const retained = capture(await ready());
      const invoke = seam === "closed"
        ? capture(within(form).getByRole("button", { name: "取消" }))
        : actionReconcile(form);
      const held = deferred();
      override = (url) => url.startsWith("/api/tasks/detail?") ? held.promise : undefined;
      act(() => {
        invoke();
        retained();
        expect(view.done).not.toHaveBeenCalled();
        expect(calls("/api/tasks/detail")).toHaveLength(1);
      });
      expect(calls("/api/tasks/detail")).toHaveLength(2);
      expect(form.isConnected).toBe(seam === "reconcile");
      await release(held, child({ state: "running", terminalAt: null }));
      fireEvent.click(await ready());
      expect(view.done).toHaveBeenCalledOnce();
      view.unmount();
      expectNoMutation();
    },
  );

  it("fences an in-flight automatic poll while preserving the ordinary task, action and entered proof", async () => {
    vi.useFakeTimers();
    serve({ state: "running", terminalAt: null });
    const view = setup();
    view.change({ context: { ...view.context, can: () => true } });
    await act(async () => {});
    await ready();
    click("请求取消任务");
    fill("操作者当前密码", "Synthetic Parent Navigation Password 123");
    fill("未使用的认证器验证码", "654321");
    const password = screen.getByLabelText("操作者当前密码");
    const form = password.closest("form");
    const result = screen.getByLabelText("任务实际结果");
    const retained = capture(await ready());
    const next = deferred();
    override = (url) => url.startsWith("/api/tasks/detail?") ? next.promise : undefined;
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    act(retained);
    expect(view.done).not.toHaveBeenCalled();
    expect(screen.getByLabelText("操作者当前密码")).toBe(password);
    expect(password).toHaveValue("Synthetic Parent Navigation Password 123");
    expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("654321");
    expect(password.closest("form")).toBe(form);
    expect(screen.getByLabelText("任务实际结果")).toBe(result);
    await release(next, child({ state: "running", terminalAt: null }));
    fireEvent.click(screen.getByRole("button", { name: name() }));
    expect(view.done).toHaveBeenCalledOnce();
    view.unmount();
    expectNoMutation();
  });

  it("does not remount existing export controls or repeat their permission reads during a detail poll", async () => {
    vi.useFakeTimers();
    serve({ taskName: "user_assets.v3.export", domainId: "synthetic-domain", state: "running", terminalAt: null });
    const view = setup();
    await act(async () => {});
    const retained = capture(screen.getByRole("button", { name: name() }));
    const result = screen.getByRole("region", { name: "任务原用户导出结果" });
    const cancel = screen.getByRole("region", { name: "任务原用户导出取消" });
    const checks = calls("/api/access/check").length;
    const held = deferred();
    override = (url) => url.startsWith("/api/tasks/detail?") ? held.promise : undefined;
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    act(retained);
    expect(view.done).not.toHaveBeenCalled();
    expect(screen.getByRole("region", { name: "任务原用户导出结果" })).toBe(result);
    expect(screen.getByRole("region", { name: "任务原用户导出取消" })).toBe(cancel);
    expect(calls("/api/access/check")).toHaveLength(checks);
    expect(screen.queryByRole("button", { name: "请求取消任务" })).toBeNull();
    expect(screen.queryByRole("button", { name: "创建恢复任务" })).toBeNull();
    view.unmount();
    expectNoMutation();
  });

  it.each([200, 403])(
    "ignores an abort-insensitive superseded completion with status %s and rejects old handlers after a new parent",
    async (status) => {
      serve();
      const view = setup();
      const retained = capture(await ready());
      const late = deferred();
      override = (url) => url.startsWith("/api/tasks/detail?") ? late.promise : undefined;
      click("刷新任务详情");
      const signal = calls("/api/tasks/detail").at(-1)![1].signal as AbortSignal;
      serve({ parentTaskUUID: ancestorID });
      click("刷新任务详情");
      await ready(ancestorID);
      expect(signal.aborted).toBe(true);
      await release(late, child(), status);
      act(retained);
      expect(view.done).not.toHaveBeenCalled();
      expect(screen.queryByRole("button", { name: name() })).toBeNull();
      fireEvent.click(await ready(ancestorID));
      expect(view.done).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: ancestorID, archived: true });
      expect(screen.queryByRole("alert")).toBeNull();
      expectNoMutation();
    },
  );

  it("uses the latest committed done and archive authority, not the source task's archive flag or old closure", async () => {
    serve({ archived: false });
    const view = setup();
    const retained = capture(await ready());
    const previous = view.done;
    const replacement = vi.fn();
    view.change({ context: { ...view.context, canArchiveView: false }, done: replacement });
    act(retained);
    expect(previous).not.toHaveBeenCalled();
    expect(replacement).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: parentID, archived: false });
    replacement.mockClear();
    view.change({ context: { ...view.context, canArchiveView: true } });
    act(retained);
    expect(replacement).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: parentID, archived: true });
    expectNoMutation();
  });

  it.each(["detail", "source visibility"])(
    "rejects a retained callback when current %s authority is revoked, including before another render",
    async (gate) => {
      serve();
      const view = setup({ archived: true });
      const retained = capture(await ready());
      if (gate === "detail") view.context.can = () => false;
      else view.context.canArchiveView = false;
      act(retained);
      expect(view.done).not.toHaveBeenCalled();
      view.change({ context: { ...view.context } });
      expect(screen.queryByRole("button", { name: /^查看父任务 / })).toBeNull();
      act(retained);
      expect(view.done).not.toHaveBeenCalled();
      expectNoMutation();
    },
  );

  it.each(["ID", "username", "csrfToken", "role", "priv", "needChangePwd", "isExpired"] as const)(
    "rejects an accepted read after current profile identity changes %s without a fresh detail",
    async (field) => {
      serve();
      const view = setup();
      const retained = capture(await ready());
      const previous = view.context.profile[field];
      const replacement = typeof previous === "number" ? previous + 1
        : typeof previous === "boolean" ? !previous : `${previous}-replacement`;
      view.change({ context: { ...view.context, profile: { ...view.context.profile, [field]: replacement } } });
      act(retained);
      expect(view.done).not.toHaveBeenCalled();
      expect(screen.queryByRole("button", { name: /^查看父任务 / })).toBeNull();
      click("刷新任务详情");
      fireEvent.click(await ready());
      expect(view.done).toHaveBeenCalledOnce();
      expectNoMutation();
    },
  );

  it("kills retained callbacks at unmount and isolates a new task before its response", async () => {
    serve();
    const view = setup();
    const retained = capture(await ready());
    const held = deferred();
    override = (url) => url.startsWith("/api/tasks/detail?") ? held.promise : undefined;
    view.change({ id: ancestorID });
    act(retained);
    expect(view.done).not.toHaveBeenCalled();
    view.unmount();
    await release(held, task(ancestorID, { parentTaskUUID: parentID }));
    act(retained);
    expect(view.done).not.toHaveBeenCalled();
    expect(screen.queryByTestId("task-detail-identity")).toBeNull();
    expectNoMutation();
  });

  it("restores the live fence after StrictMode cleanup while ignoring the first mount response", async () => {
    const old = deferred();
    let reads = 0;
    override = (url) => url.startsWith("/api/tasks/detail?")
      ? ++reads === 1 ? old.promise : response({ task: child(), events: [] })
      : undefined;
    const view = setup({ strict: true });
    const button = await ready();
    expect(calls("/api/tasks/detail")).toHaveLength(2);
    expect(calls("/api/tasks/detail")[0][1].signal.aborted).toBe(true);
    await release(old, child({ parentTaskUUID: ancestorID }));
    expect(screen.queryByRole("button", { name: name(ancestorID) })).toBeNull();
    fireEvent.click(button);
    expect(view.done).toHaveBeenCalledExactlyOnceWith({ type: "detail", id: parentID, archived: true });
    expectNoMutation();
  });

  const stored = () => Object.fromEntries(
    Array.from({ length: sessionStorage.length }, (_, index) => sessionStorage.key(index)!)
      .sort().map((key) => [key, sessionStorage.getItem(key)]),
  );
  function seedOtherRecords() {
    const key = "bbbbbbbb-0000-4000-8000-000000000004";
    sessionStorage.setItem("adtr.pending-task:202:other_actor", JSON.stringify({
      owner: 202, username: "other_actor", action: "recover", taskUUID: firstID, key,
    }));
    for (const [kind, actorID] of [
      ["user", profile.ID], ["computer", 202], ["group", profile.ID],
    ] as const)
      sessionStorage.setItem(`adtr.${kind}-assets-v3.export-recovery`, JSON.stringify({
        version: 1, kind: `${kind}_assets.v3.export`, actorID,
        domainId: "synthetic-domain", idempotencyKey: key,
      }));
    sessionStorage.setItem(`adtr.pending-task-maintenance:${profile.ID}:${profile.username}`, JSON.stringify({
      owner: profile.ID, username: profile.username, key, action: "restore",
      input: { reason: "synthetic pending maintenance", targets: [{ taskUUID: ancestorID, visibilityVersion: 1 }] },
    }));
  }

  it.each(["submit", "current recovery", "other recovery"])(
    "preserves attempted %s and dedicated/maintenance/other-actor bytes through read-only navigation",
    async (pending) => {
      seedOtherRecords();
      const intent = beginTaskIntent(profile, pending === "submit" ? "submit" : "recover",
        pending === "submit" ? undefined : pending === "current recovery" ? firstID : ancestorID);
      attemptTaskIntent(profile, intent);
      const before = stored();
      serve({ state: "failed" });
      save({ view: { type: "detail", id: firstID }, query: applied });
      const view = render(<App />);
      await ready();
      if (pending === "current recovery") {
        fill("操作者当前密码", "Synthetic Unsent Recovery Password 123");
        fill("未使用的认证器验证码", "123456");
      }
      fireEvent.click(await ready());
      await waitFor(() => expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(parentID));
      expect(stored()).toEqual(before);
      expect(taskIntent(profile)).toBe(intent);
      expect(intent.attempted).toBe(true);
      expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
      expect(JSON.stringify(window.history.state)).not.toContain("Synthetic Unsent");
      expect(JSON.stringify(window.history.state)).not.toContain("parentReadToken");
      expectNoMutation();
      view.unmount();
      discardMaintenanceIntent(profile);
    },
  );

  it.each([false, true])(
    "preserves the existing matching invalid generic recovery cleanup for a dedicated kind, attempted=%s",
    async (attempted) => {
      seedOtherRecords();
      const intent = beginTaskIntent(profile, "recover", firstID);
      if (attempted) attemptTaskIntent(profile, intent);
      const ownerKey = `adtr.pending-task:${profile.ID}:${profile.username}`;
      const expected = stored();
      delete expected[ownerKey];
      serve({ taskName: "domain.directory_read.v4", state: "failed" });
      save({ view: { type: "detail", id: firstID } });
      const view = render(<App />);
      await ready();
      await waitFor(() => expect(taskIntent(profile)).toBeUndefined());
      expect(sessionStorage.getItem(ownerKey)).toBeNull();
      expect(screen.queryByRole("button", { name: "创建恢复任务" })).toBeNull();
      expect(screen.queryByRole("button", { name: "确认恢复任务" })).toBeNull();
      expect(stored()).toEqual(expected);
      fireEvent.click(await ready());
      await waitFor(() => expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(parentID));
      expect(stored()).toEqual(expected);
      expectNoMutation();
      view.unmount();
      discardMaintenanceIntent(profile);
    },
  );

  it("does not clear a nonmatching generic recovery when the child uses dedicated controls", async () => {
    seedOtherRecords();
    const intent = beginTaskIntent(profile, "recover", ancestorID);
    attemptTaskIntent(profile, intent);
    const before = stored();
    serve({ taskName: "domain.directory_read.v4", state: "failed" });
    save({ view: { type: "detail", id: firstID } });
    const view = render(<App />);
    fireEvent.click(await ready());
    await waitFor(() => expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(parentID));
    expect(taskIntent(profile)).toBe(intent);
    expect(stored()).toEqual(before);
    expectNoMutation();
    view.unmount();
    discardMaintenanceIntent(profile);
  });

  it("preserves actor-bound child/parent history, query, selection and fresh reads through Back, Forward and remount", async () => {
    serve();
    // Page two's selected child differs from the parent and grandparent.
    override = (url) => {
      if (url.startsWith("/api/tasks/detail?")) {
        const id = queryOf(url).get("taskUUID")!;
        return response({ task: task(id, { parentTaskUUID: id === secondID ? firstID : ancestorID }), events: [] });
      }
      return undefined;
    };
    const view = render(<App />);
    await screen.findByRole("table", { name: "任务列表" });
    await filterPageTwo();
    await openDetail(secondID);
    const childEntry = currentEntry();
    click(name(firstID));
    await waitFor(() => expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(firstID));
    expect(calls("/api/tasks/detail").at(-1)![0]).toBe(`/api/tasks/detail?taskUUID=${firstID}&visibility=all`);
    const parentEntry = currentEntry();
    expect(parentEntry.state[namespace].owner).toEqual(childEntry.state[namespace].owner);
    expect(parentEntry.state[namespace].query).toEqual(applied);
    expect(parentEntry.state[namespace].listReturn).toEqual(childEntry.state[namespace].listReturn);
    act(() => window.history.back());
    await waitFor(() => expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(secondID));
    expect(currentEntry()).toEqual(childEntry);
    expect(calls("/api/tasks/detail").at(-1)![0]).toBe(`/api/tasks/detail?taskUUID=${secondID}`);
    act(() => window.history.forward());
    await waitFor(() => expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(firstID));
    expect(currentEntry()).toEqual(parentEntry);
    const reads = calls("/api/tasks/detail").length;
    view.unmount();
    render(<App />);
    await screen.findByTestId("task-detail-identity");
    expect(calls("/api/tasks/detail")).toHaveLength(reads + 1);
    expect(currentEntry()).toEqual(parentEntry);
    click("返回任务列表");
    expect(await screen.findByRole("button", { name: `详情 ${secondID}` })).toHaveFocus();
    expectInputs(applied);
    expectNoMutation();
  });

  it.each([403, 404])(
    "lets the destination deny an active parent with %s, clearing child content and never broadening visibility",
    async (status) => {
      menu.find((node) => node.mark === "tasks")!.auth.writeable = false;
      menu.find((node) => node.mark === "task_archive")!.auth.readable = false;
      denied = new Set(taskOperations.filter((operation) => operation.startsWith("POST")));
      override = (url) => {
        if (!url.startsWith("/api/tasks/detail?")) return undefined;
        return queryOf(url).get("taskUUID") === firstID
          ? response({ task: child({ result: { marker: "private-child-only" } }), events: [] })
          : response({ error: status === 403 ? "forbidden" : "not_found" }, status);
      };
      save({ view: { type: "detail", id: firstID } });
      render(<App />);
      fireEvent.click(await ready());
      await screen.findByRole("alert");
      expect(calls("/api/tasks/detail").at(-1)![0]).toBe(`/api/tasks/detail?taskUUID=${parentID}`);
      expect(calls("/api/tasks/detail")).toHaveLength(2);
      expect(screen.queryByTestId("task-detail-identity")).toBeNull();
      expect(screen.queryByText(/private-child-only/)).toBeNull();
      expect(screen.queryByLabelText("任务实际结果")).toBeNull();
      expect(screen.queryByRole("button", { name: /^查看父任务 / })).toBeNull();
      act(() => window.history.back());
      await ready();
      expect(screen.getByTestId("task-detail-identity")).toHaveTextContent(firstID);
      expectNoMutation();
    },
  );

  it.each(["ID", "username", "csrfToken"] as const)(
    "isolates retained parent actions and late responses after workspace %s replacement",
    async (field) => {
      serve();
      save({ view: { type: "detail", id: firstID } });
      const view = render(<TaskWorkspace profile={profile} sessionChanged={vi.fn()} />);
      const retained = capture(await ready());
      const late = deferred();
      override = (url) => url.startsWith("/api/tasks/detail?") ? late.promise : undefined;
      click("刷新任务详情");
      const signal = calls("/api/tasks/detail").at(-1)![1].signal as AbortSignal;
      profile = field === "ID" ? { ...profile, ID: profile.ID + 1 }
        : { ...profile, [field]: `${profile[field]}-replacement` };
      serve({ parentTaskUUID: ancestorID });
      view.rerender(<TaskWorkspace profile={profile} sessionChanged={vi.fn()} />);
      if (field === "csrfToken") await ready(ancestorID);
      else await screen.findByRole("table", { name: "任务列表" });
      const reads = calls("/api/tasks/detail").length;
      const entry = currentEntry();
      expect(signal.aborted).toBe(true);
      await release(late, child());
      act(retained);
      expect(currentEntry()).toEqual(entry);
      expect(calls("/api/tasks/detail")).toHaveLength(reads);
      expect(screen.queryByRole("button", { name: name() })).toBeNull();
      expectNoMutation();
    },
  );

  it("retires the action and old response after App accepts a replacement identity", async () => {
    serve();
    save({ view: { type: "detail", id: firstID } });
    render(<App />);
    const retained = capture(await ready());
    const late = deferred();
    override = (url) => url.startsWith("/api/tasks/detail?") ? late.promise : undefined;
    click("刷新任务详情");
    profile = { ...profile, csrfToken: `${profile.csrfToken}-accepted-replacement` };
    act(() => window.dispatchEvent(new Event("focus")));
    await screen.findByRole("heading", { name: "账户概览" });
    const reads = calls("/api/tasks/detail").length;
    const entry = currentEntry();
    await release(late, child());
    act(retained);
    expect(currentEntry()).toEqual(entry);
    expect(calls("/api/tasks/detail")).toHaveLength(reads);
    expect(screen.queryByRole("button", { name: /^查看父任务 / })).toBeNull();
    expect(screen.queryByTestId("task-detail-identity")).toBeNull();
    expectNoMutation();
  });
});
