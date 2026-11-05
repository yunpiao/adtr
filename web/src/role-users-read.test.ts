import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, type Profile } from "./api";
import type { AccessUser, UserList } from "./access-api";
import {
  parseRoleUsers,
  readRoleUsers,
  type RoleUsersQuery,
} from "./role-users-read";

const profile: Profile = {
  ID: 1,
  username: "actor",
  role: "platform_admin",
  priv: 1,
  csrfToken: "synthetic-session",
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: true,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 0,
  pwdUpdateTm: "2026-01-01T00:00:00Z",
};
const user: AccessUser = {
  ID: 2,
  username: "alice",
  role: "viewer",
  roleID: "viewer",
  roleName: "Viewer",
  priv: 3,
  passStrength: "high",
  mobile: "13800138000",
  email: "alice@example.test",
  remark: "",
  createTm: "2026-01-01T00:00:00Z",
  hasMfa: false,
  avatar: "",
  pwdUpdateTm: "2026-01-01T00:00:00Z",
  address: "",
  realName: "",
  department: "",
  post: "",
  disabled: false,
};
const query: RoleUsersQuery = {
  roleID: "viewer",
  pageIdx: 1,
  pageSize: 20,
  search: "",
};
const list = (): UserList => ({
  page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
  List: [{ ...user }],
  exhausted: true,
});
const response = (value: unknown, status = 200) =>
  ({
    ok: status >= 200 && status < 300,
    status,
    json: async () => value,
  }) as Response;
beforeEach(() => vi.unstubAllGlobals());

describe("fixed-role user response validation", () => {
  it("accepts the frozen envelope and verifies current-role/privilege semantics", () => {
    expect(parseRoleUsers(list(), query)).toEqual(list());
    const custom = list();
    custom.List[0] = {
      ...user,
      roleID: "a".repeat(24),
      roleName: "Readers",
      priv: 2,
    };
    expect(
      parseRoleUsers(custom, { ...query, roleID: "a".repeat(24) }),
    ).toEqual(custom);
    const admin = list();
    admin.List[0] = {
      ...user,
      roleID: "platform_admin",
      role: "platform_admin",
      roleName: "Admin",
      priv: 1,
    };
    expect(
      parseRoleUsers(admin, { ...query, roleID: "platform_admin" }),
    ).toEqual(admin);
  });
  it("accepts zero matches and a now-out-of-range page without asserting role existence", () => {
    const empty = {
      page: { pageIdx: 3, pageSize: 10, total: 0, totalPage: 0 },
      List: [],
      exhausted: true,
    };
    expect(
      parseRoleUsers(empty, { ...query, pageIdx: 3, pageSize: 10 }),
    ).toEqual(empty);
  });
  it.each([
    [
      "different role",
      (v: UserList) => {
        v.List[0].roleID = "platform_admin";
      },
    ],
    [
      "invalid ID",
      (v: UserList) => {
        v.List[0].ID = 0;
      },
    ],
    [
      "unsafe ID",
      (v: UserList) => {
        v.List[0].ID = Number.MAX_SAFE_INTEGER + 1;
      },
    ],
    [
      "fraction ID",
      (v: UserList) => {
        v.List[0].ID = 1.5;
      },
    ],
    [
      "wrong boolean",
      (v: UserList) => {
        Object.assign(v.List[0], { hasMfa: "false" });
      },
    ],
    [
      "missing field",
      (v: UserList) => {
        delete (v.List[0] as Partial<AccessUser>).email;
      },
    ],
    [
      "unknown row field",
      (v: UserList) => {
        Object.assign(v.List[0], { secret: "unexpected" });
      },
    ],
    [
      "invalid date",
      (v: UserList) => {
        v.List[0].createTm = "not-a-date";
      },
    ],
    [
      "invalid username",
      (v: UserList) => {
        v.List[0].username = "%_";
      },
    ],
    [
      "invalid enum",
      (v: UserList) => {
        v.List[0].passStrength = "unknown";
      },
    ],
    [
      "inconsistent privilege",
      (v: UserList) => {
        v.List[0].priv = 1;
      },
    ],
    [
      "wrong page",
      (v: UserList) => {
        v.page.pageIdx = 2;
      },
    ],
    [
      "wrong size",
      (v: UserList) => {
        v.page.pageSize = 10;
      },
    ],
    [
      "negative total",
      (v: UserList) => {
        v.page.total = -1;
      },
    ],
    [
      "wrong total pages",
      (v: UserList) => {
        v.page.totalPage = 2;
      },
    ],
    [
      "wrong exhaustion",
      (v: UserList) => {
        v.exhausted = false;
      },
    ],
    [
      "count-row inconsistency",
      (v: UserList) => {
        v.page.total = 2;
      },
    ],
    [
      "duplicate IDs",
      (v: UserList) => {
        v.List.push({ ...user, username: "bob" });
        v.page.total = 2;
      },
    ],
    [
      "duplicate usernames",
      (v: UserList) => {
        v.List.push({ ...user, ID: 3 });
        v.page.total = 2;
      },
    ],
    [
      "null rows",
      (v: UserList) => {
        Object.assign(v, { List: null });
      },
    ],
    [
      "unknown envelope field",
      (v: UserList) => {
        Object.assign(v, { extra: true });
      },
    ],
  ] as const)("rejects %s instead of showing partial data", (_, change) => {
    const value = list();
    change(value);
    expect(() => parseRoleUsers(value, query)).toThrow("invalid_response");
  });
  it("leaves PostgreSQL case folding authoritative instead of re-filtering rows locally", () => {
    expect(
      parseRoleUsers(list(), { ...query, search: "ALI" }).List,
    ).toHaveLength(1);
    expect(parseRoleUsers(list(), { ...query, search: "İ" }).List).toHaveLength(
      1,
    );
  });
  it.each([
    { roleID: "" },
    { roleID: "other-role" },
    { pageIdx: 0 },
    { pageIdx: 1_000_001 },
    { pageSize: -1 },
    { pageSize: 100 },
    { pageSize: 11 },
    { search: "😀".repeat(51) },
    { search: "a\nb" },
    { search: "a\u0085b" },
    { search: "\ud800" },
  ])("rejects invalid fixed queries %j before fetch", async (change) => {
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    await expect(
      readRoleUsers(
        { ...query, ...change },
        profile,
        new AbortController().signal,
      ),
    ).rejects.toThrow("invalid_input");
    expect(fetcher).not.toHaveBeenCalled();
  });
});

