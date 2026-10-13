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
      return response({ results: [true, true, true, true] });
    if (path === "/api/domain-selection")
      return response({
        List: [choice],
        page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
        exhausted: true,
      });
    if (path === "/api/domain-selection/resolve")
      return response({ selection: choice, checkedAt: "2026-10-10T00:00:00Z" });
    if (path === listPath) return response(page(url));
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
