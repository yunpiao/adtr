// Synthetic parser/DOM contracts, not real API/Worker/LDAP browser evidence.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import ComputerAssetsV3Workspace from "./ComputerAssetsV3Workspace";
import type { Profile } from "./api";
import type { SourceChoice } from "./domain-selection-api";
import { userAssetsV2Time } from "./user-assets-v2-filters";
import {
  type ComputerAssetsV3Query,
  type ComputerAssetV3Object,
} from "./computer-assets-v3-api";

const profile: Profile = {
  ID: 1,
  username: "reader",
  role: "custom",
  priv: 0,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: false,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 0,
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "actor-session",
};
const choice: SourceChoice = {
  domainId: "synthetic-domain",
  domain: "synthetic.invalid",
  revision: "9007199254740993",
  credentialRevision: "9007199254740995",
  dcHostName: "dc.synthetic.invalid",
  ldapAddr: "",
  port: "636",
  mode: "ldaps",
  credentialConfigured: true,
  connectionState: "unverified",
  source: "configured_connection",
  lastTest: null,
};
const otherChoice = {
  ...choice,
  domainId: "other-domain",
  domain: "other.invalid",
};
const source = {
  server_name: "dc.synthetic.invalid",
  dc_host_name: "dc.synthetic.invalid",
  domain: "synthetic.invalid",
  naming_context: "DC=synthetic,DC=invalid",
  started_at: "2026-10-10T00:00:00.123456789Z",
  completed_at: "2026-10-10T00:00:01Z",
  elapsed_milliseconds: 877,
  pages: 1,
};
const q: ComputerAssetsV3Query = {
  domainId: choice.domainId,
  expectedRevision: choice.revision,
  expectedCredentialRevision: choice.credentialRevision,
  search: "",
  pageIdx: 1,
  pageSize: 10,
};
const row = (index = 1): ComputerAssetV3Object => ({
  objectGUID: `00000000-0000-0000-0000-${String(index).padStart(12, "0")}`,
  distinguishedName: `CN=User ${index},DC=synthetic,DC=invalid`,
  kind: "computer",
  objectClass: ["computer", "top", "user"],
  samAccountName: `observed-${index}`,
  userAccountControl: 0,
  objectSid: "S-1-5-21-1-2-3-500",
  mail: " Observed@Synthetic.invalid ",
  description: ["Factual description"],
  whenCreated: `2026-01-${String(index).padStart(2, "0")}T00:00:00Z`,
  primaryGroupID: 0,
  servicePrincipalName: ["HOST/Z", "host/a\u0000\n😀<script>", "HOST/a"],
});
const selection = (query = q) => ({
  domainId: query.domainId,
  revision: query.expectedRevision,
  credentialRevision: query.expectedCredentialRevision,
});
const list = (
  query = q,
  rows = [row()],
  total = rows.length,
  observationId = "observation-one",
) => ({
  dictionaryVersion: 3,
  selection: selection(query),
  available: true,
  observedComputerCount:
    query.search || query.startTm ? Math.max(total, 12) : total,
  observationId,
  source,
  list: rows,
  page: {
    pageIdx: query.pageIdx,
    pageSize: query.pageSize,
    total,
    totalPage: Math.ceil(total / query.pageSize),
  },
});
const absent = (query = q) => ({
  dictionaryVersion: 3,
  selection: selection(query),
  available: false,
  observedComputerCount: null,
  list: [],
  page: {
    pageIdx: query.pageIdx,
    pageSize: query.pageSize,
    total: 0,
    totalPage: 0,
  },
});
const response = (value: unknown, status = 200, actor: string | null = "1") =>
  Promise.resolve(
    new Response(JSON.stringify(value), {
      status,
      headers: {
        "Content-Type": "application/json",
        ...(actor === null ? {} : { "X-ADTR-User-ID": actor }),
      },
    }),
  );
const deferred = () => {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((r) => {
    resolve = r;
  });
  return {
    promise,
    resolve: async (
      value: unknown,
      status = 200,
      actor: string | null = "1",
    ) => {
      await act(async () => resolve(await response(value, status, actor)));
    },
  };
};
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
let fetcher: ReturnType<typeof vi.fn>;
let override: (url: string, init: RequestInit) => Promise<Response> | undefined;
const calls = (path = "/api/computer-assets/v3") =>
  fetcher.mock.calls.filter(([url]) => url.split("?")[0] === path);
const params = (path = "/api/computer-assets/v3") =>
  Object.fromEntries(
    new URL(calls(path).at(-1)![0], "https://local.invalid").searchParams,
  );
