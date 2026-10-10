import { revealNavigation } from "./test-navigation";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import AccessUsers from "./AccessUsers";
import AccessRoles, { AccessPermissions } from "./AccessRoles";
import {
  accessRequest,
  labels,
  marks,
  operations,
  permissionInputs,
  queryString,
  validText,
  validUserFields,
  type AccessUser,
  type Permission,
  type Role,
} from "./access-api";
import { type AccessContext } from "./access-common";
import { type Profile } from "./api";

const profile: Profile = {
  ID: 1,
  username: "admin",
  role: "platform_admin",
  priv: 1,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: true,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 0,
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "test-csrf",
};
const user: AccessUser = {
  ID: 2,
  username: "alice",
  passStrength: "high",
  role: "viewer",
  priv: 3,
  mobile: "13800138000",
  email: "alice@example.test",
  remark: "Synthetic note",
  createTm: "2026-10-01T00:00:00Z",
  hasMfa: false,
  avatar: "",
  pwdUpdateTm: "2026-10-02T00:00:00Z",
  address: "Synthetic city",
  realName: "Alice",
  department: "Research",
  post: "Analyst",
  roleID: "viewer",
  roleName: "Viewer",
  disabled: false,
};
const roles: Role[] = [
  {
    id: "viewer",
    name: "Viewer",
    userNum: 2,
    remark: "",
    allowDelete: false,
    allowEdit: false,
    created: user.createTm,
    dataSrc: {},
  },
  {
    id: "role-custom",
    name: "Custom",
    userNum: 0,
    remark: "test role",
    allowDelete: true,
    allowEdit: true,
    created: user.createTm,
    dataSrc: {},
  },
];
const metadata = (enabled = true): Permission[] =>
  marks.map((mark) => ({
    mark,
    name: labels[mark],
    children: [],
    paths: [
      { name: labels[mark], url: `/api/access/${mark}`, auth: "readable" },
    ],
    checked: enabled,
    auth: { readable: enabled, writeable: enabled },
    allow_auth: { readable: true, writeable: true },
    icon: mark,
  }));
const list = (users = [user]) => ({
  page: { pageIdx: 1, pageSize: 20, total: users.length, totalPage: 1 },
  List: users,
  exhausted: true,
});
const roleList = {
  page: { pageIdx: 1, pageSize: 20, total: roles.length, totalPage: 1 },
  list: roles,
  exhausted: true,
};
const response = (body: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response);
let fetcher: ReturnType<typeof vi.fn>;
let override: (url: string, init: RequestInit) => Promise<Response> | undefined;
let context: AccessContext;
beforeEach(() => {
  window.history.replaceState({}, "", "#account");
  override = () => undefined;
  context = {
    profile,
    can: () => true,
    menu: metadata(),
    sessionChanged: vi.fn(),
  };
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const custom = override(url, init);
    if (custom) return custom;
    const path = url.split("?")[0];
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/menu") return response({ menu: metadata() });
    if (path === "/api/access/check")
      return response({ results: operations.map(() => true) });
    if (path === "/api/access/users") return response(list());
    if (path === "/api/access/roles") return response(roleList);
    if (path === "/api/access/roles/detail")
      return response({ role: roles[1], permissions: metadata(false) });
    if (path === "/api/access/permissions")
      return response({ permissions: metadata(false) });
    if (path === "/api/access/users/exists") return response({ result: false });
    if (path === "/api/access/roles/exists") return response({ exists: false });
    return response({ result: "SUCCESS", sessionRevoked: false });
  });
  vi.stubGlobal("fetch", fetcher);
});
const click = async (name: string) => {
  await revealNavigation(name);
  return userEvent.click(screen.getByRole("button", { name }));
};
const fill = async (name: string, value: string) => {
  const input = screen.getByLabelText(name, { exact: true });
  await userEvent.clear(input);
  if (value) await userEvent.type(input, value);
};
const proof = async () => {
  await fill("操作者当前密码", "Actor Password 123");
  await fill("未使用的认证器验证码", "123456");
};
const calls = (path: string) =>
  fetcher.mock.calls.filter(
    ([url]) => url.split("?")[0] === `/api/access${path}`,
  );
