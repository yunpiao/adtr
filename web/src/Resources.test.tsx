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
import ResourcesWorkspace from "./ResourcesWorkspace";
import ResourceGroups from "./ResourceGroups";
import ResourceTenant from "./ResourceTenant";
import ResourceScope from "./ResourceScope";
import { ApiError, type Profile } from "./api";
import { labels, marks } from "./access-api";
import {
  parseChecks,
  parseResourceIDs,
  parseCustomRoleIDs,
  parseTenant,
  resourceOperations,
  resourceRequest,
  validGroupName,
  validResourceMark,
  type ResourceMeta,
  type ResourceOperation,
} from "./resource-api";
import { type ResourceContext } from "./resource-common";
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
const group: ResourceMeta = {
  id: "group-a",
  name: "SyntheticGroup",
  mark: "Synthetic scope",
  datas: [{ appName: "ad", resources: ["domain-a"] }],
  applyRoleCount: 1,
  createTime: "2026-10-01T00:00:00Z",
};
const tenant = {
  maxAdCount: 1,
  expireTime: 4102444800,
  uid: "synthetic-customer",
  name: "合成租户",
};
const pageInfo = (total = 1, pageIdx = 1, pageSize = 20) => ({
  pageIdx,
  pageSize,
  total,
  totalPage: total
    ? Math.ceil(total / (pageSize === -1 ? total : pageSize))
    : 0,
});
const groups = (metas = [group]) => ({
  page: pageInfo(metas.length),
  metas,
  exhausted: true,
});
const associated = (ids = ["viewer"], pageSize = 20) => ({
  page: pageInfo(ids.length, 1, pageSize),
  details: ids.map((id) => ({ id, name: id, mark: "" })),
  exhausted: true,
});
const response = (body: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as unknown as Response);
let fetcher: ReturnType<typeof vi.fn>,
  override: (url: string, init: RequestInit) => Promise<Response> | undefined,
  context: ResourceContext;
