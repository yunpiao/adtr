import { revealNavigation } from "./test-navigation";
// Synthetic DOM/transport tests. Real browser/API/database evidence lives in
// e2e/credential-use.spec.ts; these fixtures do not replace it.
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import CredentialUseWorkspace, {
  CredentialUsePanel,
} from "./CredentialUseWorkspace";
import App from "./App";
import { ApiError, messages, type Profile } from "./api";
import { errorText } from "./access-api";
import {
  credentialPurpose,
  credentialUseAPI,
  credentialUseError,
  credentialUseOperations,
  validCredentialAccount,
  validCredentialGrant,
  validCredentialReceipt,
  type CredentialAccount,
  type CredentialGrant,
  type CredentialReceipt,
} from "./credential-use-api";
import {
  discardCredentialIntent,
  readCredentialIntent,
  saveCredentialIntent,
  type CredentialIntent,
} from "./credential-use-intent";
const base = "/api/credential-use",
  accountId = "synthetic-account",
  domainId = "synthetic-domain",
  roleId = "platform_admin";
const account: CredentialAccount = {
  accountId,
  domainId,
  domain: "synthetic.invalid",
  label: "测试凭据",
  revision: "9007199254740993",
  credentialRevision: "2",
};
const grant = (extra: Partial<CredentialGrant> = {}): CredentialGrant => ({
  roleId,
  roleName: "platform_admin",
  purpose: credentialPurpose,
  allowed: true,
  grantRevision: "9007199254740994",
  accountCredentialRevision: "2",
  updatedAt: "2026-10-07T00:00:00Z",
  ...extra,
});
const receipt = (
  extra: Partial<CredentialReceipt> = {},
): CredentialReceipt => ({
  result: "SUCCESS",
  operation: "grant",
  accountId,
  domainId,
  roleId,
  purpose: credentialPurpose,
  allowed: true,
  grantRevision: "9007199254740994",
  accountCredentialRevision: "2",
  replayed: false,
  accountDeleted: false,
  currentGrantRevision: "9007199254740994",
  currentAllowed: true,
  ...extra,
});
const pending = (extra: Partial<CredentialIntent> = {}): CredentialIntent => ({
  kind: "grant",
  accountId,
  domainId,
  roleId,
  purpose: credentialPurpose,
  expectedAccountRevision: account.revision,
  expectedCredentialRevision: "2",
  expectedGrantRevision: "0",
  idempotencyKey: "synthetic-original-key",
  ...extra,
});
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
  pwdUpdateTm: "2026-10-07T00:00:00Z",
  csrfToken: "synthetic-session",
};
const response = (
  value: unknown,
  status = 200,
  actor: number | null = profile.ID,
) =>
  Promise.resolve(
    new Response(JSON.stringify(value), {
      status,
      headers: {
        "Content-Type": "application/json",
        ...(actor === null ? {} : { "X-ADTR-User-ID": String(actor) }),
      },
    }),
  );
let fetcher: ReturnType<typeof vi.fn>,
  override: (url: string, init: RequestInit) => Promise<Response> | undefined,
  grants: CredentialGrant[],
  paths: boolean[],
  readable: boolean;
const signal = () => new AbortController().signal;
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
const calls = (path: string) =>
  fetcher.mock.calls.filter(([url]) => url.split("?")[0] === `${base}/${path}`);
