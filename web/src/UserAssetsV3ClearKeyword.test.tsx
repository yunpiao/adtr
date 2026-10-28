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
import {
  type ExportRecovery,
  clearExportRecovery,
  readExportRecovery,
  writeExportRecovery,
} from "./user-asset-export-v3-recovery";
import { exportKind } from "./user-asset-export-v3-api";
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

describe("UI-0494 draft-only user keyword clear", () => {
  it("preserves page, size, source, pin, accepted query, time and sort until explicit apply", async () => {
    await start();
    fill("用户资产每页条数", "20");
    await waitFor(() =>
      expectPositions(Array.from({ length: 20 }, (_, i) => i + 1)),
    );
    openFilters();
    fill("用户资产关键词", "observed");
    fill("创建时间开始（含）", "2026-01-01T00:00:00Z");
    fill("创建时间结束（不含）", "2026-02-01T00:00:00Z");
    fill("创建时间排序", "desc");
    click("搜索用户");
    await waitFor(() =>
      expectPositions(Array.from({ length: 20 }, (_, i) => 23 - i)),
    );
    click("用户资产下一页");
    await waitFor(() => expectPositions([3, 2, 1]));
    const applied = params();
    const before = fetcher.mock.calls.length;
    fireEvent.click(clearKeyword());
    expect(keyword()).toHaveValue("");
    expect(keyword()).toHaveFocus();
    expect(fetcher.mock.calls).toHaveLength(before);
    expect(params()).toEqual(applied);
    expect(
      screen.getByText("已应用搜索：“observed”", { exact: true }),
    ).toBeVisible();
    expect(
      screen.getByLabelText("创建时间开始（含）", { exact: true }),
    ).toHaveValue(applied.startTm);
    expect(
      screen.getByLabelText("创建时间结束（不含）", { exact: true }),
    ).toHaveValue(applied.endTm);
    expect(screen.getByLabelText("创建时间排序", { exact: true })).toHaveValue(
      "desc",
    );
    expect(screen.getByLabelText("用户资产每页条数")).toHaveValue("20");
    expect(
      screen.getByRole("button", { name: "用户资产上一页" }),
    ).toBeDisabled();
    expect(detailButton(3)).toBeDisabled();
    expect(exportButton()).toBeDisabled();
    expectPositions([3, 2, 1]);
    click("搜索用户");
    await waitFor(() =>
      expectPositions(Array.from({ length: 20 }, (_, i) => 23 - i)),
    );
    expect(params()).toEqual({ ...applied, search: "", pageIdx: "1" });
    expect(calls()).toHaveLength(5);
    expect(exportButton()).toBeEnabled();
  });

  it.each(["set", "clear", "not_returned"] as const)(
    "preserves exact SAM and %s UAC draft and applied pin",
    async (bit) => {
      await start();
      openFilters();
      const index = bit === "set" ? 1 : bit === "clear" ? 2 : 3;
      fill("用户资产关键词", "observed");
      fill("精确账户名（SAM）", `observed-${index}`);
      fill("本次观测的禁用位", bit);
      click("搜索用户");
      await waitFor(() => expectPositions([index]));
      const applied = params();
      const before = fetcher.mock.calls.length;
      fireEvent.click(clearKeyword());
      expect(
        screen.getByLabelText("精确账户名（SAM）", { exact: true }),
      ).toHaveValue(`observed-${index}`);
      expect(screen.getByLabelText("本次观测的禁用位")).toHaveValue(bit);
      expect(params()).toEqual(applied);
      expect(fetcher.mock.calls).toHaveLength(before);
      expectPositions([index]);
      expect(exportButton()).toBeDisabled();
      click("搜索用户");
      await waitFor(() => expect(exportButton()).toBeEnabled());
      expectPositions([index]);
      expect(params()).toEqual({ ...applied, search: "", pageIdx: "1" });
    },
  );

  it.each(["{Enter}", " "])(
    "supports keyboard %s and disables only the empty no-error action",
    async (key) => {
      const user = userEvent.setup();
      await start();
      expect(clearKeyword()).toBeDisabled();
      fill("用户资产关键词", "draft-only");
      keyword().focus();
      await user.tab();
      expect(clearKeyword()).toHaveFocus();
      const before = fetcher.mock.calls.length;
      await user.keyboard(key);
      expect(keyword()).toHaveValue("");
      expect(keyword()).toHaveFocus();
      expect(clearKeyword()).toBeDisabled();
      fireEvent.click(clearKeyword());
      expect(fetcher.mock.calls).toHaveLength(before);
      expectPositions(firstPage);
    },
  );

  it("recovers the empty keyword-only transfer error without applying or changing clean rows", async () => {
    await start();
    pasteInvalid("用户资产关键词");
    expect(clearKeyword()).toBeEnabled();
    expect(exportButton()).toBeDisabled();
    const before = fetcher.mock.calls.length;
    fireEvent.click(clearKeyword());
    expect(keyword()).toHaveValue("");
    expect(keyword()).toHaveFocus();
    expect(clearKeyword()).toBeDisabled();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(exportButton()).toBeEnabled();
    expect(detailButton()).toBeEnabled();
    expectPositions(firstPage);
    expect(fetcher.mock.calls).toHaveLength(before);
  });

  it("clears an empty keyword transfer error but preserves SAM and both time transfer errors", async () => {
    await start();
    openFilters();
    pasteInvalid("用户资产关键词");
    for (const label of [
      "精确账户名（SAM）",
      "创建时间开始（含）",
      "创建时间结束（不含）",
    ])
      pasteInvalid(label);
    expect(keyword()).toHaveValue("");
    expect(clearKeyword()).toBeEnabled();
    const before = fetcher.mock.calls.length;
    fireEvent.click(clearKeyword());
    expect(keyword()).toHaveFocus();
    expect(keyword()).toHaveAttribute("aria-invalid", "false");
    expect(clearKeyword()).toBeDisabled();
    expect(screen.queryByText(/用户资产关键词含控制字符/)).toBeNull();
    for (const label of [
      "精确账户名（SAM）",
      "创建时间开始（含）",
      "创建时间结束（不含）",
    ])
      expect(screen.getByLabelText(label, { exact: true })).toHaveAttribute(
        "aria-invalid",
        "true",
      );
    expect(screen.getByRole("button", { name: "搜索用户" })).toBeDisabled();
    expect(exportButton()).toBeDisabled();
    expect(fetcher.mock.calls).toHaveLength(before);
  });

  it.each(["SAM", "time"])(
    "retains invalid other %s draft and validates again after shared notice clears",
    async (kind) => {
      await start();
      openFilters();
      fill("用户资产关键词", "draft");
      const label = kind === "SAM" ? "精确账户名（SAM）" : "创建时间开始（含）";
      const value = kind === "SAM" ? "x".repeat(257) : "invalid-time";
      fill(label, value);
      click("搜索用户");
      expect(screen.getByRole("alert")).toHaveTextContent(
        kind === "SAM" ? "精确 SAM 最多" : "创建时间须同时",
      );
      const before = fetcher.mock.calls.length;
      fireEvent.click(clearKeyword());
      expect(screen.queryByRole("alert")).toBeNull();
      expect(screen.getByLabelText(label, { exact: true })).toHaveValue(value);
      click("搜索用户");
      expect(screen.getByRole("alert")).toHaveTextContent(
        kind === "SAM" ? "精确 SAM 最多" : "创建时间须同时",
      );
      expect(fetcher.mock.calls).toHaveLength(before);
      expectPositions(firstPage);
    },
  );

  it("preserves the existing 50 UTF-16-unit submit rejection without truncation", async () => {
    await start();
    const value = "x".repeat(51);
    fill("用户资产关键词", value);
    click("搜索用户");
    expect(keyword()).toHaveValue(value);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "搜索最多 50 个 UTF-16 单元",
    );
    expect(calls()).toHaveLength(1);
    fireEvent.click(clearKeyword());
    expect(screen.queryByRole("alert")).toBeNull();
    expect(calls()).toHaveLength(1);
  });

  it("preserves a non-search read error and does not retry it implicitly", async () => {
    await start();
    override = (url) =>
      url.split("?")[0] === "/api/user-assets/v3"
        ? response({ error: "invalid_input" }, 400)
        : undefined;
    fill("用户资产关键词", "observed");
    click("搜索用户");
    await screen.findByRole("button", { name: "重试用户资产读取" });
    const message = screen.getByRole("alert").textContent;
    const before = fetcher.mock.calls.length;
    fireEvent.click(clearKeyword());
    expect(screen.getByRole("alert")).toHaveTextContent(message!);
    expect(fetcher.mock.calls).toHaveLength(before);
    expect(keyword()).toHaveValue("");
  });

  it("does not cancel the pending applied list, whose response retains the old applied query", async () => {
    await start();
    const held = deferred();
    override = (url) =>
      url.split("?")[0] === "/api/user-assets/v3" ? held.promise : undefined;
    fill("用户资产关键词", "observed-23");
    click("搜索用户");
    const pendingSignal = calls().at(-1)![1].signal as AbortSignal;
    const before = fetcher.mock.calls.length;
    fireEvent.click(clearKeyword());
    expect(pendingSignal.aborted).toBe(false);
    expect(fetcher.mock.calls).toHaveLength(before);
    await held.resolve(list({ ...q, search: "observed-23" }, [row(23)], 1));
    expect(keyword()).toHaveValue("");
    expectPositions([23]);
    expect(
      screen.getByText("已应用搜索：“observed-23”", { exact: true }),
    ).toBeVisible();
    expect(detailButton(23)).toBeDisabled();
    expect(exportButton()).toBeDisabled();
  });

  it.each([200, 401, 403, 404, 409, 503])(
    "ignores superseded list status %s after clear and explicit apply",
    async (status) => {
      const changed = vi.fn();
      await start(changed);
      const held = deferred();
      override = (url) =>
        url.split("?")[0] === "/api/user-assets/v3" &&
        queryFrom(url).search !== ""
          ? held.promise
          : undefined;
      fill("用户资产关键词", "observed-23");
      click("搜索用户");
      const signal = calls().at(-1)![1].signal as AbortSignal;
      fireEvent.click(clearKeyword());
      expect(signal.aborted).toBe(false);
      click("搜索用户");
      expect(signal.aborted).toBe(true);
      await waitFor(() => expectPositions(firstPage));
      await held.resolve(
        status === 200
          ? list({ ...q, search: "observed-23" }, [row(23)], 1)
          : {
              error:
                status === 409
                  ? "selection_changed"
                  : status === 503
                    ? "dependency_unavailable"
                    : status === 401
                      ? "unauthenticated"
                      : status === 403
                        ? "forbidden"
                        : "not_found",
            },
        status,
      );
      expectPositions(firstPage);
      expect(keyword()).toHaveValue("");
      expect(changed).not.toHaveBeenCalled();
      expect(calls()).toHaveLength(3);
    },
  );

  it.each([200, 401, 403, 404, 409, 503])(
    "invalidates pending detail status %s without affecting list or source",
    async (status) => {
      const changed = vi.fn();
      await start(changed);
      fill("用户资产关键词", "observed");
      click("搜索用户");
      await waitFor(() => expectPositions(firstPage));
      const held = deferred();
      override = (url) =>
        url.split("?")[0] === "/api/user-assets/v3/detail"
          ? held.promise
          : undefined;
      fireEvent.click(detailButton());
      await screen.findByText("正在读取用户详情…");
      const signal = calls("/api/user-assets/v3/detail").at(-1)![1]
        .signal as AbortSignal;
      // A synthetic direct event exercises the same transition even while the drawer makes the background inert.
      const button = screen.getByRole("button", {
        name: "清除用户资产关键词",
        hidden: true,
      });
      const before = fetcher.mock.calls.length;
      fireEvent.click(button);
      expect(signal.aborted).toBe(true);
      expect(keyword()).toHaveFocus();
      await held.resolve(
        status === 200
          ? detail()
          : {
              error:
                status === 409
                  ? "selection_changed"
                  : status === 503
                    ? "dependency_unavailable"
                    : status === 401
                      ? "unauthenticated"
                      : status === 403
                        ? "forbidden"
                        : "not_found",
            },
        status,
      );
      expect(screen.queryByRole("region", { name: "用户详情" })).toBeNull();
      expectPositions(firstPage);
      expect(fetcher.mock.calls).toHaveLength(before);
      expect(changed).not.toHaveBeenCalled();
      expect(detailButton()).toBeDisabled();
      expect(exportButton()).toBeDisabled();
    },
  );

  it("keeps the old whole-filter clear behavior and removes all transfer errors", async () => {
    await start();
    openFilters();
    fill("用户资产关键词", "observed");
    fill("精确账户名（SAM）", "observed-1");
    fill("创建时间开始（含）", "2026-01-01T00:00:00Z");
    fill("创建时间结束（不含）", "2026-02-01T00:00:00Z");
    fill("创建时间排序", "desc");
    fill("本次观测的禁用位", "set");
    click("搜索用户");
    await waitFor(() => expectPositions([1]));
    pasteInvalid("用户资产关键词");
    pasteInvalid("精确账户名（SAM）");
    click("清除用户筛选");
    await waitFor(() => expectPositions(firstPage));
    for (const label of [
      "用户资产关键词",
      "精确账户名（SAM）",
      "创建时间开始（含）",
      "创建时间结束（不含）",
      "创建时间排序",
      "本次观测的禁用位",
    ])
      expect(screen.getByLabelText(label, { exact: true })).toHaveValue("");
    expect(params()).toEqual({
      ...q,
      pageIdx: "1",
      pageSize: "10",
      observationId: "observation-one",
    });
    expect(screen.queryByRole("alert")).toBeNull();
    expect(exportButton()).toBeEnabled();
    expect(calls()).toHaveLength(3);
  });

  it("preserves the original export recovery locator while dirty new exports stay blocked", async () => {
    const recovery: ExportRecovery = {
      version: 1 as const,
      kind: exportKind,
      actorID: profile.ID,
      domainId: choice.domainId,
      idempotencyKey: "original-key",
    };
    writeExportRecovery(recovery);
    await start();
    fill("用户资产关键词", "observed");
    click("搜索用户");
    await waitFor(() => expectPositions(firstPage));
    const before = fetcher.mock.calls.length;
    fireEvent.click(clearKeyword());
    expect(readExportRecovery(profile.ID)).toEqual(recovery);
    // A stored locator intentionally keeps the recovery entry point available.
    expect(exportButton()).toBeEnabled();
    expect(detailButton()).toBeDisabled();
    expect(fetcher.mock.calls).toHaveLength(before);
  });

  it.each(["source", "session", "unmount"])(
    "cannot restore cleared private state after %s invalidation",
    async (reason) => {
      const view = await start();
      const held = deferred();
      override = (url) =>
        url.split("?")[0] === "/api/user-assets/v3" ? held.promise : undefined;
      fill("用户资产关键词", "observed-23");
      click("搜索用户");
      fireEvent.click(clearKeyword());
      if (reason === "source") {
        click("更换数据源");
        fireEvent.click(
          screen.getByRole("radio", { name: "选择数据源 other.invalid" }),
        );
      } else if (reason === "session")
        view.rerender(
          <UserAssetsV3Workspace
            profile={{ ...profile, csrfToken: "replacement-session" }}
            sessionChanged={vi.fn()}
          />,
        );
      else view.unmount();
      await held.resolve(list({ ...q, search: "observed-23" }, [row(23)], 1));
      expectNoPositions();
      expect(
        screen.queryByRole("button", { name: "清除用户资产关键词" }),
      ).toBeNull();
    },
  );

  it("disables keyword clear while its observation is stale", async () => {
    await start();
    fill("用户资产关键词", "observed");
    click("搜索用户");
    await waitFor(() => expectPositions(firstPage));
    override = (url) =>
      url.startsWith("/api/user-assets/v3")
        ? response({ error: "directory_observation_unavailable" }, 409)
        : undefined;
    click("用户资产下一页");
    await screen.findByText("原用户资产观测已不可用，请明确刷新观测。");
    expect(clearKeyword()).toBeDisabled();
    fireEvent.click(clearKeyword());
    expect(keyword()).toHaveValue("observed");
    expectNoPositions();
  });
});
