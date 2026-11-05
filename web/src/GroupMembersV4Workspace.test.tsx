// Synthetic DOM/transport evidence only; no real browser, PG or AD claim.
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
import GroupMembersV4Workspace from "./GroupMembersV4Workspace";
import { type Profile } from "./api";
import { type SourceChoice } from "./domain-selection-api";
import {
  groupMembersV4Read,
  parseGroupMembersV4Page,
} from "./group-members-v4-read";
import { directoryV2BodyLimit } from "./directory-v2-json";
import { SESSION_INVALIDATION_STORAGE } from "./session-invalidation";
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

const row = (
  index = 1,
  members: string[] | null = [
    " z ",
    "CN=<script>bad</script>",
    "\0\n\u202e",
    "\\u0000",
  ],
) => ({
  objectGUID: `00000000-0000-0000-0000-${String(index).padStart(12, "0")}`,
  distinguishedName: `CN=Group ${index},DC=synthetic,DC=invalid`,
  kind: "group",
  objectClass: ["group", "top"],
  samAccountName: `group-${index}` as string | null,
  userAccountControl: null,
  objectSid: null,
  mail: null,
  description: null,
  whenCreated: null,
  primaryGroupID: null,
  servicePrincipalName: null,
  rawMember: members,
  returnedMemberCount: members === null ? null : members.length,
  memberReadState: members === null ? "not_returned" : "returned",
});
const q = {
  domainId: choice.domainId,
  expectedRevision: choice.revision,
  expectedCredentialRevision: choice.credentialRevision,
  observationId: "observation-one",
  objectGUID: row().objectGUID,
};
const detail = (members: string[] | null = row().rawMember) => ({
  dictionaryVersion: 4,
  selection: {
    domainId: q.domainId,
    revision: q.expectedRevision,
    credentialRevision: q.expectedCredentialRevision,
  },
  observationId: q.observationId,
  source,
  object: row(1, members),
});
const page = (url: string, total = 26) => {
  const p = new URL(url, "https://test.invalid").searchParams;
  const pageIdx = Number(p.get("pageIdx")),
    pageSize = Number(p.get("pageSize"));
  return {
    dictionaryVersion: 4,
    available: true,
    observationId: "observation-one",
    source,
    list: Array.from({ length: total }, (_, i) => row(i + 1)).slice(
      (pageIdx - 1) * pageSize,
      pageIdx * pageSize,
    ),
    page: { pageIdx, pageSize, total, totalPage: Math.ceil(total / pageSize) },
  };
};
const referencesPath = "/api/directory-assets/v4/raw-dn-references";
const references = (url: string, total = 26, kind = "group") => {
  const p = new URL(url, "https://test.invalid").searchParams;
  const target = row(Number(p.get("objectGUID")!.slice(-12)));
  if (kind !== "group")
    Object.assign(target, {
      kind,
      objectClass: [kind, "top"].sort(),
      samAccountName: `${kind}-${Number(target.objectGUID.slice(-12))}`,
      rawMember: null,
      returnedMemberCount: null,
      memberReadState: "not_returned",
    });
  const values = page(url, total);
  return {
    ...detail(),
    object: target,
    list: values.list.map((r) => ({
      ...r,
      rawMember: [target.distinguishedName],
      returnedMemberCount: 1,
    })),
    page: values.page,
    coverage: {
      groupCount: total + 3,
      memberReturnedGroupCount: total + 1,
      memberNotReturnedGroupCount: 2,
    },
  };
};
const response = (value: unknown, status = 200, actor = "1") =>
  Promise.resolve(
    new Response(JSON.stringify(value), {
      status,
      headers: { "X-ADTR-User-ID": actor },
    }),
  );
let override: (url: string, init: RequestInit) => Promise<Response> | undefined;
let fetcher: ReturnType<
  typeof vi.fn<(url: string, init?: RequestInit) => Promise<Response>>
>;
const deferred = () => {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((r) => (resolve = r));
  return { resolve, promise };
};
const calls = (path: string) =>
  fetcher.mock.calls.filter(([url]) => url.split("?")[0] === path);
const listPath = "/api/directory/v4/observation",
  detailPath = "/api/group-assets/v4/members";
