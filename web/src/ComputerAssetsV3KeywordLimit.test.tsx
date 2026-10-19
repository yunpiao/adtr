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

const limitMessage = "关键词最多 50 个 UTF-16 单元，已拒绝整次变更并保留此前内容；请重新编辑或清除关键词。";
const input = () => keyword() as HTMLInputElement;
function selectKeyword(start: number, end = start, direction: "forward" | "backward" | "none" = "none") {
  input().focus();
  input().setSelectionRange(start, end, direction);
  fireEvent.select(input());
}

describe("FLD-0418 whole-change UTF-16 keyword limit", () => {
  it.each(["a".repeat(50), "😀".repeat(25)])("accepts exactly 50 UTF-16 units without querying: %s", async (value) => {
    await start();
    fill("计算机资产关键词", value);
    expect(input()).toHaveValue(value);
    expect(calls()).toHaveLength(1);
    expect(input()).not.toHaveAttribute("maxlength");
  });

  it("rejects the entire 51st-unit change and restores the previous caret", async () => {
    await start();
    const previous = "a".repeat(50);
    fill("计算机资产关键词", previous);
    selectKeyword(12);
    fireEvent.change(input(), { target: { value: previous.slice(0, 12) + "Z" + previous.slice(12) } });
    expect(input()).toHaveValue(previous);
    expect(input()).toHaveFocus();
    expect(input().selectionStart).toBe(12);
    expect(input().selectionEnd).toBe(12);
    expect(screen.getByText(limitMessage)).toBeVisible();
    expect(screen.getByRole("button", { name: "应用计算机筛选" })).toBeDisabled();
    expect(calls()).toHaveLength(1);
    fill("计算机资产关键词", "shorter");
    expect(screen.queryByText(limitMessage)).toBeNull();
    expect(input()).toHaveValue("shorter");
  });

  it("uses replacement length for paste and retains selection direction on rejection", async () => {
    await start();
    const previous = "a".repeat(50);
    fill("计算机资产关键词", previous);
    selectKeyword(10, 12, "backward");
    expect(fireEvent.paste(input(), { clipboardData: { getData: () => "XYZ" } })).toBe(false);
    expect(input()).toHaveValue(previous);
    expect(input().selectionStart).toBe(10);
    expect(input().selectionEnd).toBe(12);
    expect(input().selectionDirection).toBe("backward");
    expect(fireEvent.paste(input(), { clipboardData: { getData: () => "😀" } })).toBe(true);
    // jsdom has no native paste insertion; this change is explicitly synthetic.
    const replaced = previous.slice(0, 10) + "😀" + previous.slice(12);
    fireEvent.change(input(), { target: { value: replaced } });
    expect(input()).toHaveValue(replaced);
    expect(replaced.length).toBe(50);
    expect(screen.queryByText(limitMessage)).toBeNull();
    selectKeyword(0, 50);
    expect(fireEvent.paste(input(), { clipboardData: { getData: () => "replacement" } })).toBe(true);
    fireEvent.change(input(), { target: { value: "replacement" } });
    expect(input()).toHaveValue("replacement");
    expect(calls()).toHaveLength(1);
  });

  it("never splits a surrogate pair and retains existing malformed-surrogate submit validation", async () => {
    await start();
    fill("计算机资产关键词", "a".repeat(49));
    selectKeyword(49);
    fireEvent.change(input(), { target: { value: "a".repeat(49) + "😀" } });
    expect(input()).toHaveValue("a".repeat(49));
    fill("计算机资产关键词", "\ud83d");
    click("应用计算机筛选");
    expect(screen.getByText(/请核对搜索长度与成对 RFC3339/)).toBeVisible();
    expect(calls()).toHaveLength(1);
  });

  it("rejects oversized drop payloads and validates the final value for uncertain drop positions", async () => {
    await start();
    fill("计算机资产关键词", "a".repeat(49));
    selectKeyword(0);
    expect(fireEvent.drop(input(), { dataTransfer: { getData: () => "x".repeat(51) } })).toBe(false);
    expect(input()).toHaveValue("a".repeat(49));
    expect(fireEvent.drop(input(), { dataTransfer: { getData: () => "XY" } })).toBe(true);
    // Synthetic actual insertion at a different position than the remembered caret.
    fireEvent.change(input(), { target: { value: "a".repeat(20) + "XY" + "a".repeat(29) } });
    expect(input()).toHaveValue("a".repeat(49));
    expect(screen.getByText(limitMessage)).toBeVisible();
    expect(calls()).toHaveLength(1);
  });

  it("allows temporary IME overflow but blocks form submit and rolls back the whole rejected composition", async () => {
    await start();
    fill("计算机资产关键词", "previous");
    selectKeyword(2, 5, "backward");
    fireEvent.compositionStart(input());
    const tooLong = "汉".repeat(51);
    fireEvent.change(input(), { target: { value: tooLong } });
    expect(input()).toHaveValue(tooLong);
    expect(screen.getByRole("button", { name: "应用计算机筛选" })).toBeDisabled();
    expect(clearKeyword()).toBeDisabled();
    expect(screen.getByRole("button", { name: "清除计算机筛选" })).toBeDisabled();
    fireEvent.click(clearKeyword());
    expect(input()).toHaveValue(tooLong);
    fireEvent.submit(input().closest("form")!);
    expect(calls()).toHaveLength(1);
    fireEvent.compositionEnd(input(), { data: tooLong });
    expect(input()).toHaveValue("previous");
    expect(input().selectionStart).toBe(2);
    expect(input().selectionEnd).toBe(5);
    expect(input().selectionDirection).toBe("backward");
    expect(screen.getByText(limitMessage)).toBeVisible();
    // A trailing native input may contain either the composed or restored value.
    fireEvent.change(input(), { target: { value: tooLong } });
    expect(input()).toHaveValue("previous");
    fireEvent.input(input(), { target: { value: "previous" } });
    expect(screen.getByText(limitMessage)).toBeVisible();
    fill("计算机资产关键词", "previou");
    expect(input()).toHaveValue("previou");
    expect(screen.queryByText(limitMessage)).toBeNull();
    fireEvent.click(clearKeyword());
    expect(input()).toHaveValue("");
    expect(screen.queryByText(limitMessage)).toBeNull();
  });

  it("accepts an IME result within bounds and tolerates its duplicate final input", async () => {
    await start();
    fireEvent.compositionStart(input());
    fireEvent.change(input(), { target: { value: "中".repeat(51) } });
    fireEvent.change(input(), { target: { value: "😀".repeat(25) } });
    fireEvent.compositionEnd(input());
    fireEvent.input(input(), { target: { value: "😀".repeat(25) } });
    expect(input()).toHaveValue("😀".repeat(25));
    expect(screen.queryByText(limitMessage)).toBeNull();
    expect(screen.getByRole("button", { name: "应用计算机筛选" })).toBeEnabled();
    expect(calls()).toHaveLength(1);
  });

  it("preserves time errors and a valid in-flight response after rejecting draft overflow", async () => {
    await start();
    const held = deferred();
    override = (url) => url.startsWith("/api/computer-assets/v3") ? held.promise : undefined;
    fill("计算机资产关键词", "observed-12");
    click("应用计算机筛选");
    const signal = calls().at(-1)![1].signal as AbortSignal;
    pasteInvalid("创建时间开始（含）");
    fill("计算机资产关键词", "x".repeat(51));
    expect(input()).toHaveValue("observed-12");
    expect(signal.aborted).toBe(false);
    await held.resolve(list({ ...q, search: "observed-12" }, [row(12)], 1));
    expectPositions([12]);
    expect(screen.getAllByRole("button", { name: "展开事实" })[0]).toBeDisabled();
    fireEvent.click(clearKeyword());
    expect(screen.getByLabelText("创建时间开始（含）", { exact: true })).toHaveAttribute("aria-invalid", "true");
    expect(calls()).toHaveLength(2);
  });

  it("finishes composition before blur and restores ordinary clear behavior", async () => {
    await start();
    fireEvent.compositionStart(input());
    fireEvent.change(input(), { target: { value: "完成" } });
    fireEvent.compositionEnd(input());
    fireEvent.blur(input());
    expect(input()).toHaveValue("完成");
    expect(clearKeyword()).toBeEnabled();
    fireEvent.click(clearKeyword());
    expect(input()).toHaveValue("");
    expect(input()).toHaveFocus();
    expect(calls()).toHaveLength(1);
  });

  it("pauses refresh during composition and allows ordinary input without keydown after end", async () => {
    await start();
    fill("计算机资产关键词", "accepted");
    fireEvent.compositionStart(input());
    fireEvent.change(input(), { target: { value: "composing" } });
    const refresh = screen.getByRole("button", { name: "刷新计算机资产观测" });
    expect(refresh).toBeDisabled();
    fireEvent.click(refresh);
    expect(calls()).toHaveLength(1);
    expect(input()).toHaveValue("composing");
    fireEvent.compositionEnd(input());
    expect(refresh).toBeEnabled();
    fireEvent.input(input(), { target: { value: "new input" }, inputType: "insertText" });
    expect(input()).toHaveValue("new input");
    click("刷新计算机资产观测");
    await waitFor(() => expectPositions(firstPage));
    expect(input()).toHaveValue("new input");
    expect(params().search).toBe("");
    expect(params().observationId).toBeUndefined();
    fireEvent.click(clearKeyword());
    expect(input()).toHaveValue("");
  });

  it("does not restore composing private state after an in-flight access revocation", async () => {
    const held = deferred();
    override = (url) => url.startsWith("/api/computer-assets/v3") ? held.promise : undefined;
    await mount();
    await select();
    const oldInput = input();
    fireEvent.compositionStart(oldInput);
    fireEvent.change(oldInput, { target: { value: "unfinished" } });
    await held.resolve({ error: "forbidden" }, 403);
    await waitFor(() => expect(screen.queryByLabelText("计算机资产关键词", { exact: true })).toBeNull());
    fireEvent.compositionEnd(oldInput, { data: "unfinished" });
    fireEvent.change(oldInput, { target: { value: "unfinished" } });
    expectNoPositions();
    expect(screen.queryByLabelText("计算机资产关键词", { exact: true })).toBeNull();
  });

  it("resets the accepted draft after whole-filter clear and ignores unmounted composition state", async () => {
    const view = await start();
    fill("计算机资产关键词", "old-draft");
    click("清除计算机筛选");
    await waitFor(() => expectPositions(firstPage));
    fill("计算机资产关键词", "x".repeat(51));
    expect(input()).toHaveValue("");
    fireEvent.compositionStart(input());
    fireEvent.change(input(), { target: { value: "x".repeat(51) } });
    view.unmount();
    expectNoPositions();
  });
});
