import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { createHmac } from "node:crypto";

type Authenticator = { secret: string; lastCounter: number };
import type { CredentialReceipt } from "../src/credential-use-api";

function totp(secret: string, now = Date.now()): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const char of secret.replace(/=+$/, "").toUpperCase()) {
    const index = alphabet.indexOf(char);
    if (index < 0) throw new Error("Invalid synthetic TOTP secret");
    bits += index.toString(2).padStart(5, "0");
  }
  const bytes = [];
  for (let offset = 0; offset + 8 <= bits.length; offset += 8)
    bytes.push(parseInt(bits.slice(offset, offset + 8), 2));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(now / 30_000)));
  const digest = createHmac("sha1", Buffer.from(bytes))
    .update(counter)
    .digest();
  const offset = digest[digest.length - 1] & 15;
  return ((digest.readUInt32BE(offset) & 0x7fffffff) % 1_000_000)
    .toString()
    .padStart(6, "0");
}

async function freshCode(authenticator: Authenticator): Promise<string> {
  // Each privileged write and MFA login gets a new real counter, including
  // rejected writes. Never bypass replay protection or freeze the server clock.
  const earliest = (authenticator.lastCounter + 1) * 30_000 + 1100;
  while (Date.now() < earliest)
    await new Promise((resolve) => setTimeout(resolve, earliest - Date.now()));
  const now = Date.now();
  authenticator.lastCounter = Math.floor(now / 30_000);
  return totp(authenticator.secret, now);
}

async function login(page: Page, username: string, password: string) {
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function enrollMfa(page: Page, password: string): Promise<Authenticator> {
  await page.getByRole("button", { name: "多因素认证", exact: true }).click();
  await page.getByLabel("当前密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "开始设置", exact: true }).click();
  const secret = await page
    .getByLabel("设置密钥", { exact: true })
    .inputValue();
  expect(secret.length).toBeGreaterThan(10);
  const now = Date.now();
  await page
    .getByLabel("认证器验证码", { exact: true })
    .fill(totp(secret, now));
  await page.getByRole("button", { name: "验证并启用", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用");
  return { secret, lastCounter: Math.floor(now / 30_000) };
}

async function proof(page: Page, password: string, auth: Authenticator) {
  await page.getByLabel("操作者当前密码", { exact: true }).fill(password);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(auth));
}

async function readJSON(context: BrowserContext, path: string) {
  const response = await context.request.get(path);
  expect(response.status(), `GET ${path}`).toBe(200);
  return response.json();
}

async function submit(page: Page, path: string, button: string, status = 200) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === path &&
        candidate.request().method() === "POST",
    ),
    page.getByRole("button", { name: button, exact: true }).click(),
  ]);
  expect(response.status(), `POST ${path}`).toBe(status);
  return {
    value: await response.json(),
    sent: response.request().postDataJSON(),
  };
}