function queryFrom(url: string): ComputerAssetsV3Query {
  const p = new URL(url, "https://local.invalid").searchParams;
  return {
    domainId: p.get("domainId")!,
    expectedRevision: p.get("expectedRevision")!,
    expectedCredentialRevision: p.get("expectedCredentialRevision")!,
    search: p.get("search")!,
    pageIdx: Number(p.get("pageIdx")),
    pageSize: Number(p.get("pageSize")),

    ...(p.has("startTm") ? { startTm: p.get("startTm")! } : {}),
    ...(p.has("endTm") ? { endTm: p.get("endTm")! } : {}),
    ...(p.has("creatTm")
      ? { creatTm: p.get("creatTm")! as "asc" | "desc" }
      : {}),
    ...(p.has("observationId")
      ? { observationId: p.get("observationId")! }
      : {}),
  };
}
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  override = () => undefined;
  fetcher = vi.fn((url: string, init: RequestInit = {}) => {
    const custom = override(url, init);
    if (custom) return custom;
    const path = url.split("?")[0];
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/menu")
      return response({
        menu: ["domains", "directory_assets"].map((mark) => ({
          mark,
          auth: { readable: true, writeable: false },
        })),
      });
    if (path === "/api/access/check")
      return response({ results: [true, true, true] });
    if (path === "/api/domain-selection")
      return response({
        List: [choice, otherChoice],
        page: { pageIdx: 1, pageSize: 20, total: 2, totalPage: 1 },
        exhausted: true,
      });
    if (path === "/api/domain-selection/resolve")
      return response({
        selection:
          new URL(url, "https://local.invalid").searchParams.get("domainId") ===
          otherChoice.domainId
            ? otherChoice
            : choice,
        checkedAt: "2026-10-10T00:00:00Z",
      });
    if (path === "/api/computer-assets/v3") {
      const query = queryFrom(url);
      const all = Array.from({ length: 12 }, (_, i) => row(i + 1));
      let filtered = query.search
        ? all.filter((r) => r.samAccountName!.includes(query.search))
        : all;
      if (query.startTm && query.endTm)
        filtered = filtered.filter((r) => {
          const created = userAssetsV2Time(r.whenCreated);
          return (
            created !== undefined &&
            created >= userAssetsV2Time(query.startTm)! &&
            created < userAssetsV2Time(query.endTm)!
          );
        });
      if (query.creatTm === "desc") filtered = [...filtered].reverse();
      return response(
        list(
          query,
          filtered.slice(
            (query.pageIdx - 1) * query.pageSize,
            query.pageIdx * query.pageSize,
          ),
          filtered.length,
          query.observationId ?? "observation-one",
        ),
      );
    }
    throw new Error(`Unexpected test path ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
async function mount(changed = vi.fn()) {
  const view = render(
    <ComputerAssetsV3Workspace profile={profile} sessionChanged={changed} />,
  );
  await screen.findByRole("radio", { name: "选择数据源 synthetic.invalid" });
  return view;
}
async function select(domain = "synthetic.invalid") {
  fireEvent.click(screen.getByRole("radio", { name: `选择数据源 ${domain}` }));
  click("核对并打开连接详情");
  await screen.findByRole("region", { name: "已选数据源计算机资产" });
}
async function start(changed = vi.fn()) {
  const view = await mount(changed);
  await select();
  await screen.findByRole("table", { name: "计算机资产列表" });
  return view;
}

// Only six-cell asset rows count; expanded facts are a separate spanning row.
function expectPositions(indices: number[]) {
  const table = screen.getByRole("table", { name: "计算机资产列表" });
  expect(within(table).getByRole("columnheader", { name: "本页序号" }))
    .toHaveAttribute("scope", "col");
  const rows = within(table).getAllByRole("row").filter((r) =>
    within(r).queryAllByRole("cell").length === 6,
  );
  expect(rows).toHaveLength(indices.length);
  rows.forEach((r, i) => {
    const cells = within(r).getAllByRole("cell");
    expect(cells[0]).toHaveTextContent(new RegExp(`^${i + 1}$`));
    expect(cells[1]).toHaveTextContent(new RegExp(`^observed-${indices[i]}$`));
  });
}
const firstPage = Array.from({ length: 10 }, (_, i) => i + 1);
function expectNoPositions() {
  expect(screen.queryByRole("table", { name: "计算机资产列表" })).toBeNull();
  expect(screen.queryByRole("columnheader", { name: "本页序号" })).toBeNull();
}

const clearKeyword = () => screen.getByRole("button", { name: "清除计算机资产关键词" });
const keyword = () => screen.getByLabelText("计算机资产关键词", { exact: true });
const pasteInvalid = (label: string) => fireEvent.paste(
  screen.getByLabelText(label, { exact: true }),
  { clipboardData: { getData: () => "bad\ninput" } },
);

describe("UI-0437 explicit draft-only keyword clear", () => {
  it("preserves time, sort, source, pin and applied page until explicit apply", async () => {
    await start();
    fill("计算机资产关键词", "observed");
    fill("创建时间开始（含）", "2026-01-01T00:00:00Z");
    fill("创建时间结束（不含）", "2026-02-01T00:00:00Z");
    fill("创建时间排序", "desc");
    click("应用计算机筛选");
    await waitFor(() => expectPositions([12, 11, 10, 9, 8, 7, 6, 5, 4, 3]));
    click("下一页");
    await waitFor(() => expectPositions([2, 1]));
    fireEvent.click(screen.getAllByRole("button", { name: "展开事实" })[0]);
    const before = fetcher.mock.calls.length;
    const applied = params();
    fireEvent.click(clearKeyword());
    expect(keyword()).toHaveValue("");
    expect(keyword()).toHaveFocus();
    expect(fetcher.mock.calls).toHaveLength(before);
    expect(params()).toEqual(applied);
    expect(screen.getByLabelText("创建时间开始（含）", { exact: true })).toHaveValue(applied.startTm);
    expect(screen.getByLabelText("创建时间结束（不含）", { exact: true })).toHaveValue(applied.endTm);
    expect(screen.getByLabelText("创建时间排序", { exact: true })).toHaveValue("desc");
    expect(screen.queryByText("主组 RID（primaryGroupID）")).toBeNull();
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    expect(screen.getAllByRole("button", { name: "展开事实" })[0]).toBeDisabled();
    expectPositions([2, 1]);
    click("应用计算机筛选");
    await waitFor(() => expectPositions([12, 11, 10, 9, 8, 7, 6, 5, 4, 3]));
    expect(params()).toEqual({ ...applied, search: "", pageIdx: "1" });
    expect(calls()).toHaveLength(4);
  });

  it.each(["{Enter}", " "])("supports keyboard %s and returns focus to the empty input", async (key) => {
    const user = userEvent.setup();
    await start();
    expect(clearKeyword()).toBeDisabled();
    fill("计算机资产关键词", "draft-only");
    keyword().focus();
    await user.tab();
    expect(clearKeyword()).toHaveFocus();
    await user.keyboard(key);
    expect(keyword()).toHaveValue("");
    expect(keyword()).toHaveFocus();
    expect(clearKeyword()).toBeDisabled();
    expect(calls()).toHaveLength(1);
    expectPositions(firstPage);
  });

  it("clears keyword transfer rejection even when empty, preserving time transfer rejection", async () => {
    await start();
    pasteInvalid("计算机资产关键词");
    pasteInvalid("创建时间开始（含）");
    expect(clearKeyword()).toBeEnabled();
    fireEvent.click(clearKeyword());
    expect(keyword()).toHaveAttribute("aria-invalid", "false");
    expect(screen.getByLabelText("创建时间开始（含）", { exact: true })).toHaveAttribute("aria-invalid", "true");
    expect(screen.queryByText(/计算机资产关键词含控制字符/)).toBeNull();
    expect(screen.getByText(/创建时间开始（含）含控制字符/)).toBeVisible();
    expect(screen.getByRole("button", { name: "应用计算机筛选" })).toBeDisabled();
    expect(calls()).toHaveLength(1);
  });

  it("keeps invalid time drafts and revalidates them after clearing the shared validation notice", async () => {
    await start();
    fill("计算机资产关键词", "x".repeat(51));
    fill("创建时间开始（含）", "invalid-time");
    click("应用计算机筛选");
    const message = "请核对搜索长度与成对 RFC3339 创建时间；开始须早于结束，输入不会被截断。";
    expect(screen.getByText(message)).toBeVisible();
    fireEvent.click(clearKeyword());
    expect(screen.queryByText(message)).toBeNull();
    expect(screen.getByLabelText("创建时间开始（含）", { exact: true })).toHaveValue("invalid-time");
    click("应用计算机筛选");
    expect(screen.getByText(message)).toBeVisible();
    expect(calls()).toHaveLength(1);
    fill("创建时间开始（含）", "");
    click("应用计算机筛选");
    await waitFor(() => expectPositions(firstPage));
    expect(calls()).toHaveLength(2);
  });

  it("does not cancel a valid applied read when only its draft is cleared", async () => {
    await start();
    const held = deferred();
    override = (url) => url.startsWith("/api/computer-assets/v3") ? held.promise : undefined;
    fill("计算机资产关键词", "observed-1");
    click("应用计算机筛选");
    const pendingSignal = calls().at(-1)![1].signal as AbortSignal;
    fireEvent.click(clearKeyword());
    expect(pendingSignal.aborted).toBe(false);
    expect(calls()).toHaveLength(2);
    await held.resolve(list({ ...q, search: "observed-1" }, [row(1), row(10), row(11), row(12)], 4));
    expect(keyword()).toHaveValue("");
    expectPositions([1, 10, 11, 12]);
    expect(screen.getByText("筛选尚未应用；分页和事实展开已暂停。")).toBeVisible();
    expect(screen.getAllByRole("button", { name: "展开事实" })[0]).toBeDisabled();
  });

  it("rejects a superseded response after clear and explicit apply", async () => {
    await start();
    const held = deferred();
    override = (url) => url.startsWith("/api/computer-assets/v3") && queryFrom(url).search !== "" ? held.promise : undefined;
    fill("计算机资产关键词", "observed-12");
    click("应用计算机筛选");
    fireEvent.click(clearKeyword());
    click("应用计算机筛选");
    await waitFor(() => expectPositions(firstPage));
    await held.resolve(list({ ...q, search: "observed-12" }, [row(12)], 1));
    expectPositions(firstPage);
    expect(keyword()).toHaveValue("");
  });

  it("disables clear for stale observations until explicit refresh", async () => {
    await start();
    fill("计算机资产关键词", "observed");
    click("应用计算机筛选");
    await waitFor(() => expectPositions(firstPage));
    override = (url) => url.startsWith("/api/computer-assets/v3") ? response({ error: "directory_observation_unavailable" }, 409) : undefined;
    click("下一页");
    await screen.findByText("原观测已不可用，请明确刷新观测。");
    expect(clearKeyword()).toBeDisabled();
    fireEvent.click(clearKeyword());
    expect(keyword()).toHaveValue("observed");
    expectNoPositions();
    override = () => undefined;
    click("刷新计算机资产观测");
    await waitFor(() => expectPositions(firstPage));
    expect(clearKeyword()).toBeEnabled();
  });

  it.each([401, 403, 404])("removes clear and private rows on access failure %s", async (status) => {
    await start();
    fill("计算机资产关键词", "draft");
    fireEvent.click(clearKeyword());
    override = (url) => url.startsWith("/api/computer-assets/v3") ? response({ error: status === 401 ? "unauthenticated" : status === 403 ? "forbidden" : "not_found" }, status) : undefined;
    click("下一页");
    await waitFor(() => expect(screen.queryByRole("button", { name: "清除计算机资产关键词" })).toBeNull());
    expectNoPositions();
  });

  it.each(["source", "session", "unmount"])("cannot restore cleared private state after %s invalidation", async (reason) => {
    const view = await start();
    const held = deferred();
    override = (url) => url.startsWith("/api/computer-assets/v3") ? held.promise : undefined;
    fill("计算机资产关键词", "observed-12");
    click("应用计算机筛选");
    fireEvent.click(clearKeyword());
    if (reason === "source") {
      click("更换数据源");
      fireEvent.click(screen.getByRole("radio", { name: "选择数据源 other.invalid" }));
    } else if (reason === "session") {
      view.rerender(<ComputerAssetsV3Workspace profile={{ ...profile, csrfToken: "replacement-session" }} sessionChanged={vi.fn()} />);
    } else view.unmount();
    await held.resolve(list({ ...q, search: "observed-12" }, [row(12)], 1));
    expectNoPositions();
    expect(screen.queryByRole("button", { name: "清除计算机资产关键词" })).toBeNull();
  });

  it("keeps the existing whole-filter clear behavior", async () => {
    await start();
    fill("计算机资产关键词", "observed-12");
    fill("创建时间开始（含）", "2026-01-01T00:00:00Z");
    fill("创建时间结束（不含）", "2026-02-01T00:00:00Z");
    fill("创建时间排序", "desc");
    click("应用计算机筛选");
    await waitFor(() => expectPositions([12]));
    click("清除计算机筛选");
    await waitFor(() => expectPositions(firstPage));
    for (const label of ["计算机资产关键词", "创建时间开始（含）", "创建时间结束（不含）", "创建时间排序"])
      expect(screen.getByLabelText(label, { exact: true })).toHaveValue("");
    expect(params()).toEqual({ ...q, pageIdx: "1", pageSize: "10", observationId: "observation-one" });
    expect(calls()).toHaveLength(3);
  });
});
