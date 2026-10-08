// Synthetic DOM/transport tests. Real browser/API/database evidence lives in
// directory integration remains separate; these fixtures do not replace it.
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import DirectoryV2CredentialUseWorkspace, {
  DirectoryV2CredentialUsePanel,
} from "./DirectoryV2CredentialUseWorkspace";
import { ApiError, messages, type Profile } from "./api";
import { errorText } from "./access-api";
import { directoryV2BodyLimit } from "./directory-v2-json";
import {
  directoryV2CredentialPurpose,
  directoryV2CredentialUseAPI,
  directoryV2CredentialUseError,
  directoryV2CredentialUseOperations,
  validDirectoryV2CredentialAccount,
  validDirectoryV2CredentialGrant,
  validDirectoryV2CredentialReceipt,
  type DirectoryV2CredentialAccount,
  type DirectoryV2CredentialGrant,
  type DirectoryV2CredentialReceipt,
} from "./directory-v2-credential-use-api";
import {
  discardDirectoryV2CredentialIntent,
  readDirectoryV2CredentialIntent,
  saveDirectoryV2CredentialIntent,
  type DirectoryV2CredentialIntent,
} from "./directory-v2-credential-use-intent";
import {
  credentialPurpose as connectionPurpose,
  validCredentialGrant as validConnectionGrant,
  validCredentialReceipt as validConnectionReceipt,
} from "./credential-use-api";
import {
  discardCredentialIntent as discardConnectionIntent,
  readCredentialIntent as readConnectionIntent,
  saveCredentialIntent as saveConnectionIntent,
} from "./credential-use-intent";
import {
  directoryCredentialPurpose as legacyPurpose,
  validDirectoryCredentialGrant as validLegacyGrant,
  validDirectoryCredentialReceipt as validLegacyReceipt,
} from "./directory-credential-use-api";
import {
  discardDirectoryCredentialIntent as discardLegacyIntent,
  readDirectoryCredentialIntent as readLegacyIntent,
  saveDirectoryCredentialIntent as saveLegacyIntent,
} from "./directory-credential-use-intent";
const base = "/api/directory-credential-use/v2",
  accountId = "synthetic-account",
  domainId = "synthetic-domain",
  roleId = "platform_admin";
