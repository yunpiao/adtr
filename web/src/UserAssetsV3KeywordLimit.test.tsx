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
import userEvent from "@testing-library/user-event";
import { userAssetsV2Time } from "./user-assets-v2-filters";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  matchesUserUACDisableBit,
  type UACDisableBit,
} from "./user-assets-v3-filters";
import { clearExportRecovery } from "./user-asset-export-v3-recovery";
import UserAssetsV3Workspace from "./UserAssetsV3Workspace";
import type { Profile } from "./api";
import type { SourceChoice } from "./domain-selection-api";
import {
  type UserAssetsV3Query,
  type UserAssetV3Object,
} from "./user-assets-v3-api";

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
const q: UserAssetsV3Query = {
  domainId: choice.domainId,
  expectedRevision: choice.revision,
  expectedCredentialRevision: choice.credentialRevision,
  search: "",
  pageIdx: 1,
  pageSize: 10,
};
const row = (index = 1): UserAssetV3Object => ({
  objectGUID: `00000000-0000-0000-0000-${String(index).padStart(12, "0")}`,
  distinguishedName: `CN=User ${index},DC=synthetic,DC=invalid`,
  kind: "user",
  objectClass: ["top", "user"],
  samAccountName: `observed-${index}`,
  userAccountControl: index % 3 === 0 ? null : index % 3 === 1 ? 2 : 512,
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
const calls = (path = "/api/user-assets/v3") =>
  fetcher.mock.calls.filter(([url]) => url.split("?")[0] === path);
const params = (path = "/api/user-assets/v3") =>
  Object.fromEntries(
    new URL(calls(path).at(-1)![0], "https://local.invalid").searchParams,
  );
function queryFrom(url: string): UserAssetsV3Query {
  const p = new URL(url, "https://local.invalid").searchParams;
  return {
    domainId: p.get("domainId")!,
    expectedRevision: p.get("expectedRevision")!,
    expectedCredentialRevision: p.get("expectedCredentialRevision")!,
    search: p.get("search")!,
    pageIdx: Number(p.get("pageIdx")),
    pageSize: Number(p.get("pageSize")),

    ...(p.has("sAMAccountName")
      ? { sAMAccountName: p.get("sAMAccountName")! }
      : {}),
    ...(p.has("startTm") ? { startTm: p.get("startTm")! } : {}),
    ...(p.has("endTm") ? { endTm: p.get("endTm")! } : {}),
    ...(p.has("uacDisableBit")
      ? { uacDisableBit: p.get("uacDisableBit")! as UACDisableBit }
      : {}),
    ...(p.has("creatTm")
      ? { creatTm: p.get("creatTm")! as "asc" | "desc" }
      : {}),
    ...(p.has("observationId")
      ? { observationId: p.get("observationId")! }
      : {}),
  };
}
beforeEach(() => {
  clearExportRecovery();
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
        menu: [
          "domains",
          "directory_assets",
          "tasks",
          "user_asset_exports",
        ].map((mark) => ({
          mark,
          auth: { readable: true, writeable: false },
        })),
      });
    if (path === "/api/access/check")
      return response({
        results: Array(JSON.parse(String(init.body)).paths.length).fill(true),
      });
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
    if (path === "/api/user-assets/v3") {
      const query = queryFrom(url);
      const all = Array.from({ length: 23 }, (_, i) => row(i + 1));
      let filtered = query.search
        ? all.filter((r) => r.samAccountName!.includes(query.search))
        : all;
      filtered = filtered.filter((r) =>
        matchesUserUACDisableBit(r.userAccountControl, query.uacDisableBit),
      );
      if (query.sAMAccountName)
        filtered = filtered.filter(
          (r) => r.samAccountName === query.sAMAccountName,
        );
      if (query.startTm && query.endTm)
        filtered = filtered.filter(
          (r) =>
            userAssetsV2Time(r.whenCreated)! >=
              userAssetsV2Time(query.startTm)! &&
            userAssetsV2Time(r.whenCreated)! < userAssetsV2Time(query.endTm)!,
        );
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
    if (path === "/api/user-assets/v3/detail") {
      const p = new URL(url, "https://local.invalid").searchParams;
      return response(detail(Number(p.get("objectGUID")!.slice(-12))));
    }
    throw new Error(`Unexpected test path ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  cleanup();
  clearExportRecovery();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
async function mount(changed = vi.fn()) {
  const view = render(
    <UserAssetsV3Workspace profile={profile} sessionChanged={changed} />,
  );
  await screen.findByRole("radio", { name: "选择数据源 synthetic.invalid" });
  return view;
}
async function select(domain = "synthetic.invalid") {
  fireEvent.click(screen.getByRole("radio", { name: `选择数据源 ${domain}` }));
  click("核对并打开连接详情");
  await screen.findByRole("region", { name: "已选数据源用户资产" });
}
async function start(changed = vi.fn()) {
  const view = await mount(changed);
  await select();
  await screen.findByRole("table", { name: "用户资产列表" });
  return view;
}

// These are display positions, never API identities or request parameters.
function expectPositions(indices: number[]) {
  const table = screen.getByRole("table", { name: "用户资产列表" });
  const header = within(table).getByRole("columnheader", { name: "本页序号" });
  expect(header).toHaveAttribute("scope", "col");
  expect(header).not.toHaveAttribute("aria-sort");
  expect(within(header).queryByRole("button")).toBeNull();
  expect(
    within(table)
      .getAllByRole("columnheader")
      .map((cell) => cell.style.width),
  ).toEqual(["9%", "14%", "17%", "19%", "16%", "15%", "10%"]);
  const rows = within(table).getAllByRole("row").slice(1);
  expect(rows).toHaveLength(indices.length);
  rows.forEach((r, i) => {
    const cells = within(r).getAllByRole("cell");
    expect(cells).toHaveLength(7);
    expect(cells[0]).toHaveAttribute("data-label", "本页序号");
    expect(cells[0]).toHaveTextContent(new RegExp(`^${i + 1}$`));
    expect(cells[1]).toHaveTextContent(`observed-${indices[i]}`);
    expect(cells[2]).toHaveTextContent(row(indices[i]).objectGUID);
    expect(
      within(r).getByRole("button", {
        name: `查看用户 observed-${indices[i]}`,
      }),
    ).toBeInTheDocument();
  });
}
const firstPage = Array.from({ length: 10 }, (_, i) => i + 1);
function expectNoPositions() {
  expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
  expect(screen.queryByRole("columnheader", { name: "本页序号" })).toBeNull();
}

const detail = (index = 1) => ({
  dictionaryVersion: 3,
  selection: selection(),
  observationId: "observation-one",
  source,
  object: row(index),
});
const clearKeyword = () =>
  screen.getByRole("button", { name: "清除用户资产关键词" });
const keyword = () => screen.getByLabelText("用户资产关键词", { exact: true });
const pasteInvalid = (label: string) =>
  fireEvent.paste(screen.getByLabelText(label, { exact: true }), {
    clipboardData: { getData: () => "bad\ninput" },
  });
const detailButton = (index = 1) =>
  screen.getByRole("button", { name: `查看用户 observed-${index}` });
const exportButton = () =>
  screen.getByRole("button", { name: "导出匹配用户 XLSX" });
function openFilters() {
  fireEvent.click(screen.getByText("精确账户与创建时间筛选"));
}

const limitMessage =
  "关键词最多 50 个 UTF-16 单元，已拒绝整次变更并保留此前内容；请重新编辑或清除关键词。";
const input = () => keyword() as HTMLInputElement;
function selectKeyword(
  start: number,
  end = start,
  direction: "forward" | "backward" | "none" = "none",
) {
  input().focus();
  input().setSelectionRange(start, end, direction);
  fireEvent.select(input());
}

describe("FLD-0330 whole-change UTF-16 keyword limit", () => {
  it.each(["a".repeat(50), "😀".repeat(25)])(
    "accepts exactly 50 UTF-16 units without querying: %s",
    async (value) => {
      await start();
      fill("用户资产关键词", value);
      expect(input()).toHaveValue(value);
      expect(calls()).toHaveLength(1);
      expect(input()).not.toHaveAttribute("maxlength");
    },
  );

  it("rejects the entire 51st-unit change and restores the previous caret", async () => {
    await start();
    const previous = "a".repeat(50);
    fill("用户资产关键词", previous);
    selectKeyword(12);
    fireEvent.change(input(), {
      target: { value: previous.slice(0, 12) + "Z" + previous.slice(12) },
    });
    expect(input()).toHaveValue(previous);
    expect(input()).toHaveFocus();
    expect(input().selectionStart).toBe(12);
    expect(input().selectionEnd).toBe(12);
    expect(screen.getByText(limitMessage)).toBeVisible();
    expect(screen.getByRole("button", { name: "搜索用户" })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "导出匹配用户 XLSX" }),
    ).toBeDisabled();
    expect(calls()).toHaveLength(1);
    fill("用户资产关键词", "shorter");
    expect(screen.queryByText(limitMessage)).toBeNull();
    expect(input()).toHaveValue("shorter");
  });

  it("uses replacement length for paste and retains selection direction on rejection", async () => {
    await start();
    const previous = "a".repeat(50);
    fill("用户资产关键词", previous);
    selectKeyword(10, 12, "backward");
    expect(
      fireEvent.paste(input(), { clipboardData: { getData: () => "XYZ" } }),
    ).toBe(false);
    expect(input()).toHaveValue(previous);
    expect(input().selectionStart).toBe(10);
    expect(input().selectionEnd).toBe(12);
    expect(input().selectionDirection).toBe("backward");
    expect(
      fireEvent.paste(input(), { clipboardData: { getData: () => "😀" } }),
    ).toBe(true);
    // jsdom has no native paste insertion; this change is explicitly synthetic.
    const replaced = previous.slice(0, 10) + "😀" + previous.slice(12);
    fireEvent.change(input(), { target: { value: replaced } });
    expect(input()).toHaveValue(replaced);
    expect(replaced.length).toBe(50);
    expect(screen.queryByText(limitMessage)).toBeNull();
    selectKeyword(0, 50);
    expect(
      fireEvent.paste(input(), {
        clipboardData: { getData: () => "replacement" },
      }),
    ).toBe(true);
    fireEvent.change(input(), { target: { value: "replacement" } });
    expect(input()).toHaveValue("replacement");
    expect(calls()).toHaveLength(1);
  });

  it("never splits a surrogate pair and retains existing malformed-surrogate submit validation", async () => {
    await start();
    fill("用户资产关键词", "a".repeat(49));
    selectKeyword(49);
    fireEvent.change(input(), { target: { value: "a".repeat(49) + "😀" } });
    expect(input()).toHaveValue("a".repeat(49));
    fill("用户资产关键词", "\ud83d");
    click("搜索用户");
    expect(screen.getByText(/搜索最多 50 个 UTF-16/)).toBeVisible();
    expect(calls()).toHaveLength(1);
  });

  it("rejects oversized drop payloads and validates the final value for uncertain drop positions", async () => {
    await start();
    fill("用户资产关键词", "a".repeat(49));
    selectKeyword(0);
    expect(
      fireEvent.drop(input(), {
        dataTransfer: { getData: () => "x".repeat(51) },
      }),
    ).toBe(false);
    expect(input()).toHaveValue("a".repeat(49));
    expect(
      fireEvent.drop(input(), { dataTransfer: { getData: () => "XY" } }),
    ).toBe(true);
    // Synthetic actual insertion at a different position than the remembered caret.
    fireEvent.change(input(), {
      target: { value: "a".repeat(10) + "XY" + "a".repeat(39) },
    });
    expect(input()).toHaveValue("a".repeat(49));
    expect(screen.getByText(limitMessage)).toBeVisible();
    expect(calls()).toHaveLength(1);
  });

  it("allows temporary IME overflow but blocks form submit and rolls back the whole rejected composition", async () => {
    await start();
    fill("用户资产关键词", "previous");
    selectKeyword(2, 5, "backward");
    fireEvent.compositionStart(input());
    const tooLong = "汉".repeat(51);
    fireEvent.change(input(), { target: { value: tooLong } });
    expect(input()).toHaveValue(tooLong);
    expect(screen.getByRole("button", { name: "搜索用户" })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "导出匹配用户 XLSX" }),
    ).toBeDisabled();
    expect(clearKeyword()).toBeDisabled();
    expect(screen.getByRole("button", { name: "清除用户筛选" })).toBeDisabled();
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
    fill("用户资产关键词", "previou");
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
    expect(screen.getByRole("button", { name: "搜索用户" })).toBeEnabled();
    expect(calls()).toHaveLength(1);
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
    fill("用户资产关键词", "accepted");
    fireEvent.compositionStart(input());
    fireEvent.change(input(), { target: { value: "composing" } });
    const refresh = screen.getByRole("button", { name: "刷新用户资产观测" });
    expect(refresh).toBeDisabled();
    fireEvent.click(refresh);
    expect(calls()).toHaveLength(1);
    expect(input()).toHaveValue("composing");
    fireEvent.compositionEnd(input());
    expect(refresh).toBeEnabled();
    fireEvent.input(input(), {
      target: { value: "new input" },
      inputType: "insertText",
    });
    expect(input()).toHaveValue("new input");
    click("刷新用户资产观测");
    await screen.findByRole("table", { name: "用户资产列表" });
    expect(input()).toHaveValue("new input");
    expect(params().search).toBe("");
    expect(params().observationId).toBeUndefined();
    fireEvent.click(clearKeyword());
    expect(input()).toHaveValue("");
  });

  it("does not restore composing private state after an in-flight access revocation", async () => {
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/user-assets/v3") ? held.promise : undefined;
    await mount();
    await select();
    const oldInput = input();
    fireEvent.compositionStart(oldInput);
    fireEvent.change(oldInput, { target: { value: "unfinished" } });
    await held.resolve({ error: "forbidden" }, 403);
    await waitFor(() =>
      expect(
        screen.queryByLabelText("用户资产关键词", { exact: true }),
      ).toBeNull(),
    );
    fireEvent.compositionEnd(oldInput, { data: "unfinished" });
    fireEvent.change(oldInput, { target: { value: "unfinished" } });
    expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
    expect(
      screen.queryByLabelText("用户资产关键词", { exact: true }),
    ).toBeNull();
  });

  it("resets the accepted draft after whole-filter clear and ignores unmounted composition state", async () => {
    const view = await start();
    fill("用户资产关键词", "old-draft");
    click("清除用户筛选");
    await screen.findByRole("table", { name: "用户资产列表" });
    fill("用户资产关键词", "x".repeat(51));
    expect(input()).toHaveValue("");
    fireEvent.compositionStart(input());
    fireEvent.change(input(), { target: { value: "x".repeat(51) } });
    const oldInput = input();
    view.unmount();
    fireEvent.compositionEnd(oldInput);
    fireEvent.input(oldInput, { target: { value: "late" } });
    expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
  });
  it.each(["paste", "drop"])(
    "prioritizes control rejection over oversized %s",
    async (kind) => {
      await start();
      fill("用户资产关键词", "accepted");
      const data = { getData: () => "x".repeat(51) + "\n" };
      if (kind === "paste") fireEvent.paste(input(), { clipboardData: data });
      else fireEvent.drop(input(), { dataTransfer: data });
      expect(input()).toHaveValue("accepted");
      expect(screen.getByText(/含控制字符/)).toBeVisible();
      expect(screen.queryByText(limitMessage)).toBeNull();
      expect(calls()).toHaveLength(1);
    },
  );

  it("keeps pending applied read, sort and pin after rejected edit and draft-only clear", async () => {
    await start();
    openFilters();
    fill("创建时间排序", "desc");
    fill("用户资产关键词", "observed-12");
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/user-assets/v3") ? held.promise : undefined;
    click("搜索用户");
    const applied = params();
    const signal = calls().at(-1)![1].signal as AbortSignal;
    fill("用户资产关键词", "x".repeat(51));
    expect(input()).toHaveValue("observed-12");
    expect(signal.aborted).toBe(false);
    await held.resolve(
      list({ ...q, search: "observed-12", creatTm: "desc" }, [row(12)], 1),
    );
    await screen.findByRole("table", { name: "用户资产列表" });
    expect(screen.getByText("observed-12", { exact: true })).toBeVisible();
    expect(detailButton(12)).toBeDisabled();
    fireEvent.click(clearKeyword());
    expect(input()).toHaveValue("");
    expect(input()).toHaveFocus();
    expect(screen.getByLabelText("创建时间排序", { exact: true })).toHaveValue(
      "desc",
    );
    expect(params()).toEqual(applied);
    expect(calls()).toHaveLength(2);
  });

  it.each(["完成", "汉".repeat(51)])(
    "finalizes composition on blur without compositionend: %s",
    async (value) => {
      await start();
      fill("用户资产关键词", "accepted");
      fireEvent.compositionStart(input());
      fireEvent.change(input(), { target: { value } });
      expect(screen.getByLabelText("用户资产每页条数")).toBeDisabled();
      fireEvent.blur(input());
      expect(input()).toHaveValue(value.length > 50 ? "accepted" : value);
      expect(clearKeyword()).toBeEnabled();
      fireEvent.compositionEnd(input());
      if (value.length > 50)
        expect(screen.getByText(limitMessage)).toBeVisible();
      fireEvent.click(clearKeyword());
      expect(input()).toHaveValue("");
      expect(calls()).toHaveLength(1);
    },
  );
  it.each(["reject", "composition"] as const)(
    "invalidates held detail on %s without aborting the accepted list",
    async (kind) => {
      for (const status of [200, 403]) {
        const changed = vi.fn();
        await start(changed);
        const held = deferred();
        override = (url) =>
          url.split("?")[0] === "/api/user-assets/v3/detail"
            ? held.promise
            : undefined;
        fireEvent.click(detailButton());
        await screen.findByRole("region", { name: "用户详情" });
        const detailSignal = calls("/api/user-assets/v3/detail").at(-1)![1]
          .signal as AbortSignal;
        const listSignal = calls().at(-1)![1].signal as AbortSignal;
        const before = calls().length;
        if (kind === "reject") fill("用户资产关键词", "x".repeat(51));
        else fireEvent.compositionStart(input());
        expect(detailSignal.aborted).toBe(true);
        expect(listSignal.aborted).toBe(false);
        expect(screen.queryByRole("region", { name: "用户详情" })).toBeNull();
        await held.resolve(
          status === 200 ? detail() : { error: "forbidden" },
          status,
        );
        expect(changed).not.toHaveBeenCalled();
        expectPositions(firstPage);
        expect(screen.queryByRole("region", { name: "用户详情" })).toBeNull();
        expect(calls()).toHaveLength(before);
        cleanup();
        override = () => undefined;
      }
    },
  );

  it("retains SAM and time transfer errors when length rejection and keyword-only clear run", async () => {
    await start();
    openFilters();
    fill("精确账户名（SAM）", "exact-draft");
    fill("创建时间开始（含）", "2026-01-01T00:00:00Z");
    fill("本次观测的禁用位", "not_returned");
    pasteInvalid("精确账户名（SAM）");
    pasteInvalid("创建时间开始（含）");
    fill("用户资产关键词", "accepted");
    fill("用户资产关键词", "x".repeat(51));
    expect(screen.getByRole("alert")).toHaveTextContent(
      "精确账户名（SAM）含控制字符",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "创建时间开始（含）含控制字符",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(limitMessage);
    fireEvent.click(clearKeyword());
    expect(screen.getByRole("alert")).not.toHaveTextContent(limitMessage);
    expect(
      screen.getByLabelText("精确账户名（SAM）", { exact: true }),
    ).toHaveValue("exact-draft");
    expect(
      screen.getByLabelText("精确账户名（SAM）", { exact: true }),
    ).toHaveAttribute("aria-invalid", "true");
    expect(
      screen.getByLabelText("创建时间开始（含）", { exact: true }),
    ).toHaveAttribute("aria-invalid", "true");
    expect(
      screen.getByLabelText("本次观测的禁用位", { exact: true }),
    ).toHaveValue("not_returned");
    expect(screen.getByRole("button", { name: "搜索用户" })).toBeDisabled();
    expect(calls()).toHaveLength(1);
    expectPositions(firstPage);
  });

  it("preserves a list error through length rejection and recovery without an implicit retry", async () => {
    await start();
    override = (url) =>
      url.split("?")[0] === "/api/user-assets/v3"
        ? response({ error: "invalid_input" }, 400)
        : undefined;
    fill("用户资产关键词", "observed");
    click("搜索用户");
    await screen.findByRole("button", { name: "重试用户资产读取" });
    const oldError = screen.getByRole("alert").textContent;
    const before = fetcher.mock.calls.length;
    fill("用户资产关键词", "x".repeat(51));
    expect(screen.getByRole("alert")).toHaveTextContent(limitMessage);
    fill("用户资产关键词", "shorter");
    expect(screen.getByRole("alert").textContent).toBe(oldError);
    expect(fetcher.mock.calls).toHaveLength(before);
    expectNoPositions();
  });

  it.each(["source", "session", "unmount"] as const)(
    "discards late composition/input after %s changes",
    async (kind) => {
      const view = await start();
      const oldInput = input();
      fireEvent.compositionStart(oldInput);
      fireEvent.change(oldInput, { target: { value: "private-unfinished" } });
      if (kind === "source") {
        click("更换数据源");
        await select("other.invalid");
        await screen.findByRole("table", { name: "用户资产列表" });
      } else if (kind === "session") {
        view.rerender(
          <UserAssetsV3Workspace
            profile={{ ...profile, csrfToken: "new-session" }}
            sessionChanged={vi.fn()}
          />,
        );
        await screen.findByRole("radio", {
          name: "选择数据源 synthetic.invalid",
        });
        await select();
        await screen.findByRole("table", { name: "用户资产列表" });
      } else view.unmount();
      const before = fetcher.mock.calls.length;
      fireEvent.compositionEnd(oldInput, { data: "private-unfinished" });
      fireEvent.input(oldInput, { target: { value: "private-unfinished" } });
      fireEvent.blur(oldInput);
      expect(fetcher.mock.calls).toHaveLength(before);
      if (kind !== "unmount") {
        expect(input()).toHaveValue("");
        expect(clearKeyword()).toBeDisabled();
        expectPositions(firstPage);
      } else
        expect(
          screen.queryByLabelText("用户资产关键词", { exact: true }),
        ).toBeNull();
    },
  );

  it.each(["keyword", "all"] as const)(
    "resets selection and rejected-composition memory after %s clear",
    async (kind) => {
      await start();
      fill("用户资产关键词", "previous");
      selectKeyword(2, 5, "backward");
      fireEvent.compositionStart(input());
      fireEvent.change(input(), { target: { value: "汉".repeat(51) } });
      fireEvent.compositionEnd(input());
      if (kind === "keyword") fireEvent.click(clearKeyword());
      else {
        click("清除用户筛选");
        await screen.findByRole("table", { name: "用户资产列表" });
      }
      fill("用户资产关键词", "x".repeat(51));
      expect(input()).toHaveValue("");
      expect(input().selectionStart).toBe(0);
      expect(input().selectionEnd).toBe(0);
      // The old restored value is now an ordinary new edit, not a trailing event.
      fill("用户资产关键词", "previous");
      expect(input()).toHaveValue("previous");
      expect(screen.queryByText(limitMessage)).toBeNull();
    },
  );

  it("keeps page, accepted keyword, row positions and export locked even before composition changes the value", async () => {
    await start();
    click("用户资产下一页");
    await waitFor(() =>
      expectPositions(Array.from({ length: 10 }, (_, i) => i + 11)),
    );
    const before = fetcher.mock.calls.length;
    const applied = params();
    fireEvent.compositionStart(input());
    expect(screen.getByLabelText("用户资产每页条数")).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "用户资产上一页" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "用户资产下一页" }),
    ).toBeDisabled();
    expect(exportButton()).toBeDisabled();
    fireEvent.submit(input().closest("form")!);
    expect(fetcher.mock.calls).toHaveLength(before);
    expect(params()).toEqual(applied);
    expectPositions(Array.from({ length: 10 }, (_, i) => i + 11));
    fireEvent.compositionEnd(input());
    expect(
      screen.getByRole("button", { name: "用户资产上一页" }),
    ).toBeEnabled();
    expect(exportButton()).toBeEnabled();
  });

  it("uses trusted-style keyboard editing without relying on a truncated insertion", async () => {
    const user = userEvent.setup();
    await start();
    await user.type(input(), "a".repeat(50));
    await user.type(input(), "Z");
    expect(input()).toHaveValue("a".repeat(50));
    expect(input().selectionStart).toBe(50);
    expect(calls()).toHaveLength(1);
  });
  it("recovers keyword editing after stale observation refresh without reviving the old composition node", async () => {
    await start();
    const oldInput = input();
    const staleRead = deferred();
    override = (url) =>
      url.split("?")[0] === "/api/user-assets/v3"
        ? staleRead.promise
        : undefined;
    click("刷新用户资产观测");
    fireEvent.compositionStart(oldInput);
    fireEvent.change(oldInput, { target: { value: "private-unfinished" } });
    await staleRead.resolve(
      { error: "directory_observation_unavailable" },
      409,
    );
    expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
    expect(input()).not.toBe(oldInput);
    expect(input()).toHaveValue("");
    const refreshed = deferred();
    override = (url) =>
      url.split("?")[0] === "/api/user-assets/v3"
        ? refreshed.promise
        : undefined;
    click("刷新用户资产观测");
    const before = fetcher.mock.calls.length;
    fireEvent.compositionEnd(oldInput);
    fireEvent.input(oldInput, { target: { value: "private-unfinished" } });
    expect(input()).toHaveValue("");
    fill("用户资产关键词", "observed-12");
    expect(input()).toHaveValue("observed-12");
    expect(fetcher.mock.calls).toHaveLength(before);
    await refreshed.resolve(
      list(
        q,
        Array.from({ length: 10 }, (_, i) => row(i + 1)),
        23,
        "observation-new",
      ),
    );
    await screen.findByRole("table", { name: "用户资产列表" });
    override = () => undefined;
    click("搜索用户");
    await screen.findByText("observed-12", { exact: true });
    expect(params().search).toBe("observed-12");
    expect(params().observationId).toBe("observation-new");
  });
});
