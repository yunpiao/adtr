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
import ComputerAssetsV3Workspace from "./ComputerAssetsV3Workspace";
import App from "./App";
import { SESSION_INVALIDATION_STORAGE } from "./session-invalidation";
import { ApiError, type Profile } from "./api";
import type { SourceChoice } from "./domain-selection-api";
import {
  directoryV2BodyLimit,
  directoryV2DisplayText,
} from "./directory-v2-json";
import { validDirectoryV2Object } from "./directory-v2-api";
import {
  validDirectoryV3Object,
  directoryV3DisplaySPN,
} from "./directory-v3-facts";
import { userAssetsV2Time } from "./user-assets-v2-filters";
import {
  parseComputerAssetsV3List,
  computerAssetsV3API,
  validComputerAssetsV3Search,
  computerAssetsV3Error,
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
  whenCreated: "0001-01-01T00:00:00Z",
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
const signal = () => new AbortController().signal;
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

describe("computer strict bounded fact parser", () => {
  it("rejects a structurally valid row outside the requested literal keyword", () => {
    const query = { ...q, search: "not-present-in-sam-sid-or-dn" };
    expect(() => parseComputerAssetsV3List(list(query), query)).toThrow(
      ApiError,
    );
  });
  it.each([
    ["OBSERVED-1", {}, true],
    ["s-1-5-21", {}, true],
    ["DC=SYNTHETIC", {}, true],
    ["Observed@Synthetic", {}, false],
    ["HOST/Z", {}, false],
    ["Factual description", {}, false],
    ["not-a-null-alias", { samAccountName: null, objectSid: null }, false],
    ["a%_?*[x]", { samAccountName: "prefixA%_?*[x]suffix" }, true],
    ["a.*b", { samAccountName: "axxxb" }, false],
    [" literal ", { samAccountName: "literal" }, false],
    ["i", { samAccountName: "İ", distinguishedName: "CN=X" }, true],
    ["i\u0307", { samAccountName: "İ", distinguishedName: "CN=X" }, false],
    ["ΟΣ", { samAccountName: "οσ", distinguishedName: "CN=X" }, true],
    ["ΟΣ", { samAccountName: "ος", distinguishedName: "CN=X" }, false],
    ["é", { samAccountName: "e\u0301", distinguishedName: "CN=X" }, false],
    ["𐐀", { samAccountName: "𐐨", distinguishedName: "CN=X" }, true],
  ] as const)(
    "validates Go literal search %s against allowed facts",
    (search, change, matches) => {
      const query = { ...q, search };
      const value = list(query, [{ ...row(), ...change }]);
      if (matches)
        expect(parseComputerAssetsV3List(value, query).list).toEqual(
          value.list,
        );
      else
        expect(() => parseComputerAssetsV3List(value, query)).toThrow(ApiError);
    },
  );
  it("validates every row rather than accepting one matching row", () => {
    const query = { ...q, search: "observed-1" };
    expect(() =>
      parseComputerAssetsV3List(list(query, [row(1), row(2)]), query),
    ).toThrow(ApiError);
  });
  it("distinguishes unavailable, observed zero and no matches", () => {
    expect(parseComputerAssetsV3List(absent(), q).observedComputerCount).toBe(
      null,
    );
    expect(
      parseComputerAssetsV3List(
        { ...list(q, [], 0), observedComputerCount: 0 },
        q,
      ).observedComputerCount,
    ).toBe(0);
    expect(
      parseComputerAssetsV3List(list({ ...q, search: "missing" }, [], 0), {
        ...q,
        search: "missing",
      }).observedComputerCount,
    ).toBe(12);
  });
  it.each([null, -1, 1.5, 10001, "12", undefined])(
    "rejects available count %s",
    (value) => {
      expect(() =>
        parseComputerAssetsV3List(
          { ...list(), observedComputerCount: value },
          q,
        ),
      ).toThrow();
    },
  );
  it("rejects count inconsistencies, wrong versions/kinds and added fields", () => {
    for (const value of [
      { ...list(), observedComputerCount: 23 },
      { ...list(), observedComputerCount: 0 },
      { ...absent(), observedComputerCount: 0 },
      { ...list(), dictionaryVersion: 2 },
      {
        ...list(),
        list: [{ ...row(), kind: "user", objectClass: ["top", "user"] }],
      },
      { ...list(), risk: 0 },
      { ...list(), list: [{ ...row(), DNSAddress: "192.0.2.1" }] },
    ])
      expect(() => parseComputerAssetsV3List(value, q)).toThrow();
  });
  it("checks source pins, unique GUIDs, ordering and row/page math", () => {
    for (const value of [
      { ...list(), selection: { ...selection(), revision: "1" } },
      {
        ...list(),
        list: [row(2), row(1)],
        page: { pageIdx: 1, pageSize: 10, total: 2, totalPage: 1 },
      },
      {
        ...list(),
        list: [row(), row()],
        page: { pageIdx: 1, pageSize: 10, total: 2, totalPage: 1 },
      },
      { ...list(), page: { pageIdx: 1, pageSize: 10, total: 2, totalPage: 1 } },
    ])
      expect(() => parseComputerAssetsV3List(value, q)).toThrow();
    expect(() =>
      parseComputerAssetsV3List(list(), { ...q, observationId: "other" }),
    ).toThrow();
  });
  it("rejects a wrong actor before body acceptance and serializes only bounded parameters", async () => {
    override = (url) =>
      url.startsWith("/api/computer-assets/v3")
        ? response(list(), 200, "2")
        : undefined;
    await expect(
      computerAssetsV3API.list(q, 1, signal()),
    ).rejects.toMatchObject({ code: "unauthenticated", status: 401 });
    expect(calls()[0][1]).toMatchObject({
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
    });
    expect(params()).toEqual({ ...q, pageIdx: "1", pageSize: "10" });
  });
});
describe("computer private workspace", () => {
  it("clears prior facts and rejects a successful response outside the active keyword", async () => {
    await start();
    override = (url) =>
      url.startsWith("/api/computer-assets/v3")
        ? response(list(queryFrom(url), [row(1)]))
        : undefined;
    fill("计算机资产关键词", "observed-2");
    click("应用计算机筛选");
    await screen.findByText(
      "服务器结果无法安全确认，未展示计算机资产。请重试原读取。",
    );
    expect(screen.queryByRole("table", { name: "计算机资产列表" })).toBeNull();
    expect(screen.queryByText("observed-1")).toBeNull();
  });
  it("paginates within captured observation and expands only pinned list facts", async () => {
    await start();
    expect(params().observationId).toBeUndefined();
    fireEvent.click(screen.getAllByRole("button", { name: "展开事实" })[0]);
    expect(screen.getByText("主组 RID（primaryGroupID）")).toBeVisible();
    expect(screen.getByText("host/a\\u0000\\u000a😀<script>")).toBeVisible();
    expect(calls("/api/computer-assets/v3/detail")).toHaveLength(0);
    click("下一页");
    await screen.findByText("observed-11");
    expect(params().observationId).toBe("observation-one");
    expect(screen.queryByText("主组 RID（primaryGroupID）")).toBeNull();
    click("刷新计算机资产观测");
    await screen.findByText("observed-1");
    expect(params().observationId).toBeUndefined();
  });
  it("keeps draft separate, closes facts and preserves pin across filters", async () => {
    await start();
    fireEvent.click(screen.getAllByRole("button", { name: "展开事实" })[0]);
    fill("计算机资产关键词", "observed-12");
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    expect(screen.queryByText("主组 RID（primaryGroupID）")).toBeNull();
    click("应用计算机筛选");
    await waitFor(() =>
      expect(screen.getAllByRole("button", { name: "展开事实" })).toHaveLength(
        1,
      ),
    );
    expect(params().observationId).toBe("observation-one");
  });
  it.each([401, 403, 404])(
    "clears facts/source on access status %s",
    async (status) => {
      const changed = vi.fn();
      await start(changed);
      fireEvent.click(screen.getAllByRole("button", { name: "展开事实" })[0]);
      override = (url) =>
        url.startsWith("/api/computer-assets/v3")
          ? response(
              {
                error:
                  status === 401
                    ? "unauthenticated"
                    : status === 403
                      ? "forbidden"
                      : "not_found",
              },
              status,
            )
          : undefined;
      click("下一页");
      await waitFor(() =>
        expect(
          screen.queryByRole("table", { name: "计算机资产列表" }),
        ).toBeNull(),
      );
      expect(screen.queryByText("主组 RID（primaryGroupID）")).toBeNull();
      if (status === 401) expect(changed).toHaveBeenCalled();
    },
  );
  it("rejects delayed ignored-abort data after unmount", async () => {
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/computer-assets/v3") ? held.promise : undefined;
    const view = await mount();
    await select();
    view.unmount();
    await held.resolve(list());
    expect(screen.queryByRole("table")).toBeNull();
  });
  it("shows separate no observation, zero computer and zero match messages", async () => {
    override = (url) =>
      url.startsWith("/api/computer-assets/v3")
        ? response(absent(queryFrom(url)))
        : undefined;
    await mount();
    await select();
    await screen.findByText("暂无可读取的已完成观测，尚不能确定计算机数量。");
    override = (url) =>
      url.startsWith("/api/computer-assets/v3")
        ? response({ ...list(queryFrom(url), [], 0), observedComputerCount: 0 })
        : undefined;
    click("刷新计算机资产观测");
    await screen.findByText("本次观测未返回计算机对象。");
    override = (url) =>
      url.startsWith("/api/computer-assets/v3")
        ? response(list(queryFrom(url), [], 0))
        : undefined;
    fill("计算机资产关键词", "missing");
    click("应用计算机筛选");
    await screen.findByText("本次观测有计算机，但没有符合当前筛选的结果。");
  });
  it("blocks destructive native control-character paste before input rewriting", async () => {
    await start();
    const input = screen.getByLabelText("计算机资产关键词", { exact: true });
    fireEvent.paste(input, {
      clipboardData: { getData: () => "observed-1\n2" },
    });
    expect(input).toHaveValue("");
    expect(
      screen.getByRole("button", { name: "应用计算机筛选" }),
    ).toBeDisabled();
    fill("计算机资产关键词", "observed-12");
    expect(
      screen.getByRole("button", { name: "应用计算机筛选" }),
    ).toBeEnabled();
  });
});