const params = (path: string) =>
  Object.fromEntries(
    new URL(calls(path).at(-1)![0], "https://test.invalid").searchParams,
  );
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
async function open() {
  const result = render(
    <GroupMembersV4Workspace profile={profile} sessionChanged={vi.fn()} />,
  );
  fireEvent.click(
    await screen.findByRole("radio", { name: `选择数据源 ${choice.domain}` }),
  );
  click("核对并打开连接详情");
  await screen.findByText("group-1");
  return result;
}
async function expand() {
  fireEvent.click(screen.getAllByRole("button", { name: "查看原始成员" })[0]);
  await screen.findByRole("region", { name: "组原始成员详情" });
}
beforeEach(() => {
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
          auth: { readable: true },
        })),
      });
    if (path === "/api/access/check")
      return response({
        results: JSON.parse(String(init.body)).paths.map(() => true),
      });
    if (path === "/api/domain-selection")
      return response({
        List: [choice],
        page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
        exhausted: true,
      });
    if (path === "/api/domain-selection/resolve")
      return response({ selection: choice, checkedAt: "2026-10-10T00:00:00Z" });
    if (path === listPath) {
      const value = page(url);
      const kind =
        new URL(url, "https://test.invalid").searchParams.get("kind") ??
        "group";
      if (kind !== "group")
        value.list = value.list.map((r) => ({
          ...r,
          kind,
          objectClass: [kind, "top"].sort(),
          samAccountName: `${kind}-${Number(r.objectGUID.slice(-12))}`,
          rawMember: null,
          returnedMemberCount: null,
          memberReadState: "not_returned",
        }));
      return response(value);
    }
    if (path === referencesPath) return response(references(url));
    if (path === detailPath) {
      const value = detail();
      value.object.objectGUID = new URL(
        url,
        "https://test.invalid",
      ).searchParams.get("objectGUID")!;
      return response(value);
    }
    throw new Error(`Unexpected request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
describe("isolated dictionary4 membership consumer", () => {
  it("keeps the old pin and restores detail on focus, then explicitly rediscovers a newer empty observation", async () => {
    await open();
    await expand();
    const newSource = {
      ...source,
      started_at: "2026-10-11T00:00:00Z",
      completed_at: "2026-10-11T00:00:01Z",
    };
    override = (url) => {
      if (
        url.startsWith(listPath) &&
        !new URL(url, "https://test.invalid").searchParams.has("observationId")
      )
        return response({
          ...page(url, 0),
          observationId: "observation-two",
          source: newSource,
        });
    };
    fireEvent.blur(window);
    expect(
      screen.queryByRole("region", { name: "组原始成员详情" }),
    ).not.toBeInTheDocument();
    fireEvent.focus(window);
    await screen.findByRole("region", { name: "组原始成员详情" });
    expect(params(listPath).observationId).toBe("observation-one");
    expect(params(detailPath).observationId).toBe("observation-one");
    click("重读原观测");
    await screen.findByText("group-1");
    expect(params(listPath).observationId).toBe("observation-one");
    expect(
      screen.queryByRole("region", { name: "组原始成员详情" }),
    ).not.toBeInTheDocument();
    await expand();
    const details = calls(detailPath).length;
    for (let i = 0; i < 2; i++) {
      click("重新选择数据源");
      expect(
        screen.queryByRole("region", { name: "组原始成员详情" }),
      ).not.toBeInTheDocument();
      expect(screen.queryByText("observation-one")).not.toBeInTheDocument();
      fireEvent.click(
        await screen.findByRole("radio", {
          name: `选择数据源 ${choice.domain}`,
        }),
      );
      click("核对并打开连接详情");
      await screen.findByText("本页观测未返回组对象；成员情况未知。");
      expect(params(listPath).observationId).toBeUndefined();
      expect(screen.getByText("observation-two")).toBeVisible();
      expect(screen.getByText(newSource.started_at)).toBeVisible();
      expect(
        screen.queryByRole("button", { name: "查看原始成员" }),
      ).not.toBeInTheDocument();
    }
    override = (url) =>
      url.startsWith(listPath)
        ? response({
            ...page(url, 0),
            observationId: "observation-two",
            source: newSource,
          })
        : undefined;
    click("重读原观测");
    await screen.findByText("本页观测未返回组对象；成员情况未知。");
    expect(params(listPath).observationId).toBe("observation-two");
    expect(calls(detailPath)).toHaveLength(details);
  });

  it("wraps long observation and DN facts locally without truncating their text", async () => {
    const dn = "CN=" + "x".repeat(300) + ",OU=Groups,DC=synthetic,DC=invalid";
    override = (url) => {
      if (url.startsWith(listPath)) {
        const value = page(url);
        value.list[1].samAccountName = null;
        value.list[1].distinguishedName = dn;
        return response(value);
      }
      if (url.startsWith(detailPath)) {
        const value = detail([dn]);
        value.object.distinguishedName = dn;
        return response(value);
      }
    };
    await open();
    await expand();
    const workspace = screen.getByRole("region", {
      name: "组成员原始观测 · 字典 4",
    });
    expect(workspace).toHaveStyle({ overflowWrap: "anywhere" });
    expect(workspace.style.overflow).toBe("");
    expect(workspace.style.overflowX).toBe("");
    expect(workspace.style.textOverflow).toBe("");
    expect(
      within(workspace).getByRole("heading", { name: dn }).textContent,
    ).toBe(dn);
    expect(within(workspace).getAllByText(dn)).toHaveLength(3);
    expect(within(workspace).getByText(q.observationId)).toBeVisible();
    expect(within(workspace).getByText(source.started_at)).toBeVisible();
  });
  it("requires explicit selection, pins pages and sends exactly five detail pins with no write endpoints", async () => {
    render(
      <GroupMembersV4Workspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByRole("radio");
    expect(calls(listPath)).toHaveLength(0);
    fireEvent.click(screen.getByRole("radio"));
    click("核对并打开连接详情");
    await screen.findByText("group-1");
    expect(params(listPath)).toEqual({
      domainId: choice.domainId,
      kind: "group",
      pageIdx: "1",
      pageSize: "25",
    });
    await expand();
    expect(params(detailPath)).toEqual(q);
    click("下一页");
    await screen.findByText("group-26");
    expect(params(listPath).observationId).toBe("observation-one");
    expect(screen.queryByRole("region", { name: "组原始成员详情" })).toBeNull();
    fireEvent.change(screen.getByLabelText("每页组数"), {
      target: { value: "50" },
    });
    await screen.findByText("group-1");
    expect(params(listPath)).toMatchObject({
      pageIdx: "1",
      pageSize: "50",
      observationId: "observation-one",
    });
    expect(
      fetcher.mock.calls.every(
        ([url, init]) => init?.method === "GET" || url === "/api/access/check",
      ),
    ).toBe(true);
    expect(
      fetcher.mock.calls.some(([url]) =>
        /\/v3|sync|cancel|credential-use/.test(url),
      ),
    ).toBe(false);
  });
  it("preserves exact 25+1 GUID order, second-page member pins and clears details on return and page-size changes", async () => {
    const witness = [
      "CN=page-two-only",
      "CN=raw\u0000\n😀\u202e<script>",
      "\\u0000",
      "CN=page-two-last",
    ];
    override = (url) => {
      if (url.startsWith(detailPath)) {
        const value = detail(witness);
        value.object = row(26, witness);
        return response(value);
      }
    };
    await open();
    const guids = () =>
      within(screen.getByRole("list", { name: "本次观测的组" }))
        .getAllByRole("listitem")
        .map((item) => item.querySelectorAll("span")[1].textContent);
    expect(guids()).toEqual(
      Array.from({ length: 25 }, (_, i) => row(i + 1).objectGUID),
    );
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "下一页" })).toBeEnabled();
    click("下一页");
    await screen.findByText("group-26");
    expect(guids()).toEqual([row(26).objectGUID]);
    expect(params(listPath)).toEqual({
      domainId: choice.domainId,
      kind: "group",
      pageIdx: "2",
      pageSize: "25",
      observationId: q.observationId,
    });
    expect(screen.getByRole("button", { name: "上一页" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    await expand();
    expect(params(detailPath)).toEqual({
      ...q,
      objectGUID: row(26).objectGUID,
    });
    expect(
      within(
        screen.getByRole("list", { name: "原始 member 值（可逆展示转义）" }),
      )
        .getAllByRole("listitem")
        .map((item) => item.textContent),
    ).toEqual([
      "CN=page-two-only",
      "CN=raw\\u0000\\u000a😀\\u202e<script>",
      "\\\\u0000",
      "CN=page-two-last",
    ]);
    click("上一页");
    await screen.findByText("group-1");
    expect(guids()).toEqual(
      Array.from({ length: 25 }, (_, i) => row(i + 1).objectGUID),
    );
    expect(screen.queryByRole("region", { name: "组原始成员详情" })).toBeNull();
    fireEvent.change(screen.getByLabelText("每页组数"), {
      target: { value: "50" },
    });
    await screen.findByText("group-26");
    expect(guids()).toEqual(
      Array.from({ length: 26 }, (_, i) => row(i + 1).objectGUID),
    );
    expect(params(listPath)).toMatchObject({
      pageIdx: "1",
      pageSize: "50",
      observationId: q.observationId,
    });
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("每页组数"), {
      target: { value: "25" },
    });
    await waitFor(() => expect(guids()).toHaveLength(25));
    expect(params(listPath)).toMatchObject({
      pageIdx: "1",
      pageSize: "25",
      observationId: q.observationId,
    });
    expect(screen.queryByText("group-26")).toBeNull();
    click("重读原观测");
    await screen.findByText("group-1");
    expect(params(listPath)).toMatchObject({
      pageIdx: "1",
      pageSize: "25",
      observationId: q.observationId,
    });
    expect(calls(detailPath)).toHaveLength(1);
  });
  it.each([null, []] as (string[] | null)[])(
    "distinguishes missing and returned empty %j",
    async (members) => {
      override = (url) =>
        url.startsWith(detailPath) ? response(detail(members)) : undefined;
      await open();
      await expand();
      expect(
        screen.getByText(
          members === null
            ? /member 未返回，成员情况未知/
            : /member 已返回空列表/,
        ),
      ).toBeInTheDocument();
    },
  );
  it("renders all raw values as reversible text, never HTML or DN links", async () => {
    await open();
    await expand();
    const list = screen.getByRole("list", {
      name: "原始 member 值（可逆展示转义）",
    });
    expect(
      within(list)
        .getAllByRole("listitem")
        .map((x) => x.textContent),
    ).toEqual([
      " z ",
      "CN=<script>bad</script>",
      "\\u0000\\u000a\\u202e",
      "\\\\u0000",
    ]);
    expect(list.querySelector("a,script")).toBeNull();
  });
  it.each([401, 403, 409])(
    "clears private state on authoritative HTTP %i even malformed error body",
    async (status) => {
      await open();
      override = (url) =>
        url.startsWith(detailPath)
          ? Promise.resolve(new Response("{bad", { status }))
          : undefined;
      fireEvent.click(
        screen.getAllByRole("button", { name: "查看原始成员" })[0],
      );
      await screen.findByRole("alert");
      expect(screen.queryByText("group-1")).toBeNull();
      expect(calls(detailPath)).toHaveLength(1);
      if (status === 409) await screen.findByRole("radio");
    },
  );
  it("404 explains observation unavailability without asserting AD deletion", async () => {
    await open();
    override = (url) =>
      url.startsWith(detailPath) ? response({}, 404) : undefined;
    fireEvent.click(screen.getAllByRole("button", { name: "查看原始成员" })[0]);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "不能确定对象是否已从 AD 删除",
    );
  });
  it.each(["close", "source", "unmount", "invalidation"])(
    "held detail cannot restore after %s",
    async (action) => {
      const mounted = await open(),
        held = deferred();
      override = (url) =>
        url.startsWith(detailPath) ? held.promise : undefined;
      fireEvent.click(
        screen.getAllByRole("button", { name: "查看原始成员" })[0],
      );
      await waitFor(() => expect(calls(detailPath)).toHaveLength(1));
      if (action === "close") click("关闭成员详情");
      else if (action === "source") click("重新选择数据源");
      else if (action === "unmount") mounted.unmount();
      else
        fireEvent(
          window,
          new StorageEvent("storage", {
            key: SESSION_INVALIDATION_STORAGE,
            newValue: "a".repeat(32),
            storageArea: localStorage,
          }),
        );
      await act(async () => held.resolve(await response(detail())));
      expect(
        screen.queryByRole("region", { name: "组原始成员详情" }),
      ).toBeNull();
    },
  );
  it("focus clears facts until same identity reauthorization, then reloads same page and detail pins", async () => {
    await open();
    await expand();
    const held = deferred();
    let once = true;
    override = (url) =>
      url === "/api/auth/me" && once
        ? ((once = false), held.promise)
        : undefined;
    fireEvent.focus(window);
    expect(screen.queryByText("group-1")).toBeNull();
    expect(screen.queryByRole("region", { name: "组原始成员详情" })).toBeNull();
    await act(async () => held.resolve(await response(profile)));
    await screen.findByRole("region", { name: "组原始成员详情" });
    expect(params(listPath).observationId).toBe("observation-one");
    expect(params(detailPath)).toEqual(q);
  });
  it("same actor with changed session token cannot restore facts", async () => {
    await open();
    await expand();
    override = (url) =>
      url === "/api/auth/me"
        ? response({ ...profile, csrfToken: "new-session" })
        : undefined;
    fireEvent.focus(window);
    await screen.findByRole("alert");
    expect(screen.queryByText("group-1")).toBeNull();
  });
  it("old group response cannot replace a newer group detail", async () => {
    await open();
    const held = deferred();
    override = (url) =>
      url.startsWith(detailPath) && url.includes(q.objectGUID)
        ? held.promise
        : undefined;
    fireEvent.click(screen.getAllByRole("button", { name: "查看原始成员" })[0]);
    await waitFor(() => expect(calls(detailPath)).toHaveLength(1));
    fireEvent.click(screen.getAllByRole("button", { name: "查看原始成员" })[1]);
    await screen.findByRole("region", { name: "组原始成员详情" });
    await act(async () => held.resolve(await response(detail())));
    expect(
      screen.getByRole("region", { name: "组原始成员详情" }),
    ).toHaveTextContent(row(2).objectGUID);
  });
});
describe("observation and authorization races", () => {
  it.each(["source", "unmount", "focus", "invalidation"])(
    "held page cannot restore after %s",
    async (action) => {
      const mounted = await open(),
        held = deferred();
      override = (url) => (url.startsWith(listPath) ? held.promise : undefined);
      click("下一页");
      await waitFor(() => expect(calls(listPath)).toHaveLength(2));
      if (action === "source") click("重新选择数据源");
      else if (action === "unmount") mounted.unmount();
      else if (action === "invalidation")
        fireEvent(
          window,
          new StorageEvent("storage", {
            key: SESSION_INVALIDATION_STORAGE,
            newValue: "b".repeat(32),
            storageArea: localStorage,
          }),
        );
      else {
        override = (url) =>
          url === "/api/auth/me" ? response({ ...profile, ID: 2 }) : undefined;
        fireEvent.focus(window);
      }
      await act(async () =>
        held.resolve(await response(page(`${listPath}?pageIdx=2&pageSize=25`))),
      );
      expect(screen.queryByText("group-26")).toBeNull();
    },
  );
  it("unavailable observation stays unknown and does not request detail or fallback", async () => {
    override = (url) =>
      url.startsWith(listPath)
        ? response({
            dictionaryVersion: 4,
            available: false,
            list: [],
            page: { pageIdx: 1, pageSize: 25, total: 0, totalPage: 0 },
          })
        : undefined;
    render(
      <GroupMembersV4Workspace profile={profile} sessionChanged={vi.fn()} />,
    );
    fireEvent.click(await screen.findByRole("radio"));
    click("核对并打开连接详情");
    await screen.findByText(/暂无可读取的字典 4 已完成观测/);
    expect(calls(detailPath)).toHaveLength(0);
  });
  it("renders an available zero-group observation as unknown, pins reread and disables both pages", async () => {
    override = (url) =>
      url.startsWith(listPath) ? response(page(url, 0)) : undefined;
    render(
      <GroupMembersV4Workspace profile={profile} sessionChanged={vi.fn()} />,
    );
    fireEvent.click(await screen.findByRole("radio"));
    click("核对并打开连接详情");
    expect(await screen.findByRole("status")).toHaveTextContent(
      "本页观测未返回组对象；成员情况未知。",
    );
    expect(screen.queryByText(/暂无可读取的字典 4 已完成观测/)).toBeNull();
    expect(screen.getByText("observation-one")).toBeVisible();
    expect(screen.getByText(source.completed_at)).toBeVisible();
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    expect(screen.getByLabelText("每页组数")).toHaveValue("25");
    expect(
      screen.getByText("第 1 页 · 共 0 页（仅本次观测中的组）"),
    ).toBeVisible();
    expect(
      within(screen.getByRole("list", { name: "本次观测的组" })).queryAllByRole(
        "listitem",
      ),
    ).toHaveLength(0);
    expect(screen.queryByRole("button", { name: "查看原始成员" })).toBeNull();
    expect(screen.queryByRole("region", { name: "组原始成员详情" })).toBeNull();
    click("上一页");
    click("下一页");
    expect(calls(listPath)).toHaveLength(1);
    click("重读原观测");
    await waitFor(() => expect(calls(listPath)).toHaveLength(2));
    await screen.findByRole("button", { name: "重读原观测" });
    expect(params(listPath)).toEqual({
      domainId: choice.domainId,
      kind: "group",
      pageIdx: "1",
      pageSize: "25",
      observationId: "observation-one",
    });
    expect(screen.getByRole("status")).toHaveTextContent(
      "本页观测未返回组对象；成员情况未知。",
    );
    expect(calls(detailPath)).toHaveLength(0);
    expect(
      fetcher.mock.calls.every(
        ([url, init]) => init?.method === "GET" || url === "/api/access/check",
      ),
    ).toBe(true);
  });
  it.each([401, 403, 409])(
    "page %i clears old source and data without latest rediscovery",
    async (status) => {
      await open();
      override = (url) =>
        url.startsWith(listPath) ? response({}, status) : undefined;
      click("下一页");
      await screen.findByRole("alert");
      expect(screen.queryByText("group-1")).toBeNull();
      expect(calls(listPath)).toHaveLength(2);
    },
  );
  it("new session props remount and require explicit fresh selection", async () => {
    const mounted = await open();
    await expand();
    const next = { ...profile, csrfToken: "different" };
    override = (url) => (url === "/api/auth/me" ? response(next) : undefined);
    mounted.rerender(
      <GroupMembersV4Workspace profile={next} sessionChanged={vi.fn()} />,
    );
    expect(screen.queryByRole("region", { name: "组原始成员详情" })).toBeNull();
    await screen.findByRole("radio");
    expect(calls(listPath)).toHaveLength(1);
  });
  it("blur immediately hides all private rows, visibility return rechecks exact pin", async () => {
    await open();
    await expand();
    fireEvent.blur(window);
    expect(screen.queryByText("group-1")).toBeNull();
    fireEvent(document, new Event("visibilitychange"));
    await screen.findByRole("region", { name: "组原始成员详情" });
    expect(params(listPath).observationId).toBe(q.observationId);
  });
  it("revoked access check prevents source picker and observation discovery", async () => {
    override = (url) =>
      url === "/api/access/check"
        ? response({ results: [true, false, true, true] })
        : undefined;
    render(
      <GroupMembersV4Workspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByRole("alert");
    expect(screen.queryByRole("radio")).toBeNull();
    expect(calls(listPath)).toHaveLength(0);
  });
  it("source change detected after observation prevents publication", async () => {
    let resolutions = 0;
    override = (url) =>
      url.startsWith("/api/domain-selection/resolve") && ++resolutions === 3
        ? response({}, 409)
        : undefined;
    render(
      <GroupMembersV4Workspace profile={profile} sessionChanged={vi.fn()} />,
    );
    fireEvent.click(await screen.findByRole("radio"));
    click("核对并打开连接详情");
    await screen.findByRole("alert");
    expect(screen.queryByText("group-1")).toBeNull();
  });
  it("actor changes after detail response and before publication clear all facts", async () => {
    await open();
    let received = false;
    override = (url) => {
      if (url.startsWith(detailPath)) {
        received = true;
        return response(detail());
      }
      if (url === "/api/auth/me" && received)
        return response({ ...profile, ID: 2 });
      return undefined;
    };
    fireEvent.click(screen.getAllByRole("button", { name: "查看原始成员" })[0]);
    await screen.findByRole("alert");
    expect(screen.queryByText("group-1")).toBeNull();
    expect(screen.queryByRole("region", { name: "组原始成员详情" })).toBeNull();
  });
});
describe("new transport boundaries", () => {
  it.each(["actor", "utf8", "oversize", "v3", "wrong-pin"])(
    "rejects %s",
    async (failure) => {
      override = (url) => {
        if (!url.startsWith(detailPath)) return;
        if (failure === "actor") return response(detail(), 200, "2");
        if (failure === "utf8")
          return Promise.resolve(
            new Response(new Uint8Array([0xff]), {
              headers: { "X-ADTR-User-ID": "1" },
            }),
          );
        if (failure === "oversize")
          return Promise.resolve(
            new Response("{}", {
              headers: {
                "X-ADTR-User-ID": "1",
                "Content-Length": String(directoryV2BodyLimit + 1),
              },
            }),
          );
        return response({
          ...detail(),
          ...(failure === "v3"
            ? { dictionaryVersion: 3 }
            : { observationId: "wrong" }),
        });
      };
      await expect(
        groupMembersV4Read.detail(q, profile, new AbortController().signal),
      ).rejects.toBeDefined();
    },
  );
  it("rejects all missing request pins before any network", async () => {
    for (const key of Object.keys(q)) {
      const wrong = { ...q };
      Reflect.deleteProperty(wrong, key);
      await expect(
        groupMembersV4Read.detail(wrong, profile, new AbortController().signal),
      ).rejects.toMatchObject({ code: "invalid_input" });
    }
    expect(fetcher).not.toHaveBeenCalled();
  });
  it("observation parser rejects v3, wrong kind, unknown fields and changing pin", () => {
    const query = {
      domainId: choice.domainId,
      pageIdx: 1,
      pageSize: 25,
      observationId: "observation-one",
    };
    const value = page(`${listPath}?pageIdx=1&pageSize=25`, 1);
    for (const change of [
      { dictionaryVersion: 3 },
      { extra: 1 },
      { observationId: "new" },
      { list: [{ ...row(), kind: "user" }] },
    ])
      expect(() =>
        parseGroupMembersV4Page({ ...value, ...change }, query),
      ).toThrow();
  });
});

describe("same-observation raw DN references", () => {
  async function expandReferences() {
    fireEvent.click(
      screen.getAllByRole("button", { name: "引用此 DN 原文的组" })[0],
    );
    return screen.findByRole("region", { name: "引用此 DN 原文的组" });
  }
  it.each(["group", "user", "computer"])(
    "reads %s from the same v4 observation and binds seven query keys",
    async (kind) => {
      await open();
      if (kind !== "group")
        fireEvent.change(screen.getByLabelText("对象类别"), {
          target: { value: kind },
        });
      await screen.findByText(`${kind}-1`);
      override = (url) =>
        url.startsWith(referencesPath)
          ? response(references(url, 26, kind))
          : undefined;
      const region = await expandReferences();
      expect(params(referencesPath)).toEqual({
        ...q,
        pageIdx: "1",
        pageSize: "25",
      });
      expect(region).toHaveTextContent("member 未返回的组：2");
      expect(within(region).getAllByRole("listitem")).toHaveLength(25);
      click("引用组下一页");
      await waitFor(() =>
        expect(
          screen.getByRole("region", { name: "引用此 DN 原文的组" }),
        ).toHaveTextContent("引用组第 2 页"),
      );
      expect(params(referencesPath).observationId).toBe(q.observationId);
      click("重读 DN 原文引用");
      await screen.findByRole("region", { name: "引用此 DN 原文的组" });
      expect(params(referencesPath).pageIdx).toBe("2");
      fireEvent.change(screen.getByLabelText("每页引用组数"), {
        target: { value: "50" },
      });
      await waitFor(() =>
        expect(
          screen.getByRole("region", { name: "引用此 DN 原文的组" }),
        ).toHaveTextContent("引用组第 1 页"),
      );
      expect(params(referencesPath).pageSize).toBe("50");
      click("关闭 DN 原文引用");
      fireEvent.blur(window);
      fireEvent.focus(window);
      await screen.findByText(`${kind}-1`);
      expect(
        screen.queryByRole("region", { name: "引用此 DN 原文的组" }),
      ).toBeNull();
      expect(fetcher.mock.calls.some(([url]) => url.includes("/v3/"))).toBe(
        false,
      );
    },
  );
  it("shows exact zero text and unknown coverage, never empty membership inference", async () => {
    await open();
    override = (url) =>
      url.startsWith(referencesPath) ? response(references(url, 0)) : undefined;
    const region = await expandReferences();
    expect(region).toHaveTextContent("本观测未发现完全相同的 DN 文本引用");
    expect(region).toHaveTextContent("member 未返回的组：2");
    expect(
      within(region).getByRole("button", { name: "引用组下一页" }),
    ).toBeDisabled();
    expect(
      within(region).getByRole("button", { name: "引用组上一页" }),
    ).toBeDisabled();
  });
  it.each([
    "kind",
    "source",
    "object",
    "close",
    "reread",
    "back",
    "forward",
    "unmount",
    "session",
  ])("ignores an uncancelable late reference after %s", async (action) => {
    const mounted = await open(),
      held = deferred();
    let heldURL = "";
    override = (url) => {
      if (url.startsWith(referencesPath)) {
        heldURL = url;
        return held.promise;
      }
    };
    fireEvent.click(
      screen.getAllByRole("button", { name: "引用此 DN 原文的组" })[0],
    );
    await waitFor(() => expect(calls(referencesPath)).toHaveLength(1));
    if (action === "kind")
      fireEvent.change(screen.getByLabelText("对象类别"), {
        target: { value: "user" },
      });
    if (action === "source") click("重新选择数据源");
    if (action === "object")
      fireEvent.click(
        screen.getAllByRole("button", { name: "查看原始成员" })[1],
      );
    if (action === "close") click("关闭 DN 原文引用");
    if (action === "reread") click("重读原观测");
    if (action === "back" || action === "forward")
      fireEvent(window, new PopStateEvent("popstate", { state: action }));
    if (action === "unmount") mounted.unmount();
    if (action === "session")
      fireEvent(
        window,
        new StorageEvent("storage", {
          key: SESSION_INVALIDATION_STORAGE,
          newValue: "c".repeat(32),
          storageArea: localStorage,
        }),
      );
    await act(async () => held.resolve(await response(references(heldURL))));
    expect(
      screen.queryByRole("region", { name: "引用此 DN 原文的组" }),
    ).toBeNull();
  });
  it.each([401, 403, 409])(
    "clears all private facts on reference %i",
    async (status) => {
      await open();
      override = (url) =>
        url.startsWith(referencesPath) ? response({}, status) : undefined;
      fireEvent.click(
        screen.getAllByRole("button", { name: "引用此 DN 原文的组" })[0],
      );
      await screen.findByRole("alert");
      expect(screen.queryByText("group-1")).toBeNull();
      expect(
        screen.queryByRole("region", { name: "引用此 DN 原文的组" }),
      ).toBeNull();
    },
  );
  it.each([
    "actor-header",
    "actor-after",
    "source-after",
    "source-facts",
    "object-facts",
    "deny",
  ])("fails closed on reference %s", async (mode) => {
    await open();
    let received = false;
    override = (url, init) => {
      if (url.startsWith(referencesPath)) {
        received = true;
        const value = references(url);
        if (mode === "source-facts")
          value.source = { ...source, dc_host_name: "other.invalid" };
        if (mode === "object-facts") {
          value.object.distinguishedName = "CN=other";
          value.list.forEach((r) => {
            r.rawMember = ["CN=other"];
          });
        }
        return response(value, 200, mode === "actor-header" ? "2" : "1");
      }
      if (received && mode === "actor-after" && url === "/api/auth/me")
        return response({ ...profile, ID: 2 });
      if (
        received &&
        mode === "source-after" &&
        url.startsWith("/api/domain-selection/resolve")
      )
        return response({}, 409);
      if (
        mode === "deny" &&
        url === "/api/access/check" &&
        JSON.parse(String(init.body)).paths.length === 1
      )
        return response({ results: [false] });
    };
    fireEvent.click(
      screen.getAllByRole("button", { name: "引用此 DN 原文的组" })[0],
    );
    await screen.findByRole("alert");
    expect(
      screen.queryByRole("region", { name: "引用此 DN 原文的组" }),
    ).toBeNull();
  });
});

describe("raw DN reference transport guards", () => {
  const query = { ...q, pageIdx: 1, pageSize: 25 };
  const read = () =>
    groupMembersV4Read.references(
      query,
      choice,
      profile,
      new AbortController().signal,
    );
  it("rejects omitted pins and changed source choice before dispatch", async () => {
    for (const key of Object.keys(query)) {
      const bad = { ...query };
      Reflect.deleteProperty(bad, key);
      await expect(
        groupMembersV4Read.references(
          bad,
          choice,
          profile,
          new AbortController().signal,
        ),
      ).rejects.toMatchObject({ code: "invalid_input" });
    }
    for (const patch of [
      { domainId: "other" },
      { revision: "2" },
      { credentialRevision: "2" },
    ])
      await expect(
        groupMembersV4Read.references(
          query,
          { ...choice, ...patch },
          profile,
          new AbortController().signal,
        ),
      ).rejects.toMatchObject({ code: "invalid_input" });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it.each(["utf8", "oversize", "json", "partial", "unknown"])(
    "rejects %s body without returning partial facts",
    async (mode) => {
      override = (url) => {
        if (!url.startsWith(referencesPath)) return;
        const headers = { "X-ADTR-User-ID": "1" };
        if (mode === "utf8")
          return Promise.resolve(
            new Response(new Uint8Array([0xff]), { headers }),
          );
        if (mode === "oversize")
          return Promise.resolve(
            new Response("{}", {
              headers: {
                ...headers,
                "Content-Length": String(directoryV2BodyLimit + 1),
              },
            }),
          );
        if (mode === "json")
          return Promise.resolve(new Response("{", { headers }));
        if (mode === "unknown")
          return response({ ...references(url), extra: true });
        let count = 0;
        return Promise.resolve(
          new Response(
            new ReadableStream(
              {
                pull(c) {
                  if (count++ === 0)
                    c.enqueue(
                      new TextEncoder().encode(JSON.stringify(references(url))),
                    );
                  else c.error(new Error("synthetic-stream-failure"));
                },
              },
              { highWaterMark: 0 },
            ),
            { headers },
          ),
        );
      };
      await expect(read()).rejects.toBeDefined();
    },
  );
  it.each([401, 403, 409])(
    "preserves authoritative %i even with an oversized malformed body",
    async (status) => {
      override = (url) =>
        url.startsWith(referencesPath)
          ? Promise.resolve(
              new Response("{", {
                status,
                headers: { "Content-Length": String(directoryV2BodyLimit + 1) },
              }),
            )
          : undefined;
      await expect(read()).rejects.toMatchObject({ status });
    },
  );
  it.each([
    null,
    {},
    { results: [] },
    { results: [true, true] },
    { results: ["true"] },
    { results: [true], extra: true },
  ])("rejects malformed single-route preflight %#", async (value) => {
    override = (url, init) =>
      url === "/api/access/check" &&
      JSON.parse(String(init.body)).paths.length === 1
        ? response(value)
        : undefined;
    await expect(read()).rejects.toMatchObject({ code: "invalid_response" });
    expect(calls(referencesPath)).toHaveLength(0);
  });
});