// An isolated fresh database and key-only runtime are required. Domain/account,
// resource scope, proof, grants and receipts all use the actual authenticated
// API. The only network fault is dropping a real committed response; no API
// body or database outcome is mocked. This governance-only suite starts no LDAP worker; installed capability does not imply execution.
// Never retain credential-bearing snapshots, traces or videos.
test.use({ trace: "off", screenshot: "off", video: "off" });
test("default admin deny, explicit role grant, receipt recovery, expired-tenant cleanup and no execution", async ({
  page,
  context,
}) => {
  test.setTimeout(480_000);
  const username = process.env.ADTR_E2E_USERNAME,
    bootstrapPassword = process.env.ADTR_E2E_PASSWORD;
  if (!username || !bootstrapPassword || !process.env.ADTR_E2E_BASE_URL)
    throw new Error(
      "A fresh synthetic bootstrap account and ADTR_E2E_BASE_URL are required",
    );
  const password = "Changed Governance Password 123",
    domain = "synthetic.invalid",
    group = "E2ECredentialGovernance",
    label = "Governed synthetic credential";
  const domainUser = "domain-fixture@synthetic.invalid",
    domainPassword = "Synthetic domain credential 481";
  const accountUser = "operation-fixture@synthetic.invalid",
    accountPassword = "Synthetic account credential 739";
  const secrets = [
    bootstrapPassword,
    password,
    domainUser,
    domainPassword,
    accountUser,
    accountPassword,
  ];
  const safe = (value: unknown) => {
    const serialized = JSON.stringify(value);
    for (const secret of secrets)
      expect(serialized.includes(secret), "write-only credential leaked").toBe(
        false,
      );
    expect(
      /"(?:username|password|actorPassword|totpCode|ciphertext|userInfo)"\s*:/u.test(
        serialized,
      ),
      "secret-bearing public field",
    ).toBe(false);
  };
  const safeBrowser = async () => {
    const state = await page.evaluate(async () => ({
      url: location.href,
      history: history.state,
      local: { ...localStorage },
      session: { ...sessionStorage },
      databases: await indexedDB.databases(),
      text: document.body.innerText,
      inputs: Array.from(document.querySelectorAll("input")).map(
        (input) => input.value,
      ),
    }));
    safe(state);
    expect(state.local).toEqual({});
    expect(state.databases).toEqual([]);
    return state;
  };
  const consoles: string[] = [];
  page.on("console", (m) => consoles.push(m.text()));
  page.on("pageerror", (e) => consoles.push(e.message));
  let grantPosts = 0,
    revokePosts = 0;
  const remoteRequests: string[] = [];
  context.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    if (request.method() !== "POST") return;
    if (path === "/api/credential-use/grant") grantPosts++;
    if (path === "/api/credential-use/revoke") revokePosts++;
    if (
      path === "/api/domains/test" ||
      path.startsWith("/api/tasks/") ||
      path === "/api/credential-use/execute"
    )
      remoteRequests.push(path);
  });
  await page.goto("/");
  await login(page, username, bootstrapPassword);
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await page.getByLabel("当前密码", { exact: true }).fill(bootstrapPassword);
  await page.getByLabel("新密码", { exact: true }).fill(password);
  await page.getByLabel("确认新密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  const auth = await enrollMfa(page, password);
  secrets.push(auth.secret);
  const tenant = await readJSON(context, "/api/resources/tenant");
  expect(tenant.maxAdCount).toBe(2);
  expect(tenant.expireTime).toBeGreaterThan(Math.floor(Date.now() / 1000));
  expect(
    (await readJSON(context, "/api/credential-use/accounts")).List,
  ).toEqual([]);
  expect((await readJSON(context, "/api/operation-accounts")).List).toEqual([]);

  await page.getByRole("button", { name: "域连接", exact: true }).click();
  await page.getByRole("button", { name: "新增域连接", exact: true }).click();
  await page.getByLabel("域 DNS 名称", { exact: true }).fill(domain);
  await page
    .getByLabel("域控 DNS 名称", { exact: true })
    .fill("dc.synthetic.invalid");
  await page.getByLabel("AD 用户名", { exact: true }).fill(domainUser);
  await page.getByLabel("AD 密码", { exact: true }).fill(domainPassword);
  await proof(page, password, auth);
  const domainCreated = await submit(
    page,
    "/api/domains/create",
    "保存本地连接",
  );
  expect(domainCreated.value).toMatchObject({
    result: "SUCCESS",
    revision: "1",
    requiresResourceAssignment: true,
  });
  safe(domainCreated.value);
  const domainId = domainCreated.value.domainId as string;
  expect(
    (await readJSON(context, "/api/operation-accounts/domains")).domains,
  ).toEqual([]);
  await page.getByRole("button", { name: "资源与租户", exact: true }).click();
  await page.getByRole("button", { name: "资源组", exact: true }).click();
  await page.getByRole("button", { name: "新增资源组", exact: true }).click();
  await page.getByLabel("资源组名称", { exact: true }).fill(group);
  await page.getByLabel("域 ID", { exact: true }).fill(domainId);
  await proof(page, password, auth);
  expect(
    (await submit(page, "/api/resources/groups/create", "保存资源组")).value,
  ).toMatchObject({ result: "SUCCESS" });
  await page
    .getByRole("button", { name: `查看 ${group}`, exact: true })
    .click();
  await page.getByRole("button", { name: "调整关联角色", exact: true }).click();
  await page.getByLabel("显式关联 platform_admin", { exact: true }).check();
  await proof(page, password, auth);
  expect(
    (await submit(page, "/api/resources/groups/assign", "保存角色关联")).value,
  ).toMatchObject({ result: "SUCCESS", sessionRevoked: true });
  await login(page, username, password);
  await page
    .getByLabel("二次认证验证码", { exact: true })
    .fill(await freshCode(auth));
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  const actorId = (await readJSON(context, "/api/auth/me")).ID;
  await page.getByRole("button", { name: "管理操作账户", exact: true }).click();
  await page.getByRole("button", { name: "新增操作账户", exact: true }).click();
  await page
    .getByRole("button", { name: `选择 ${domain}`, exact: true })
    .click();
  await page.getByLabel("登记标签（可选）", { exact: true }).fill(label);
  await page.getByLabel("AD 用户名", { exact: true }).fill(accountUser);
  await page.getByLabel("AD 密码", { exact: true }).fill(accountPassword);
  await proof(page, password, auth);
  const created = await submit(
    page,
    "/api/operation-accounts/create",
    "保存本地登记",
  );
  safe(created.value);
  const accountId = created.value.accountId as string;
  await expect(
    page.getByRole("heading", { name: `操作账户详情：${label}`, exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "查看凭据使用授权", exact: true })
    .click();
  await expect(page.getByText("否（默认拒绝）", { exact: true })).toBeVisible();
  const getUse = async (path: string) => {
    const response = await context.request.get(`/api/credential-use${path}`);
    expect(response.status()).toBe(200);
    expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId));
    expect(response.headers()["cache-control"]).toBe("no-store");
    const value = await response.json();
    safe(value);
    expect(value.consumerEnabled).toBe(true);
    return value;
  };
  expect(await getUse(`/effective?accountId=${accountId}`)).toMatchObject({
    accountId,
    domainId,
    purpose: "domain.connection_test",
    explicitlyGranted: false,
    eligible: false,
    grantRevision: "0",
    consumerEnabled: true,
  });
  expect((await getUse(`/grants?accountId=${accountId}`)).grants).toEqual([]);
  const targets = (await getUse(`/roles?accountId=${accountId}`)).roles;
  expect(targets).toContainEqual({
    roleId: "platform_admin",
    roleName: "platform_admin",
    memberCount: 1,
  });
  expect(targets.some((r: { roleId: string }) => r.roleId === "viewer")).toBe(
    false,
  );
  await page
    .getByLabel("授权目标角色", { exact: true })
    .selectOption("platform_admin");
  await page.getByRole("button", { name: "审阅角色授权", exact: true }).click();
  await expect(
    page.getByText("当前成员数：1。此操作影响该角色当前及未来全部成员。", {
      exact: true,
    }),
  ).toBeVisible();
  await page
    .getByRole("checkbox", {
      name: "我已核对目标角色，确认授权覆盖当前及未来全部成员",
    })
    .check();
  await proof(page, password, auth);
  type Committed = { receipt: CredentialReceipt; key: string };
  let resolveCommit!: (value: Committed) => void,
    rejectCommit!: (reason: unknown) => void;
  const committed = new Promise<Committed>((resolve, reject) => {
    resolveCommit = resolve;
    rejectCommit = reject;
  });
  // The real POST commits, then its response is lost. Block receipt delivery
  // until safe pending state is inspected, while still calling the real API.
  await page.route("**/api/credential-use/mutation?*", async (route) => {
    const actual = await route.fetch({ maxRetries: 0 });
    expect(actual.status()).toBe(200);
    safe(await actual.json());
    await route.abort("connectionreset");
  });
  await page.route(
    "**/api/credential-use/grant",
    async (route) => {
      try {
        const actual = await route.fetch({ maxRetries: 0 });
        expect(actual.status()).toBe(200);
        expect(actual.headers()["x-adtr-user-id"]).toBe(String(actorId));
        const result = await actual.json();
        safe(result);
        expect(result.consumerEnabled).toBe(true);
        const sent = route.request().postDataJSON();
        expect(Object.keys(sent).sort()).toEqual(
          [
            "accountId",
            "actorPassword",
            "expectedAccountRevision",
            "expectedCredentialRevision",
            "expectedGrantRevision",
            "idempotencyKey",
            "purpose",
            "roleId",
            "totpCode",
          ].sort(),
        );
        const { actorPassword: _password, totpCode: _code, ...metadata } = sent;
        expect(metadata).toMatchObject({
          accountId,
          roleId: "platform_admin",
          purpose: "domain.connection_test",
          expectedAccountRevision: "1",
          expectedCredentialRevision: "1",
          expectedGrantRevision: "0",
        });
        await route.abort("connectionreset");
        resolveCommit({ receipt: result, key: sent.idempotencyKey });
      } catch (error) {
        rejectCommit(error);
        await route.abort().catch(() => {});
      }
    },
    { times: 1 },
  );
  await page
    .getByRole("button", { name: "确认授予角色使用权", exact: true })
    .click();
  const granted = await committed;
  expect(granted.receipt).toMatchObject({
    result: "SUCCESS",
    operation: "grant",
    accountId,
    domainId,
    roleId: "platform_admin",
    allowed: true,
    replayed: false,
    currentAllowed: true,
  });
  await expect(
    page.getByRole("heading", { name: "核对未确认的凭据授权", exact: true }),
  ).toBeVisible();
  const state = await safeBrowser();
  const stored = JSON.parse(
    Object.entries(state.session).find(([key]) =>
      key.startsWith("adtr.credential-use-intent.v1:"),
    )![1],
  );
  expect(stored).toEqual({
    owner: `${actorId}:${username}`,
    intent: {
      kind: "grant",
      accountId,
      domainId,
      roleId: "platform_admin",
      purpose: "domain.connection_test",
      expectedAccountRevision: "1",
      expectedCredentialRevision: "1",
      expectedGrantRevision: "0",
      idempotencyKey: granted.key,
    },
  });
  await page.unroute("**/api/credential-use/mutation?*");
  await page.reload();
  await page.getByRole("button", { name: "凭据授权清理", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "凭据授权回执", exact: true }),
  ).toBeVisible();
  expect(grantPosts).toBe(1);
  expect((await safeBrowser()).session).toEqual({});
  const recovered = await getUse(`/mutation?idempotencyKey=${granted.key}`);
  expect(recovered.receipt).toMatchObject({
    operation: "grant",
    accountId,
    roleId: "platform_admin",
    replayed: true,
    currentAllowed: true,
    grantRevision: granted.receipt.grantRevision,
  });
  expect(await getUse(`/effective?accountId=${accountId}`)).toMatchObject({
    explicitlyGranted: true,
    eligible: true,
    consumerEnabled: true,
  });

  // Expire through the actual scoped, proof-bearing administrative path. This
  // invalidates the session; reauthentication still permits reducing authority.
  await page.getByRole("button", { name: "资源与租户", exact: true }).click();
  await page.getByRole("button", { name: "租户配置", exact: true }).click();
  await page.getByRole("button", { name: "编辑租户配置", exact: true }).click();
  await page.getByLabel("失效时间（UTC Unix 秒）", { exact: true }).fill("0");
  await proof(page, password, auth);
  expect(
    (await submit(page, "/api/resources/tenant/save", "保存租户配置")).value,
  ).toMatchObject({ result: "SUCCESS", sessionRevoked: true });
  await login(page, username, password);
  await page
    .getByLabel("二次认证验证码", { exact: true })
    .fill(await freshCode(auth));
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  const unavailable = await context.request.get(
    `/api/credential-use/roles?accountId=${accountId}`,
  );
  expect(unavailable.status()).toBe(403);
  expect(await unavailable.json()).toEqual({ error: "tenant_expired" });
  const cleanup = await getUse("/accounts");
  expect(cleanup.List.map((a: { accountId: string }) => a.accountId)).toEqual([
    accountId,
  ]);
  await page.getByRole("button", { name: "凭据授权清理", exact: true }).click();
  await page
    .getByRole("button", { name: `清理 ${label}`, exact: true })
    .click();
  await expect(
    page.getByText(
      "当前无法确认可新增授权的角色；已有授权仍可按上方列表撤销。",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "审阅角色授权", exact: true }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "撤销 platform_admin", exact: true })
    .click();
  await page
    .getByRole("checkbox", {
      name: "我已核对目标角色，确认撤销覆盖当前及未来全部成员",
    })
    .check();
  await proof(page, password, auth);
  const revoked = await submit(
    page,
    "/api/credential-use/revoke",
    "确认撤销角色使用权",
  );
  safe(revoked.value);
  expect(revoked.value).toMatchObject({
    operation: "revoke",
    allowed: false,
    currentAllowed: false,
    consumerEnabled: true,
    accountId,
    roleId: "platform_admin",
  });
  expect(BigInt(revoked.value.grantRevision)).toBeGreaterThan(
    BigInt(granted.receipt.grantRevision),
  );
  expect(revoked.sent.expectedGrantRevision).toBe(
    granted.receipt.grantRevision,
  );
  await expect(
    page.getByRole("heading", { name: "凭据授权回执", exact: true }),
  ).toBeVisible();
  expect((await getUse("/accounts")).List).toEqual([]);
  expect(
    (await getUse(`/grants?accountId=${accountId}`)).grants,
  ).toContainEqual(
    expect.objectContaining({ roleId: "platform_admin", allowed: false }),
  );
  expect(
    (await getUse(`/mutation?idempotencyKey=${granted.key}`)).receipt,
  ).toMatchObject({
    operation: "grant",
    allowed: true,
    currentAllowed: false,
    currentGrantRevision: revoked.value.grantRevision,
  });
  expect(grantPosts).toBe(1);
  expect(revokePosts).toBe(1);
  expect(remoteRequests).toEqual([]);
  await safeBrowser();
  safe(consoles);
});