describe("read-only session-bound transport", () => {
  it("brackets a fixed-role GET with actual identity reads, without POST, proof or persistence", async () => {
    const fetcher = vi.fn(async (url: string) =>
      response(url === "/api/auth/me" ? profile : list()),
    );
    vi.stubGlobal("fetch", fetcher);
    const signal = new AbortController().signal;
    await expect(readRoleUsers(query, profile, signal)).resolves.toEqual(
      list(),
    );
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual([
      "/api/auth/me",
      "/api/access/users?roleID=viewer&pageIdx=1&pageSize=20&sort=-1",
      "/api/auth/me",
    ]);
    for (const [, init] of fetcher.mock.calls as unknown as [
      string,
      RequestInit,
    ][])
      expect(init).toEqual({
        method: "GET",
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
        signal,
      });
    expect(localStorage.length + sessionStorage.length).toBe(0);
  });
  it.each([
    "ID",
    "username",
    "role",
    "priv",
    "csrfToken",
    "needChangePwd",
    "isExpired",
  ])("rejects changed %s before querying the old target", async (key) => {
    const fetcher = vi.fn(async () =>
      response({ ...profile, [key]: "changed" }),
    );
    vi.stubGlobal("fetch", fetcher);
    await expect(
      readRoleUsers(query, profile, new AbortController().signal),
    ).rejects.toMatchObject({ status: 401 });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it("rejects an intervening cookie/session switch after users GET", async () => {
    let sessions = 0;
    const fetcher = vi.fn(async (url: string) =>
      response(
        url === "/api/auth/me"
          ? {
              ...profile,
              csrfToken: ++sessions === 1 ? profile.csrfToken : "new-session",
            }
          : list(),
      ),
    );
    vi.stubGlobal("fetch", fetcher);
    await expect(
      readRoleUsers(query, profile, new AbortController().signal),
    ).rejects.toMatchObject({ status: 401 });
    expect(fetcher).toHaveBeenCalledTimes(3);
  });
  it("snapshots the opened query and expected session before awaiting caller-owned objects", async () => {
    const mutableProfile = { ...profile },
      mutableQuery = { ...query };
    const fetcher = vi.fn(async (url: string) => {
      if (url === "/api/auth/me") return response({ ...mutableProfile });
      mutableProfile.csrfToken = "mutated-session";
      mutableQuery.roleID = "platform_admin";
      return response(list());
    });
    vi.stubGlobal("fetch", fetcher);
    await expect(
      readRoleUsers(mutableQuery, mutableProfile, new AbortController().signal),
    ).rejects.toMatchObject({ status: 401 });
    expect(fetcher.mock.calls[1][0]).toContain("roleID=viewer");
  });
  it.each([401, 403])(
    "honors status %i even with malformed JSON",
    async (status) => {
      vi.stubGlobal(
        "fetch",
        vi.fn(
          async () =>
            ({
              ok: false,
              status,
              json: async () => {
                throw new Error("malformed");
              },
            }) as unknown as Response,
        ),
      );
      await expect(
        readRoleUsers(query, profile, new AbortController().signal),
      ).rejects.toMatchObject({ status });
    },
  );
  it("preserves password-change-required for session invalidation", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => response({ error: "password_change_required" }, 403)),
    );
    await expect(
      readRoleUsers(query, profile, new AbortController().signal),
    ).rejects.toEqual(new ApiError("password_change_required", 403));
  });
  it("discards a late JSON body after abort and starts no further reads", async () => {
    let resolve!: (value: unknown) => void;
    const fetcher = vi.fn(async (url: string) =>
      url === "/api/auth/me"
        ? response(profile)
        : ({
            ok: true,
            status: 200,
            json: () =>
              new Promise((r) => {
                resolve = r;
              }),
          } as Response),
    );
    vi.stubGlobal("fetch", fetcher);
    const controller = new AbortController(),
      result = readRoleUsers(query, profile, controller.signal);
    await vi.waitFor(() => expect(resolve).toBeTypeOf("function"));
    controller.abort();
    resolve(list());
    await expect(result).rejects.toThrow();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});