async function panel(p = profile, sessionChanged = vi.fn()) {
  const view = render(
    <CredentialUsePanel
      profile={p}
      sessionChanged={sessionChanged}
      accountId={accountId}
    />,
  );
  click("查看凭据使用授权");
  await waitFor(() =>
    expect(screen.queryByText("正在核对凭据授权权限…")).not.toBeInTheDocument(),
  );
  return view;
}
async function grantForm() {
  await panel();
  await waitFor(() =>
    expect(screen.getByLabelText("授权目标角色")).toBeEnabled(),
  );
  fill("授权目标角色", roleId);
  click("审阅角色授权");
}
function proof(kind = "授权") {
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: `我已核对目标角色，确认${kind}覆盖当前及未来全部成员`,
    }),
  );
  fill("操作者当前密码", "Synthetic actor password 482");
  fill("未使用的认证器验证码", "123456");
}
beforeEach(() => {
  discardCredentialIntent(profile);
  discardCredentialIntent({ ...profile, ID: 2 });
  sessionStorage.clear();
  localStorage.clear();
  grants = [];
  paths = credentialUseOperations.map(() => true);
  readable = true;
  override = () => undefined;
  fetcher = vi.fn((url: string, init: RequestInit = {}) => {
    const changed = override(url, init);
    if (changed) return changed;
    const path = url.split("?")[0];
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/menu")
      return response({
        menu: [
          { mark: "operation_accounts", auth: { readable, writeable: false } },
        ],
      });
    if (path === "/api/access/check") return response({ results: paths });
    if (path === `${base}/accounts`)
      return response({
        page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
        List: [account],
        exhausted: true,
        consumerEnabled: false,
      });
    if (path === `${base}/grants`)
      return response({ account, grants, consumerEnabled: false });
    if (path === `${base}/roles`)
      return response({
        roles: [{ roleId, roleName: roleId, memberCount: 3 }],
        consumerEnabled: false,
      });
    if (path === `${base}/effective`)
      return response({
        accountId,
        domainId,
        purpose: credentialPurpose,
        explicitlyGranted: grants.some((g) => g.allowed),
        eligible: grants.some((g) => g.allowed),
        grantRevision: grants[0]?.grantRevision ?? "0",
        accountCredentialRevision: "2",
        consumerEnabled: false,
      });
    if (path === `${base}/grant`) {
      grants = [grant()];
      return response({ ...receipt(), consumerEnabled: false });
    }
    if (path === `${base}/revoke`) {
      grants = [grant({ allowed: false, grantRevision: "9007199254740995" })];
      return response({
        ...receipt({
          operation: "revoke",
          allowed: false,
          currentAllowed: false,
          grantRevision: "9007199254740995",
          currentGrantRevision: "9007199254740995",
        }),
        consumerEnabled: false,
      });
    }
    if (path === `${base}/mutation`)
      return response({
        receipt: receipt({ replayed: true }),
        consumerEnabled: false,
      });
    throw new Error(`Unexpected test path ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
describe("credential governance contract", () => {
  it("accepts installed capability separately from own-role eligibility", async () => {
    override = (url) =>
      url.startsWith("/api/credential-use/effective")
        ? response({
            accountId,
            domainId,
            purpose: credentialPurpose,
            explicitlyGranted: false,
            eligible: false,
            grantRevision: "0",
            accountCredentialRevision: account.credentialRevision,
            consumerEnabled: true,
          })
        : undefined;
    await expect(
      credentialUseAPI.effective(accountId, 1, signal()),
    ).resolves.toMatchObject({ consumerEnabled: true, eligible: false });
  });
  it("strictly rejects unknown, secret, null, numeric revision and inconsistent fields", () => {
    expect(validCredentialAccount(account)).toBe(true);
    expect(validCredentialGrant(grant())).toBe(true);
    expect(validCredentialReceipt(receipt())).toBe(true);
    for (const value of [
      { ...account, password: "forbidden" },
      { ...account, revision: 1 },
      { ...account, label: null },
      { ...account, credentialRevision: "9007199254740994" },
    ])
      expect(validCredentialAccount(value)).toBe(false);
    for (const value of [
      { ...grant(), purpose: "arbitrary_query" },
      { ...grant(), grantRevision: "01" },
      { ...grant(), roleId: "viewer" },
      { ...grant(), actorPassword: "secret" },
    ])
      expect(validCredentialGrant(value)).toBe(false);
    for (const value of [
      { ...receipt(), currentGrantRevision: "0" },
      { ...receipt(), accountDeleted: true },
      { ...receipt(), operation: "revoke" },
      { ...receipt(), currentGrantRevision: "3" },
      { ...receipt(), unknown: true },
    ])
      expect(validCredentialReceipt(value)).toBe(false);
    expect(
      validCredentialReceipt(
        receipt({
          accountDeleted: true,
          currentAllowed: false,
          currentGrantRevision: "0",
        }),
      ),
    ).toBe(true);
  });
  it("binds every successful route to the current actor header", async () => {
    const routes = [
      () => credentialUseAPI.accounts("", 1, signal()),
      () => credentialUseAPI.grants(accountId, 1, signal()),
      () => credentialUseAPI.roles(accountId, 1, signal()),
      () => credentialUseAPI.effective(accountId, 1, signal()),
      () => credentialUseAPI.receipt("original-key", 1, signal()),
      () =>
        credentialUseAPI.mutate(
          "grant",
          pending(),
          { actorPassword: "proof", totpCode: "123456" },
          "csrf",
          1,
          signal(),
        ),
    ];
    for (const actor of [2, null]) {
      override = () => response({}, 200, actor);
      for (const call of routes)
        await expect(call()).rejects.toMatchObject({
          code: "credential_use_identity_changed",
        });
    }
  });
  it("rejects duplicate grants, stale allow binding, oversized views and nonboolean capability", async () => {
    for (const body of [
      { account, grants: [grant(), grant()], consumerEnabled: false },
      {
        account,
        grants: [grant({ accountCredentialRevision: "1" })],
        consumerEnabled: false,
      },
      { account, grants: [], consumerEnabled: "true" },
      { account, grants: [], consumerEnabled: false, credentials: {} },
      { account, grants: Array(1001).fill(grant()), consumerEnabled: false },
    ]) {
      override = () => response(body);
      await expect(
        credentialUseAPI.grants(accountId, 1, signal()),
      ).rejects.toMatchObject({ code: "invalid_response" });
    }
  });
  it("rejects ineligible effective contradictions and invalid role member counts", async () => {
    override = () =>
      response({
        accountId,
        domainId,
        purpose: credentialPurpose,
        explicitlyGranted: false,
        eligible: true,
        grantRevision: "0",
        accountCredentialRevision: "2",
        consumerEnabled: false,
      });
    await expect(
      credentialUseAPI.effective(accountId, 1, signal()),
    ).rejects.toMatchObject({ code: "invalid_response" });
    for (const memberCount of [-1, 1.5, "3", null]) {
      override = () =>
        response({
          roles: [{ roleId, roleName: roleId, memberCount }],
          consumerEnabled: false,
        });
      await expect(
        credentialUseAPI.roles(accountId, 1, signal()),
      ).rejects.toMatchObject({ code: "invalid_response" });
    }
  });
  it("rejects mismatched receipt identity and malformed pagination", async () => {
    override = () =>
      response({
        ...receipt({ accountId: "different" }),
        consumerEnabled: false,
      });
    await expect(
      credentialUseAPI.mutate(
        "grant",
        pending(),
        { actorPassword: "proof", totpCode: "123456" },
        "csrf",
        1,
        signal(),
      ),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () =>
      response({
        page: { pageIdx: 1, pageSize: 100, total: 1, totalPage: 1 },
        List: [account],
        exhausted: true,
        consumerEnabled: false,
      });
    await expect(
      credentialUseAPI.accounts("", 1, signal()),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("stores only nonsecret intent bound to the originating actor", () => {
    saveCredentialIntent(profile, {
      ...pending(),
      actorPassword: "not persisted",
      totpCode: "123456",
      label: "not persisted",
    } as CredentialIntent);
    expect(readCredentialIntent(profile)).toEqual(pending());
    expect(readCredentialIntent({ ...profile, ID: 2 })).toBeNull();
    const stored = JSON.stringify({ ...sessionStorage });
    expect(stored).not.toMatch(
      /not persisted|123456|actorPassword|totpCode|label/,
    );
    expect(JSON.parse(Object.values(sessionStorage)[0])).toEqual({
      owner: "1:admin",
      intent: pending(),
    });
  });
  it("uses safe governance errors for account reset and access management", () => {
    expect(messages.credential_use_governance_required).toContain("平台管理员");
    expect(
      errorText(new ApiError("credential_use_governance_required")),
    ).toContain("平台管理员");
    expect(errorText(new ApiError("raw PostgreSQL secret"))).not.toContain(
      "PostgreSQL",
    );
    expect(
      credentialUseError(new ApiError("raw PostgreSQL secret")),
    ).not.toContain("PostgreSQL");
  });
});
describe("credential governance views", () => {
  it.each(["__proto__", "constructor", "toString", "hasOwnProperty"])(
    "renders a safe fallback for prototype-key error %s",
    async (code) => {
      const fallback = "凭据授权请求失败，请重新读取服务器状态后核对。";
      expect(credentialUseError(new ApiError(code, 400))).toBe(fallback);
      override = (url) =>
        url.split("?")[0] === `${base}/accounts`
          ? response({ error: code }, 400)
          : undefined;
      render(
        <CredentialUseWorkspace profile={profile} sessionChanged={vi.fn()} />,
      );
      expect(await screen.findByRole("alert")).toHaveTextContent(fallback);
      expect(screen.queryByRole("table")).toBeNull();
    },
  );
  it("shows default admin deny and disabled consumer without execution controls", async () => {
    await panel();
    expect(await screen.findByText("否（默认拒绝）")).toBeVisible();
    expect(
      screen.getByText(
        "没有显式使用授权，所有角色（包括平台管理员）默认拒绝。",
      ),
    ).toBeVisible();
    expect(
      screen.queryByRole("button", { name: /执行|测试连接|启动任务/ }),
    ).not.toBeInTheDocument();
  });
  it("requires role-wide confirmation and fresh proof, sends pinned revisions, clears proof", async () => {
    await grantForm();
    expect(
      screen.getByText("当前成员数：3。此操作影响该角色当前及未来全部成员。"),
    ).toBeVisible();
    fireEvent.submit(
      screen
        .getByRole("heading", { name: "确认角色凭据授权" })
        .closest("form")!,
    );
    expect(calls("grant")).toHaveLength(0);
    proof();
    click("确认授予角色使用权");
    await screen.findByRole("heading", { name: "凭据授权回执" });
    expect(JSON.parse(calls("grant")[0][1].body)).toEqual({
      accountId,
      roleId,
      purpose: credentialPurpose,
      expectedAccountRevision: account.revision,
      expectedCredentialRevision: "2",
      expectedGrantRevision: "0",
      idempotencyKey: expect.any(String),
      actorPassword: "Synthetic actor password 482",
      totpCode: "123456",
    });
    expect(calls("grant")[0][1]).toMatchObject({
      redirect: "error",
      cache: "no-store",
      headers: { "X-CSRF-Token": profile.csrfToken },
    });
    expect(readCredentialIntent(profile)).toBeNull();
    expect(
      screen.queryByLabelText("操作者当前密码", { exact: true }),
    ).not.toBeInTheDocument();
    expect(await screen.findAllByText("是", { selector: "dd" })).toHaveLength(
      2,
    );
  });
  it.each(["tenant_expired", "tenant_domain_limit_exceeded"])(
    "keeps safe revoke available when eligible roles fail with %s",
    async (code) => {
      grants = [grant()];
      override = (url) =>
        url.includes("/roles?") ? response({ error: code }, 403) : undefined;
      await panel();
      await screen.findByText(
        "当前无法确认可新增授权的角色；已有授权仍可按上方列表撤销。",
      );
      expect(
        screen.getByRole("button", { name: "审阅角色授权" }),
      ).toBeDisabled();
      click("撤销 platform_admin");
      expect(
        screen.getByText(
          "当前成员数不可读取。此操作影响该角色当前及未来全部成员。",
        ),
      ).toBeVisible();
      proof("撤销");
      click("确认撤销角色使用权");
      await screen.findByRole("heading", { name: "凭据授权回执" });
      expect(JSON.parse(calls("revoke")[0][1].body).expectedGrantRevision).toBe(
        "9007199254740994",
      );
      expect(calls("grant")).toHaveLength(0);
    },
  );
  it("recovers a lost POST response by GET receipt with the same key and no replay", async () => {
    let recover = false;
    override = (url) =>
      url === `${base}/grant`
        ? Promise.reject(new TypeError("network"))
        : url.includes("/mutation?") && !recover
          ? response({ error: "not_found" }, 404)
          : undefined;
    await grantForm();
    proof();
    click("确认授予角色使用权");
    await screen.findByRole("heading", { name: "核对未确认的凭据授权" });
    const intent = readCredentialIntent(profile)!;
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "查询原授权回执" }),
      ).toBeEnabled(),
    );
    expect(
      screen.queryByLabelText("操作者当前密码", { exact: true }),
    ).not.toBeInTheDocument();
    recover = true;
    click("查询原授权回执");
    await screen.findByRole("heading", { name: "凭据授权回执" });
    expect(calls("grant")).toHaveLength(1);
    expect(
      calls("mutation").every(
        ([url]) =>
          new URL(url, "http://localhost").searchParams.get(
            "idempotencyKey",
          ) === intent.idempotencyKey,
      ),
    ).toBe(true);
    expect(readCredentialIntent(profile)).toBeNull();
  });
  it("retains intent on session loss and invalidates the visible actor state", async () => {
    override = (url) =>
      url === `${base}/grant`
        ? response({ error: "unauthenticated" }, 401)
        : undefined;
    const changed = vi.fn();
    await panel(profile, changed);
    await waitFor(() =>
      expect(screen.getByLabelText("授权目标角色")).toBeEnabled(),
    );
    fill("授权目标角色", roleId);
    click("审阅角色授权");
    proof();
    click("确认授予角色使用权");
    await waitFor(() => expect(changed).toHaveBeenCalledOnce());
    expect(readCredentialIntent(profile)).not.toBeNull();
    expect(
      screen.queryByRole("heading", { name: "角色使用授权管理" }),
    ).not.toBeInTheDocument();
  });
  it("rejects another actor’s late successful response and refreshes session", async () => {
    override = (url) =>
      url.includes("/effective?") ? response({}, 200, 2) : undefined;
    const changed = vi.fn();
    await panel(profile, changed);
    await waitFor(() => expect(changed).toHaveBeenCalled());
    expect(screen.queryByText("否（默认拒绝）")).not.toBeInTheDocument();
  });
  it("cancels a pending mutation when the panel closes and preserves only its safe intent", async () => {
    let resolve!: (r: Response) => void;
    override = (url) =>
      url === `${base}/grant`
        ? new Promise<Response>((done) => {
            resolve = done;
          })
        : undefined;
    await grantForm();
    proof();
    click("确认授予角色使用权");
    click("收起凭据使用授权");
    expect(calls("grant")[0][1].signal.aborted).toBe(true);
    await act(async () => {
      resolve(await response({ ...receipt(), consumerEnabled: false }));
    });
    expect(
      screen.queryByRole("heading", { name: "凭据授权回执" }),
    ).not.toBeInTheDocument();
    expect(readCredentialIntent(profile)).not.toBeNull();
    expect(JSON.stringify({ ...sessionStorage })).not.toContain(
      "Synthetic actor password",
    );
  });
  it("preserves actor-bound intent on browser Back and ignores a late grant response before GET recovery", async () => {
    window.history.replaceState({}, "", "#account");
    let finishGrant!: (r: Response) => void;
    override = (url) =>
      url === `${base}/grant`
        ? new Promise<Response>((resolve) => {
            finishGrant = resolve;
          })
        : undefined;
    render(<App />);
    await screen.findByRole("navigation", { name: "账户设置" });
    await revealNavigation("凭据授权清理");
    await screen.findByRole("button", { name: "凭据授权清理" });
    click("凭据授权清理");
    await screen.findByRole("table", { name: "具有已保存使用授权的账户" });
    click("清理 测试凭据");
    await waitFor(() =>
      expect(screen.getByLabelText("授权目标角色")).toBeEnabled(),
    );
    fill("授权目标角色", roleId);
    click("审阅角色授权");
    proof();
    click("确认授予角色使用权");
    const intent = readCredentialIntent(profile)!;
    expect(intent).toEqual(pending({ idempotencyKey: expect.any(String) }));
    expect(readCredentialIntent({ ...profile, ID: 2 })).toBeNull();
    act(() => window.history.back());
    await waitFor(() => expect(window.location.hash).toBe("#account"));
    await screen.findByRole("heading", { name: "账户概览" });
    expect(calls("grant")[0][1].signal.aborted).toBe(true);
    expect(
      screen.queryByLabelText("操作者当前密码", { exact: true }),
    ).not.toBeInTheDocument();
    await act(async () => {
      finishGrant(await response({ ...receipt(), consumerEnabled: false }));
    });
    expect(
      screen.queryByRole("heading", { name: "凭据授权回执" }),
    ).not.toBeInTheDocument();
    expect(readCredentialIntent(profile)).toEqual(intent);
    expect(calls("grant")).toHaveLength(1);
    expect(calls("mutation")).toHaveLength(0);
    const saved = JSON.parse(Object.values(sessionStorage)[0]);
    expect(saved).toEqual({ owner: "1:admin", intent });
    expect(JSON.stringify(saved)).not.toMatch(
      /Synthetic actor password|123456|actorPassword|totpCode/,
    );
    await revealNavigation("凭据授权清理");
    click("凭据授权清理");
    await screen.findByRole("heading", { name: "凭据授权回执" });
    expect(calls("mutation")).toHaveLength(1);
    expect(
      new URL(calls("mutation")[0][0], "http://localhost").searchParams.get(
        "idempotencyKey",
      ),
    ).toBe(intent.idempotencyKey);
    expect(calls("mutation")[0][1].method).toBe("GET");
    expect(calls("grant")).toHaveLength(1);
    expect(readCredentialIntent(profile)).toBeNull();
  });
  it("clears a revision-conflicted grant form and requires a fresh read and explicit role selection without replay", async () => {
    let finishRead!: (r: Response) => void;
    const currentAccount = {
      ...account,
      revision: "9007199254740994",
      credentialRevision: "3",
    };
    override = (url) => {
      if (url === `${base}/grant`)
        return response({ error: "revision_conflict" }, 409);
      if (url.startsWith(`${base}/grants?`) && calls("grants").length > 1)
        return new Promise<Response>((resolve) => {
          finishRead = resolve;
        });
      return undefined;
    };
    await grantForm();
    proof();
    click("确认授予角色使用权");
    await screen.findByText(
      "账户、凭据或授权版本已改变。已清除旧表单，请重新读取授权状态后重新选择角色。",
    );
    expect(readCredentialIntent(profile)).toBeNull();
    expect(
      screen.queryByLabelText("操作者当前密码", { exact: true }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText("未使用的认证器验证码", { exact: true }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("checkbox", {
        name: "我已核对目标角色，确认授权覆盖当前及未来全部成员",
      }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "审阅角色授权" }),
    ).not.toBeInTheDocument();
    expect(calls("grant")).toHaveLength(1);
    expect(calls("grants")).toHaveLength(1);
    expect(calls("mutation")).toHaveLength(0);
    click("重新读取授权状态");
    await waitFor(() => expect(calls("grants")).toHaveLength(2));
    expect(
      screen.queryByRole("button", { name: "审阅角色授权" }),
    ).not.toBeInTheDocument();
    await act(async () => {
      finishRead(
        await response({
          account: currentAccount,
          grants: [grant({ allowed: false })],
          consumerEnabled: false,
        }),
      );
    });
    await waitFor(() =>
      expect(screen.getByLabelText("授权目标角色")).toBeEnabled(),
    );
    expect(screen.getByLabelText("授权目标角色")).toHaveValue("");
    expect(screen.getByRole("button", { name: "审阅角色授权" })).toBeDisabled();
    expect(calls("grant")).toHaveLength(1);
    fill("授权目标角色", roleId);
    click("审阅角色授权");
    expect(
      screen.getByLabelText("操作者当前密码", { exact: true }),
    ).toHaveValue("");
    expect(
      screen.getByLabelText("未使用的认证器验证码", { exact: true }),
    ).toHaveValue("");
    expect(
      screen.getByRole("checkbox", {
        name: "我已核对目标角色，确认授权覆盖当前及未来全部成员",
      }),
    ).not.toBeChecked();
    expect(
      screen.getByText("用途：domain.connection_test · 凭据版本：3"),
    ).toBeVisible();
    fireEvent.submit(
      screen
        .getByRole("heading", { name: "确认角色凭据授权" })
        .closest("form")!,
    );
    expect(calls("grant")).toHaveLength(1);
  });
  it("recovery validates all safe intent identity fields before clearing anything", async () => {
    saveCredentialIntent(profile, pending({ domainId: "different-domain" }));
    await panel();
    await screen.findByText(
      "授权结果无法安全确认，请查询原授权回执或重新读取。",
    );
    expect(readCredentialIntent(profile)?.domainId).toBe("different-domain");
    expect(
      screen.queryByRole("heading", { name: "凭据授权回执" }),
    ).not.toBeInTheDocument();
  });
  it("requires operation_accounts readable for a custom self-effective view", async () => {
    const custom = { ...profile, role: "custom-role" };
    readable = false;
    const view = await panel(custom);
    await screen.findByText("当前账户没有读取此凭据使用授权的权限。");
    expect(calls("effective")).toHaveLength(0);
    expect(calls("grants")).toHaveLength(0);
    view.unmount();
    readable = true;
    await panel(custom);
    await screen.findByText("否（默认拒绝）");
    expect(calls("effective")).toHaveLength(1);
    expect(calls("grants")).toHaveLength(0);
  });
  it("never opens admin reads for custom actors even if path checks are stale", async () => {
    render(
      <CredentialUseWorkspace
        profile={{ ...profile, role: "custom-role" }}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByText("当前账户没有凭据授权清理权限。");
    expect(calls("accounts")).toHaveLength(0);
    expect(calls("grants")).toHaveLength(0);
  });
  it("offers cleanup outside ordinary account metadata and live tenant reads", async () => {
    readable = false;
    grants = [grant()];
    override = (url) =>
      url.includes("/roles?")
        ? response({ error: "tenant_expired" }, 403)
        : undefined;
    render(
      <CredentialUseWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByRole("table", { name: "具有已保存使用授权的账户" });
    click("清理 测试凭据");
    await screen.findByRole("button", { name: "撤销 platform_admin" });
    expect(calls("effective")).toHaveLength(0);
    expect(
      fetcher.mock.calls.some(([url]) =>
        url.startsWith("/api/operation-accounts"),
      ),
    ).toBe(false);
    expect(screen.getByRole("button", { name: "审阅角色授权" })).toBeDisabled();
  });
  it("honors deny path checks for the cleanup catalogue", async () => {
    paths[0] = false;
    render(
      <CredentialUseWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByText("当前账户没有凭据授权清理权限。");
    expect(calls("accounts")).toHaveLength(0);
  });
  it("makes admin cleanup reachable in the application shell", async () => {
    render(<App />);
    await screen.findByRole("navigation", { name: "账户设置" });
    await revealNavigation("凭据授权清理");
    await screen.findByRole("button", { name: "凭据授权清理" });
    click("凭据授权清理");
    await screen.findByRole("table", { name: "具有已保存使用授权的账户" });
    expect(
      within(screen.getByRole("navigation", { name: "账户设置" })).getByRole(
        "button",
        { name: "凭据授权清理" },
      ),
    ).toHaveAttribute("aria-current", "page");
  });
});
