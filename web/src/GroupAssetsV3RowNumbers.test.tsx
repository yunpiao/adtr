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
import GroupAssetsV3Workspace from "./GroupAssetsV3Workspace";
import type { Profile } from "./api";
import type { SourceChoice } from "./domain-selection-api";
import {
  type GroupAssetsV3Query,
  type GroupAssetV3Object,
} from "./group-assets-v3-api";

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
const q: GroupAssetsV3Query = {
  domainId: choice.domainId,
  expectedRevision: choice.revision,
  expectedCredentialRevision: choice.credentialRevision,
  search: "",
  pageIdx: 1,
  pageSize: 10,
};
const row = (index = 1): GroupAssetV3Object => ({
  objectGUID: `00000000-0000-0000-0000-${String(index).padStart(12, "0")}`,
  distinguishedName: `CN=Group ${index},DC=synthetic,DC=invalid`,
  kind: "group",
  objectClass: ["group", "top"],
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
  observedGroupCount: query.search ? Math.max(total, 23) : total,
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
  observedGroupCount: null,
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
const calls = (path = "/api/group-assets/v3") =>
  fetcher.mock.calls.filter(([url]) => url.split("?")[0] === path);
const params = (path = "/api/group-assets/v3") =>
  Object.fromEntries(
    new URL(calls(path).at(-1)![0], "https://local.invalid").searchParams,
  );
function queryFrom(url: string): GroupAssetsV3Query {
  const p = new URL(url, "https://local.invalid").searchParams;
  return {
    domainId: p.get("domainId")!,
    expectedRevision: p.get("expectedRevision")!,
    expectedCredentialRevision: p.get("expectedCredentialRevision")!,
    search: p.get("search")!,
    pageIdx: Number(p.get("pageIdx")),
    pageSize: Number(p.get("pageSize")),

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
    if (path === "/api/group-assets/v3") {
      const query = queryFrom(url);
      const all = Array.from({ length: 23 }, (_, i) => row(i + 1));
      let filtered = query.search
        ? all.filter((r) => r.samAccountName!.includes(query.search))
        : all;
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
    <GroupAssetsV3Workspace profile={profile} sessionChanged={changed} />,
  );
  await screen.findByRole("radio", { name: "选择数据源 synthetic.invalid" });
  return view;
}
async function select(domain = "synthetic.invalid") {
  fireEvent.click(screen.getByRole("radio", { name: `选择数据源 ${domain}` }));
  click("核对并打开连接详情");
  await screen.findByRole("region", { name: "已选数据源组资产" });
}
async function start(changed = vi.fn()) {
  const view = await mount(changed);
  await select();
  await screen.findByRole("table", { name: "组资产列表" });
  return view;
}

// Only six-cell asset rows count; expanded facts are a separate spanning row.
function expectPositions(indices: number[]) {
  const table = screen.getByRole("table", { name: "组资产列表" });
  expect(
    within(table).getByRole("columnheader", { name: "本页序号" }),
  ).toHaveAttribute("scope", "col");
  const rows = within(table)
    .getAllByRole("row")
    .filter((r) => within(r).queryAllByRole("cell").length === 6);
  expect(rows).toHaveLength(indices.length);
  rows.forEach((r, i) => {
    const cells = within(r).getAllByRole("cell");
    expect(cells[0]).toHaveTextContent(new RegExp(`^${i + 1}$`));
    expect(cells[1]).toHaveTextContent(new RegExp(`^observed-${indices[i]}$`));
    expect(within(r).getByRole("button")).toHaveAttribute(
      "aria-controls",
      `group-facts-${row(indices[i]).objectGUID}`,
    );
  });
}
const firstPage = Array.from({ length: 10 }, (_, i) => i + 1);
function expectNoPositions() {
  expect(screen.queryByRole("table", { name: "组资产列表" })).toBeNull();
  expect(screen.queryByRole("columnheader", { name: "本页序号" })).toBeNull();
}

describe("UI-0447 page-local group row numbers", () => {
  it("numbers accepted rows 1..N, restarts on all three pages and never numbers facts", async () => {
    await start();
    expectPositions(firstPage);
    const beforeExpand = fetcher.mock.calls.length;
    fireEvent.click(screen.getAllByRole("button", { name: "展开事实" })[0]);
    expectPositions(firstPage);
    const facts = screen.getByText("主组 RID（primaryGroupID）").closest("td")!;
    expect(facts).toHaveAttribute("colspan", "6");
    expect(fetcher.mock.calls).toHaveLength(beforeExpand);
    click("下一页");
    await screen.findByText("observed-11");
    expectPositions(Array.from({ length: 10 }, (_, i) => i + 11));
    expect(params()).toEqual({
      ...q,
      pageIdx: "2",
      pageSize: "10",
      observationId: "observation-one",
    });
    click("下一页");
    await screen.findByText("observed-23");
    expectPositions([21, 22, 23]);
    expect(params().pageIdx).toBe("3");
    expect(params().observationId).toBe("observation-one");
    expect(calls()).toHaveLength(3);
  });

  it("renumbers after page size, filter, sort, clear and refresh responses", async () => {
    await start();
    fill("组每页条数", "20");
    await screen.findByText("observed-20");
    expectPositions(Array.from({ length: 20 }, (_, i) => i + 1));
    fill("组资产关键词", "observed-1");
    expectPositions(Array.from({ length: 20 }, (_, i) => i + 1));
    click("应用组筛选");
    await waitFor(() =>
      expect(screen.getAllByRole("button", { name: "展开事实" })).toHaveLength(
        11,
      ),
    );
    expectPositions([1, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19]);
    fill("创建时间排序", "desc");
    expectPositions([1, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19]);
    click("应用组筛选");
    await waitFor(() =>
      expectPositions([19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 1]),
    );
    click("刷新组资产观测");
    await waitFor(() =>
      expectPositions([19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 1]),
    );
    expect(params().observationId).toBeUndefined();
    click("清除组筛选");
    await waitFor(() =>
      expectPositions(Array.from({ length: 20 }, (_, i) => i + 1)),
    );
    expect(calls()).toHaveLength(6);
  });

  it("keeps draft positions and hides all positions until a new page is accepted", async () => {
    await start();
    fill("组资产关键词", "observed-12");
    expectPositions(firstPage);
    expect(calls()).toHaveLength(1);
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    for (const button of screen.getAllByRole("button", { name: "展开事实" }))
      expect(button).toBeDisabled();
    fill("组资产关键词", "");
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/group-assets/v3") ? held.promise : undefined;
    click("下一页");
    expectNoPositions();
    await held.resolve(list({ ...q, pageIdx: 2 }, [row(11), row(12)], 12));
    expectPositions([11, 12]);
  });

  it("rejects ignored-abort earlier results after a newer filter response", async () => {
    await start();
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/group-assets/v3") && queryFrom(url).pageIdx === 2
        ? held.promise
        : undefined;
    click("下一页");
    fill("组资产关键词", "observed-12");
    click("应用组筛选");
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
          url.startsWith("/api/group-assets/v3")
            ? response(list(queryFrom(url), [], 1))
            : undefined;
        click("下一页");
        await screen.findByText("此页没有结果，请返回前一页。");
      } else {
        override = (url) => {
          if (!url.startsWith("/api/group-assets/v3")) return undefined;
          const query = queryFrom(url);
          return response(
            kind === "absent" ? absent(query) : list(query, [], 0),
          );
        };
        await mount();
        await select();
        await screen.findByText(
          kind === "absent"
            ? "暂无可读取的已完成观测，尚不能确定组数量。"
            : "本次观测未返回组对象。",
        );
        if (kind === "no-match") {
          fill("组资产关键词", "missing");
          click("应用组筛选");
          await screen.findByText("本次观测有组，但没有符合当前筛选的结果。");
        }
      }
      expectNoPositions();
    },
  );

  it.each([401, 403, 404])(
    "clears numbered rows on access failure %s",
    async (status) => {
      await start();
      override = (url) =>
        url.startsWith("/api/group-assets/v3")
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
      await waitFor(expectNoPositions);
      await waitFor(() =>
        expect(
          screen.queryByRole("region", { name: "已选数据源组资产" }),
        ).toBeNull(),
      );
    },
  );

  it("clears positions on selection revision invalidation", async () => {
    await start();
    override = (url) =>
      url.startsWith("/api/group-assets/v3")
        ? response({ error: "selection_changed" }, 409)
        : undefined;
    click("下一页");
    await waitFor(() =>
      expect(
        screen.queryByRole("region", { name: "已选数据源组资产" }),
      ).toBeNull(),
    );
    expectNoPositions();
    expect(calls()).toHaveLength(2);
  });

  it("clears positions when the pinned observation is unavailable until explicit refresh", async () => {
    await start();
    override = (url) =>
      url.startsWith("/api/group-assets/v3")
        ? response({ error: "directory_observation_unavailable" }, 409)
        : undefined;
    click("下一页");
    await screen.findByText("原观测已不可用，请明确刷新观测。");
    expectNoPositions();
    expect(calls()).toHaveLength(2);
    override = () => undefined;
    click("刷新组资产观测");
    await screen.findByRole("table", { name: "组资产列表" });
    expectPositions(firstPage);
    expect(params().observationId).toBeUndefined();
    expect(calls()).toHaveLength(3);
  });

  it("clears rows on source change and ignores the previous source response", async () => {
    await start();
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/group-assets/v3") ? held.promise : undefined;
    click("下一页");
    click("更换数据源");
    fireEvent.click(
      screen.getByRole("radio", { name: "选择数据源 other.invalid" }),
    );
    expectNoPositions();
    await held.resolve(list({ ...q, pageIdx: 2 }, [row(11), row(12)], 12));
    expectNoPositions();
    override = () => undefined;
    click("核对并打开连接详情");
    await screen.findByRole("table", { name: "组资产列表" });
    expectPositions(firstPage);
    expect(params().domainId).toBe(otherChoice.domainId);
  });

  it("cannot restore positions after session replacement", async () => {
    const view = await start();
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/group-assets/v3") ? held.promise : undefined;
    click("下一页");
    view.rerender(
      <GroupAssetsV3Workspace
        profile={{ ...profile, csrfToken: "replacement-session" }}
        sessionChanged={vi.fn()}
      />,
    );
    expectNoPositions();
    await held.resolve(list({ ...q, pageIdx: 2 }, [row(11), row(12)], 12));
    expectNoPositions();
    view.unmount();
    expectNoPositions();
  });

  it("cannot restore numbered rows after navigation unmount", async () => {
    const view = await start();
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/group-assets/v3") ? held.promise : undefined;
    click("下一页");
    view.unmount();
    await held.resolve(list({ ...q, pageIdx: 2 }, [row(11), row(12)], 12));
    expectNoPositions();
  });
});
