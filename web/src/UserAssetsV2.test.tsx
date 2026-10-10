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
import UserAssetsV2Workspace from "./UserAssetsV2Workspace";
import App from "./App";
import { SESSION_INVALIDATION_STORAGE } from "./session-invalidation";
import { ApiError, type Profile } from "./api";
import type { SourceChoice } from "./domain-selection-api";
import {
  directoryV2BodyLimit,
  directoryV2DisplayText,
} from "./directory-v2-json";
import { parseDirectoryV2Observation } from "./directory-v2-api";
import {
  parseUserAssetV2Detail,
  parseUserAssetsV2List,
  userAssetsV2API,
  validUserAssetsV2Search,
  userAssetsV2Error,
  type UserAssetsV2Query,
  type UserAssetV2Object,
} from "./user-assets-v2-api";

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
const q: UserAssetsV2Query = {
  domainId: choice.domainId,
  expectedRevision: choice.revision,
  expectedCredentialRevision: choice.credentialRevision,
  search: "",
  pageIdx: 1,
  pageSize: 10,
};
const row = (index = 1): UserAssetV2Object => ({
  objectGUID: `00000000-0000-0000-0000-${String(index).padStart(12, "0")}`,
  distinguishedName: `CN=User ${index},DC=synthetic,DC=invalid`,
  kind: "user",
  objectClass: ["top", "user"],
  samAccountName: `observed-${index}`,
  userAccountControl: 0,
  objectSid: "S-1-5-21-1-2-3-500",
  mail: " Observed@Synthetic.invalid ",
  description: ["Factual description"],
  whenCreated: "0001-01-01T00:00:00Z",
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
  dictionaryVersion: 2,
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
const absent = (query = q) => ({
  dictionaryVersion: 2,
  selection: selection(query),
  available: false,
  list: [],
  page: {
    pageIdx: query.pageIdx,
    pageSize: query.pageSize,
    total: 0,
    totalPage: 0,
  },
});
const detailQuery = {
  domainId: q.domainId,
  expectedRevision: q.expectedRevision,
  expectedCredentialRevision: q.expectedCredentialRevision,
  observationId: "observation-one",
  objectGUID: row().objectGUID,
};
const detail = (object = row(), query = detailQuery) => ({
  dictionaryVersion: 2,
  selection: {
    domainId: query.domainId,
    revision: query.expectedRevision,
    credentialRevision: query.expectedCredentialRevision,
  },
  observationId: query.observationId,
  source,
  object,
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
const calls = (path = "/api/user-assets/v2") =>
  fetcher.mock.calls.filter(([url]) => url.split("?")[0] === path);
const params = (path = "/api/user-assets/v2") =>
  Object.fromEntries(
    new URL(calls(path).at(-1)![0], "https://local.invalid").searchParams,
  );
function queryFrom(url: string): UserAssetsV2Query {
  const p = new URL(url, "https://local.invalid").searchParams;
  return {
    domainId: p.get("domainId")!,
    expectedRevision: p.get("expectedRevision")!,
    expectedCredentialRevision: p.get("expectedCredentialRevision")!,
    search: p.get("search")!,
    pageIdx: Number(p.get("pageIdx")),
    pageSize: Number(p.get("pageSize")),
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
      return response({ results: [true, true, true, true] });
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
    if (path === "/api/user-assets/v2") {
      const query = queryFrom(url);
      const all = Array.from({ length: 12 }, (_, i) => row(i + 1));
      const filtered = query.search
        ? all.filter((r) => r.samAccountName!.includes(query.search))
        : all;
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
    if (path === "/api/user-assets/v2/detail") {
      const p = new URL(url, "https://local.invalid").searchParams;
      const query = {
        domainId: p.get("domainId")!,
        expectedRevision: p.get("expectedRevision")!,
        expectedCredentialRevision: p.get("expectedCredentialRevision")!,
        observationId: p.get("observationId")!,
        objectGUID: p.get("objectGUID")!,
      };
      return response(detail(row(Number(query.objectGUID.slice(-12))), query));
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
    <UserAssetsV2Workspace profile={profile} sessionChanged={changed} />,
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

describe("UserAssetsV2 strict parser and transport", () => {
  it("preserves exact ten-key facts, null versus zero and year 0001", () => {
    expect(parseUserAssetsV2List(list(), q).list[0]).toEqual(row());
    const nullable = {
      ...row(),
      samAccountName: null,
      userAccountControl: null,
      objectSid: null,
      mail: null,
      description: null,
      whenCreated: null,
    };
    expect(
      parseUserAssetV2Detail(detail(nullable), detailQuery).object,
    ).toEqual(nullable);
    expect(
      parseUserAssetV2Detail(detail(), detailQuery).object.whenCreated,
    ).toBe("0001-01-01T00:00:00Z");
  });
  it.each([10, 20, 30, 40, 50])(
    "enforces exact page arithmetic for size %s and past-end pages",
    (size) => {
      const query = {
        ...q,
        pageSize: size,
        pageIdx: 2,
        observationId: "observation-one",
      };
      const rows = Array.from({ length: 3 }, (_, i) => row(size + i + 1));
      expect(
        parseUserAssetsV2List(list(query, rows, size + 3), query).page
          .totalPage,
      ).toBe(2);
      const past = { ...query, pageIdx: 10000 };
      expect(
        parseUserAssetsV2List(list(past, [], size + 3), past).list,
      ).toEqual([]);
    },
  );
  it("distinguishes unavailable from observed empty and rejects pinned absence", () => {
    expect(parseUserAssetsV2List(absent(), q).available).toBe(false);
    expect(parseUserAssetsV2List(list(q, []), q).available).toBe(true);
    expect(() =>
      parseUserAssetsV2List(absent(), {
        ...q,
        observationId: "observation-one",
      }),
    ).toThrow("invalid_response");
    expect(() => parseUserAssetsV2List({ ...absent(), source }, q)).toThrow();
  });
  it.each([
    (v: any) => {
      v.dictionaryVersion = 1;
    },
    (v: any) => {
      v.extra = true;
    },
    (v: any) => {
      v.selection.revision = "1";
    },
    (v: any) => {
      v.selection.credentialRevision = "1";
    },
    (v: any) => {
      v.selection.domainId = "other-domain";
    },
    (v: any) => {
      v.selection.extra = "bad";
    },
    (v: any) => {
      v.observationId = "";
    },
    (v: any) => {
      v.source.pages = 101;
    },
    (v: any) => {
      v.source.extra = true;
    },
    (v: any) => {
      v.page.total = 10001;
    },
    (v: any) => {
      v.page.totalPage = 99;
    },
    (v: any) => {
      v.page.pageIdx = 2;
    },
    (v: any) => {
      v.page.pageSize = 50;
    },
    (v: any) => {
      v.list.push(row());
    },
    (v: any) => {
      v.list[0].kind = "computer";
      v.list[0].objectClass = ["computer", "top", "user"];
    },
    (v: any) => {
      v.list[0].mail = "";
    },
    (v: any) => {
      v.list[0].description = [];
    },
    (v: any) => {
      v.list[0].objectSid = "S-1-05-500";
    },
    (v: any) => {
      v.list[0].whenCreated = "0000-01-01T00:00:00Z";
    },
    (v: any) => {
      v.list[0].email = "invented";
    },
    (v: any) => {
      v.list[0].samAccountName = undefined;
    },
  ])(
    "rejects malformed envelopes, selection, fields and counts (%#)",
    (mutate) => {
      const value = structuredClone(list());
      mutate(value);
      expect(() => parseUserAssetsV2List(value, q)).toThrow("invalid_response");
    },
  );
  it("rejects duplicate/unordered GUIDs, mismatched pins and non-user/missing detail", () => {
    for (const rows of [
      [row(), row()],
      [row(2), row(1)],
    ])
      expect(() => parseUserAssetsV2List(list(q, rows), q)).toThrow();
    expect(() =>
      parseUserAssetsV2List(list(), {
        ...q,
        observationId: "other-observation",
      }),
    ).toThrow();
    for (const value of [
      { ...detail(), available: true },
      { ...detail(), object: row(2) },
      { ...detail(), observationId: "other-observation" },
      { ...detail(), object: null },
      { ...detail(), selection: { ...selection(), credentialRevision: 2 } },
      {
        ...detail(),
        object: { ...row(), kind: "group", objectClass: ["group", "top"] },
      },
    ])
      expect(() => parseUserAssetV2Detail(value, detailQuery)).toThrow();
  });
  it("validates literal 50 UTF-16 search without normalization or trimming", () => {
    for (const value of [
      "",
      " ",
      "x".repeat(50),
      "😀".repeat(25),
      "\u0000\u202e\n",
      "%_*? '\"\\()[]",
      "e\u0301",
    ])
      expect(validUserAssetsV2Search(value)).toBe(true);
    for (const value of ["x".repeat(51), "😀".repeat(26), "\ud800", "\udfff"])
      expect(validUserAssetsV2Search(value)).toBe(false);
  });
  it.each([
    { pageSize: -1 },
    { pageSize: 25 },
    { pageIdx: 0 },
    { pageIdx: 1.1 },
    { pageIdx: 10001 },
    { pageIdx: 2 },
    { observationId: "" },
    { expectedRevision: "01" },
    { expectedRevision: "9223372036854775808" },
    { expectedCredentialRevision: "0" },
    { search: "😀".repeat(26) },
    { search: "\ud800" },
  ])("rejects invalid request before fetching (%#)", async (change) => {
    await expect(
      userAssetsV2API.list({ ...q, ...change }, 1, signal()),
    ).rejects.toMatchObject({ code: "invalid_input" });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it("encodes raw query exactly once and uses actor-bound protected GET", async () => {
    const search = " +&%_?*'\\()\u0000\u202e😀 ";
    const query = { ...q, search, observationId: "observation-one" };
    override = () => response(list(query, []));
    await userAssetsV2API.list(query, 1, signal());
    expect(params()).toEqual({
      domainId: choice.domainId,
      expectedRevision: choice.revision,
      expectedCredentialRevision: choice.credentialRevision,
      observationId: "observation-one",
      search,
      pageIdx: "1",
      pageSize: "10",
    });
    expect(calls()[0][1]).toMatchObject({
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
    });
    expect(calls()[0][1].body).toBeUndefined();
  });
  it.each([null, "2", "01"])(
    "rejects actor header %s in list and detail",
    async (actor) => {
      override = () => response(list(), 200, actor);
      await expect(userAssetsV2API.list(q, 1, signal())).rejects.toMatchObject({
        code: "unauthenticated",
        status: 401,
      });
      override = () => response(detail(), 200, actor);
      await expect(
        userAssetsV2API.detail(detailQuery, 1, signal()),
      ).rejects.toMatchObject({ code: "unauthenticated", status: 401 });
    },
  );
  it.each(["missing-length", "lying-length", "invalid-utf8", "declared-limit"])(
    "bounds streaming %s responses",
    async (kind) => {
      const headers: Record<string, string> = { "X-ADTR-User-ID": "1" };
      if (kind === "lying-length") headers["Content-Length"] = "1";
      if (kind === "declared-limit")
        headers["Content-Length"] = String(directoryV2BodyLimit + 1);
      const bytes =
        kind === "invalid-utf8"
          ? new Uint8Array([0xc3, 0x28])
          : new Uint8Array(directoryV2BodyLimit + 1);
      override = () =>
        Promise.resolve(
          new Response(
            new ReadableStream({
              start(controller) {
                controller.enqueue(bytes);
                controller.close();
              },
            }),
            { headers },
          ),
        );
      await expect(userAssetsV2API.list(q, 1, signal())).rejects.toMatchObject({
        code:
          kind === "invalid-utf8"
            ? "invalid_response"
            : "directory_limit_exceeded",
      });
    },
  );
  it("preserves the old exact v2 envelope and page-size contract", async () => {
    const { selection: _selection, ...legacy } = list({ ...q, pageSize: 50 });
    expect(
      parseDirectoryV2Observation(legacy, {
        domainId: choice.domainId,
        kind: "",
        pageIdx: 1,
        pageSize: 50,
      }).list,
    ).toEqual([row()]);
    expect(() =>
      parseDirectoryV2Observation(
        { ...legacy, selection: selection() },
        { domainId: choice.domainId, kind: "", pageIdx: 1, pageSize: 50 },
      ),
    ).toThrow();
  });
});

describe("UserAssetsV2 private workspace", () => {
  it("successful source resolution collapses its chooser and focuses the asset query", async () => {
    await start();
    expect(document.getElementById("asset-source-picker")).not.toBeVisible();
    expect(screen.getByLabelText("用户资产关键词")).toHaveFocus();
    click("更换数据源");
    expect(document.getElementById("asset-source-picker")).toBeVisible();
    expect(screen.getByRole("table", { name: "用户资产列表" })).toBeVisible();
    click("收起数据源选择");
    expect(document.getElementById("asset-source-picker")).not.toBeVisible();
    expect(screen.getByRole("table", { name: "用户资产列表" })).toBeVisible();
  });

  it("is read-only without tasks or producer privileges and fetches GUID detail with both source pins", async () => {
    await start();
    expect(JSON.parse(calls("/api/access/check")[0][1].body).paths).toEqual([
      "GET /api/user-assets/v2",
      "GET /api/user-assets/v2/detail",
      "GET /api/domain-selection",
      "GET /api/domain-selection/resolve",
    ]);
    expect(params()).toEqual({
      domainId: choice.domainId,
      expectedRevision: choice.revision,
      expectedCredentialRevision: choice.credentialRevision,
      search: "",
      pageIdx: "1",
      pageSize: "10",
    });
    click("查看用户 observed-1");
    const panel = await screen.findByRole("region", { name: "用户详情" });
    await within(panel).findByText("Factual description");
    expect(params("/api/user-assets/v2/detail")).toEqual(detailQuery);
    for (const key of [
      "对象 GUID",
      "目录路径（DN）",
      "对象类型",
      "目录对象类",
      "账户名（SAM）",
      "账户控制值（原始整数）",
      "安全标识（SID）",
      "电子邮箱",
      "描述",
      "创建时间（UTC）",
    ])
      expect(within(panel).getByText(key)).toBeVisible();
    expect(within(panel).getByText("0")).toBeVisible();
    expect(within(panel).getByText("0001-01-01T00:00:00Z")).toBeVisible();
    expect(
      screen.queryByRole("button", { name: /同步|采集|导出|取消任务/ }),
    ).toBeNull();
    expect(localStorage.length + sessionStorage.length).toBe(0);
  });
  it("renders null/hostile supplemental and provenance as inert escaped facts", async () => {
    const hostile = {
      ...row(),
      mail: " <img src=x onerror=evil()>\u0000\u202e😀 ",
      description: ["<script>evil()</script>\n"] as [string],
    };
    override = (url) =>
      url.includes("/detail?") ? response(detail(hostile)) : undefined;
    await start();
    click("查看用户 observed-1");
    const panel = screen.getByRole("region", { name: "用户详情" });
    await within(panel).findByText(
      directoryV2DisplayText(hostile.description[0]),
    );
    expect(panel.textContent).toContain(directoryV2DisplayText(hostile.mail));
    expect(panel.querySelector("a,img,script")).toBeNull();
    expect(panel.textContent).not.toMatch(/[\u0000\u202e]/u);
    click("关闭用户详情");
    override = (url) =>
      url.includes("/detail?")
        ? response(
            detail({
              ...row(),
              samAccountName: null,
              userAccountControl: null,
              objectSid: null,
              mail: null,
              description: null,
              whenCreated: null,
            }),
          )
        : undefined;
    click("查看用户 observed-1");
    await waitFor(() =>
      expect(
        within(screen.getByRole("region", { name: "用户详情" })).getAllByText(
          "未返回",
        ),
      ).toHaveLength(6),
    );
  });
  it("retains observation for paging, submitted/cleared search and all sizes; only refresh chooses latest", async () => {
    await start();
    click("用户资产下一页");
    await screen.findByText("observed-12");
    expect(params()).toMatchObject({
      observationId: "observation-one",
      pageIdx: "2",
    });
    fill("用户资产关键词", "observed-12");
    expect(
      screen.getByRole("button", { name: "用户资产上一页" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "查看用户 observed-12" }),
    ).toBeDisabled();
    click("搜索用户");
    await screen.findByText("匹配用户 1 个", { exact: false });
    expect(params()).toMatchObject({
      observationId: "observation-one",
      pageIdx: "1",
      search: "observed-12",
    });
    click("清除用户搜索");
    await screen.findByText("observed-1");
    for (const size of [20, 30, 40, 50, 10]) {
      fill("用户资产每页条数", String(size));
      await screen.findByText("observed-1");
      expect(params()).toMatchObject({
        observationId: "observation-one",
        pageIdx: "1",
        pageSize: String(size),
        search: "",
      });
    }
    click("刷新用户资产观测");
    expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
    await screen.findByText("observed-1");
    expect(params()).not.toHaveProperty("observationId");
  });
  it("rejects overlong input without truncation and visibly distinguishes raw applied controls", async () => {
    await start();
    // Browser-native maxlength truncates before React receives the value, so
    // explicit validation must see the entire typed/pasted query instead.
    expect(
      screen.getByLabelText("用户资产关键词", { exact: true }),
    ).not.toHaveAttribute("maxlength");
    fill("用户资产关键词", "😀".repeat(26));
    expect(
      screen.getByLabelText("用户资产关键词", { exact: true }),
    ).toHaveValue("😀".repeat(26));
    const count = calls().length;
    click("搜索用户");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "输入不会被截断",
    );
    expect(calls()).toHaveLength(count);
    fill("用户资产关键词", " \u0000\u202e ");
    click("搜索用户");
    await screen.findByText("此观测在当前筛选下没有匹配用户。");
    expect(params().search).toBe(" \u0000\u202e ");
    expect(screen.getByText(/已应用搜索/).textContent).toBe(
      "已应用搜索：“ \\u0000\\u202e ”",
    );
  });
  it.each(["absent", "empty"])(
    "distinguishes %s evidence without invented counts",
    async (state) => {
      override = (url) =>
        url.split("?")[0] === "/api/user-assets/v2"
          ? response(state === "absent" ? absent() : list(q, []))
          : undefined;
      await mount();
      await select();
      await screen.findByText(
        state === "absent"
          ? "当前数据源没有可用的字典 2 观测；尚不能确认用户数量。"
          : "此观测在当前筛选下没有匹配用户。",
      );
      expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
      if (state === "absent")
        expect(screen.queryByText(/匹配用户 0 个/)).toBeNull();
    },
  );
  it("Close and Escape invalidate held details and return keyboard focus", async () => {
    await start();
    const held = deferred();
    override = (url) => (url.includes("/detail?") ? held.promise : undefined);
    const button = screen.getByRole("button", { name: "查看用户 observed-1" });
    click("查看用户 observed-1");
    const panel = screen.getByRole("region", { name: "用户详情" });
    expect(panel).toHaveFocus();
    fireEvent.keyDown(panel, { key: "Escape" });
    expect(button).toHaveFocus();
    await held.resolve(detail());
    expect(screen.queryByRole("region", { name: "用户详情" })).toBeNull();
    override = () => undefined;
    click("查看用户 observed-1");
    await screen.findByText("Factual description");
    click("关闭用户详情");
    expect(button).toHaveFocus();
  });
  it("row switches reject an older held detail even if transport ignores abort", async () => {
    await start();
    const held = deferred();
    override = (url) =>
      url.includes("/detail?") && url.includes(row().objectGUID)
        ? held.promise
        : undefined;
    click("查看用户 observed-1");
    click("关闭用户详情");
    click("查看用户 observed-2");
    const panel = screen.getByRole("region", { name: "用户详情" });
    await within(panel).findByText(row(2).objectGUID);
    await held.resolve(
      detail({ ...row(), description: ["old-private-description"] }),
    );
    expect(panel).not.toHaveTextContent("old-private-description");
    expect(within(panel).getByText(row(2).objectGUID)).toBeVisible();
  });
  it("same-parameter refresh clears success immediately and invalidates held list/detail", async () => {
    await start();
    click("查看用户 observed-1");
    await screen.findByText("Factual description");
    click("关闭用户详情");
    const first = deferred(),
      second = deferred();
    let count = 0;
    override = (url) =>
      url.split("?")[0] === "/api/user-assets/v2"
        ? ++count === 1
          ? first.promise
          : second.promise
        : undefined;
    click("刷新用户资产观测");
    expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
    expect(screen.queryByRole("region", { name: "用户详情" })).toBeNull();
    click("刷新用户资产观测");
    await second.resolve(list(q, [row(2)], 1, "observation-new"));
    await screen.findByText("observed-2");
    await first.resolve(list(q, [row(1)]));
    expect(screen.queryByText("observed-1")).toBeNull();
    expect(screen.getByText("观测来源 · observation-new")).toBeVisible();
  });
  it("source selection resets private rows, draft and pin before resolution and ignores held old detail", async () => {
    await start();
    const held = deferred();
    override = (url) => (url.includes("/detail?") ? held.promise : undefined);
    click("查看用户 observed-1");
    click("关闭用户详情");
    click("更换数据源");
    fireEvent.click(
      screen.getByRole("radio", { name: "选择数据源 other.invalid" }),
    );
    expect(screen.queryByRole("region", { name: "用户详情" })).toBeNull();
    expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
    click("核对并打开连接详情");
    await screen.findByRole("table", { name: "用户资产列表" });
    expect(params()).toMatchObject({
      domainId: otherChoice.domainId,
      pageIdx: "1",
      search: "",
    });
    expect(params()).not.toHaveProperty("observationId");
    await held.resolve(
      detail({ ...row(), description: ["old-private-description"] }),
    );
    expect(screen.queryByText("old-private-description")).toBeNull();
  });
  it.each([
    [404, "not_found"],
    [409, "selection_changed"],
    [403, "forbidden"],
    [403, "tenant_expired"],
    [403, "tenant_domain_limit_exceeded"],
    [401, "unauthenticated"],
    [403, "password_change_required"],
  ] as const)(
    "detail %s/%s clears every protected view",
    async (status, code) => {
      const changed = vi.fn();
      await start(changed);
      override = (url) =>
        url.includes("/detail?")
          ? response({ error: code }, status)
          : undefined;
      click("查看用户 observed-1");
      await waitFor(() =>
        expect(
          screen.queryByRole("region", { name: "已选数据源用户资产" }),
        ).toBeNull(),
      );
      expect(screen.queryByText("observed-1")).toBeNull();
      if (status === 401 || code === "password_change_required")
        expect(changed).toHaveBeenCalledOnce();
      if (status === 404)
        expect(screen.getByRole("alert")).toHaveTextContent(
          "当前无法读取此用户，请重新选择数据源",
        );
      const count = calls("/api/user-assets/v2/detail").length;
      await act(async () => {});
      expect(calls("/api/user-assets/v2/detail")).toHaveLength(count);
    },
  );
  it("stale observation removes rows and pinned controls, and explicit refresh retains source revision checks", async () => {
    await start();
    override = (url) =>
      url.includes("/detail?")
        ? response({ error: "directory_observation_unavailable" }, 409)
        : undefined;
    click("查看用户 observed-1");
    await screen.findByText("原用户资产观测已不可用，请明确刷新观测。");
    expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
    expect(screen.queryByRole("button", { name: "用户资产下一页" })).toBeNull();
    expect(screen.getByRole("button", { name: "搜索用户" })).toBeDisabled();
    click("刷新用户资产观测");
    await screen.findByText("observed-1");
    expect(params()).toMatchObject({
      expectedRevision: choice.revision,
      expectedCredentialRevision: choice.credentialRevision,
    });
    expect(params()).not.toHaveProperty("observationId");
  });
  it("same-identity retry preserves captured pin on schema failure and clears error on recovery", async () => {
    await start();
    let count = 0;
    override = (url) =>
      url.split("?")[0] === "/api/user-assets/v2" && count++ === 0
        ? response({ error: "schema_unavailable" }, 503)
        : undefined;
    click("用户资产下一页");
    await screen.findByRole("alert");
    expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
    const failed = params();
    click("重试用户资产读取");
    await screen.findByText("observed-12");
    expect(params()).toEqual(failed);
    expect(params()).toMatchObject({
      observationId: "observation-one",
      pageIdx: "2",
    });
    expect(screen.queryByRole("alert")).toBeNull();
  });
  it("automatically retries network reads with exactly the intended pin", async () => {
    await start();
    let count = 0;
    override = (url) =>
      url.split("?")[0] === "/api/user-assets/v2" && count++ === 0
        ? Promise.reject(new Error("network unavailable"))
        : undefined;
    click("用户资产下一页");
    await screen.findByRole("alert");
    const failed = params();
    await screen.findByText("observed-12", {}, { timeout: 4000 });
    expect(params()).toEqual(failed);
    expect(screen.queryByRole("alert")).toBeNull();
  });
  it("actor/session remount and unmount discard all held successes", async () => {
    const view = await start();
    const held = deferred();
    override = (url) => (url.includes("/detail?") ? held.promise : undefined);
    click("查看用户 observed-1");
    view.rerender(
      <UserAssetsV2Workspace
        profile={{ ...profile, csrfToken: "new-session" }}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByRole("radio", { name: "选择数据源 synthetic.invalid" });
    await held.resolve(
      detail({ ...row(), description: ["old-private-description"] }),
    );
    expect(screen.queryByText("old-private-description")).toBeNull();
    expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
    await select();
    await screen.findByRole("table", { name: "用户资产列表" });
    const pending = deferred();
    override = (url) =>
      url.split("?")[0] === "/api/user-assets/v2" ? pending.promise : undefined;
    click("刷新用户资产观测");
    view.unmount();
    await pending.resolve(list());
    expect(screen.queryByText("observed-1")).toBeNull();
  });
  it.each(["domains", "directory_assets"])(
    "requires readable %s even with route checks",
    async (missing) => {
      override = (url) =>
        url === "/api/access/menu"
          ? response({
              menu: ["domains", "directory_assets"].map((mark) => ({
                mark,
                auth: { readable: mark !== missing },
              })),
            })
          : undefined;
      render(
        <UserAssetsV2Workspace profile={profile} sessionChanged={vi.fn()} />,
      );
      await screen.findByText("当前账户没有用户资产读取权限。");
      expect(calls()).toHaveLength(0);
      expect(calls("/api/domain-selection")).toHaveLength(0);
    },
  );
  it("blocks detail independently when its exact route check is false", async () => {
    override = (url) =>
      url === "/api/access/check"
        ? response({ results: [true, false, true, true] })
        : undefined;
    await start();
    expect(
      screen.getByRole("button", { name: "查看用户 observed-1" }),
    ).toBeDisabled();
    expect(calls("/api/user-assets/v2/detail")).toHaveLength(0);
  });
});

describe("UserAssetsV2 App navigation and session boundaries", () => {
  async function openApp() {
    window.history.replaceState({}, "", "#user-assets-v2");
    render(<App />);
    await screen.findByRole("radio", { name: "选择数据源 synthetic.invalid" });
    await select();
    await screen.findByRole("table", { name: "用户资产列表" });
  }
  it("Back/Forward rechecks identity and returns without old list or closed panel", async () => {
    await openApp();
    const held = deferred();
    override = (url) => (url.includes("/detail?") ? held.promise : undefined);
    click("查看用户 observed-1");
    await act(async () => {
      window.history.replaceState({}, "", "#account");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await screen.findByRole("heading", { name: "账户概览" });
    await held.resolve(
      detail({ ...row(), description: ["old-private-description"] }),
    );
    await act(async () => {
      window.history.replaceState({}, "", "#user-assets-v2");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await screen.findByRole("radio", { name: "选择数据源 synthetic.invalid" });
    expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
    expect(screen.queryByRole("region", { name: "用户详情" })).toBeNull();
    expect(screen.queryByText("old-private-description")).toBeNull();
    expect(calls("/api/auth/me").length).toBeGreaterThanOrEqual(3);
  });
  it.each(["focus", "visibility"])(
    "same-identity %s resume retains modal ownership and pinned detail",
    async (channel) => {
      await openApp();
      click("查看用户 observed-1");
      await screen.findByText("Factual description");
      const before = params("/api/user-assets/v2/detail");
      const held = deferred();
      override = (url) => (url === "/api/auth/me" ? held.promise : undefined);
      await act(async () => {
        if (channel === "focus") window.dispatchEvent(new Event("focus"));
        else document.dispatchEvent(new Event("visibilitychange"));
      });
      expect(
        screen.getByRole("dialog", { name: "用户详情抽屉", hidden: true }),
      ).not.toBeVisible();
      await held.resolve(profile);
      await screen.findByRole("dialog", { name: "用户详情抽屉" });
      expect(document.getElementById("soc-navigation-panel")).toHaveAttribute(
        "inert",
      );
      expect(document.querySelector(".soc-topbar")).toHaveAttribute("inert");
      expect(params("/api/user-assets/v2/detail")).toEqual(before);
      expect(screen.getByText("Factual description")).toBeVisible();
      click("关闭用户详情");
      expect(
        document.getElementById("soc-navigation-panel"),
      ).not.toHaveAttribute("inert");
      expect(document.querySelector(".soc-topbar")).not.toHaveAttribute(
        "inert",
      );
      expect(
        screen.getByRole("button", { name: "查看用户 observed-1" }),
      ).toHaveFocus();
    },
  );
  it.each(["storage", "focus", "visibility"])(
    "%s identity revalidation removes assets and rejects old actor responses",
    async (channel) => {
      await openApp();
      const held = deferred();
      override = (url) => (url.includes("/detail?") ? held.promise : undefined);
      click("查看用户 observed-1");
      override = (url) =>
        url === "/api/auth/me"
          ? response({
              ...profile,
              ID: 2,
              username: "new-reader",
              csrfToken: "new-session",
            })
          : url.includes("/detail?")
            ? held.promise
            : undefined;
      await act(async () => {
        if (channel === "storage")
          window.dispatchEvent(
            new StorageEvent("storage", {
              key: SESSION_INVALIDATION_STORAGE,
              newValue: "abcdef0123456789abcdef0123456789",
              storageArea: window.localStorage,
            }),
          );
        else if (channel === "focus") window.dispatchEvent(new Event("focus"));
        else document.dispatchEvent(new Event("visibilitychange"));
      });
      await screen.findByRole("heading", { name: "账户概览" });
      expect(screen.queryByRole("table", { name: "用户资产列表" })).toBeNull();
      expect(screen.queryByRole("region", { name: "用户详情" })).toBeNull();
      await held.resolve(
        detail({ ...row(), description: ["old-private-description"] }),
      );
      expect(screen.queryByText("old-private-description")).toBeNull();
      expect(
        screen.getByText("new-reader", { selector: "strong" }),
      ).toBeVisible();
    },
  );
});

describe("UserAssetsV2 generation isolation independent of AbortSignal", () => {
  it("a changed search supersedes a pending list while identical repeat submissions stay disabled", async () => {
    await start();
    const held = deferred();
    override = (url) =>
      url.includes("/api/user-assets/v2?") &&
      queryFrom(url).search === "observed-2"
        ? held.promise
        : undefined;
    fill("用户资产关键词", "observed-2");
    click("搜索用户");
    expect(screen.getByRole("button", { name: "搜索用户" })).toBeDisabled();
    fill("用户资产关键词", "observed-12");
    expect(screen.getByRole("button", { name: "搜索用户" })).toBeEnabled();
    fireEvent.submit(
      screen.getByRole("button", { name: "搜索用户" }).closest("form")!,
    );
    await screen.findByText("observed-12");
    await held.resolve(list({ ...q, search: "observed-2" }, [row(2)]));
    expect(screen.queryByText("observed-2")).toBeNull();
    expect(screen.getByText("observed-12")).toBeVisible();
    expect(params()).toMatchObject({
      search: "observed-12",
      observationId: "observation-one",
    });
  });

  it("rejects an obsolete parsed list even when the entire API ignores abort", async () => {
    await start();
    let deliver!: (value: ReturnType<typeof parseUserAssetsV2List>) => void;
    const held = new Promise<ReturnType<typeof parseUserAssetsV2List>>(
      (resolve) => {
        deliver = resolve;
      },
    );
    vi.spyOn(userAssetsV2API, "list").mockImplementationOnce(() => held);
    click("刷新用户资产观测");
    click("刷新用户资产观测");
    await screen.findByText("observed-1");
    await act(async () =>
      deliver(
        parseUserAssetsV2List(
          list(q, [{ ...row(), samAccountName: "obsolete-private-user" }]),
          q,
        ),
      ),
    );
    expect(screen.queryByText("obsolete-private-user")).toBeNull();
    expect(screen.getByText("observed-1")).toBeVisible();
  });
  it("rejects a closed parsed detail even when the entire API ignores abort", async () => {
    await start();
    let deliver!: (value: ReturnType<typeof parseUserAssetV2Detail>) => void;
    const held = new Promise<ReturnType<typeof parseUserAssetV2Detail>>(
      (resolve) => {
        deliver = resolve;
      },
    );
    vi.spyOn(userAssetsV2API, "detail").mockImplementationOnce(() => held);
    click("查看用户 observed-1");
    click("关闭用户详情");
    await act(async () =>
      deliver(
        parseUserAssetV2Detail(
          detail({ ...row(), description: ["obsolete-private-detail"] }),
          detailQuery,
        ),
      ),
    );
    expect(screen.queryByRole("region", { name: "用户详情" })).toBeNull();
    expect(screen.queryByText("obsolete-private-detail")).toBeNull();
  });
  it("keeps arbitrary error strings inert and never indexes inherited messages", () => {
    for (const code of [
      "constructor",
      "__proto__",
      "<img src=x onerror=evil()>",
    ]) {
      expect(userAssetsV2Error(new ApiError(code))).toBe(
        "用户资产读取失败，请重试原读取。",
      );
    }
  });
});