beforeEach(() => {
  window.history.replaceState({}, "", "#account");
  override = () => undefined;
  context = { profile, can: () => true, sessionChanged: vi.fn() };
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const changed = override(url, init);
    if (changed) return changed;
    const path = url.split("?")[0];
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/menu")
      return response({
        menu: marks.map((mark) => ({
          mark,
          name: labels[mark],
          auth: { readable: true, writeable: true },
        })),
      });
    if (path === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map(() => true),
      });
    if (path === "/api/resources/groups") return response(groups());
    if (path === "/api/resources/groups/detail")
      return response({ meta: group });
    if (path === "/api/resources/groups/exists")
      return response({ isExist: false });
    if (path === "/api/resources/groups/roles")
      return response(
        associated(
          ["viewer"],
          new URL(`https://test${url}`).searchParams.get("pageSize") === "-1"
            ? -1
            : 20,
        ),
      );
    if (path === "/api/resources/tenant") return response(tenant);
    if (path === "/api/resources/grants")
      return response({ list: [], authorizationScopeOnly: true });
    if (path === "/api/resources/check")
      return response({
        results: JSON.parse(init.body as string).resources.map(() => false),
        authorizationScopeOnly: true,
      });
    return response({
      result: "SUCCESS",
      sessionRevoked: false,
      id: "new-group",
    });
  });
  vi.stubGlobal("fetch", fetcher);
});
const click = async (name: string) =>
  userEvent.click(await screen.findByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
const proof = () => {
  fill("操作者当前密码", "Synthetic Password 123");
  fill("未使用的认证器验证码", "123456");
};
const requests = (path: string) =>
  fetcher.mock.calls.filter(([url]) => url === path);
const body = (path: string) => JSON.parse(requests(path).at(-1)![1].body);
async function detail() {
  await click("查看 SyntheticGroup");
  await screen.findByRole("heading", { name: "资源组详情：SyntheticGroup" });
}

describe("resource API and frozen validation", () => {
  it("sends current-session JSON/CSRF without a caller token or tenant", async () => {
    await resourceRequest(
      "/check",
      new AbortController().signal,
      {
        resourceType: 2,
        resources: [{ application: "ad", dataResource: ["domain-a"] }],
      },
      "csrf",
    );
    expect(fetcher).toHaveBeenCalledWith(
      "/api/resources/check",
      expect.objectContaining({
        method: "POST",
        credentials: "same-origin",
        cache: "no-store",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": "csrf" },
      }),
    );
    expect(body("/api/resources/check")).not.toHaveProperty("token");
    expect(body("/api/resources/check")).not.toHaveProperty("tenant");
  });
  it("distinguishes server errors, network uncertainty and malformed JSON", async () => {
    override = () => response({ error: "tenant_expired" }, 403);
    await expect(
      resourceRequest("/grants", new AbortController().signal),
    ).rejects.toMatchObject({ code: "tenant_expired", status: 403 });
    override = () => Promise.reject(new TypeError("offline"));
    await expect(
      resourceRequest("/grants", new AbortController().signal),
    ).rejects.toMatchObject({ code: "network" });
    override = () =>
      Promise.resolve({
        ok: true,
        status: 200,
        json: async () => {
          throw new Error("not JSON");
        },
      } as unknown as Response);
    await expect(
      resourceRequest("/grants", new AbortController().signal),
    ).rejects.toBeInstanceOf(ApiError);
  });
  it("enforces UTF-8 byte group names, Unicode marks and ID limits", () => {
    expect(validGroupName("界".repeat(85))).toBe(true);
    expect(validGroupName("界".repeat(86))).toBe(false);
    for (const name of ["", "a b", "a\u0085b", "a\u2003b", "a\u007fb"])
      expect(validGroupName(name)).toBe(false);
    expect(validResourceMark("😀".repeat(500))).toBe(true);
    expect(validResourceMark("😀".repeat(501))).toBe(false);
    expect(validResourceMark("a\u0085b")).toBe(false);
    expect(parseCustomRoleIDs("r".repeat(24))).toEqual(["r".repeat(24)]);
    expect(parseCustomRoleIDs("role-custom")).toBeNull();
    expect(parseCustomRoleIDs("r".repeat(23) + ".")).toBeNull();
    expect(parseResourceIDs("")).toEqual([]);
    expect(parseResourceIDs("a,b\nc")).toEqual(["a", "b", "c"]);
    for (const ids of ["a,a", "非法域", "a".repeat(129)])
      expect(parseResourceIDs(ids)).toBeNull();
    expect(
      parseResourceIDs(
        Array.from({ length: 1001 }, (_, i) => `domain-${i}`).join("\n"),
      ),
    ).toBeNull();
  });
  it("retains zero but rejects milliseconds, fractional fields and invented perpetual values", () => {
    expect(
      parseTenant({ maxAdCount: "0", expireTime: "0", uid: "u", name: "n" }),
    ).toEqual({ maxAdCount: 0, expireTime: 0, uid: "u", name: "n" });
    const values = {
      maxAdCount: "1",
      expireTime: "4102444800",
      uid: "客户",
      name: "租户",
    };
    expect(parseTenant(values)).not.toBeNull();
    for (const changed of [
      { maxAdCount: "100001" },
      { maxAdCount: "1.5" },
      { expireTime: "4102444800000" },
      { expireTime: "" },
      { expireTime: "-1" },
      { uid: " u" },
      { name: "a\u0085b" },
      { name: "😀".repeat(33) },
    ])
      expect(parseTenant({ ...values, ...changed })).toBeNull();
    expect(parseTenant({ ...values, name: "😀".repeat(32) })).not.toBeNull();
  });
  it("makes each check a nonempty AND group and never infers legacy enums", () => {
    expect(parseChecks("a,b\nc")).toEqual({
      resourceType: 2,
      resources: [
        { application: "ad", dataResource: ["a", "b"] },
        { application: "ad", dataResource: ["c"] },
      ],
    });
    for (const input of ["", "a\n\nb", "a,a", Array(101).fill("a").join("\n")])
      expect(parseChecks(input)).toBeNull();
  });
});

describe("server capabilities and session scope", () => {
  it("only loads resource APIs after explicit navigation and admins have no automatic domains", async () => {
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    expect(
      fetcher.mock.calls.some(([url]) => url.startsWith("/api/resources")),
    ).toBe(false);
    await click("资源与租户");
    await screen.findByText(
      "当前没有获授权的活动域。平台管理员也没有自动数据授权。",
    );
    expect(body("/api/access/check")).toEqual({ paths: resourceOperations });
  });
  it("uses server tenant capability even when profile says platform admin and denies writes without menu grant", async () => {
    override = (url, init) =>
      url === "/api/access/check"
        ? response({
            results: JSON.parse(init.body as string).paths.map(
              (path: ResourceOperation) => !path.includes("/tenant"),
            ),
          })
        : url === "/api/access/menu"
          ? response({
              menu: [
                { mark: "roles", auth: { readable: true, writeable: false } },
              ],
            })
          : undefined;
    render(
      <ResourcesWorkspace
        profile={profile}
        sessionChanged={context.sessionChanged}
      />,
    );
    await click("资源组");
    await screen.findByRole("rowheader", { name: /SyntheticGroup/ });
    expect(
      screen.queryByRole("button", { name: "租户配置" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "新增资源组" }),
    ).not.toBeInTheDocument();
  });
  it("permits introspection without management function permissions", async () => {
    override = (url, init) =>
      url === "/api/access/menu"
        ? response({ menu: [] })
        : url === "/api/access/check"
          ? response({
              results: JSON.parse(init.body as string).paths.map(
                (path: string) =>
                  path.endsWith("/grants") || path.endsWith("/check"),
              ),
            })
          : undefined;
    render(
      <ResourcesWorkspace
        profile={{ ...profile, role: "viewer" }}
        sessionChanged={context.sessionChanged}
      />,
    );
    await screen.findByText(
      "当前没有获授权的活动域。平台管理员也没有自动数据授权。",
    );
    expect(
      screen.queryByRole("button", { name: "资源组" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "检查数据授权" })).toBeVisible();
  });
  it("fails closed on malformed capability results and retries reads explicitly", async () => {
    override = (url) =>
      url === "/api/access/check" ? response({ results: [] }) : undefined;
    render(
      <ResourcesWorkspace
        profile={profile}
        sessionChanged={context.sessionChanged}
      />,
    );
    await screen.findByRole("alert");
    expect(
      screen.queryByRole("button", { name: "资源组" }),
    ).not.toBeInTheDocument();
    override = () => undefined;
    await click("重新读取资源权限");
    await screen.findByRole("button", { name: "资源组" });
  });
  it("shows tenant-state errors and checks exact ordered domain requests", async () => {
    override = (url) =>
      url === "/api/resources/grants"
        ? response({ error: "tenant_expired" }, 403)
        : undefined;
    render(<ResourceScope context={context} />);
    await screen.findByText(/当前租户配置已过期/);
    fill("待检查域 ID", "domain-a,domain-b\nunknown-domain");
    await click("检查数据授权");
    expect(body("/api/resources/check")).toEqual(
      parseChecks("domain-a,domain-b\nunknown-domain"),
    );
    const results = await screen.findByRole("list", {
      name: "数据授权检查结果",
    });
    expect(results).toHaveTextContent("不允许 · domain-a、domain-b");
    expect(results).toHaveTextContent("不允许 · unknown-domain");
  });
  it("does not present malformed check responses as allowed", async () => {
    override = (url) =>
      url === "/api/resources/check"
        ? response({ results: [true], authorizationScopeOnly: false })
        : undefined;
    render(<ResourceScope context={context} />);
    fill("待检查域 ID", "domain-a");
    await click("检查数据授权");
    await screen.findByRole("alert");
    expect(
      screen.queryByRole("list", { name: "数据授权检查结果" }),
    ).not.toBeInTheDocument();
  });
  it("refreshes identity after revocation or forced-password errors", async () => {
    override = (url) =>
      url === "/api/resources/grants"
        ? response({ error: "unauthenticated" }, 401)
        : undefined;
    render(<ResourceScope context={context} />);
    await waitFor(() =>
      expect(context.sessionChanged).toHaveBeenCalledTimes(1),
    );
  });
});

describe("durable resource groups", () => {
  it("supports literal name filters, ascending bool sort, all-results and server pagination", async () => {
    render(<ResourceGroups context={context} />);
    await screen.findByRole("rowheader", { name: /SyntheticGroup/ });
    fill("搜索资源组名称", "a%_界");
    fill("每页条数", "-1");
    fill("资源组排序", "true");
    await click("筛选资源组");
    await waitFor(() =>
      expect(fetcher).toHaveBeenCalledWith(
        "/api/resources/groups?pageIdx=1&pageSize=-1&name=a%25_%E7%95%8C&sort=true",
        expect.anything(),
      ),
    );
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
  });
  it("shows genuine empty results and result-too-large errors", async () => {
    override = (url) =>
      url.startsWith("/api/resources/groups?")
        ? response(groups([]))
        : undefined;
    render(<ResourceGroups context={context} />);
    await screen.findByText("没有匹配的资源组。");
    override = (url) =>
      url.startsWith("/api/resources/groups?")
        ? response({ error: "result_too_large" }, 422)
        : undefined;
    await click("刷新资源组列表");
    await screen.findByText(/结果超过 1,000 条/);
    expect(screen.queryByText("SyntheticGroup")).not.toBeInTheDocument();
  });
  it("creates empty scopes with proof, without server-owned fields or domains", async () => {
    render(<ResourceGroups context={context} />);
    await click("新增资源组");
    fill("资源组名称", "EmptyGroup");
    fill("资源组备注", "备注");
    proof();
    await click("检查资源组名称");
    await screen.findByText(/当前未占用/);
    await click("保存资源组");
    await screen.findByText("资源组已创建。");
    expect(body("/api/resources/groups/create")).toEqual({
      meta: { name: "EmptyGroup", mark: "备注", datas: [] },
      actorPassword: "Synthetic Password 123",
      totpCode: "123456",
    });
  });
  it("reloads detail then replaces name and full membership on edit", async () => {
    render(<ResourceGroups context={context} />);
    await detail();
    await screen.findByText(/viewer · viewer/);
    await click("编辑资源组");
    await screen.findByLabelText("资源组名称");
    fill("资源组名称", "Renamed");
    fill("域 ID", "domain-b\ndomain-c");
    proof();
    await click("保存资源组");
    await screen.findByText("资源组已更新。");
    expect(body("/api/resources/groups/update")).toEqual({
      id: "group-a",
      meta: {
        name: "Renamed",
        mark: "Synthetic scope",
        datas: [{ appName: "ad", resources: ["domain-b", "domain-c"] }],
      },
      actorPassword: "Synthetic Password 123",
      totpCode: "123456",
    });
    expect(
      fetcher.mock.calls.filter(
        ([url]) => url === "/api/resources/groups/detail?id=group-a",
      ),
    ).toHaveLength(2);
  });
  it("rejects duplicate/invalid memberships locally and reports unknown domains from server", async () => {
    render(<ResourceGroups context={context} />);
    await click("新增资源组");
    fill("资源组名称", "EmptyGroup");
    fill("域 ID", "a,a");
    proof();
    await click("保存资源组");
    await screen.findByRole("alert");
    expect(requests("/api/resources/groups/create")).toHaveLength(0);
    override = (url) =>
      url === "/api/resources/groups/create"
        ? response({ error: "unavailable_resource" }, 422)
        : undefined;
    fill("域 ID", "unknown-domain");
    await click("保存资源组");
    await screen.findByText(/包含不可用的域 ID/);
    expect(requests("/api/resources/groups/create")).toHaveLength(1);
  });
  it("loads complete role set before replacing builtin/custom associations", async () => {
    render(<ResourceGroups context={context} />);
    await detail();
    await click("调整关联角色");
    await screen.findByLabelText("显式关联 viewer");
    expect(screen.getByLabelText("显式关联 viewer")).toBeChecked();
    await userEvent.click(screen.getByLabelText("显式关联 platform_admin"));
    fill("自定义角色 ID", "rrrrrrrrrrrrrrrrrrrrrrrr");
    proof();
    await click("保存角色关联");
    await screen.findByText("资源组关联已保存。");
    expect(body("/api/resources/groups/assign")).toMatchObject({
      id: "group-a",
      roleIds: ["platform_admin", "viewer", "rrrrrrrrrrrrrrrrrrrrrrrr"],
    });
    expect(fetcher).toHaveBeenCalledWith(
      "/api/resources/groups/roles?id=group-a&pageIdx=1&pageSize=-1",
      expect.anything(),
    );
  });
  it("can explicitly remove all associations and honors caller session revocation", async () => {
    override = (url) =>
      url === "/api/resources/groups/assign"
        ? response({ result: "SUCCESS", sessionRevoked: true })
        : undefined;
    render(<ResourceGroups context={context} />);
    await detail();
    await click("调整关联角色");
    await screen.findByLabelText("显式关联 viewer");
    await userEvent.click(screen.getByLabelText("显式关联 viewer"));
    proof();
    await click("保存角色关联");
    await waitFor(() =>
      expect(context.sessionChanged).toHaveBeenCalledTimes(1),
    );
    expect(body("/api/resources/groups/assign").roleIds).toEqual([]);
  });
  it("never edits a truncated association set", async () => {
    override = (url) =>
      url.includes("/groups/roles?") && url.includes("pageSize=-1")
        ? response({ ...associated(), exhausted: false })
        : undefined;
    render(<ResourceGroups context={context} />);
    await detail();
    await click("调整关联角色");
    await screen.findByRole("alert");
    expect(
      screen.queryByRole("button", { name: "保存角色关联" }),
    ).not.toBeInTheDocument();
  });
  it("confirms delete with target and proof and reloads storage", async () => {
    render(<ResourceGroups context={context} />);
    await detail();
    await click("删除资源组");
    await screen.findByRole("heading", { name: "删除资源组：SyntheticGroup" });
    proof();
    await click("确认删除资源组");
    await screen.findByText("资源组已删除。");
    expect(body("/api/resources/groups/delete")).toMatchObject({
      id: "group-a",
      actorPassword: "Synthetic Password 123",
      totpCode: "123456",
    });
  });
  it("blocks repeated writes, cancels pending forms, and discards late success", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url === "/api/resources/groups/create"
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    render(<ResourceGroups context={context} />);
    await click("新增资源组");
    fill("资源组名称", "Pending");
    proof();
    const submit = screen.getByRole("button", { name: "保存资源组" });
    fireEvent.submit(submit.closest("form")!);
    fireEvent.submit(submit.closest("form")!);
    await waitFor(() =>
      expect(requests("/api/resources/groups/create")).toHaveLength(1),
    );
    await click("取消");
    expect(requests("/api/resources/groups/create")[0][1].signal.aborted).toBe(
      true,
    );
    await act(async () =>
      resolve(await response({ result: "SUCCESS", sessionRevoked: true })),
    );
    expect(context.sessionChanged).not.toHaveBeenCalled();
    expect(screen.queryByText("资源组已创建。")).not.toBeInTheDocument();
  });
  it("discards stale list responses when a newer filter completes", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url.includes("/groups?") && !url.includes("name=")
        ? new Promise((r) => {
            resolve = r;
          })
        : url.includes("name=new")
          ? response(groups([]))
          : undefined;
    render(<ResourceGroups context={context} />);
    fill("搜索资源组名称", "new");
    await click("筛选资源组");
    await screen.findByText("没有匹配的资源组。");
    await act(async () => resolve(await response(groups())));
    expect(
      screen.queryByRole("rowheader", { name: /SyntheticGroup/ }),
    ).not.toBeInTheDocument();
  });
});