const account: DirectoryV2CredentialAccount = {
  accountId,
  domainId,
  domain: "synthetic.invalid",
  label: "测试凭据",
  revision: "9007199254740993",
  credentialRevision: "2",
};
const grant = (
  extra: Partial<DirectoryV2CredentialGrant> = {},
): DirectoryV2CredentialGrant => ({
  roleId,
  roleName: "platform_admin",
  purpose: directoryV2CredentialPurpose,
  allowed: true,
  grantRevision: "9007199254740994",
  accountCredentialRevision: "2",
  updatedAt: "2026-10-07T00:00:00Z",
  ...extra,
});
const receipt = (
  extra: Partial<DirectoryV2CredentialReceipt> = {},
): DirectoryV2CredentialReceipt => ({
  result: "SUCCESS",
  operation: "grant",
  accountId,
  domainId,
  roleId,
  purpose: directoryV2CredentialPurpose,
  allowed: true,
  grantRevision: "9007199254740994",
  accountCredentialRevision: "2",
  replayed: false,
  accountDeleted: false,
  currentGrantRevision: "9007199254740994",
  currentAllowed: true,
  ...extra,
});
const pending = (
  extra: Partial<DirectoryV2CredentialIntent> = {},
): DirectoryV2CredentialIntent => ({
  kind: "grant",
  accountId,
  domainId,
  roleId,
  purpose: directoryV2CredentialPurpose,
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
  grants: DirectoryV2CredentialGrant[],
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
    <DirectoryV2CredentialUsePanel
      profile={p}
      sessionChanged={sessionChanged}
      accountId={accountId}
    />,
  );
  click("查看补充目录读取凭据使用授权");
  await waitFor(() =>
    expect(
      screen.queryByText("正在核对目录读取凭据授权权限…"),
    ).not.toBeInTheDocument(),
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
  discardConnectionIntent(profile);
  discardDirectoryV2CredentialIntent(profile);
  discardDirectoryV2CredentialIntent({ ...profile, ID: 2 });
  sessionStorage.clear();
  localStorage.clear();
  grants = [];
  paths = directoryV2CredentialUseOperations.map(() => true);
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
          { mark: "domains", auth: { readable, writeable: false } },
          { mark: "directory_assets", auth: { readable, writeable: false } },
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
        purpose: directoryV2CredentialPurpose,
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
      url.startsWith("/api/directory-credential-use/v2/effective")
        ? response({
            accountId,
            domainId,
            purpose: directoryV2CredentialPurpose,
            explicitlyGranted: false,
            eligible: false,
            grantRevision: "0",
            accountCredentialRevision: account.credentialRevision,
            consumerEnabled: true,
          })
        : undefined;
    await expect(
      directoryV2CredentialUseAPI.effective(accountId, 1, signal()),
    ).resolves.toMatchObject({ consumerEnabled: true, eligible: false });
  });
  it("strictly rejects unknown, secret, null, numeric revision and inconsistent fields", () => {
    expect(validDirectoryV2CredentialAccount(account)).toBe(true);
    expect(validDirectoryV2CredentialGrant(grant())).toBe(true);
    expect(validDirectoryV2CredentialReceipt(receipt())).toBe(true);
    for (const value of [
      { ...account, password: "forbidden" },
      { ...account, revision: 1 },
      { ...account, label: null },
      { ...account, credentialRevision: "9007199254740994" },
    ])
      expect(validDirectoryV2CredentialAccount(value)).toBe(false);
    for (const value of [
      { ...grant(), purpose: "arbitrary_query" },
      { ...grant(), grantRevision: "01" },
      { ...grant(), roleId: "viewer" },
      { ...grant(), actorPassword: "secret" },
    ])
      expect(validDirectoryV2CredentialGrant(value)).toBe(false);
    for (const value of [
      { ...receipt(), currentGrantRevision: "0" },
      { ...receipt(), accountDeleted: true },
      { ...receipt(), operation: "revoke" },
      { ...receipt(), currentGrantRevision: "3" },
      { ...receipt(), unknown: true },
    ])
      expect(validDirectoryV2CredentialReceipt(value)).toBe(false);
    expect(
      validDirectoryV2CredentialReceipt(
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
      () => directoryV2CredentialUseAPI.accounts("", 1, signal()),
      () => directoryV2CredentialUseAPI.grants(accountId, 1, signal()),
      () => directoryV2CredentialUseAPI.roles(accountId, 1, signal()),
      () => directoryV2CredentialUseAPI.effective(accountId, 1, signal()),
      () => directoryV2CredentialUseAPI.receipt("original-key", 1, signal()),
      () =>
        directoryV2CredentialUseAPI.mutate(
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
        directoryV2CredentialUseAPI.grants(accountId, 1, signal()),
      ).rejects.toMatchObject({ code: "invalid_response" });
    }
  });
  it("rejects ineligible effective contradictions and invalid role member counts", async () => {
    override = () =>
      response({
        accountId,
        domainId,
        purpose: directoryV2CredentialPurpose,
        explicitlyGranted: false,
        eligible: true,
        grantRevision: "0",
        accountCredentialRevision: "2",
        consumerEnabled: false,
      });
    await expect(
      directoryV2CredentialUseAPI.effective(accountId, 1, signal()),
    ).rejects.toMatchObject({ code: "invalid_response" });
    for (const memberCount of [-1, 1.5, "3", null]) {
      override = () =>
        response({
          roles: [{ roleId, roleName: roleId, memberCount }],
          consumerEnabled: false,
        });
      await expect(
        directoryV2CredentialUseAPI.roles(accountId, 1, signal()),
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
      directoryV2CredentialUseAPI.mutate(
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
      directoryV2CredentialUseAPI.accounts("", 1, signal()),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("stores only nonsecret intent bound to the originating actor", () => {
    saveDirectoryV2CredentialIntent(profile, {
      ...pending(),
      actorPassword: "not persisted",
      totpCode: "123456",
      label: "not persisted",
    } as DirectoryV2CredentialIntent);
    expect(readDirectoryV2CredentialIntent(profile)).toEqual(pending());
    expect(readDirectoryV2CredentialIntent({ ...profile, ID: 2 })).toBeNull();
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
      directoryV2CredentialUseError(new ApiError("raw PostgreSQL secret")),
    ).not.toContain("PostgreSQL");
  });
});
describe("credential governance views", () => {
  it.each(["__proto__", "constructor", "toString", "hasOwnProperty"])(
    "renders a safe fallback for prototype-key error %s",
    async (code) => {
      const fallback = "凭据授权请求失败，请重新读取服务器状态后核对。";
      expect(directoryV2CredentialUseError(new ApiError(code, 400))).toBe(
        fallback,
      );
      override = (url) =>
        url.split("?")[0] === `${base}/accounts`
          ? response({ error: code }, 400)
          : undefined;
      render(
        <DirectoryV2CredentialUseWorkspace
          profile={profile}
          sessionChanged={vi.fn()}
        />,
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
        .getByRole("heading", { name: "确认角色目录读取凭据授权" })
        .closest("form")!,
    );
    expect(calls("grant")).toHaveLength(0);
    proof();
    click("确认授予角色使用权");
    await screen.findByRole("heading", { name: "目录读取凭据授权回执" });
    expect(JSON.parse(calls("grant")[0][1].body)).toEqual({
      accountId,
      roleId,
      purpose: directoryV2CredentialPurpose,
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
    expect(readDirectoryV2CredentialIntent(profile)).toBeNull();
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
      await screen.findByRole("heading", { name: "目录读取凭据授权回执" });
      expect(JSON.parse(calls("revoke")[0][1].body).expectedGrantRevision).toBe(
        "9007199254740994",
      );
      expect(calls("grant")).toHaveLength(0);
    },
  );
  it.each(["lost", "oversized"])(
    "recovers an uncertain %s POST response by GET receipt with the same key and no replay",
    async (failure) => {
      let recover = false;
      override = (url) =>
        url === `${base}/grant`
          ? failure === "lost"
            ? Promise.reject(new TypeError("network"))
            : Promise.resolve(
                new Response("{}", {
                  headers: {
                    "X-ADTR-User-ID": "1",
                    "Content-Length": String(directoryV2BodyLimit + 1),
                  },
                }),
              )
          : url.includes("/mutation?") && !recover
            ? response({ error: "not_found" }, 404)
            : undefined;
      await grantForm();
      proof();
      click("确认授予角色使用权");
      await screen.findByRole("heading", {
        name: "核对未确认的目录读取凭据授权",
      });
      const intent = readDirectoryV2CredentialIntent(profile)!;
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
      await screen.findByRole("heading", { name: "目录读取凭据授权回执" });
      expect(calls("grant")).toHaveLength(1);
      expect(
        calls("mutation").every(
          ([url]) =>
            new URL(url, "http://localhost").searchParams.get(
              "idempotencyKey",
            ) === intent.idempotencyKey,
        ),
      ).toBe(true);
      expect(readDirectoryV2CredentialIntent(profile)).toBeNull();
    },
  );
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
    expect(readDirectoryV2CredentialIntent(profile)).not.toBeNull();
    expect(
      screen.queryByRole("heading", { name: "角色目录读取授权管理" }),
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
    click("收起补充目录读取凭据使用授权");
    expect(calls("grant")[0][1].signal.aborted).toBe(true);
    await act(async () => {
      resolve(await response({ ...receipt(), consumerEnabled: false }));
    });
    expect(
      screen.queryByRole("heading", { name: "目录读取凭据授权回执" }),
    ).not.toBeInTheDocument();
    expect(readDirectoryV2CredentialIntent(profile)).not.toBeNull();
    expect(JSON.stringify({ ...sessionStorage })).not.toContain(
      "Synthetic actor password",
    );
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
    expect(readDirectoryV2CredentialIntent(profile)).toBeNull();
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
      screen.getByText("用途：domain.directory_read.v2 · 凭据版本：3"),
    ).toBeVisible();
    fireEvent.submit(
      screen
        .getByRole("heading", { name: "确认角色目录读取凭据授权" })
        .closest("form")!,
    );
    expect(calls("grant")).toHaveLength(1);
  });
  it("recovery validates all safe intent identity fields before clearing anything", async () => {
    saveDirectoryV2CredentialIntent(
      profile,
      pending({ domainId: "different-domain" }),
    );
    await panel();
    await screen.findByText(
      "授权结果无法安全确认，请查询原授权回执或重新读取。",
    );
    expect(readDirectoryV2CredentialIntent(profile)?.domainId).toBe(
      "different-domain",
    );
    expect(
      screen.queryByRole("heading", { name: "目录读取凭据授权回执" }),
    ).not.toBeInTheDocument();
  });
  it("requires domains and directory_assets readable for a custom self-effective view", async () => {
    const custom = { ...profile, role: "custom-role" };
    readable = false;
    const view = await panel(custom);
    await screen.findByText(
      "当前账户没有读取此补充目录读取凭据使用授权的权限。",
    );
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
      <DirectoryV2CredentialUseWorkspace
        profile={{ ...profile, role: "custom-role" }}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByText("当前账户没有补充目录读取凭据授权清理权限。");
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
      <DirectoryV2CredentialUseWorkspace
        profile={profile}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByRole("table", { name: "具有已保存目录读取授权的账户" });
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
      <DirectoryV2CredentialUseWorkspace
        profile={profile}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByText("当前账户没有补充目录读取凭据授权清理权限。");
    expect(calls("accounts")).toHaveLength(0);
  });
});

describe("directory purpose isolation", () => {
  it("projects the fixed v2 grant wire input without caller profile selectors or private extras", async () => {
    const input = {
      ...pending(),
      dictionaryVersion: 1,
      rawCredential: "DO_NOT_TRANSMIT",
    };
    await directoryV2CredentialUseAPI.mutate(
      "grant",
      input,
      { actorPassword: "proof", totpCode: "123456" },
      "csrf",
      1,
      signal(),
    );
    const sent = JSON.parse(calls("grant")[0][1].body);
    expect(Object.keys(sent).sort()).toEqual(
      [
        "accountId",
        "roleId",
        "purpose",
        "expectedAccountRevision",
        "expectedCredentialRevision",
        "expectedGrantRevision",
        "idempotencyKey",
        "actorPassword",
        "totpCode",
      ].sort(),
    );
    expect(sent.purpose).toBe("domain.directory_read.v2");
    expect(JSON.stringify(sent)).not.toContain("DO_NOT_TRANSMIT");
  });
  it("never treats legacy directory grants or recovery records as dictionary 2 authority", async () => {
    const legacy = {
      ...pending(),
      purpose: legacyPurpose,
      idempotencyKey: "legacy-grant-key",
    };
    discardLegacyIntent(profile);
    saveLegacyIntent(profile, legacy);
    expect(readDirectoryV2CredentialIntent(profile)).toBeNull();
    saveDirectoryV2CredentialIntent(profile, pending());
    expect(readLegacyIntent(profile)).toEqual(legacy);
    expect(readDirectoryV2CredentialIntent(profile)).toEqual(pending());
    discardDirectoryV2CredentialIntent(profile);
    expect(readLegacyIntent(profile)).toEqual(legacy);
    expect(
      validDirectoryV2CredentialGrant({ ...grant(), purpose: legacyPurpose }),
    ).toBe(false);
    expect(
      validDirectoryV2CredentialReceipt({
        ...receipt(),
        purpose: legacyPurpose,
      }),
    ).toBe(false);
    expect(validLegacyGrant(grant())).toBe(false);
    expect(validLegacyReceipt(receipt())).toBe(false);
    await expect(
      directoryV2CredentialUseAPI.mutate(
        "grant",
        legacy as unknown as DirectoryV2CredentialIntent,
        { actorPassword: "proof", totpCode: "123456" },
        "csrf",
        1,
        signal(),
      ),
    ).rejects.toMatchObject({ code: "invalid_input" });
    expect(fetcher).not.toHaveBeenCalled();
    discardLegacyIntent(profile);
  });
  it("requires exact v2 purpose for self-effective results and unknown-result receipts", async () => {
    override = () =>
      response({
        accountId,
        domainId,
        purpose: legacyPurpose,
        explicitlyGranted: true,
        eligible: true,
        grantRevision: "1",
        accountCredentialRevision: "2",
        consumerEnabled: true,
      });
    await expect(
      directoryV2CredentialUseAPI.effective(accountId, 1, signal()),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () =>
      response({
        receipt: { ...receipt(), purpose: legacyPurpose, replayed: true },
        consumerEnabled: true,
      });
    await expect(
      directoryV2CredentialUseAPI.receipt("original-key", 1, signal()),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("never parses grants, effective status or receipts for connection testing", async () => {
    const otherGrant = { ...grant(), purpose: connectionPurpose };
    const otherReceipt = { ...receipt(), purpose: connectionPurpose };
    expect(validDirectoryV2CredentialGrant(otherGrant)).toBe(false);
    expect(validDirectoryV2CredentialReceipt(otherReceipt)).toBe(false);
    expect(validConnectionGrant(grant())).toBe(false);
    expect(validConnectionReceipt(receipt())).toBe(false);
    override = () =>
      response({ account, grants: [otherGrant], consumerEnabled: true });
    await expect(
      directoryV2CredentialUseAPI.grants(accountId, 1, signal()),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () =>
      response({
        accountId,
        domainId,
        purpose: connectionPurpose,
        explicitlyGranted: false,
        eligible: false,
        grantRevision: "0",
        accountCredentialRevision: "2",
        consumerEnabled: true,
      });
    await expect(
      directoryV2CredentialUseAPI.effective(accountId, 1, signal()),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () =>
      response({
        receipt: { ...otherReceipt, replayed: true },
        consumerEnabled: true,
      });
    await expect(
      directoryV2CredentialUseAPI.receipt("original-key", 1, signal()),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () => response({ ...otherReceipt, consumerEnabled: true });
    await expect(
      directoryV2CredentialUseAPI.mutate(
        "grant",
        pending(),
        { actorPassword: "proof", totpCode: "123456" },
        "csrf",
        1,
        signal(),
      ),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("keeps directory and connection recovery buckets independent for the same actor", () => {
    const connectionIntent = {
      ...pending(),
      purpose: connectionPurpose,
      idempotencyKey: "connection-original-key",
    };
    saveConnectionIntent(profile, connectionIntent);
    expect(readDirectoryV2CredentialIntent(profile)).toBeNull();
    saveDirectoryV2CredentialIntent(profile, pending());
    expect(readConnectionIntent(profile)).toEqual(connectionIntent);
    expect(readDirectoryV2CredentialIntent(profile)).toEqual(pending());
    expect(
      sessionStorage.getItem("adtr.directory-credential-use-intent.v2:1:admin"),
    ).toContain("domain.directory_read.v2");
    discardDirectoryV2CredentialIntent(profile);
    expect(readConnectionIntent(profile)).toEqual(connectionIntent);
    expect(() =>
      saveDirectoryV2CredentialIntent(
        profile,
        connectionIntent as unknown as DirectoryV2CredentialIntent,
      ),
    ).toThrow("Invalid directory credential intent");
    sessionStorage.setItem(
      "adtr.directory-credential-use-intent.v2:1:admin",
      JSON.stringify({ owner: "1:admin", intent: connectionIntent }),
    );
    expect(readDirectoryV2CredentialIntent(profile)).toBeNull();
    expect(readConnectionIntent(profile)).toEqual(connectionIntent);
  });
  it.each(["domains", "directory_assets"])(
    "requires %s independently for the effective view",
    async (missing) => {
      override = (url) =>
        url === "/api/access/menu"
          ? response({
              menu: [
                {
                  mark: "domains",
                  auth: { readable: missing !== "domains", writeable: false },
                },
                {
                  mark: "directory_assets",
                  auth: {
                    readable: missing !== "directory_assets",
                    writeable: false,
                  },
                },
                {
                  mark: "operation_accounts",
                  auth: { readable: true, writeable: true },
                },
              ],
            })
          : undefined;
      await panel({ ...profile, role: "custom-role" });
      await screen.findByText(
        "当前账户没有读取此补充目录读取凭据使用授权的权限。",
      );
      expect(calls("effective")).toHaveLength(0);
      expect(calls("grants")).toHaveLength(0);
    },
  );
  it("describes compiled capability without claiming the deployment is enabled or starting sync", async () => {
    override = (url) =>
      url.startsWith(`${base}/effective?`)
        ? response({
            accountId,
            domainId,
            purpose: directoryV2CredentialPurpose,
            explicitlyGranted: false,
            eligible: false,
            grantRevision: "0",
            accountCredentialRevision: "2",
            consumerEnabled: true,
          })
        : undefined;
    await panel();
    expect(
      await screen.findByText(
        "当前程序已编译目录读取能力；此状态不表示部署开关已开启，实际同步仍取决于部署配置、域范围和显式用途授权。",
      ),
    ).toBeVisible();
    expect(
      screen.getByText(
        "保存目录读取授权不会读取凭据、连接域或启动同步；目录同步需要另行明确提交。",
      ),
    ).toBeVisible();
    expect(
      fetcher.mock.calls.some(
        ([url]) =>
          url.startsWith("/api/credential-use") ||
          url.startsWith("/api/directory/v2/sync"),
      ),
    ).toBe(false);
  });
});
