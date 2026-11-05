import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AccessRoles from "./AccessRoles";
import RoleUsersReadPanel from "./RoleUsersReadPanel";
import type { AccessContext } from "./access-common";
import type { AccessUser, Permission, Role } from "./access-api";
import type { Profile } from "./api";
import { SESSION_INVALIDATION_STORAGE } from "./session-invalidation";

const profile: Profile = {
  ID: 1,
  username: "actor",
  role: "platform_admin",
  priv: 1,
  csrfToken: "synthetic-csrf",
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
const role: Role = {
  id: "a".repeat(24),
  name: "Synthetic readers",
  userNum: 99,
  remark: "",
  allowDelete: true,
  allowEdit: true,
  created: "2026-01-01T00:00:00Z",
  dataSrc: {},
};
const otherRole: Role = {
  ...role,
  id: "viewer",
  name: "Viewer",
  userNum: 1,
  allowDelete: false,
  allowEdit: false,
};
const user = (ID: number, target = role): AccessUser => ({
  ID,
  username: `member${String(ID).padStart(2, "0")}`,
  role: "viewer",
  roleID: target.id,
  roleName: target.name,
  priv: target.id === "viewer" ? 3 : 2,
  passStrength: "high",
  mobile: "13800138000",
  email: `member${ID}@example.test`,
  remark: "",
  createTm: "2026-01-01T00:00:00Z",
  hasMfa: ID % 2 === 0,
  avatar: "",
  pwdUpdateTm: "2026-01-01T00:00:00Z",
  address: "",
  realName: "",
  department: "",
  post: "",
  disabled: ID % 2 !== 0,
});
const permissions = (): Permission[] =>
  ["users", "roles", "permissions"].map((mark) => ({
    mark,
    name: mark,
    auth: { readable: true, writeable: true },
    allow_auth: { readable: true, writeable: true },
    children: [],
    paths: [],
    checked: true,
    icon: mark,
  }));
const response = (body: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response);
function page(url: string) {
  const q = new URL(url, "http://localhost").searchParams,
    roleID = q.get("roleID"),
    target = roleID === role.id ? role : otherRole;
  const all = Array.from({ length: target === role ? 23 : 1 }, (_, i) =>
    user(i + 1, target),
  ).filter((u) =>
    u.username.toLowerCase().includes((q.get("search") ?? "").toLowerCase()),
  );
  const pageIdx = Number(q.get("pageIdx")),
    pageSize = Number(q.get("pageSize")),
    offset = (pageIdx - 1) * pageSize,
    rows = all.slice(offset, offset + pageSize);
  return {
    page: {
      pageIdx,
      pageSize,
      total: all.length,
      totalPage: Math.ceil(all.length / pageSize),
    },
    List: rows,
    exhausted: offset + rows.length >= all.length,
  };
}
let context: AccessContext;
let fetcher: ReturnType<typeof vi.fn>;
let override: (url: string, init: RequestInit) => Promise<Response> | undefined;
beforeEach(() => {
  window.history.replaceState({}, "", "#access/roles");
  context = {
    profile: { ...profile },
    menu: permissions(),
    can: () => true,
    sessionChanged: vi.fn(),
  };
  override = () => undefined;
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const custom = override(url, init);
    if (custom) return custom;
    if (url === "/api/auth/me") return response(context.profile);
    if (url.startsWith("/api/access/users?")) return response(page(url));
    if (url.startsWith("/api/access/roles/detail?"))
      return response({ role, permissions: permissions() });
    if (url.startsWith("/api/access/roles?"))
      return response({
        page: { pageIdx: 1, pageSize: 20, total: 2, totalPage: 1 },
        list: [role, otherRole],
        exhausted: true,
      });
    throw new Error(`Unexpected test request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
const userCalls = () =>
  fetcher.mock.calls.filter(([url]) => url.startsWith("/api/access/users?"));
const button = (name: string) => screen.getByRole("button", { name });
const click = (name: string) => fireEvent.click(button(name));
const table = () => screen.findByRole("table", { name: "角色关联用户列表" });
async function openRoles() {
  const result = render(<AccessRoles context={context} />);
  await screen.findByRole("table", { name: "角色列表" });
  click(`查看关联用户 ${role.name}`);
  await table();
  return result;
}
function panel(props: Partial<Parameters<typeof RoleUsersReadPanel>[0]> = {}) {
  return (
    <RoleUsersReadPanel
      context={context}
      role={role}
      close={vi.fn()}
      returnToList={vi.fn()}
      {...props}
    />
  );
}
const setSearch = (value: string) =>
  fireEvent.change(screen.getByLabelText("关联用户名搜索"), {
    target: { value },
  });

describe("fixed-role associated user workflow", () => {
  it("opens from a real list target, displays current facts and distinct count semantics without write controls", async () => {
    await openRoles();
    expect(
      screen.getByRole("heading", { name: `角色关联用户：${role.name}` }),
    ).toBeInTheDocument();
    expect(screen.getByText(/读取时的用户数：99/)).toBeInTheDocument();
    expect(screen.getByText(/共 23 条/)).toBeInTheDocument();
    const row = screen
      .getByRole("rowheader", { name: "member01 ID 1" })
      .closest("tr")!;
    for (const fact of [
      role.name,
      role.id,
      "13800138000",
      "member1@example.test",
      "账户已停用",
      "已关闭",
    ])
      expect(within(row).getByText(fact)).toBeInTheDocument();
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    expect(document.querySelector("section form")).toBeNull();
    expect(
      screen.queryByRole("button", { name: /保存|分配|删除/ }),
    ).not.toBeInTheDocument();
    for (const node of within(
      screen.getByRole("region", { name: "角色关联用户工作区" }),
    ).getAllByRole("button"))
      expect(node).toHaveAttribute("type", "button");
    expect(fetcher.mock.calls.every(([, init]) => init.method === "GET")).toBe(
      true,
    );
    expect(window.location.hash).toBe("#access/roles/users");
    expect(JSON.stringify(window.history.state)).toBe("{}");
    expect(localStorage.length + sessionStorage.length).toBe(0);
  });
  it("paginates on the server and resets search/size to the first page with only the fixed roleID", async () => {
    await openRoles();
    click("下一页");
    await screen.findByRole("rowheader", { name: "member21 ID 21" });
    expect(
      screen.queryByRole("rowheader", { name: "member01 ID 1" }),
    ).not.toBeInTheDocument();
    expect(button("下一页")).toBeDisabled();
    setSearch("MEMBER01");
    fireEvent.change(screen.getByLabelText("关联用户每页条数"), {
      target: { value: "10" },
    });
    click("搜索关联用户");
    await table();
    expect(screen.getByText(/共 1 条/)).toBeInTheDocument();
    expect(
      Object.fromEntries(
        new URL(userCalls().at(-1)![0], "http://localhost").searchParams,
      ),
    ).toEqual({
      roleID: role.id,
      pageIdx: "1",
      pageSize: "10",
      search: "MEMBER01",
      sort: "-1",
    });
    expect(
      [
        ...screen.getByLabelText<HTMLSelectElement>("关联用户每页条数").options,
      ].map((o) => o.value),
    ).toEqual(["10", "20", "30", "40", "50"]);
    click("清除关联用户搜索");
    await table();
    expect(screen.getByLabelText("关联用户名搜索")).toHaveValue("");
    expect(screen.getByLabelText("关联用户每页条数")).toHaveValue("20");
    expect(screen.getByText(/共 23 条/)).toBeInTheDocument();
    for (const [url] of userCalls())
      expect(
        new URL(url, "http://localhost").searchParams.getAll("roleID"),
      ).toEqual([role.id]);
  });
  it("searches % and _ literally and separates an empty result from a failed read", async () => {
    await openRoles();
    setSearch("%_");
    click("搜索关联用户");
    await screen.findByText(/当前页没有符合条件的关联用户/);
    expect(
      new URL(userCalls().at(-1)![0], "http://localhost").searchParams.get(
        "search",
      ),
    ).toBe("%_");
    expect(screen.getByText(/空结果不证明角色仍存在/)).toBeInTheDocument();
    override = (url) =>
      url.startsWith("/api/access/users?")
        ? response({ error: "internal" }, 500)
        : undefined;
    click("刷新关联用户");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "关联用户读取失败",
    );
    expect(
      screen.queryByText(/当前页没有符合条件的关联用户/),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    override = () => undefined;
    click("清除关联用户搜索");
    await table();
  });
  it("enforces 50 Unicode codepoints and permits Enter without submitting a privileged form", async () => {
    await openRoles();
    const calls = userCalls().length;
    setSearch("😀".repeat(51));
    click("搜索关联用户");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "最多 50 个 Unicode 字符",
    );
    expect(userCalls()).toHaveLength(calls);
    setSearch("😀".repeat(50));
    fireEvent.keyDown(screen.getByLabelText("关联用户名搜索"), {
      key: "Enter",
    });
    await table();
    expect(
      new URL(userCalls().at(-1)![0], "http://localhost").searchParams.get(
        "search",
      ),
    ).toBe("😀".repeat(50));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
  it("opens from detail as a sibling view, never submitting save/delete/assignments, and re-reads detail on return", async () => {
    render(<AccessRoles context={context} />);
    await screen.findByRole("table");
    click(`详情 ${role.name}`);
    await screen.findByRole("heading", { name: `角色详情：${role.name}` });
    expect(screen.getByLabelText("操作者当前密码")).toBeInTheDocument();
    const entry = button(`查看关联用户 ${role.name}`);
    expect(entry).toHaveAttribute("type", "button");
    fireEvent.click(entry);
    await table();
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    expect(document.querySelector("form")).toBeNull();
    click("返回角色详情");
    await screen.findByRole("heading", { name: `角色详情：${role.name}` });
    expect(
      fetcher.mock.calls.filter(([url]) =>
        url.startsWith("/api/access/roles/detail?"),
      ).length,
    ).toBe(2);
    expect(fetcher.mock.calls.every(([, init]) => init.method === "GET")).toBe(
      true,
    );
  });
  it("clears search and private rows on close/reopen and explicitly changing targets", async () => {
    await openRoles();
    setSearch("member01");
    click("搜索关联用户");
    await table();
    click("关闭关联用户");
    await screen.findByRole("table", { name: "角色列表" });
    expect(screen.queryByText("member01")).not.toBeInTheDocument();
    click(`查看关联用户 ${otherRole.name}`);
    await table();
    expect(
      screen.getByRole("heading", { name: `角色关联用户：${otherRole.name}` }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("关联用户名搜索")).toHaveValue("");
    expect(
      new URL(userCalls().at(-1)![0], "http://localhost").searchParams.get(
        "roleID",
      ),
    ).toBe("viewer");
    click("返回角色列表");
    await screen.findByRole("table", { name: "角色列表" });
    expect(window.location.hash).toBe("#access/roles");
    click(`查看关联用户 ${role.name}`);
    await table();
    expect(screen.getByText(/共 23 条/)).toBeInTheDocument();
    expect(localStorage.length + sessionStorage.length).toBe(0);
  });
  it("Back and Forward dismiss rather than reconstruct any private selection", async () => {
    await openRoles();
    const reads = userCalls().length;
    act(() => window.history.back());
    await waitFor(() => expect(window.location.hash).toBe("#access/roles"));
    await screen.findByRole("table", { name: "角色列表" });
    act(() => window.history.forward());
    await waitFor(() =>
      expect(window.location.hash).toBe("#access/roles/users"),
    );
    await screen.findByRole("table", { name: "角色列表" });
    expect(screen.queryByText("member01")).not.toBeInTheDocument();
    expect(userCalls()).toHaveLength(reads);
  });
});

describe("associated user permission and stale-response barriers", () => {
  it.each(["menu", "operation"])(
    "requires independent users %s permission at list and detail entries",
    async (missing) => {
      if (missing === "menu")
        context.menu = permissions().filter((node) => node.mark !== "users");
      else context.can = (op) => op !== "GET /api/access/users";
      render(<AccessRoles context={context} />);
      await screen.findByRole("table");
      expect(
        screen.queryByRole("button", { name: /查看关联用户/ }),
      ).not.toBeInTheDocument();
      click(`详情 ${role.name}`);
      await screen.findByRole("heading", { name: `角色详情：${role.name}` });
      expect(
        screen.queryByRole("button", { name: /查看关联用户/ }),
      ).not.toBeInTheDocument();
      expect(userCalls()).toHaveLength(0);
    },
  );
  it.each(["ID", "username", "role", "priv", "csrfToken"])(
    "does not inherit an old selection when profile %s changes",
    async (key) => {
      const view = await openRoles();
      const count = userCalls().length;
      const next = {
        ...context,
        profile: {
          ...profile,
          [key]: key === "ID" || key === "priv" ? 7 : "new-value",
        },
      };
      view.rerender(<AccessRoles context={next} />);
      expect(screen.queryByText("member01")).not.toBeInTheDocument();
      await screen.findByRole("table", { name: "角色列表" });
      expect(userCalls()).toHaveLength(count);
    },
  );
  it("hides rows and aborts pending reads on permission loss; restoration does not reopen", async () => {
    const view = render(panel());
    await table();
    let resolve!: (result: Response) => void;
    override = (url) =>
      url.startsWith("/api/access/users?")
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    click("刷新关联用户");
    await waitFor(() => expect(resolve).toBeTypeOf("function"));
    const signal = userCalls().at(-1)![1].signal as AbortSignal;
    const denied = {
      ...context,
      menu: permissions().filter((node) => node.mark !== "users"),
    };
    view.rerender(panel({ context: denied }));
    expect(signal.aborted).toBe(true);
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    view.rerender(panel());
    await act(async () =>
      resolve(await response(page(userCalls().at(-1)![0]))),
    );
    expect(screen.queryByText("member01")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "刷新关联用户" }),
    ).not.toBeInTheDocument();
    expect(userCalls()).toHaveLength(2);
  });
  it.each(["identity", "target", "permission", "unmount"])(
    "aborts and discards delayed JSON on %s change",
    async (reason) => {
      let resolve!: (result: unknown) => void;
      override = (url) =>
        url.startsWith("/api/access/users?")
          ? Promise.resolve({
              ok: true,
              status: 200,
              json: () =>
                new Promise((r) => {
                  resolve = r;
                }),
            } as Response)
          : undefined;
      const view = render(panel());
      await waitFor(() => expect(resolve).toBeTypeOf("function"));
      const [url, init] = userCalls()[0];
      if (reason === "unmount") view.unmount();
      else
        view.rerender(
          panel(
            reason === "target"
              ? { role: otherRole }
              : {
                  context:
                    reason === "identity"
                      ? {
                          ...context,
                          profile: { ...profile, csrfToken: "rotated" },
                        }
                      : { ...context, can: () => false },
                },
          ),
        );
      expect(init.signal.aborted).toBe(true);
      await act(async () => resolve(page(url)));
      expect(screen.queryByText("member01")).not.toBeInTheDocument();
      expect(userCalls()).toHaveLength(1);
    },
  );
  it("Close aborts a pending read and a late response cannot repopulate a reopened target", async () => {
    let resolve!: (result: Response) => void;
    override = (url) =>
      url.startsWith("/api/access/users?")
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    render(<AccessRoles context={context} />);
    await screen.findByRole("table");
    click(`查看关联用户 ${role.name}`);
    await waitFor(() => expect(resolve).toBeTypeOf("function"));
    const [url, init] = userCalls()[0];
    click("关闭关联用户");
    expect(init.signal.aborted).toBe(true);
    await screen.findByRole("table", { name: "角色列表" });
    override = () => undefined;
    click(`查看关联用户 ${otherRole.name}`);
    await table();
    await act(async () => resolve(await response(page(url))));
    expect(
      screen.getByRole("heading", { name: "角色关联用户：Viewer" }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("rowheader")).toHaveLength(1);
    expect(screen.getByText(/共 1 条/)).toBeInTheDocument();
  });
  it("collapses repeated reads while pending and retries a failed GET only on an explicit read", async () => {
    await openRoles();
    let resolve!: (result: Response) => void;
    override = (url) =>
      url.startsWith("/api/access/users?")
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    click("刷新关联用户");
    click("刷新关联用户");
    await waitFor(() => expect(resolve).toBeTypeOf("function"));
    expect(userCalls()).toHaveLength(2);
    expect(screen.queryByText("member01")).not.toBeInTheDocument();
    await act(async () => resolve(await response({ error: "internal" }, 500)));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(userCalls()).toHaveLength(2);
    override = () => undefined;
    click("刷新关联用户");
    await table();
    expect(userCalls()).toHaveLength(3);
  });
  it.each(["cross-role", "malformed"])(
    "fails closed on a %s response and never displays partial rows",
    async (kind) => {
      override = (url) => {
        if (!url.startsWith("/api/access/users?")) return undefined;
        const result = page(url);
        if (kind === "cross-role") result.List[0].roleID = "viewer";
        else Object.assign(result.List[0], { ID: "1" });
        return response(result);
      };
      render(panel());
      expect(await screen.findByRole("alert")).toHaveTextContent("读取失败");
      expect(screen.queryByRole("table")).not.toBeInTheDocument();
      expect(screen.queryByText(/当前页没有/)).not.toBeInTheDocument();
    },
  );
  it("403 clears all rows and closes reads and entries even when its JSON is malformed", async () => {
    await openRoles();
    override = (url) =>
      url.startsWith("/api/access/users?")
        ? Promise.resolve({
            ok: false,
            status: 403,
            json: async () => {
              throw new Error("bad");
            },
          } as unknown as Response)
        : undefined;
    click("刷新关联用户");
    expect(screen.queryByText("member01")).not.toBeInTheDocument();
    expect(await screen.findByRole("alert")).toHaveTextContent("关闭读取入口");
    expect(
      screen.queryByRole("button", { name: "刷新关联用户" }),
    ).not.toBeInTheDocument();
    click("返回角色列表");
    await screen.findByRole("table");
    expect(
      screen.queryByRole("button", { name: /查看关联用户/ }),
    ).not.toBeInTheDocument();
    expect(context.sessionChanged).not.toHaveBeenCalled();
  });
  it.each([
    [401, "unauthenticated"],
    [403, "password_change_required"],
  ] as const)("invalidates session on %i %s", async (status, error) => {
    override = (url) =>
      url.startsWith("/api/access/users?")
        ? response({ error }, status)
        : undefined;
    render(panel());
    await waitFor(() =>
      expect(context.sessionChanged).toHaveBeenCalledTimes(1),
    );
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "刷新关联用户" }),
    ).not.toBeInTheDocument();
  });
  it("an intervening cookie switch rejects the result in the DOM and requests identity refresh", async () => {
    let sessionReads = 0;
    override = (url) =>
      url === "/api/auth/me"
        ? response(
            ++sessionReads === 1
              ? profile
              : {
                  ...profile,
                  ID: 9,
                  username: "other-actor",
                  csrfToken: "other-session",
                },
          )
        : undefined;
    render(panel());
    await waitFor(() =>
      expect(context.sessionChanged).toHaveBeenCalledTimes(1),
    );
    expect(userCalls()).toHaveLength(1);
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.queryByText("member01")).not.toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("登录身份或会话已失效");
  });
  it("close while initial identity JSON is pending prevents the target request entirely", async () => {
    let resolve!: (value: unknown) => void;
    override = (url) =>
      url === "/api/auth/me"
        ? Promise.resolve({
            ok: true,
            status: 200,
            json: () =>
              new Promise((r) => {
                resolve = r;
              }),
          } as Response)
        : undefined;
    const close = vi.fn();
    render(panel({ close }));
    await waitFor(() => expect(resolve).toBeTypeOf("function"));
    const signal = fetcher.mock.calls[0][1].signal as AbortSignal;
    click("关闭关联用户");
    expect(signal.aborted).toBe(true);
    expect(close).toHaveBeenCalledTimes(1);
    await act(async () => resolve(profile));
    expect(userCalls()).toHaveLength(0);
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
  it.each(["menu", "operation"])(
    "clears already displayed rows synchronously on %s loss",
    async (kind) => {
      const view = render(panel());
      await table();
      view.rerender(
        panel({
          context: {
            ...context,
            ...(kind === "menu" ? { menu: [] } : { can: () => false }),
          },
        }),
      );
      expect(screen.queryByText("member01")).not.toBeInTheDocument();
      expect(screen.queryByRole("table")).not.toBeInTheDocument();
      expect(userCalls()).toHaveLength(1);
    },
  );
  it("returns a concurrently emptied page to page one without losing its committed search or size", async () => {
    await openRoles();
    setSearch("member");
    fireEvent.change(screen.getByLabelText("关联用户每页条数"), {
      target: { value: "10" },
    });
    click("搜索关联用户");
    await table();
    click("下一页");
    await screen.findByRole("rowheader", { name: "member11 ID 11" });
    override = (url) => {
      if (!url.startsWith("/api/access/users?")) return undefined;
      const result = page(url);
      return response({
        ...result,
        page: { ...result.page, total: 0, totalPage: 0 },
        List: [],
        exhausted: true,
      });
    };
    click("刷新关联用户");
    await screen.findByText(/当前页没有符合条件的关联用户/);
    click("返回关联用户第一页");
    await table();
    expect(
      Object.fromEntries(
        new URL(userCalls().at(-1)![0], "http://localhost").searchParams,
      ),
    ).toEqual({
      roleID: role.id,
      pageIdx: "1",
      pageSize: "10",
      search: "member",
      sort: "-1",
    });
  });
  it("session invalidation removes loaded data without querying the old role again", async () => {
    render(panel());
    await table();
    act(() =>
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: SESSION_INVALIDATION_STORAGE,
          newValue: "a".repeat(32),
          storageArea: localStorage,
        }),
      ),
    );
    expect(screen.queryByText("member01")).not.toBeInTheDocument();
    expect(context.sessionChanged).toHaveBeenCalledTimes(1);
    expect(userCalls()).toHaveLength(1);
  });
});
