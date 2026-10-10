import { navigateTo } from "./navigation";
import {
  test,
  expect,
  type BrowserContext,
  type Locator,
  type Page,
} from "@playwright/test";
import { createHmac } from "node:crypto";

type Authenticator = { secret: string; lastCounter: number };
type Receipt = {
  result: string;
  operation: string;
  accountId: string;
  domainId: string;
  revision: string;
  credentialRevision: string;
  verificationState: string;
  deleted: boolean;
  replayed: boolean;
  currentRevision: string;
  currentCredentialRevision: string;
};

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

async function fillSecret(field: Locator, value: string) {
  try {
    await field.fill(value);
  } catch {
    throw new Error("Unable to enter synthetic authentication proof");
  }
}

async function login(page: Page, username: string, password: string) {
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await fillSecret(page.getByLabel("密码", { exact: true }), password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function enrollMfa(page: Page, password: string): Promise<Authenticator> {
  await navigateTo(page, "多因素认证");
  await fillSecret(page.getByLabel("当前密码", { exact: true }), password);
  await page.getByRole("button", { name: "开始设置", exact: true }).click();
  const secret = await page
    .getByLabel("设置密钥", { exact: true })
    .inputValue();
  expect(secret.length).toBeGreaterThan(10);
  const now = Date.now();
  await fillSecret(
    page.getByLabel("认证器验证码", { exact: true }),
    totp(secret, now),
  );
  await page.getByRole("button", { name: "验证并启用", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用");
  return { secret, lastCounter: Math.floor(now / 30_000) };
}

async function proof(page: Page, password: string, auth: Authenticator) {
  await fillSecret(
    page.getByLabel("操作者当前密码", { exact: true }),
    password,
  );
  await fillSecret(
    page.getByLabel("未使用的认证器验证码", { exact: true }),
    await freshCode(auth),
  );
}

async function readJSON(
  context: BrowserContext,
  path: string,
  actorId?: number,
) {
  const response = await context.request.get(path);
  expect(response.status(), `GET ${path}`).toBe(200);
  if (actorId !== undefined)
    expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId));
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

async function openOperations(page: Page) {
  await navigateTo(page, "管理操作账户");
  await expect(
    page.getByRole("heading", { name: "管理操作账户", exact: true }),
  ).toBeVisible();
}

async function openAccount(page: Page, label: string) {
  await page
    .getByRole("button", { name: `查看 ${label}`, exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "编辑登记", exact: true }),
  ).toBeVisible();
}

async function openDomains(page: Page) {
  await navigateTo(page, "域连接");
  await expect(
    page.getByRole("heading", { name: "域连接", exact: true }),
  ).toBeVisible();
}

// This suite needs an isolated fresh bootstrap database, an eligible tenant
// (maxAdCount=2, expiry tomorrow), a separate domain key and probes disabled.
// Only the real browser -> API -> PostgreSQL path is used. The fault injection
// loses a committed POST response and the first real receipt GET response;
// no response is invented.
// Do not retain credential-bearing DOM/network snapshots as test artifacts.
test.use({ trace: "off", screenshot: "off", video: "off" });

test("local operation-account CRUD, lost-response recovery, stale editor and domain dependency", async ({
  page,
  context,
}) => {
  test.setTimeout(480_000);
  let username = process.env.ADTR_E2E_USERNAME;
  const bootstrapPassword = process.env.ADTR_E2E_PASSWORD;
  if (!username || !bootstrapPassword || !process.env.ADTR_E2E_BASE_URL)
    throw new Error(
      "A fresh synthetic bootstrap account and ADTR_E2E_BASE_URL are required",
    );

  let password = "Changed Operation Password 123";
  const operatorUsername = "operation-account-operator";
  const operatorInitial = "Initial Operation Operator 123";
  const operatorPassword = "Changed Operation Operator 456";
  const domain = "synthetic.invalid";
  const group = "E2EOperationAccounts";
  const label = "Local operation registration";
  const editedLabel = "Reviewed local registration";
  const domainUser = "domain-fixture@synthetic.invalid";
  const domainPassword = "Synthetic domain credential 731";
  const firstUser = "operation-fixture@synthetic.invalid";
  const firstPassword = "Synthetic operation credential 419";
  const replacementUser = "replacement-fixture@synthetic.invalid";
  const replacementPassword = "Synthetic replacement credential 823";
  const forbiddenValues = [
    domainUser,
    domainPassword,
    firstUser,
    firstPassword,
    replacementUser,
    replacementPassword,
    bootstrapPassword,
    password,
    operatorInitial,
    operatorPassword,
  ];
  const safe = (value: unknown) => {
    const serialized = JSON.stringify(value);
    // Boolean assertions do not echo a leaked credential into a failing report.
    for (const secret of forbiddenValues)
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
  const safeBrowser = async (target: Page) => {
    for (const label of [
      "AD 用户名",
      "AD 密码",
      "操作者当前密码",
      "未使用的认证器验证码",
    ]) {
      const field = target.getByLabel(label, { exact: true });
      if (await field.count()) await expect(field).toHaveValue("");
    }
    const state = await target.evaluate(async () => ({
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
  const consoleMessages: string[] = [];
  page.on("console", (message) => consoleMessages.push(message.text()));
  page.on("pageerror", (error) => consoleMessages.push(error.message));
  let accountCreates = 0;
  const remoteRequests: string[] = [];
  context.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    if (
      request.method() === "POST" &&
      path === "/api/operation-accounts/create"
    )
      accountCreates++;
    if (
      request.method() === "POST" &&
      (path === "/api/domains/test" || path.startsWith("/api/tasks/"))
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
  let auth = await enrollMfa(page, password);
  forbiddenValues.push(auth.secret);
  const tenant = await readJSON(context, "/api/resources/tenant");
  expect(tenant.maxAdCount).toBe(2);
  expect(tenant.expireTime).toBeGreaterThan(Math.floor(Date.now() / 1000));
  expect((await readJSON(context, "/api/domains")).page.total).toBe(0);
  expect((await readJSON(context, "/api/operation-accounts")).List).toEqual([]);

  await openDomains(page);
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
  const domainId = domainCreated.value.domainId as string;
  safe(domainCreated.value);
  // Builtin platform_admin must still obtain exact-domain F47 membership.
  expect(
    (await readJSON(context, "/api/operation-accounts/domains")).domains,
  ).toEqual([]);
  expect(
    (
      await context.request.get(`/api/domains/detail?domainId=${domainId}`)
    ).status(),
  ).toBe(404);
  await navigateTo(page, "资源与租户");
  await page.getByRole("button", { name: "资源组", exact: true }).click();
  await page.getByRole("button", { name: "新增资源组", exact: true }).click();
  await page.getByLabel("资源组名称", { exact: true }).fill(group);
  await page.getByLabel("域 ID", { exact: true }).fill(domainId);
  await proof(page, password, auth);
  expect(
    (await submit(page, "/api/resources/groups/create", "保存资源组")).value,
  ).toMatchObject({ result: "SUCCESS", sessionRevoked: false });
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
  expect(
    (await readJSON(context, "/api/operation-accounts/domains")).domains,
  ).toEqual([{ domainId, domain, revision: "1" }]);

  // Production's ten-action budget is per user, including failed proof-checked
  // mutations. Bootstrap: password/MFA (3), domain/group/assignment (3), user
  // creation (1) = 7. Operator: password/MFA (3), account create/metadata/stale
  // edit/replacement (4), blocked domain delete/account delete/domain delete (3)
  // = 10. Session rotation, logout and the stale editor tab do not reset it.
  const bootstrapActorId = (await readJSON(context, "/api/auth/me")).ID;
  await navigateTo(page, "访问管理");
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  await page.getByLabel("用户名", { exact: true }).fill(operatorUsername);
  await fillSecret(
    page.getByLabel("初始密码", { exact: true }),
    operatorInitial,
  );
  await fillSecret(
    page.getByLabel("确认初始密码", { exact: true }),
    operatorInitial,
  );
  await page.getByLabel("角色", { exact: true }).fill("platform_admin");
  await proof(page, password, auth);
  const operator = await submit(page, "/api/access/users/create", "创建用户");
  safe(operator.value);
  expect(operator.value).toMatchObject({
    result: "SUCCESS",
    sessionRevoked: false,
  });
  expect(operator.value.ID).toBeGreaterThan(0);
  expect(operator.value.ID).not.toBe(bootstrapActorId);
  await navigateTo(page, "退出登录");
  await login(page, operatorUsername, operatorInitial);
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await fillSecret(
    page.getByLabel("当前密码", { exact: true }),
    operatorInitial,
  );
  await fillSecret(
    page.getByLabel("新密码", { exact: true }),
    operatorPassword,
  );
  await fillSecret(
    page.getByLabel("确认新密码", { exact: true }),
    operatorPassword,
  );
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  auth = await enrollMfa(page, operatorPassword);
  forbiddenValues.push(auth.secret);
  username = operatorUsername;
  password = operatorPassword;
  const actorId = (await readJSON(context, "/api/auth/me")).ID as number;
  expect(actorId).toBe(operator.value.ID);

  await openOperations(page);
  await page.getByRole("button", { name: "新增操作账户", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "选择已授权的域", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: `选择 ${domain}`, exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "登记操作账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("登记标签（可选）", { exact: true }).fill(label);
  await page.getByLabel("AD 用户名", { exact: true }).fill(firstUser);
  await page.getByLabel("AD 密码", { exact: true }).fill(firstPassword);
  await proof(page, password, auth);
  type Committed = { receipt: Receipt; key: string };
  let resolveCommit!: (value: Committed) => void;
  let rejectCommit!: (reason: unknown) => void;
  const committed = new Promise<Committed>((resolve, reject) => {
    resolveCommit = resolve;
    rejectCommit = reject;
  });
  // Keep the real recovery responses interrupted until safe pending state has
  // been inspected. This remains deterministic even if automatic GET retries
  // run on a slow browser worker. No response body is fabricated.
  await page.route("**/api/operation-accounts/mutation?*", async (route) => {
    const actual = await route.fetch({ maxRetries: 0 });
    expect(actual.status()).toBe(200);
    expect(actual.headers()["x-adtr-user-id"]).toBe(String(actorId));
    safe(await actual.json());
    await route.abort("connectionreset");
  });
  await page.route(
    "**/api/operation-accounts/create",
    async (route) => {
      try {
        const actual = await route.fetch({ maxRetries: 0 });
        expect(actual.status()).toBe(200);
        expect(actual.headers()["x-adtr-user-id"]).toBe(String(actorId));
        const receipt = (await actual.json()) as Receipt;
        const key = route.request().postDataJSON().idempotencyKey as string;
        await route.abort("connectionreset");
        resolveCommit({ receipt, key });
      } catch (error) {
        rejectCommit(error);
        await route.abort().catch(() => {});
      }
    },
    { times: 1 },
  );
  await page.getByRole("button", { name: "保存本地登记", exact: true }).click();
  const created = await committed;
  const accountId = created.receipt.accountId;
  expect(created.receipt).toMatchObject({
    result: "SUCCESS",
    operation: "create",
    domainId,
    revision: "1",
    credentialRevision: "1",
    verificationState: "unverified",
    deleted: false,
    replayed: false,
  });
  expect(accountId).not.toBe(domainId);
  safe(created.receipt);
  await expect(
    page.getByRole("heading", { name: "核对未确认的操作", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "查询原操作回执", exact: true }),
  ).toBeEnabled();
  const pendingState = await safeBrowser(page);
  const pending = JSON.stringify(pendingState.session);
  expect(pending).toContain(created.key);
  expect(pending).not.toContain(label);
  expect(pending).not.toContain("fingerprint");
  const saved = JSON.parse(
    Object.entries(pendingState.session).find(([key]) =>
      key.startsWith("adtr.operation-account-intent.v1:"),
    )![1],
  );
  expect(Object.keys(saved).sort()).toEqual(["intent", "owner"]);
  expect(saved.owner).toBe(`${actorId}:${username}`);
  expect(Object.keys(pendingState.session)).toEqual([
    `adtr.operation-account-intent.v1:${actorId}:${username}`,
  ]);
  expect(saved.intent).toEqual({
    kind: "create",
    key: created.key,
    domainId,
  });
  // Reload must recover using the same safe intent, without replaying secrets.
  await expect(page).toHaveURL(/#operation-accounts\/create$/);
  await page.unroute("**/api/operation-accounts/mutation?*");
  await page.reload();
  // The restored workspace performs recovery itself. Reopening it can discard
  // a receipt that finished before the navigation click.
  await expect(
    page.getByRole("button", { name: "管理操作账户", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  await expect(
    page.getByRole("heading", { name: "管理操作账户", exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("账户 ID", { exact: true })).toHaveValue(
    accountId,
  );
  expect(accountCreates).toBe(1);
  const recovered = await readJSON(
    context,
    `/api/operation-accounts/mutation?idempotencyKey=${created.key}`,
    actorId,
  );
  expect(recovered.receipt).toMatchObject({
    accountId,
    domainId,
    operation: "create",
    revision: "1",
    credentialRevision: "1",
    currentRevision: "1",
    currentCredentialRevision: "1",
    deleted: false,
  });
  safe(recovered);
  expect((await safeBrowser(page)).session).toEqual({});

  await page.reload();
  await openOperations(page);
  const initial = (
    await readJSON(
      context,
      `/api/operation-accounts/detail?accountId=${accountId}`,
    )
  ).account;
  expect(initial).toMatchObject({
    accountId,
    domainId,
    domain,
    label,
    revision: "1",
    credentialRevision: "1",
    credentialConfigured: true,
    storageState: "saved",
    verificationState: "unverified",
  });
  expect(Number.isNaN(Date.parse(initial.createdAt))).toBe(false);
  expect(Number.isNaN(Date.parse(initial.updatedAt))).toBe(false);
  safe(initial);
  const listed = await readJSON(context, "/api/operation-accounts");
  expect(listed.page.total).toBe(1);
  expect(listed.List).toEqual([initial]);
  safe(listed);
  for (const [query, code] of [
    ["isPlaintext=1", "plaintext_unavailable"],
    ["filterStatus=enabled", "unsupported_status_filter"],
  ]) {
    const unsupported = await context.request.get(
      `/api/operation-accounts?${query}`,
    );
    expect(unsupported.status()).toBe(422);
    const value = await unsupported.json();
    expect(value).toEqual({ error: code });
    safe(value);
  }

  // Hold revision 1 in a second real editor, then commit a label-only edit.
  const stalePage = await context.newPage();
  await stalePage.goto("/");
  await openOperations(stalePage);
  await openAccount(stalePage, label);
  await stalePage
    .getByRole("button", { name: "编辑登记", exact: true })
    .click();
  await expect(
    stalePage.getByLabel("登记标签（可选）", { exact: true }),
  ).toHaveValue(label);
  await openAccount(page, label);
  await page.getByRole("button", { name: "编辑登记", exact: true }).click();
  await expect(page.getByLabel("AD 用户名", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("AD 密码", { exact: true })).toHaveCount(0);
  await page.getByLabel("登记标签（可选）", { exact: true }).fill(editedLabel);
  await proof(page, password, auth);
  const metadataEdit = await submit(
    page,
    "/api/operation-accounts/update",
    "保存登记修改",
  );
  expect(metadataEdit.sent).not.toHaveProperty("username");
  expect(metadataEdit.sent).not.toHaveProperty("password");
  expect(metadataEdit.sent.expectedRevision).toBe("1");
  expect(metadataEdit.value).toMatchObject({
    operation: "update",
    revision: "2",
    credentialRevision: "1",
  });
  safe(metadataEdit.value);
  await stalePage
    .getByLabel("登记标签（可选）", { exact: true })
    .fill("Stale editor must not overwrite");
  await proof(stalePage, password, auth);
  const stale = await submit(
    stalePage,
    "/api/operation-accounts/update",
    "保存登记修改",
    409,
  );
  expect(stale.sent.expectedRevision).toBe("1");
  expect(stale.value).toEqual({ error: "revision_conflict" });
  safe(stale.value);
  await safeBrowser(stalePage);
  await stalePage.close();
  expect(
    (
      await readJSON(
        context,
        `/api/operation-accounts/detail?accountId=${accountId}`,
      )
    ).account,
  ).toMatchObject({
    label: editedLabel,
    revision: "2",
    credentialRevision: "1",
  });

  await page.reload();
  await openOperations(page);
  await openAccount(page, editedLabel);
  await page.getByRole("button", { name: "编辑登记", exact: true }).click();
  await page.getByLabel("替换已保存的凭据", { exact: true }).check();
  await expect(page.getByLabel("AD 用户名", { exact: true })).toHaveValue("");
  await expect(page.getByLabel("AD 密码", { exact: true })).toHaveValue("");
  // Unsubmitted values must disappear after browser Back/Forward navigation.
  await page.getByLabel("AD 用户名", { exact: true }).fill(replacementUser);
  await page.getByLabel("AD 密码", { exact: true }).fill(replacementPassword);
  await page.goBack();
  await page.goForward();
  await openOperations(page);
  await openAccount(page, editedLabel);
  await page.getByRole("button", { name: "编辑登记", exact: true }).click();
  await page.getByLabel("替换已保存的凭据", { exact: true }).check();
  await expect(page.getByLabel("AD 用户名", { exact: true })).toHaveValue("");
  await expect(page.getByLabel("AD 密码", { exact: true })).toHaveValue("");
  await page.getByLabel("AD 用户名", { exact: true }).fill(replacementUser);
  await page.getByLabel("AD 密码", { exact: true }).fill(replacementPassword);
  await proof(page, password, auth);
  const replaced = await submit(
    page,
    "/api/operation-accounts/update",
    "保存登记修改",
  );
  expect(replaced.sent.expectedRevision).toBe("2");
  expect(
    replaced.sent.username === replacementUser &&
      replaced.sent.password === replacementPassword,
  ).toBe(true);
  expect(replaced.value).toMatchObject({
    accountId,
    revision: "3",
    credentialRevision: "2",
    verificationState: "unverified",
  });
  safe(replaced.value);
  await safeBrowser(page);
  const current = (
    await readJSON(
      context,
      `/api/operation-accounts/detail?accountId=${accountId}`,
    )
  ).account;
  expect(current).toMatchObject({
    label: editedLabel,
    revision: "3",
    credentialRevision: "2",
    verificationState: "unverified",
  });
  safe(current);
  const originalReceipt = await readJSON(
    context,
    `/api/operation-accounts/mutation?idempotencyKey=${created.key}`,
    actorId,
  );
  expect(originalReceipt.receipt).toMatchObject({
    revision: "1",
    credentialRevision: "1",
    currentRevision: "3",
    currentCredentialRevision: "2",
  });
  safe(originalReceipt);

  // Registration pins its parent; deleting the parent cannot orphan the pair.
  await openDomains(page);
  await page
    .getByRole("button", { name: `查看 ${domain}`, exact: true })
    .click();
  await page.getByRole("button", { name: "删除域连接", exact: true }).click();
  await page.getByLabel("输入域名确认删除", { exact: true }).fill(domain);
  await proof(page, password, auth);
  const blocked = await submit(
    page,
    "/api/domains/delete",
    "确认删除本地连接",
    409,
  );
  expect(blocked.value).toEqual({ error: "domain_in_use" });
  safe(blocked.value);
  expect(
    (await readJSON(context, `/api/domains/detail?domainId=${domainId}`))
      .connection.revision,
  ).toBe("1");
  expect(
    (
      await readJSON(
        context,
        `/api/operation-accounts/detail?accountId=${accountId}`,
      )
    ).account.revision,
  ).toBe("3");

  await openOperations(page);
  await openAccount(page, editedLabel);
  await page.getByRole("button", { name: "删除本地登记", exact: true }).click();
  await page
    .getByLabel("输入完整账户 ID 确认", { exact: true })
    .fill(accountId);
  await proof(page, password, auth);
  const deleted = await submit(
    page,
    "/api/operation-accounts/delete",
    "确认删除本地登记",
  );
  expect(deleted.sent).toMatchObject({
    accountId,
    expectedRevision: "3",
    confirmAccountId: accountId,
  });
  expect(deleted.value).toMatchObject({
    result: "SUCCESS",
    operation: "delete",
    accountId,
    revision: "4",
    credentialRevision: "3",
    deleted: true,
  });
  safe(deleted.value);
  expect(
    (
      await context.request.get(
        `/api/operation-accounts/detail?accountId=${accountId}`,
      )
    ).status(),
  ).toBe(404);
  const empty = await readJSON(context, "/api/operation-accounts");
  expect(empty).toMatchObject({
    page: { total: 0 },
    List: [],
    exhausted: true,
  });
  safe(empty);
  const tombstoneReceipt = await readJSON(
    context,
    `/api/operation-accounts/mutation?idempotencyKey=${created.key}`,
    actorId,
  );
  expect(tombstoneReceipt.receipt).toMatchObject({
    accountId,
    revision: "1",
    credentialRevision: "1",
    currentRevision: "4",
    currentCredentialRevision: "3",
    deleted: true,
  });
  safe(tombstoneReceipt);

  // Check for implicit diagnostics while the domain is still authorized. An
  // explicit domain scope also permits an empty task list before any first task.
  const diagnosticTasksPath = `/api/tasks?taskName=domain.connection_test&domainId=${domainId}`;
  const diagnosticTasks = await readJSON(context, diagnosticTasksPath);
  expect(diagnosticTasks).toMatchObject({
    page: { total: 0 },
    tasks: [],
    exhausted: true,
  });
  safe(diagnosticTasks);
  expect(remoteRequests).toEqual([]);

  await openDomains(page);
  await page
    .getByRole("button", { name: `查看 ${domain}`, exact: true })
    .click();
  await page.getByRole("button", { name: "删除域连接", exact: true }).click();
  await page.getByLabel("输入域名确认删除", { exact: true }).fill(domain);
  await proof(page, password, auth);
  expect(
    (await submit(page, "/api/domains/delete", "确认删除本地连接")).value,
  ).toMatchObject({ result: "SUCCESS", domainId });
  expect(
    (
      await context.request.get(`/api/domains/detail?domainId=${domainId}`)
    ).status(),
  ).toBe(404);
  // Deleting the domain removes its resource grant, including for this admin.
  const deletedDomainTasks = await context.request.get(diagnosticTasksPath);
  expect(deletedDomainTasks.status()).toBe(403);
  expect(await deletedDomainTasks.json()).toEqual({ error: "forbidden" });
  expect(remoteRequests).toEqual([]);
  expect(accountCreates).toBe(1);
  await page.reload();
  await openOperations(page);
  expect((await readJSON(context, "/api/operation-accounts")).List).toEqual([]);
  expect((await safeBrowser(page)).session).toEqual({});
  safe(consoleMessages);
});