describe("tenant configuration and navigation", () => {
  it("does not invent unconfigured values and saves exact seconds with proof", async () => {
    override = (url) =>
      url === "/api/resources/tenant"
        ? response({ error: "tenant_not_configured" }, 409)
        : url === "/api/resources/tenant/save"
          ? response({ result: "SUCCESS", sessionRevoked: true })
          : undefined;
    render(<ResourceTenant context={context} />);
    await screen.findByText(/没有预填的默认配置/);
    await click("编辑租户配置");
    expect(screen.getByLabelText("活动域数量上限")).toHaveValue("");
    expect(screen.getByLabelText("失效时间（UTC Unix 秒）")).toHaveValue("");
    fill("活动域数量上限", "0");
    fill("失效时间（UTC Unix 秒）", "0");
    fill("客户 UID", "u");
    fill("租户名称", "n");
    proof();
    await click("保存租户配置");
    await waitFor(() =>
      expect(context.sessionChanged).toHaveBeenCalledTimes(1),
    );
    expect(body("/api/resources/tenant/save")).toEqual({
      maxAdCount: 0,
      expireTime: 0,
      uid: "u",
      name: "n",
      actorPassword: "Synthetic Password 123",
      totpCode: "123456",
    });
  });
  it("blocks repeated association writes before disabled form parsing", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url === "/api/resources/groups/assign"
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    render(<ResourceGroups context={context} />);
    await detail();
    await click("调整关联角色");
    await screen.findByLabelText("显式关联 viewer");
    proof();
    const form = screen
      .getByRole("button", { name: "保存角色关联" })
      .closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    await waitFor(() =>
      expect(requests("/api/resources/groups/assign")).toHaveLength(1),
    );
    await click("取消");
    await act(async () =>
      resolve(await response({ result: "SUCCESS", sessionRevoked: true })),
    );
    expect(context.sessionChanged).not.toHaveBeenCalled();
  });
  it("blocks repeated check submissions and discards results after a new input", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url === "/api/resources/check"
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    render(<ResourceScope context={context} />);
    fill("待检查域 ID", "domain-a");
    const form = screen
      .getByRole("button", { name: "检查数据授权" })
      .closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    await waitFor(() =>
      expect(requests("/api/resources/check")).toHaveLength(1),
    );
    await act(async () =>
      resolve(
        await response({ results: [true], authorizationScopeOnly: true }),
      ),
    );
    expect(
      screen.getByRole("list", { name: "数据授权检查结果" }),
    ).toHaveTextContent("允许（仅范围） · domain-a");
    fill("待检查域 ID", "domain-b");
    expect(
      screen.queryByRole("list", { name: "数据授权检查结果" }),
    ).not.toBeInTheDocument();
  });
  it("cancels tenant writes without a late response resurrecting the flow", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url === "/api/resources/tenant/save"
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    render(<ResourceTenant context={context} />);
    await click("编辑租户配置");
    fill("租户名称", "changed");
    proof();
    const form = screen
      .getByRole("button", { name: "保存租户配置" })
      .closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    await waitFor(() =>
      expect(requests("/api/resources/tenant/save")).toHaveLength(1),
    );
    await click("取消");
    await screen.findByRole("button", { name: "编辑租户配置" });
    expect(requests("/api/resources/tenant/save")[0][1].signal.aborted).toBe(
      true,
    );
    await act(async () =>
      resolve(await response({ result: "SUCCESS", sessionRevoked: true })),
    );
    expect(context.sessionChanged).not.toHaveBeenCalled();
  });
  it("clears draft fields on Back and rechecks account on navigation away", async () => {
    render(<App />);
    await click("资源与租户");
    await click("资源组");
    await click("新增资源组");
    fill("资源组名称", "discard-me");
    const readsBefore = requests("/api/auth/me").length;
    act(() => window.history.back());
    await waitFor(() => expect(window.location.hash).toBe("#resources/groups"));
    await screen.findByRole("heading", { name: "资源与租户" });
    expect(requests("/api/auth/me").length).toBeGreaterThan(readsBefore);
    expect(screen.queryByLabelText("资源组名称")).not.toBeInTheDocument();
    await click("资源与租户");
    await click("资源组");
    await click("新增资源组");
    expect(screen.getByLabelText("资源组名称")).toHaveValue("");
    const before = requests("/api/auth/me").length;
    await click("账户概览");
    await screen.findByRole("heading", { name: "账户概览" });
    expect(requests("/api/auth/me").length).toBeGreaterThan(before);
  });
});
