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
import type { Profile } from "./api";
import type { SourceChoice } from "./domain-selection-api";
import {
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
  userAccountControl: index % 3 === 0 ? null : index % 3 === 1 ? 2 : 512,
  objectSid: "S-1-5-21-1-2-3-500",
  mail: " Observed@Synthetic.invalid ",
  description: ["Factual description"],
  whenCreated: `2026-01-${String(index).padStart(2, "0")}T00:00:00Z`,
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

    ...(p.has("sAMAccountName")
      ? { sAMAccountName: p.get("sAMAccountName")! }
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
      const all = Array.from({ length: 23 }, (_, i) => row(i + 1));
      let filtered = query.search
        ? all.filter((r) => r.samAccountName!.includes(query.search))
        : all;
      if (query.sAMAccountName !== undefined)
        filtered = filtered.filter(
          (r) => r.samAccountName === query.sAMAccountName,
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

describe("UI-0495 v2 page-local user row numbers", () => {
  it("numbers accepted rows 1..N and restarts on every page without new requests or identities", async () => {
    await start();
    expectPositions(firstPage);
    const before = fetcher.mock.calls.length;
    fireEvent.click(screen.getByRole("columnheader", { name: "本页序号" }));
    fireEvent.click(
      screen
        .getByRole("table", { name: "用户资产列表" })
        .querySelector("tbody td")!,
    );
    expect(fetcher.mock.calls).toHaveLength(before);
    click("用户资产下一页");
    await screen.findByText("observed-11");
    expectPositions(Array.from({ length: 10 }, (_, i) => i + 11));
    expect(params()).toEqual({
      ...q,
      pageIdx: "2",
      pageSize: "10",
      observationId: "observation-one",
    });
    click("用户资产下一页");
    await screen.findByText("observed-23");
    expectPositions([21, 22, 23]);
    expect(params().pageIdx).toBe("3");
    click("用户资产上一页");
    await screen.findByText("observed-11");
    expectPositions(Array.from({ length: 10 }, (_, i) => i + 11));
    expect(calls()).toHaveLength(4);
  });

  it.each([20, 30, 40, 50])(
    "resets to page one and numbers actual rows after page size %s",
    async (size) => {
      await start();
      click("用户资产下一页");
      await screen.findByText("observed-11");
      fill("用户资产每页条数", String(size));
      await screen.findByText("observed-1");
      expectPositions(
        Array.from({ length: Math.min(size, 23) }, (_, i) => i + 1),
      );
      expect(params().pageIdx).toBe("1");
      expect(params().pageSize).toBe(String(size));
      expect(params().observationId).toBe("observation-one");
    },
  );

  it("renumbers accepted filter, sort, clear and refresh responses but never draft state", async () => {
    await start();
    fill("用户资产关键词", "observed-1");
    expectPositions(firstPage);
    expect(calls()).toHaveLength(1);
    click("搜索用户");
    await waitFor(() =>
      expectPositions([1, 10, 11, 12, 13, 14, 15, 16, 17, 18]),
    );
    fill("创建时间排序", "desc");
    expectPositions([1, 10, 11, 12, 13, 14, 15, 16, 17, 18]);
    click("搜索用户");
    await waitFor(() =>
      expectPositions([19, 18, 17, 16, 15, 14, 13, 12, 11, 10]),
    );
    click("用户资产下一页");
    await waitFor(() => expectPositions([1]));
    click("刷新用户资产观测");
    await waitFor(() =>
      expectPositions([19, 18, 17, 16, 15, 14, 13, 12, 11, 10]),
    );
    expect(params().observationId).toBeUndefined();
    click("清除用户筛选");
    await waitFor(() => expectPositions(firstPage));
    expect(calls()).toHaveLength(6);
  });

  it("numbers exact SAM matches without changing accepted rows on draft edits", async () => {
    await start();
    fill("精确账户名（SAM）", "observed-12");
    expectPositions(firstPage);
    expect(calls()).toHaveLength(1);
    click("搜索用户");
    await waitFor(() => expectPositions([12]));
    expect(params().sAMAccountName).toBe("observed-12");
    click("清除用户筛选");
    await waitFor(() => expectPositions(firstPage));
    expect(params().sAMAccountName).toBeUndefined();
  });

  it("preserves the actual GUID detail identity rather than using the displayed position", async () => {
    await start();
    click("用户资产下一页");
    await waitFor(() =>
      expectPositions(Array.from({ length: 10 }, (_, i) => i + 11)),
    );
    override = (url) => {
      if (!url.startsWith("/api/user-assets/v2/detail?")) return undefined;
      return response({
        dictionaryVersion: 2,
        selection: selection(),
        observationId: "observation-one",
        source,
        object: row(12),
      });
    };
    click("查看用户 observed-12");
    await screen.findByRole("dialog", { name: "用户详情抽屉" });
    expect(params("/api/user-assets/v2/detail")).toEqual({
      domainId: q.domainId,
      expectedRevision: q.expectedRevision,
      expectedCredentialRevision: q.expectedCredentialRevision,
      observationId: "observation-one",
      objectGUID: row(12).objectGUID,
    });
    click("关闭用户详情");
    expectPositions(Array.from({ length: 10 }, (_, i) => i + 11));
  });

  it.each(["x".repeat(51), "😀".repeat(26)])(
    "keeps the old v2 whole overlength draft and submit-only no-read behavior (%s)",
    async (value) => {
      await start();
      fill("用户资产关键词", value);
      expect(
        screen.getByLabelText("用户资产关键词", { exact: true }),
      ).toHaveValue(value);
      expectPositions(firstPage);
      expect(calls()).toHaveLength(1);
      click("搜索用户");
      expect(screen.getByRole("alert")).toHaveTextContent(
        "搜索最多 50 个 UTF-16 单元",
      );
      expect(
        screen.getByLabelText("用户资产关键词", { exact: true }),
      ).toHaveValue(value);
      expectPositions(firstPage);
      expect(calls()).toHaveLength(1);
    },
  );

  it("keeps draft positions and removes positions while a page response is pending", async () => {
    await start();
    fill("用户资产关键词", "observed-12");
    expectPositions(firstPage);
    expect(
      screen.getByRole("button", { name: "用户资产下一页" }),
    ).toBeDisabled();
    for (const button of screen.getAllByRole("button", { name: /^查看用户 / }))
      expect(button).toBeDisabled();
    fill("用户资产关键词", "");
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/user-assets/v2") ? held.promise : undefined;
    click("用户资产下一页");
    expectNoPositions();
    await held.resolve(list({ ...q, pageIdx: 2 }, [row(11), row(12)], 12));
    expectPositions([11, 12]);
  });

  it("rejects ignored-abort earlier results after a newer filter response", async () => {
    await start();
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/user-assets/v2") && queryFrom(url).pageIdx === 2
        ? held.promise
        : undefined;
    click("用户资产下一页");
    fill("用户资产关键词", "observed-12");
    click("搜索用户");
    await screen.findByText("observed-12");
    expectPositions([12]);
    await held.resolve(list({ ...q, pageIdx: 2 }, [row(11), row(12)], 12));
    expectPositions([12]);
    expect(screen.queryByText("observed-11")).toBeNull();
  });

  it.each(["absent", "zero", "no-match", "out-of-range"])(
    "does not manufacture numbered rows for %s",
    async (kind) => {
      if (kind === "out-of-range") {
        await start();
        override = (url) =>
          url.startsWith("/api/user-assets/v2")
            ? response(list(queryFrom(url), [], 1))
            : undefined;
        click("用户资产下一页");
        await screen.findByText("此观测在当前筛选下没有匹配用户。");
      } else if (kind === "no-match") {
        await start();
        fill("用户资产关键词", "missing");
        click("搜索用户");
        await screen.findByText("此观测在当前筛选下没有匹配用户。");
      } else {
        override = (url) =>
          url.startsWith("/api/user-assets/v2")
            ? response(
                kind === "absent"
                  ? absent(queryFrom(url))
                  : list(queryFrom(url), [], 0),
              )
            : undefined;
        await mount();
        await select();
        await screen.findByText(
          kind === "absent"
            ? "当前数据源没有可用的字典 2 观测；尚不能确认用户数量。"
            : "此观测在当前筛选下没有匹配用户。",
        );
      }
      expectNoPositions();
    },
  );

  it.each([401, 403, 404])(
    "clears numbered rows on access failure %s",
    async (status) => {
      const changed = vi.fn();
      await start(changed);
      override = (url) =>
        url.startsWith("/api/user-assets/v2")
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
      click("用户资产下一页");
      await waitFor(() =>
        expect(
          screen.queryByRole("region", { name: "已选数据源用户资产" }),
        ).toBeNull(),
      );
      expectNoPositions();
      if (status === 401) expect(changed).toHaveBeenCalledOnce();
      else expect(changed).not.toHaveBeenCalled();
    },
  );

  it("does not restore positions from ignored-abort bytes after a newer revocation", async () => {
    await start();
    const held = deferred();
    override = (url) => {
      if (!url.startsWith("/api/user-assets/v2?")) return undefined;
      return queryFrom(url).pageIdx === 2
        ? held.promise
        : response({ error: "forbidden" }, 403);
    };
    click("用户资产下一页");
    expectNoPositions();
    fill("用户资产关键词", "observed-12");
    click("搜索用户");
    await waitFor(() =>
      expect(
        screen.queryByRole("region", { name: "已选数据源用户资产" }),
      ).toBeNull(),
    );
    expectNoPositions();
    await held.resolve(list({ ...q, pageIdx: 2 }, [row(11), row(12)], 12));
    expectNoPositions();
  });

  it("clears positions on selection revision invalidation", async () => {
    await start();
    override = (url) =>
      url.startsWith("/api/user-assets/v2")
        ? response({ error: "selection_changed" }, 409)
        : undefined;
    click("用户资产下一页");
    await waitFor(() =>
      expect(
        screen.queryByRole("region", { name: "已选数据源用户资产" }),
      ).toBeNull(),
    );
    expectNoPositions();
  });

  it("clears positions when the pinned observation is unavailable until explicit refresh", async () => {
    await start();
    override = (url) =>
      url.startsWith("/api/user-assets/v2")
        ? response({ error: "directory_observation_unavailable" }, 409)
        : undefined;
    click("用户资产下一页");
    await screen.findByText("原用户资产观测已不可用，请明确刷新观测。");
    expectNoPositions();
    expect(calls()).toHaveLength(2);
    override = () => undefined;
    click("刷新用户资产观测");
    await screen.findByRole("table", { name: "用户资产列表" });
    expectPositions(firstPage);
    expect(params().observationId).toBeUndefined();
  });

  it("clears rows on source change and ignores the previous source response", async () => {
    await start();
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/user-assets/v2") ? held.promise : undefined;
    click("用户资产下一页");
    click("更换数据源");
    fireEvent.click(
      screen.getByRole("radio", { name: "选择数据源 other.invalid" }),
    );
    expectNoPositions();
    await held.resolve(list({ ...q, pageIdx: 2 }, [row(11), row(12)], 12));
    expectNoPositions();
    override = () => undefined;
    click("核对并打开连接详情");
    await screen.findByRole("table", { name: "用户资产列表" });
    expectPositions(firstPage);
    expect(params().domainId).toBe(otherChoice.domainId);
  });

  it("cannot restore positions after session replacement", async () => {
    const view = await start();
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/user-assets/v2") ? held.promise : undefined;
    click("用户资产下一页");
    view.rerender(
      <UserAssetsV2Workspace
        profile={{ ...profile, csrfToken: "replacement-session" }}
        sessionChanged={vi.fn()}
      />,
    );
    expectNoPositions();
    await held.resolve(list({ ...q, pageIdx: 2 }, [row(11), row(12)], 12));
    expectNoPositions();
  });

  it("cannot restore numbered rows after navigation unmount", async () => {
    const view = await start();
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/user-assets/v2") ? held.promise : undefined;
    click("用户资产下一页");
    view.unmount();
    await held.resolve(list({ ...q, pageIdx: 2 }, [row(11), row(12)], 12));
    expectNoPositions();
  });
});
