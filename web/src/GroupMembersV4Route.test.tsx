// Synthetic App/DOM routing evidence only; not browser, PostgreSQL or AD proof.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { Profile } from "./api";
import type { SourceChoice } from "./domain-selection-api";
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
  samAccountName: `group-${index}`,
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
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
beforeEach(() => {
  window.history.replaceState({}, "", "#account");
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
  window.history.replaceState({}, "", "#account");
});
const entry = "组原始成员 · 字典 4";
const title = "组成员原始观测 · 字典 4";
async function selectSource() {
  fireEvent.click(
    await screen.findByRole("radio", { name: `选择数据源 ${choice.domain}` }),
  );
  click("核对并打开连接详情");
  await screen.findByText("group-1");
}
async function openApp() {
  window.history.replaceState({}, "", "#group-members-v4");
  render(<App />);
  await selectSource();
}
async function expand() {
  fireEvent.click(screen.getAllByRole("button", { name: "查看原始成员" })[0]);
  await screen.findByRole("region", { name: "组原始成员详情" });
}
function expectPrivateCleared() {
  expect(screen.queryByText("group-1")).toBeNull();
  expect(screen.queryByRole("region", { name: "组原始成员详情" })).toBeNull();
}

describe("dictionary4 raw membership application route", () => {
  it("has distinct v3/v4 navigation, exact current markers and no automatic data reads", async () => {
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    click(entry);
    await screen.findByRole("radio");
    expect(window.location.hash).toBe("#group-members-v4");
    expect(screen.getByRole("button", { name: entry })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(screen.getByRole("button", { name: "组资产" })).not.toHaveAttribute(
      "aria-current",
    );
    expect(calls(listPath)).toHaveLength(0);
    click("组资产");
    await screen.findByRole("heading", { name: "组资产" });
    expect(window.location.hash).toBe("#group-assets-v3");
    expect(screen.getByRole("button", { name: "组资产" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(screen.getByRole("button", { name: entry })).not.toHaveAttribute(
      "aria-current",
    );
    expect(screen.queryByRole("heading", { name: title })).toBeNull();
    expect(calls(listPath)).toHaveLength(0);
  });

  it.each(["menu", "operation"])(
    "visible navigation cannot bypass denied %s permission",
    async (denial) => {
      override = (url) => {
        if (denial === "menu" && url === "/api/access/menu")
          return response({ menu: [] });
        if (denial === "operation" && url === "/api/access/check")
          return response({ results: [true, false, true, true] });
      };
      render(<App />);
      click((await screen.findByRole("button", { name: entry })).textContent!);
      await screen.findByText(/当前读取权限已不可用/);
      expect(screen.queryByRole("radio")).toBeNull();
      expect(calls("/api/domain-selection")).toHaveLength(0);
      expect(calls(listPath)).toHaveLength(0);
      expect(calls(detailPath)).toHaveLength(0);
    },
  );

  it("direct hash entry requires fresh authorization before showing sources", async () => {
    const held = deferred();
    override = (url) =>
      url === "/api/access/check" ? held.promise : undefined;
    window.history.replaceState({}, "", "#group-members-v4");
    render(<App />);
    await waitFor(() => expect(calls("/api/access/check")).toHaveLength(1));
    expect(screen.queryByRole("radio")).toBeNull();
    expect(calls("/api/domain-selection")).toHaveLength(0);
    await act(async () =>
      held.resolve(await response({ results: [false, false, false, false] })),
    );
    await screen.findByText(/当前读取权限已不可用/);
    expect(calls(listPath)).toHaveLength(0);
  });

  it("navigation unmounts private state and rejects a late resolved detail", async () => {
    await openApp();
    const held = deferred();
    override = (url) => (url.startsWith(detailPath) ? held.promise : undefined);
    fireEvent.click(screen.getAllByRole("button", { name: "查看原始成员" })[0]);
    await waitFor(() => expect(calls(detailPath)).toHaveLength(1));
    const signal = calls(detailPath)[0][1]?.signal;
    click("组资产");
    await screen.findByRole("heading", { name: "组资产" });
    expect(signal?.aborted).toBe(true);
    await act(async () => held.resolve(await response(detail())));
    click(entry);
    await screen.findByRole("radio");
    expectPrivateCleared();
    expect(calls(listPath)).toHaveLength(1);
  });

  it("native history Back/Forward clears source and detail and authorizes each reentry", async () => {
    render(<App />);
    click((await screen.findByRole("button", { name: entry })).textContent!);
    await selectSource();
    await expand();
    const before = calls("/api/access/check").length;
    await act(async () => window.history.back());
    await screen.findByRole("heading", { name: "账户概览" });
    expectPrivateCleared();
    await act(async () => window.history.forward());
    await screen.findByRole("radio");
    expect(window.location.hash).toBe("#group-members-v4");
    expectPrivateCleared();
    expect(calls("/api/access/check").length).toBeGreaterThan(before);
    expect(calls(listPath)).toHaveLength(1);
    expect(calls(detailPath)).toHaveLength(1);
  });

  it.each(["focus", "visibility", "storage"])(
    "%s actor change clears protected state before any new source choice",
    async (channel) => {
      await openApp();
      await expand();
      override = (url) =>
        url === "/api/auth/me"
          ? response({
              ...profile,
              ID: 2,
              username: "new-reader",
              csrfToken: "new-session",
            })
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
      expectPrivateCleared();
      expect(
        screen.getByText("new-reader", { selector: "strong" }),
      ).toBeVisible();
      expect(calls(listPath)).toHaveLength(1);
    },
  );

  it("same actor permission revocation hides facts and prevents further reads", async () => {
    await openApp();
    await expand();
    override = (url) =>
      url === "/api/access/check"
        ? response({ results: [true, false, true, true] })
        : undefined;
    await act(async () => window.dispatchEvent(new Event("focus")));
    await screen.findByText(/当前读取权限已不可用/);
    expectPrivateCleared();
    expect(screen.queryByRole("radio")).toBeNull();
    expect(calls(listPath)).toHaveLength(1);
    expect(calls(detailPath)).toHaveLength(1);
  });

  it("expired session returns to login and drops raw members", async () => {
    await openApp();
    await expand();
    override = (url) =>
      url === "/api/auth/me"
        ? response({ error: "unauthenticated" }, 401)
        : undefined;
    await act(async () => window.dispatchEvent(new Event("focus")));
    await screen.findByRole("heading", { name: "安全登录" });
    expectPrivateCleared();
    expect(screen.queryByRole("button", { name: entry })).toBeNull();
  });

  it("repeated current-route navigation discards source and detail rather than retaining a private draft", async () => {
    await openApp();
    await expand();
    const before = calls("/api/access/check").length;
    click(entry);
    await screen.findByRole("radio");
    expectPrivateCleared();
    expect(calls("/api/access/check").length).toBeGreaterThan(before);
    expect(calls(listPath)).toHaveLength(1);
  });

  it.each([
    "#group-members-v4/untrusted",
    "#group-members-v4?observationId=old",
  ])("rejects non-exact membership hash %s", async (hash) => {
    window.history.replaceState({}, "", hash);
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    expect(screen.queryByRole("heading", { name: title })).toBeNull();
    expect(calls("/api/access/check")).toHaveLength(0);
    expect(calls(listPath)).toHaveLength(0);
  });

  it("v4 unavailable responses never read v3 fallback endpoints", async () => {
    override = (url) =>
      url.startsWith(listPath)
        ? response({ error: "not_found" }, 404)
        : undefined;
    window.history.replaceState({}, "", "#group-members-v4");
    render(<App />);
    fireEvent.click(await screen.findByRole("radio"));
    click("核对并打开连接详情");
    await screen.findByText(/该对象或数据源在本次读取中不可用/);
    expectPrivateCleared();
    expect(fetcher.mock.calls.some(([url]) => /\/v3\//.test(url))).toBe(false);
    expect(calls(listPath)).toHaveLength(1);
  });
});
