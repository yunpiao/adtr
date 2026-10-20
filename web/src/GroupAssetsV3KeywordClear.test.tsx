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

const keyword = () => screen.getByRole("textbox", { name: "组资产关键词" });
const clear = () => screen.getByRole("button", { name: "清除组关键词" });
const apply = () => click("应用组筛选");
const ready = () => screen.findByRole("table", { name: "组资产列表" });

describe("UI-0446 independent group keyword clear", () => {
  it("clears only the draft, preserves sort and rows, focuses input without a request", async () => {
    await start();
    fill("创建时间排序", "desc");
    fill("组资产关键词", "observed");
    const before = fetcher.mock.calls.length;
    expect(clear()).toHaveAttribute("type", "button");
    fireEvent.click(clear());
    expect(keyword()).toHaveValue("");
    expect(keyword()).toHaveFocus();
    expect(screen.getByLabelText("创建时间排序", { exact: true })).toHaveValue(
      "desc",
    );
    expect(fetcher.mock.calls).toHaveLength(before);
    expect(params()).toMatchObject({
      search: "",
      pageSize: "10",
      pageIdx: "1",
    });
    expect(screen.getByText("observed-1", { exact: true })).toBeVisible();
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
  });

  it("keeps applied search until Apply, then preserves sort/source/pin/page size and resets page", async () => {
    await start();
    fill("组每页条数", "20");
    await ready();
    fill("创建时间排序", "desc");
    fill("组资产关键词", "observed");
    apply();
    await ready();
    click("下一页");
    await ready();
    const applied = params(),
      count = calls().length;
    fireEvent.click(clear());
    expect(params()).toEqual(applied);
    expect(calls()).toHaveLength(count);
    expect(screen.getByText("observed-3", { exact: true })).toBeVisible();
    expect(screen.getByLabelText("组每页条数")).toBeDisabled();
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    screen
      .getAllByRole("button", { name: "展开事实" })
      .forEach((b) => expect(b).toBeDisabled());
    apply();
    await ready();
    expect(params()).toEqual({ ...applied, search: "", pageIdx: "1" });
    expect(screen.getByLabelText("组每页条数")).toHaveValue("20");
    expect(screen.getByText("observed-23", { exact: true })).toBeVisible();
  });

  it.each(["Enter", " "])(
    "supports keyboard %s and repeated empty clears without submission",
    async (key) => {
      await start();
      fill("组资产关键词", "observed");
      const count = calls().length;
      clear().focus();
      await userEvent.keyboard(key === "Enter" ? "{Enter}" : " ");
      expect(keyword()).toHaveValue("");
      expect(keyword()).toHaveFocus();
      fireEvent.click(clear());
      expect(calls()).toHaveLength(count);
      expect(screen.getByRole("button", { name: "下一页" })).toBeEnabled();
    },
  );

  it("clears an overlength rejected draft and its validation error without submitting", async () => {
    await start();
    fill("组资产关键词", "x".repeat(21));
    apply();
    expect(screen.getByText(/搜索最多20个UTF-16/)).toBeVisible();
    const count = calls().length;
    fireEvent.click(clear());
    expect(keyword()).toHaveValue("");
    expect(screen.queryByText(/搜索最多20个UTF-16/)).toBeNull();
    expect(calls()).toHaveLength(count);
  });

  it.each(["paste", "drop"])(
    "clears rejected %s state even with an empty draft",
    async (kind) => {
      await start();
      const data = { getData: () => "bad\nvalue" };
      if (kind === "paste") fireEvent.paste(keyword(), { clipboardData: data });
      else fireEvent.drop(keyword(), { dataTransfer: data });
      expect(keyword()).toHaveAttribute("aria-invalid", "true");
      const count = calls().length;
      fireEvent.click(clear());
      expect(keyword()).toHaveAttribute("aria-invalid", "false");
      expect(screen.queryByText(/含控制字符/)).toBeNull();
      expect(screen.getByRole("button", { name: "应用组筛选" })).toBeEnabled();
      expect(calls()).toHaveLength(count);
    },
  );

  it("closes expanded facts through existing edit behavior even when already empty", async () => {
    await start();
    fireEvent.click(screen.getAllByRole("button", { name: "展开事实" })[0]);
    expect(screen.getByText("主组 RID（primaryGroupID）")).toBeVisible();
    const count = calls().length;
    fireEvent.click(clear());
    expect(screen.queryByText("主组 RID（primaryGroupID）")).toBeNull();
    expect(calls()).toHaveLength(count);
    expect(
      screen.getByRole("columnheader", { name: "本页序号" }),
    ).toBeVisible();
  });

  it("does not abort loading; a late applied-query response remains dirty after clear", async () => {
    await start();
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/group-assets/v3") ? held.promise : undefined;
    fill("组资产关键词", "observed-1");
    apply();
    const applied = queryFrom(calls().at(-1)![0]);
    const signal = calls().at(-1)![1].signal as AbortSignal;
    fireEvent.click(clear());
    expect(signal.aborted).toBe(false);
    expect(calls()).toHaveLength(2);
    await held.resolve(list(applied, [row()], 1));
    await ready();
    expect(keyword()).toHaveValue("");
    expect(screen.getByText("observed-1", { exact: true })).toBeVisible();
    expect(screen.getByRole("button", { name: "展开事实" })).toBeDisabled();
    expect(params().search).toBe("observed-1");
  });

  it("cannot revive a stale observation by clearing its draft", async () => {
    await start();
    override = (url) =>
      url.startsWith("/api/group-assets/v3")
        ? response({ error: "directory_observation_unavailable" }, 409)
        : undefined;
    fill("组资产关键词", "observed");
    apply();
    await screen.findByText(/原观测已不可用/);
    const count = calls().length;
    fireEvent.click(clear());
    expect(keyword()).toHaveValue("");
    expect(screen.getByRole("button", { name: "应用组筛选" })).toBeDisabled();
    expect(screen.queryByRole("table", { name: "组资产列表" })).toBeNull();
    expect(calls()).toHaveLength(count);
  });

  it("retains full-clear semantics: resets sorting and immediately queries", async () => {
    await start();
    fill("创建时间排序", "desc");
    fill("组资产关键词", "observed");
    apply();
    await ready();
    const count = calls().length;
    click("清除组筛选");
    await ready();
    expect(calls()).toHaveLength(count + 1);
    expect(params().search).toBe("");
    expect(params()).not.toHaveProperty("creatTm");
    expect(screen.getByLabelText("创建时间排序", { exact: true })).toHaveValue(
      "",
    );
  });
  it.each(["no observation", "zero groups", "zero matches"])(
    "clears in %s without inventing result rows",
    async (state) => {
      override = (url) => {
        if (!url.startsWith("/api/group-assets/v3")) return;
        const query = queryFrom(url);
        return response(
          state === "no observation" ? absent(query) : list(query, [], 0),
        );
      };
      await mount();
      await select();
      await waitFor(() =>
        expect(screen.queryByText("正在读取组观测…")).toBeNull(),
      );
      fill("组资产关键词", state === "zero matches" ? "missing" : "draft");
      if (state === "zero matches") {
        apply();
        await waitFor(() =>
          expect(screen.queryByText("正在读取组观测…")).toBeNull(),
        );
      }
      const count = calls().length;
      fireEvent.click(clear());
      expect(keyword()).toHaveValue("");
      expect(calls()).toHaveLength(count);
      expect(screen.queryByRole("table", { name: "组资产列表" })).toBeNull();
    },
  );

  it("does not resurrect controls or data after source revocation and a late read", async () => {
    await start();
    const held = deferred();
    override = (url) =>
      url.startsWith("/api/group-assets/v3") ? held.promise : undefined;
    fill("组资产关键词", "observed");
    apply();
    fireEvent.click(clear());
    await held.resolve({ error: "forbidden" }, 403);
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "清除组关键词" })).toBeNull(),
    );
    expect(screen.queryByRole("table", { name: "组资产列表" })).toBeNull();
    expect(calls()).toHaveLength(2);
  });
});
