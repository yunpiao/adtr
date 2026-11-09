// Offline component + strict API parser regression evidence, not a live journal.
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import OperationalLogsWorkspace from "./OperationalLogsWorkspace";
import type { Profile } from "./api";
import { discardBundleIntent } from "./operational-log-intent";

const profile: Profile = {
  ID: 1,
  username: "synthetic-log-reader",
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
  pwdUpdateTm: "2026-11-01T00:00:00Z",
  csrfToken: "synthetic-log-session",
};
const startLabel = "入库开始时间（UTC）";
const endLabel = "入库结束时间（UTC，不包含）";
const recentName = "查询最近 24 小时";
const defaultURL =
  "/api/system/logs?pageIdx=1&pageSize=20&systemType=api&systemType=worker";
const recent = {
  startTm: "2026-11-08T12:00:00.000001Z",
  endTm: "2026-11-09T12:00:00.000001Z",
};
const response = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json", "X-ADTR-User-ID": "1" },
  });
function list(url: string) {
  const q = new URL(url, "https://synthetic.invalid").searchParams;
  return {
    page: {
      pageIdx: Number(q.get("pageIdx")),
      pageSize: Number(q.get("pageSize")),
      total: 0,
      totalPage: 0,
    },
    List: [],
    exhausted: true,
    selection: {
      startTm: q.get("startTm") ?? recent.startTm,
      endTm: q.get("endTm") ?? recent.endTm,
      systemType: q.getAll("systemType"),
    },
    coverage: {
      mode: "best_effort",
      gapsPossible: true,
      journalCreatedAt: "2026-11-01T00:00:00Z",
      firstRecordedAt: null,
    },
  };
}
let allowed: boolean;
let override: (
  url: string,
  init: RequestInit,
) => Response | Promise<Response> | undefined;
let fetcher: ReturnType<typeof vi.fn>;
const queries = () =>
  fetcher.mock.calls.filter(([url]) =>
    String(url).startsWith("/api/system/logs?"),
  );
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
function modules(values: string[]) {
  const select = screen.getByLabelText("事件模块") as HTMLSelectElement;
  for (const option of select.options)
    option.selected = values.includes(option.value);
  fireEvent.change(select);
  expect(select).toHaveValue(values);
}
function draft() {
  fill(startLabel, "2026-11-02T01:00:00.123");
  fill(endLabel, "2026-11-02T02:00:00.456");
  modules(["worker"]);
  fill("每页事件数", "50");
}
function expectDefaults() {
  expect(screen.getByLabelText(startLabel)).toHaveValue("");
  expect(screen.getByLabelText(endLabel)).toHaveValue("");
  expect(screen.getByLabelText("事件模块")).toHaveValue(["api", "worker"]);
  expect(screen.getByLabelText("每页事件数")).toHaveValue("20");
}
async function ready() {
  await screen.findByRole("table", { name: "运行事件记录" });
}
async function start() {
  const result = render(
    <>
      <input aria-label="unrelated draft" defaultValue="untouched" />
      <OperationalLogsWorkspace profile={profile} sessionChanged={vi.fn()} />
    </>,
  );
  await ready();
  expect(queries()).toHaveLength(1);
  return result;
}
beforeEach(() => {
  allowed = true;
  override = () => undefined;
  discardBundleIntent();
  sessionStorage.clear();
  window.history.replaceState({}, "", "#operational-logs");
  fetcher = vi.fn(async (url: string, init: RequestInit) => {
    const custom = override(url, init);
    if (custom) return custom;
    if (url === "/api/access/check")
      return response({
        results: JSON.parse(String(init.body)).paths.map(() => allowed),
      });
    if (url.startsWith("/api/system/logs?")) return response(list(url));
    throw new Error(`Unexpected request: ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  discardBundleIntent();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("recent journal range replaces draft controls and refreshes the default query", () => {
  it.each([startLabel, endLabel, "事件模块", "每页事件数"])(
    "resets an unapplied %s draft on an already-default query",
    async (label) => {
      await start();
      if (label === "事件模块") modules(["worker"]);
      else
        fill(label, label === "每页事件数" ? "100" : "2026-11-02T01:02:03.123");
      click(recentName);
      await ready();
      expectDefaults();
      expect(queries().map(([url]) => url)).toEqual([defaultURL, defaultURL]);
      expect(screen.getByLabelText("unrelated draft")).toHaveValue("untouched");
    },
  );
  it("does not resubmit discarded draft values on the next query", async () => {
    await start();
    draft();
    click(recentName);
    await ready();
    click("查询事件");
    await ready();
    expect(queries().map(([url]) => url)).toEqual([defaultURL, defaultURL]);
    expectDefaults();
  });
  it("clears invalid range and empty-module errors along with their drafts", async () => {
    await start();
    draft();
    fill(endLabel, "2026-11-01T00:00");
    modules([]);
    click("查询事件");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "请选择 api 或 worker",
    );
    expect(queries()).toHaveLength(1);
    click(recentName);
    await ready();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expectDefaults();
    expect(queries()).toHaveLength(2);
  });
  it("preserves the focused button and refreshes once on each repeated action", async () => {
    await start();
    const button = screen.getByRole("button", { name: recentName });
    button.focus();
    for (let i = 0; i < 3; i++) {
      draft();
      fireEvent.click(button);
      await ready();
      expectDefaults();
      expect(screen.getByRole("button", { name: recentName })).toBe(button);
      expect(button).toHaveFocus();
      expect(queries()).toHaveLength(i + 2);
    }
  });
  it("still resets an applied custom range and later draft to defaults", async () => {
    await start();
    draft();
    click("查询事件");
    await ready();
    expect(queries()).toHaveLength(2);
    expect(queries()[1][0]).toContain("systemType=worker");
    fill(startLabel, "2026-11-03T01:00");
    click(recentName);
    await ready();
    expectDefaults();
    expect(queries()).toHaveLength(3);
    expect(queries()[2][0]).toBe(defaultURL);
  });
  it("aborts a pending custom query and ignores its late completion after the refresh", async () => {
    await start();
    let finish!: (value: Response) => void;
    let oldSignal!: AbortSignal;
    override = (url, init) => {
      if (!url.includes("startTm=")) return undefined;
      oldSignal = init.signal as AbortSignal;
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    };
    draft();
    click("查询事件");
    await waitFor(() => expect(finish).toBeTypeOf("function"));
    click(recentName);
    await ready();
    expect(oldSignal.aborted).toBe(true);
    expectDefaults();
    await act(async () => finish(response(list(queries()[1][0]))));
    expectDefaults();
    expect(screen.getByText(/已确认入库时间范围/)).toHaveTextContent(
      recent.startTm,
    );
    expect(queries()).toHaveLength(3);
  });
  it("replaces a pending default refresh without retaining its drafts or late result", async () => {
    await start();
    let finish!: (value: Response) => void;
    let oldSignal!: AbortSignal;
    override = (url, init) => {
      if (url !== defaultURL) return undefined;
      oldSignal = init.signal as AbortSignal;
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    };
    click("刷新事件");
    await waitFor(() => expect(finish).toBeTypeOf("function"));
    draft();
    override = () => undefined;
    click(recentName);
    await ready();
    expect(oldSignal.aborted).toBe(true);
    expectDefaults();
    const stale = list(defaultURL);
    stale.selection.startTm = "2026-11-06T12:00:00.000001Z";
    stale.selection.endTm = "2026-11-07T12:00:00.000001Z";
    await act(async () => finish(response(stale)));
    expectDefaults();
    expect(screen.getByText(/已确认入库时间范围/)).toHaveTextContent(
      recent.startTm,
    );
    expect(screen.getByText(/已确认入库时间范围/)).not.toHaveTextContent(
      stale.selection.startTm,
    );
    expect(queries().map(([url]) => url)).toEqual([
      defaultURL,
      defaultURL,
      defaultURL,
    ]);
  });
  it("does not restore a draft after the refreshed request fails, and retries defaults", async () => {
    await start();
    draft();
    override = (url) =>
      url.startsWith("/api/system/logs?")
        ? response({ error: "internal" }, 500)
        : undefined;
    click(recentName);
    await screen.findByRole("alert");
    expectDefaults();
    expect(
      screen.queryByRole("table", { name: "运行事件记录" }),
    ).not.toBeInTheDocument();
    override = () => undefined;
    click("刷新事件");
    await ready();
    expect(queries().map(([url]) => url)).toEqual([
      defaultURL,
      defaultURL,
      defaultURL,
    ]);
  });
  it("uses the newly confirmed server range for bundle review without submitting a task", async () => {
    await start();
    draft();
    click(recentName);
    await ready();
    expectDefaults();
    click("确认当前筛选并打包");
    expect(
      await screen.findByRole("heading", { name: "确认运行日志诊断包" }),
    ).toBeInTheDocument();
    expect(screen.getByText(/已确认入库时间范围/)).toHaveTextContent(
      recent.startTm,
    );
    expect(screen.getByText(/已确认入库时间范围/)).toHaveTextContent(
      "api、worker",
    );
    expect(
      fetcher.mock.calls.every(
        ([url, init]) => url === "/api/access/check" || init.method === "GET",
      ),
    ).toBe(true);
    expect(
      fetcher.mock.calls.some(([url]) => String(url).includes("/bundles")),
    ).toBe(false);
  });
  it("does not expose the reset action or read logs without the existing permission", async () => {
    allowed = false;
    render(
      <OperationalLogsWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByText(/当前账户没有此运行日志操作权限/);
    expect(
      screen.queryByRole("button", { name: recentName }),
    ).not.toBeInTheDocument();
    expect(queries()).toHaveLength(0);
  });
});