const body = (path: string) =>
  JSON.parse(calls(path).at(-1)![1].body as string);
async function startUsers() {
  render(<AccessUsers context={context} roles={roles} />);
  await screen.findByRole("table");
}
async function startApp() {
  render(<App />);
  await screen.findByRole("heading", { name: "账户概览" });
  await click("访问管理");
  await screen.findByRole("table");
}

describe("access transport and validation", () => {
  it("uses the existing cookie/CSRF contract without exposing actor identity or setting Origin", async () => {
    const controller = new AbortController();
    await accessRequest(
      "/users/delete",
      controller.signal,
      {
        username: "alice",
        actorPassword: "Actor Password 123",
        totpCode: "123456",
      },
      profile.csrfToken,
    );
    expect(calls("/users/delete")[0][1]).toEqual(
      expect.objectContaining({
        method: "POST",
        credentials: "same-origin",
        cache: "no-store",
        signal: controller.signal,
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": "test-csrf",
        },
      }),
    );
    expect(localStorage.length + sessionStorage.length).toBe(0);
  });
  it("serializes repeated filters and strips server metadata from submitted permissions", () => {
    const query = new URLSearchParams(
      queryString({
        filterRole: ["role-a", "role-b"],
        isSelf: false,
        search: "%_",
        pageSize: -1,
        absent: undefined,
      }),
    );
    expect(query.getAll("filterRole")).toEqual(["role-a", "role-b"]);
    expect(query.get("search")).toBe("%_");
    expect(query.has("absent")).toBe(false);
    expect(permissionInputs(metadata())).toEqual(
      marks.map((mark) => ({
        mark,
        auth: { readable: true, writeable: true },
      })),
    );
  });
  it("validates Unicode profile bounds, phone, email bytes and control characters", () => {
    expect(validText("😀".repeat(50), 50)).toBe(true);
    expect(validText("😀".repeat(51), 50)).toBe(false);
    expect(validText("a\nb", 50)).toBe(false);
    expect(
      validUserFields({
        mobile: "13800138000",
        email: "a@example.test",
        remark: "😀".repeat(150),
      }),
    ).toBe(true);
    for (const values of [
      { mobile: "12800138000" },
      { mobile: "138001380000" },
      { email: "A <a@example.test>" },
      { email: `${"a".repeat(250)}@b.test` },
      { department: "x".repeat(51) },
      { remark: "x".repeat(151) },
    ])
      expect(validUserFields(values)).toBe(false);
  });
});
describe("server-driven navigation and sessions", () => {
  it("does not call management APIs until explicitly opened", async () => {
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual(["/api/auth/me"]);
    await click("访问管理");
    await screen.findByRole("table");
    expect(body("/check")).toEqual({ paths: operations });
  });
  it("requires both a readable menu and checked operation, hiding denied actions", async () => {
    override = (url) =>
      url.endsWith("/menu")
        ? response({ menu: [metadata()[0]] })
        : url.endsWith("/check")
          ? response({
              results: operations.map((op) => op === "GET /api/access/users"),
            })
          : undefined;
    await startApp();
    expect(
      screen.queryByRole("button", { name: "角色管理" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "功能权限" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "新增用户" }),
    ).not.toBeInTheDocument();
    expect(calls("/roles")).toHaveLength(0);
  });
  it("fails closed when positional check response is malformed", async () => {
    override = (url) =>
      url.endsWith("/check") ? response({ results: [true] }) : undefined;
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    await click("访问管理");
    expect(await screen.findByRole("alert")).toHaveTextContent("无法识别");
    expect(calls("/users")).toHaveLength(0);
  });
  it("shows no management functions for a viewer", async () => {
    override = (url) =>
      url.endsWith("/menu")
        ? response({ menu: [] })
        : url.endsWith("/check")
          ? response({ results: operations.map(() => false) })
          : undefined;
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    await click("访问管理");
    expect(
      await screen.findByText("当前账户没有访问管理功能权限。"),
    ).toBeInTheDocument();
  });
  it("reloads auth identity when the caller session is revoked", async () => {
    await startApp();
    await click("删除 alice");
    await proof();
    override = (url) =>
      url.endsWith("/users/delete")
        ? response({ result: "SUCCESS", sessionRevoked: true })
        : url.endsWith("/auth/me")
          ? response({ error: "unauthenticated" }, 401)
          : undefined;
    await click("确认删除用户");
    expect(
      await screen.findByRole("heading", { name: "登录账户" }),
    ).toBeInTheDocument();
  });
  it("leaving a pending management write rechecks identity and discards the late response", async () => {
    await startApp();
    await click("删除 alice");
    await proof();
    let resolve!: (value: Response) => void;
    override = (url) =>
      url.endsWith("/users/delete")
        ? new Promise((r) => {
            resolve = r;
          })
        : url.endsWith("/auth/me")
          ? response({ error: "unauthenticated" }, 401)
          : undefined;
    await click("确认删除用户");
    const pendingSignal = calls("/users/delete")[0][1].signal as AbortSignal;
    await click("账户概览");
    expect(
      await screen.findByRole("heading", { name: "登录账户" }),
    ).toBeInTheDocument();
    expect(pendingSignal.aborted).toBe(true);
    await act(async () =>
      resolve(await response({ result: "SUCCESS", sessionRevoked: true })),
    );
    expect(screen.queryByText("用户已删除")).not.toBeInTheDocument();
  });
  it("Back destroys the privileged form and reloads authoritative identity", async () => {
    await startApp();
    await click("新增用户");
    await proof();
    const sessionReads = () =>
      fetcher.mock.calls.filter(([url]) => url === "/api/auth/me").length;
    const readsBefore = sessionReads();
    act(() => window.history.back());
    await waitFor(() => expect(window.location.hash).toBe("#access"));
    expect(
      await screen.findByRole("heading", { name: "访问管理" }),
    ).toBeInTheDocument();
    expect(sessionReads()).toBeGreaterThan(readsBefore);
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
  });
});
describe("users and assignment workflows", () => {
  it("displays persisted profile fields and sends complete filter and paging semantics", async () => {
    await startUsers();
    await userEvent.click(
      screen.getByText("alice@example.test", { selector: "summary" }),
    );
    expect(screen.getByText("Synthetic city")).toBeInTheDocument();
    expect(screen.getByText("Research")).toBeInTheDocument();
    await fill("搜索用户名", "%_");
    await userEvent.click(screen.getByText("更多筛选条件"));
    await fill("筛选角色 ID", "viewer,role-custom");
    await fill("分配列表角色 ID", "viewer");
    await userEvent.click(screen.getByLabelText("仅当前账户"));
    await userEvent.click(screen.getByLabelText("已开启", { exact: true }));
    await userEvent.click(screen.getByLabelText("已关闭", { exact: true }));
    await userEvent.click(screen.getByLabelText("高", { exact: true }));
    fireEvent.change(screen.getByLabelText("创建开始时间", { exact: true }), {
      target: { value: "2026-10-01T00:00" },
    });
    fireEvent.change(screen.getByLabelText("创建结束时间", { exact: true }), {
      target: { value: "2026-10-05T00:00" },
    });
    fireEvent.change(
      screen.getByLabelText("密码更新开始时间", { exact: true }),
      { target: { value: "2026-10-01T00:00" } },
    );
    fireEvent.change(
      screen.getByLabelText("密码更新结束时间", { exact: true }),
      { target: { value: "2026-10-05T00:00" } },
    );
    await userEvent.selectOptions(
      screen.getByLabelText("每页条数", { exact: true }),
      "-1",
    );
    await userEvent.selectOptions(
      screen.getByLabelText("排序", { exact: true }),
      "-2",
    );
    await click("应用筛选");
    await screen.findByRole("table");
    const query = new URL(calls("/users").at(-1)![0], "https://test.invalid")
      .searchParams;
    expect(query.getAll("filterRole")).toEqual(["viewer", "role-custom"]);
    expect(query.getAll("filterMfaStatus")).toEqual(["enable", "stop"]);
    expect(query.getAll("filterPassStrength")).toEqual(["high"]);
    expect(query.get("pageSize")).toBe("-1");
    expect(query.get("sort")).toBe("-2");
    expect(query.get("isSelf")).toBe("true");
    expect(query.get("search")).toBe("%_");
    expect(query.get("roleID")).toBe("viewer");
    for (const key of [
      "filterStartCreateTm",
      "filterEndCreateTm",
      "filterStartPassTm",
      "filterEndPassTm",
    ])
      expect(query.get(key)).toMatch(/Z$/);
  });
  it("uses server paging and does not mix older filter responses into the current list", async () => {
    let resolveOld!: (value: Response) => void;
    let requests = 0;
    override = (url) => {
      if (!url.startsWith("/api/access/users?")) return;
      requests++;
      if (requests === 1)
        return new Promise((resolve) => {
          resolveOld = resolve;
        });
      return response({
        ...list(),
        page: { pageIdx: 1, pageSize: 20, total: 21, totalPage: 2 },
        exhausted: false,
      });
    };
    render(<AccessUsers context={context} roles={roles} />);
    await fill("搜索用户名", "alice");
    await click("应用筛选");
    await screen.findByRole("table");
    await act(async () =>
      resolveOld(await response(list([{ ...user, username: "stale" }]))),
    );
    expect(screen.queryByText("stale")).not.toBeInTheDocument();
    await click("下一页");
    await waitFor(() =>
      expect(calls("/users").at(-1)![0]).toContain("pageIdx=2"),
    );
  });
  it("creates a user with complete profile, distinct passwords and exactly one pending write", async () => {
    await startUsers();
    await click("新增用户");
    await fill("用户名", "New.User");
    await click("检查用户名");
    await screen.findByText(/用户名当前可用/);
    await fill("初始密码", "Created Password 123");
    await fill("确认初始密码", "Created Password 123");
    await fill("角色", "role-custom");
    for (const [label, value] of [
      ["手机号", "13800138000"],
      ["邮箱", "new@example.test"],
      ["备注", "note"],
      ["所在地", "City"],
      ["真实姓名", "New User"],
      ["部门", "Team"],
      ["岗位", "Tester"],
    ])
      await fill(label, value);
    await proof();
    let resolve!: (value: Response) => void;
    override = (url) =>
      url.endsWith("/users/create")
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    const form = screen
      .getByRole("button", { name: "创建用户" })
      .closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    expect(calls("/users/create")).toHaveLength(1);
    expect(body("/users/create")).toEqual({
      username: "new.user",
      password: "Created Password 123",
      roleID: "role-custom",
      mobile: "13800138000",
      email: "new@example.test",
      remark: "note",
      address: "City",
      realName: "New User",
      department: "Team",
      post: "Tester",
      actorPassword: "Actor Password 123",
      totpCode: "123456",
    });
    await act(async () =>
      resolve(
        await response({ result: "SUCCESS", sessionRevoked: false, ID: 9 }),
      ),
    );
    expect(await screen.findByText("用户已创建")).toBeInTheDocument();
    await screen.findByRole("table");
    expect(calls("/users")).toHaveLength(2);
  });
  it("validates initial password confirmation and Unicode limits before sending", async () => {
    await startUsers();
    await click("新增用户");
    await fill("用户名", "valid");
    await fill("初始密码", "😀".repeat(65));
    await fill("确认初始密码", "😀".repeat(65));
    await proof();
    await click("创建用户");
    expect(await screen.findByRole("alert")).toHaveTextContent("12–64");
    expect(calls("/users/create")).toHaveLength(0);
  });
  it("keeps username immutable, persists cleared fields and distinct disabled status", async () => {
    await startUsers();
    await click("编辑 alice");
    expect(screen.getByLabelText("用户名", { exact: true })).toBeDisabled();
    await fill("邮箱", "");
    await fill("备注", "");
    await userEvent.click(screen.getByLabelText(/停用账户/));
    await proof();
    await click("保存用户");
    await screen.findByText("用户资料已保存");
    expect(body("/users/update")).toMatchObject({
      username: "alice",
      email: "",
      remark: "",
      disabled: true,
      mobile: user.mobile,
      roleID: "viewer",
    });
    expect(body("/users/update")).not.toHaveProperty("password");
  });
  it("omits role changes when a users writer cannot assign roles", async () => {
    context.can = (operation) => operation !== "POST /api/access/assignments";
    await startUsers();
    await click("编辑 alice");
    expect(
      screen.queryByLabelText("角色", { exact: true }),
    ).not.toBeInTheDocument();
    await fill("备注", "Profile-only update");
    await proof();
    await click("保存用户");
    await screen.findByText("用户资料已保存");
    expect(body("/users/update")).not.toHaveProperty("roleID");
    await click("新增用户");
    await fill("用户名", "plain.user");
    await fill("初始密码", "Initial Password 123");
    await fill("确认初始密码", "Initial Password 123");
    expect(
      screen.getByText(/viewer（当前账户没有分配角色权限）/),
    ).toBeInTheDocument();
    await proof();
    await click("创建用户");
    await screen.findByText("用户已创建");
    expect(body("/users/create")).not.toHaveProperty("roleID");
  });
  it("preserves disabled status when a profile editor cannot reactivate the account", async () => {
    context.can = (operation) => operation !== "POST /api/access/assignments";
    override = (url) =>
      url.startsWith("/api/access/users?")
        ? response(list([{ ...user, disabled: true }]))
        : undefined;
    await startUsers();
    await click("编辑 alice");
    const disabled = screen.getByLabelText(/停用账户/);
    expect(disabled).toBeChecked();
    expect(disabled).toBeDisabled();
    expect(
      screen.getByText(/重新启用账户需要角色分配权限/),
    ).toBeInTheDocument();
    await fill("备注", "Update disabled account profile");
    await proof();
    await click("保存用户");
    await screen.findByText("用户资料已保存");
    expect(body("/users/update")).toMatchObject({
      username: "alice",
      disabled: true,
      remark: "Update disabled account profile",
    });
    expect(body("/users/update")).not.toHaveProperty("roleID");
  });
  it("shows server denial when the actor cannot delegate a reactivated account's role", async () => {
    override = (url) =>
      url.startsWith("/api/access/users?")
        ? response(list([{ ...user, disabled: true }]))
        : url.endsWith("/users/update")
          ? response({ error: "forbidden" }, 403)
          : undefined;
    await startUsers();
    await click("编辑 alice");
    await userEvent.click(screen.getByLabelText(/停用账户/));
    await proof();
    await click("保存用户");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "没有执行此操作的权限",
    );
    expect(body("/users/update")).toMatchObject({ disabled: false });
    expect(screen.queryByText("用户资料已保存")).not.toBeInTheDocument();
    expect(calls("/users/update")).toHaveLength(1);
  });
  it("supports cancellation without mutation and ignores a late cancelled mutation response", async () => {
    await startUsers();
    await click("删除 alice");
    await click("取消");
    await screen.findByRole("table");
    expect(calls("/users/delete")).toHaveLength(0);
    await click("删除 alice");
    await proof();
    let resolve!: (value: Response) => void;
    override = (url) =>
      url.endsWith("/users/delete")
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    await click("确认删除用户");
    const request = calls("/users/delete")[0][1];
    await click("取消");
    await screen.findByRole("table");
    expect((request.signal as AbortSignal).aborted).toBe(true);
    await act(async () =>
      resolve(await response({ result: "SUCCESS", sessionRevoked: false })),
    );
    expect(screen.queryByText("用户已删除")).not.toBeInTheDocument();
  });
  it("submits distinct-user bulk assignments atomically and supports a single assignment", async () => {
    override = (url) =>
      url.startsWith("/api/access/users?")
        ? response(list([user, { ...user, ID: 3, username: "bob" }]))
        : undefined;
    await startUsers();
    await userEvent.click(screen.getByLabelText("选择 alice"));
    await userEvent.click(screen.getByLabelText("选择 bob"));
    await click("批量分配（2）");
    await fill("角色：alice", "role-custom");
    await fill("角色：bob", "viewer");
    await proof();
    await click("保存角色分配");
    await screen.findByRole("table");
    expect(body("/assignments")).toEqual({
      userRoles: [
        { username: "alice", roleID: "role-custom" },
        { username: "bob", roleID: "viewer" },
      ],
      actorPassword: "Actor Password 123",
      totpCode: "123456",
    });
    await click("分配 alice");
    await fill("角色：alice", "role-custom");
    await proof();
    await click("保存角色分配");
    await screen.findByRole("table");
    expect(body("/assignments").userRoles).toEqual([
      { username: "alice", roleID: "role-custom" },
    ]);
  });
  it("surfaces replay and uncertain-network failures without retrying writes", async () => {
    await startUsers();
    await click("删除 alice");
    await proof();
    override = (url) =>
      url.endsWith("/users/delete")
        ? response({ error: "invalid_credentials" }, 401)
        : undefined;
    await click("确认删除用户");
    expect(await screen.findByRole("alert")).toHaveTextContent("下一个未使用");
    expect(context.sessionChanged).not.toHaveBeenCalled();
    override = (url) =>
      url.endsWith("/users/delete")
        ? Promise.reject(new TypeError("offline"))
        : undefined;
    await click("确认删除用户");
    expect(await screen.findByRole("alert")).toHaveTextContent("结果尚未确认");
    expect(calls("/users/delete")).toHaveLength(2);
  });
});
describe("roles and explicit function permissions", () => {
  it("creates a role, automatically pairs write with read, and sends only grant input", async () => {
    render(<AccessRoles context={context} />);
    await screen.findByRole("table");
    await click("新增角色");
    await fill("角色名称", "Investigators");
    await click("检查角色名称");
    await screen.findByText(/角色名称当前可用/);
    await fill("角色备注", "Read and write users");
    await userEvent.click(screen.getByLabelText("用户管理：写入"));
    expect(screen.getByLabelText("用户管理：读取")).toBeChecked();
    await proof();
    await click("保存角色");
    await screen.findByText("角色已保存");
    expect(body("/roles/save")).toEqual({
      roleName: "Investigators",
      remark: "Read and write users",
      permissions: [
        { mark: "users", auth: { readable: true, writeable: true } },
        { mark: "roles", auth: { readable: false, writeable: false } },
        { mark: "permissions", auth: { readable: false, writeable: false } },
        { mark: "tasks", auth: { readable: false, writeable: false } },
        { mark: "audit", auth: { readable: false, writeable: false } },
        { mark: "audit_exports", auth: { readable: false, writeable: false } },
        { mark: "system", auth: { readable: false, writeable: false } },
        { mark: "schedules", auth: { readable: false, writeable: false } },
        { mark: "task_archive", auth: { readable: false, writeable: false } },
        { mark: "domains", auth: { readable: false, writeable: false } },
        {
          mark: "operation_accounts",
          auth: { readable: false, writeable: false },
        },
        { mark: "system_logs", auth: { readable: false, writeable: false } },
        {
          mark: "directory_assets",
          auth: { readable: false, writeable: false },
        },
      ],
      actorPassword: "Actor Password 123",
      totpCode: "123456",
    });
  });
  it("constrains new grants by actual actor menu grants, not registry allow_auth", async () => {
    context.menu = metadata().map((node) => ({
      ...node,
      auth: {
        readable: node.mark === "roles",
        writeable: node.mark === "roles",
      },
    }));
    render(<AccessRoles context={context} />);
    await screen.findByRole("table");
    await click("新增角色");
    expect(screen.getByLabelText("用户管理：读取")).toBeDisabled();
    expect(screen.getByLabelText("用户管理：写入")).toBeDisabled();
    expect(screen.getByLabelText("角色管理：读取")).toBeEnabled();
  });
  it("reads role detail envelope, preserves immutable name and saves allowed edits", async () => {
    render(<AccessRoles context={context} />);
    await screen.findByRole("table");
    await click("详情 Custom");
    await screen.findByRole("heading", { name: "角色详情：Custom" });
    expect(screen.getByLabelText("角色名称", { exact: true })).toHaveAttribute(
      "readonly",
    );
    await fill("角色备注", "Updated note");
    await proof();
    await click("保存角色");
    await screen.findByText("角色已保存");
    expect(body("/roles/save")).toMatchObject({
      roleID: "role-custom",
      roleName: "Custom",
      remark: "Updated note",
    });
    expect(calls("/roles/detail")[0][0]).toContain("roleID=role-custom");
  });
  it("deletes only a selected allowed role with proof and keeps builtins disabled", async () => {
    render(<AccessRoles context={context} />);
    await screen.findByRole("table");
    expect(screen.getByRole("button", { name: "删除 Viewer" })).toBeDisabled();
    await click("删除 Custom");
    await proof();
    await click("确认删除角色");
    await screen.findByText("角色已删除");
    expect(body("/roles/delete")).toEqual({
      roleID: "role-custom",
      actorPassword: "Actor Password 123",
      totpCode: "123456",
    });
  });
  it("loads and saves explicit permissions without echoing server-owned fields", async () => {
    render(<AccessPermissions context={context} roles={roles} />);
    await fill("权限角色 ID", "role-custom");
    await click("读取权限");
    await screen.findByText("角色权限：role-custom");
    await waitFor(() =>
      expect(screen.getByLabelText("用户管理：读取")).toBeEnabled(),
    );
    await userEvent.click(screen.getByLabelText("用户管理：读取"));
    await proof();
    await click("保存功能权限");
    await screen.findByText("权限已保存，受影响用户的会话已撤销");
    expect(body("/permissions/save")).toMatchObject({
      roleID: "role-custom",
      permissions: emptyExpected(true),
    });
    expect(JSON.stringify(body("/permissions/save"))).not.toMatch(
      /allow_auth|children|paths|checked|icon/,
    );
  });
  it("displays immutable builtin permissions without save controls", async () => {
    render(<AccessPermissions context={context} roles={roles} />);
    await click("读取权限");
    await screen.findByText("角色权限：viewer");
    await waitFor(() =>
      expect(screen.getByLabelText("用户管理：读取")).toBeDisabled(),
    );
    expect(
      screen.queryByRole("button", { name: "保存功能权限" }),
    ).not.toBeInTheDocument();
  });
  it("handles empty and result-too-large states without pretending all-results succeeded", async () => {
    override = (url) =>
      url.startsWith("/api/access/users?") ? response(list([])) : undefined;
    await startUsers();
    expect(screen.getByText("没有符合条件的用户。")).toBeInTheDocument();
    override = (url) =>
      url.startsWith("/api/access/users?")
        ? response({ error: "result_too_large" }, 422)
        : undefined;
    await userEvent.selectOptions(
      screen.getByLabelText("每页条数", { exact: true }),
      "-1",
    );
    await click("应用筛选");
    expect(await screen.findByRole("alert")).toHaveTextContent("超过 1,000 条");
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
});
function emptyExpected(usersReadable: boolean) {
  return marks.map((mark) => ({
    mark,
    auth: { readable: mark === "users" && usersReadable, writeable: false },
  }));
}
