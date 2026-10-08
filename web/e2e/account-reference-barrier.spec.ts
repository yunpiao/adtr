import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { createHmac, randomBytes } from "node:crypto";
import { readFile, rename, writeFile } from "node:fs/promises";
import { join, isAbsolute } from "node:path";

type Authenticator = { secret: string; lastCounter: number };

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
  // Setup and post-release writes wait for a new real current counter.
  // Never bypass replay protection or freeze the server clock.
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

type Snapshot = {
  taskId: string;
  tenantId: string;
  actorId: string;
  domainId: string;
  accountId: string;
  taskKind: string;
  taskState: string;
  useState: string;
  openedAtPresent: boolean;
  openerPresent: boolean;
  openerUnchanged: boolean;
  quiescedAtPresent: boolean;
  quiescenceReason: string;
  diagnosticCode: string;
  diagnosticSuccessful: boolean;
  accountRevision: string;
  accountCredentialRevision: string;
  accountDeleted: boolean;
  accountCredentialPresent: boolean;
  connectionRevision: string;
  connectionCredentialGeneration: string;
  credentialSource: string;
  accountPointerPresent: boolean;
  accountPointerMatches: boolean;
  customCredentialCount: number;
  bindingDependencyCount: number;
  exactBindingDependencyCount: number;
  taskDependencyCount: number;
  exactTaskDependencyCount: number;
  totalDependencyCount: number;
};
type BarrierStatus = {
  version: number;
  ticket: string;
  phase: string;
  sequence: number;
  armed: boolean;
  ledgerLocked: boolean;
  lockHeld: boolean;
  fixtureReached: boolean;
  fixtureReleased: boolean;
  fixtureResponseWritten: boolean;
  fixtureHandlerClosed: boolean;
  released: boolean;
  quiesced: boolean;
  errorCode: string;
  snapshot: Snapshot | null;
};
const delay = (ms: number) =>
  new Promise<void>((resolve) => setTimeout(resolve, ms));
async function atomicJSON(dir: string, name: string, value: unknown) {
  const temporary = join(dir, `.${name}.${randomBytes(8).toString("hex")}.tmp`);
  await writeFile(temporary, JSON.stringify(value), {
    flag: "wx",
    mode: 0o600,
  });
  await rename(temporary, join(dir, name));
}
async function status(dir: string): Promise<BarrierStatus | null> {
  let raw: string;
  try {
    raw = await readFile(join(dir, "barrier-status.json"), "utf8");
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
    throw error;
  }
  // Assert booleans so malformed/secret-bearing helper data is never echoed.
  let value: BarrierStatus;
  try {
    value = JSON.parse(raw) as BarrierStatus;
  } catch {
    throw new Error("Invalid barrier status JSON");
  }
  const keys =
    "version ticket phase sequence armed ledgerLocked lockHeld fixtureReached fixtureReleased fixtureResponseWritten fixtureHandlerClosed released quiesced errorCode snapshot"
      .split(" ")
      .sort();
  expect(
    Object.keys(value).sort().join(" ") === keys.join(" "),
    "strict barrier status fields",
  ).toBe(true);
  expect(
    value.version === 1 && Number.isSafeInteger(value.sequence),
    "barrier protocol version/sequence",
  ).toBe(true);
  if (value.errorCode || value.phase === "failed")
    throw new Error(
      "Disposable barrier helper failed; inspect sanitized harness report",
    );
  if (value.snapshot) {
    const snapshotKeys =
      "taskId tenantId actorId domainId accountId taskKind taskState useState openedAtPresent openerPresent openerUnchanged quiescedAtPresent quiescenceReason diagnosticCode diagnosticSuccessful accountRevision accountCredentialRevision accountDeleted accountCredentialPresent connectionRevision connectionCredentialGeneration credentialSource accountPointerPresent accountPointerMatches customCredentialCount bindingDependencyCount exactBindingDependencyCount taskDependencyCount exactTaskDependencyCount totalDependencyCount"
        .split(" ")
        .sort();
    expect(
      Object.keys(value.snapshot).sort().join(" ") === snapshotKeys.join(" "),
      "strict safe snapshot fields",
    ).toBe(true);
  }
  return value;
}
async function waitStatus(
  dir: string,
  ticket: string,
  predicate: (s: BarrierStatus) => boolean,
  timeout = 20_000,
) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const current = await status(dir);
    if (current && (!ticket || current.ticket === ticket) && predicate(current))
      return current;
    await delay(25);
  }
  throw new Error("Timed out waiting for genuine barrier evidence");
}
async function changePassword(page: Page, before: string, after: string) {
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await page.getByLabel("当前密码", { exact: true }).fill(before);
  await page.getByLabel("新密码", { exact: true }).fill(after);
  await page.getByLabel("确认新密码", { exact: true }).fill(after);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
}
async function fillProof(page: Page, password: string, code: string) {
  await page.getByLabel("操作者当前密码", { exact: true }).fill(password);
  await page.getByLabel("未使用的认证器验证码", { exact: true }).fill(code);
}
async function accountForm(
  page: Page,
  label: string,
  kind: "update" | "delete",
) {
  await page.goto("/");
  await page.getByRole("button", { name: "管理操作账户", exact: true }).click();
  await page
    .getByRole("button", { name: `查看 ${label}`, exact: true })
    .click();
  await page
    .getByRole("button", {
      name: kind === "update" ? "编辑登记" : "删除本地登记",
      exact: true,
    })
    .click();
  if (kind === "update")
    await page.getByLabel("替换已保存的凭据", { exact: true }).check();
}
// Only the synthetic helper's opened-use row lock is controlled. The real LDAP
// operation completes; this proves pending executor-return acknowledgement, not
// a live LDAP socket during detach. No lifecycle writes or clock/proof resets.
test.use({ trace: "off", screenshot: "off", video: "off" });
test("opened account use blocks replacement and removal until real executor-return acknowledgement", async ({
  page,
  context,
  browser,
}, testInfo) => {
  test.setTimeout(480_000);
  const username = process.env.ADTR_E2E_USERNAME,
    bootstrapPassword = process.env.ADTR_E2E_PASSWORD;
  const controlDir = process.env.ADTR_E2E_BARRIER_CONTROL_DIR;
  const accountUser = process.env.ADTR_E2E_LDAP_USERNAME,
    accountPassword = process.env.ADTR_E2E_LDAP_PASSWORD,
    ldapIP = process.env.ADTR_E2E_LDAP_IP;
  if (
    !username ||
    !bootstrapPassword ||
    !process.env.ADTR_E2E_BASE_URL ||
    !controlDir ||
    !isAbsolute(controlDir) ||
    !accountUser ||
    !accountPassword ||
    !ldapIP
  )
    throw new Error(
      "Fresh real API/DB/worker, synthetic LDAP credentials and absolute barrier control directory required",
    );
  await waitStatus(controlDir, "", (s) => s.phase === "ready");
  const ready = JSON.parse(
    await readFile(join(controlDir, "barrier-ready.json"), "utf8"),
  );
  expect(ready).toEqual({ version: 1, phase: "ready" });
  const password = "Changed Barrier Password 123",
    domain = "synthetic.invalid",
    group = "E2EAcknowledgementBarrier",
    label = "Pending acknowledgement account";
  const secrets = [bootstrapPassword, password, accountUser, accountPassword];
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
  await page.getByLabel("连接 IP（可选）", { exact: true }).fill(ldapIP);
  await page
    .getByLabel("初始凭据来源", { exact: true })
    .selectOption("unconfigured");
  await expect(page.getByLabel("AD 用户名", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("AD 密码", { exact: true })).toHaveCount(0);
  await proof(page, password, auth);
  const domainCreated = await submit(
    page,
    "/api/domains/create-unconfigured",
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
  expect(
    Number.isSafeInteger(actorId) && actorId > 0,
    "canonical positive actor ID",
  ).toBe(true);
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
  const granted = await submit(
    page,
    "/api/credential-use/grant",
    "确认授予角色使用权",
  );
  expect(granted.value).toMatchObject({
    operation: "grant",
    allowed: true,
    consumerEnabled: true,
  });
  safe(granted.value);
  const openSource = async (target: Page) => {
    await target.getByRole("button", { name: "域连接", exact: true }).click();
    const back = target.getByRole("button", {
      name: "返回域连接列表",
      exact: true,
    });
    if (await back.isVisible()) await back.click();
    await target
      .getByRole("button", { name: `查看 ${domain}`, exact: true })
      .click();
    await target
      .getByRole("button", { name: "管理凭据来源", exact: true })
      .click();
    await expect(
      target.getByLabel("新的凭据来源", { exact: true }),
    ).toBeVisible();
  };
  // Production allows ten sensitive actions per actor per fifteen minutes.
  // Bootstrap: password/MFA (3), domain/group/assignment (3), account/grant (2),
  // manager creation (1), task admission (1) = 10. Manager: password/MFA (3),
  // reference/detach (2), denied replacement/removal (2), replacement/removal
  // after real acknowledgement (2) = 9. Both rejected writes spend rate slots;
  // tabs share the manager's bucket and re-login never resets either bucket.
  const managerName = "barrier-manager",
    managerInitial = "Initial Manager Password 481",
    managerPassword = "Changed Manager Password 482";
  secrets.push(managerInitial, managerPassword);
  await page.getByRole("button", { name: "访问管理", exact: true }).click();
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  await page.getByLabel("用户名", { exact: true }).fill(managerName);
  await page.getByLabel("初始密码", { exact: true }).fill(managerInitial);
  await page.getByLabel("确认初始密码", { exact: true }).fill(managerInitial);
  await page.getByLabel("角色", { exact: true }).fill("platform_admin");
  await proof(page, password, auth);
  const managerCreated = await submit(
    page,
    "/api/access/users/create",
    "创建用户",
  );
  safe(managerCreated.value);
  expect(managerCreated.value).toMatchObject({
    result: "SUCCESS",
    sessionRevoked: false,
  });
  expect(managerCreated.value.ID).toBeGreaterThan(0);
  expect(managerCreated.value.ID).not.toBe(actorId);
  const managerContext = await browser.newContext({
    baseURL: process.env.ADTR_E2E_BASE_URL,
  });
  const manager = await managerContext.newPage();
  let releaseRequested = false,
    armed = false;
  const ticket = randomBytes(16).toString("hex");
  try {
    await manager.goto("/");
    await login(manager, managerName, managerInitial);
    await changePassword(manager, managerInitial, managerPassword);
    const managerAuth = await enrollMfa(manager, managerPassword);
    secrets.push(managerAuth.secret);
    const managerId = (await readJSON(managerContext, "/api/auth/me")).ID;
    expect(managerId).toBe(managerCreated.value.ID);
    expect(managerId).not.toBe(actorId);
    await openSource(manager);
    await manager
      .getByLabel("新的凭据来源", { exact: true })
      .selectOption("reference");
    await manager
      .getByRole("button", { name: `选择账户 ${label}`, exact: true })
      .click();
    await proof(manager, managerPassword, managerAuth);
    const bound = await submit(
      manager,
      "/api/domains/credential-source/reference",
      "保存凭据来源",
    );
    expect(bound.value).toMatchObject({
      operation: "reference",
      credentialSource: "operation_account",
    });
    safe(bound.value);
    // All manager forms are prepared before the 60-second row-lock watchdog.
    await openSource(manager);
    await manager
      .getByLabel("新的凭据来源", { exact: true })
      .selectOption("detach");
    const replacer = await managerContext.newPage(),
      remover = await managerContext.newPage();
    await accountForm(replacer, label, "update");
    await accountForm(remover, label, "delete");
    const replacementUser = "replacement@synthetic.invalid",
      replacementPassword = "Synthetic replacement credential 823";
    secrets.push(replacementUser, replacementPassword);
    const fillReplacement = async () => {
      await replacer
        .getByLabel("AD 用户名", { exact: true })
        .fill(replacementUser);
      await replacer
        .getByLabel("AD 密码", { exact: true })
        .fill(replacementPassword);
    };
    await fillReplacement();
    await remover
      .getByLabel("输入完整账户 ID 确认", { exact: true })
      .fill(accountId);
    // The binding belongs to the connection; bootstrap remains the task owner.
    await openSource(page);
    await page
      .getByRole("button", { name: "关闭来源编辑", exact: true })
      .click();
    await page
      .getByRole("button", { name: "检测已保存连接", exact: true })
      .click();
    // Real current/next unused counters, accepted by verifyTOTP's ±1 window.
    // account_in_use rolls back requireAccessProof's consumption in the same TX.
    // Every wait for fresh counters is outside the held-lock interval.
    const earliest =
      (Math.max(auth.lastCounter, managerAuth.lastCounter) + 1) * 30_000 + 1100;
    if (Date.now() < earliest) await delay(earliest - Date.now());
    const detachCounter = Math.floor(Date.now() / 30_000);
    await fillProof(page, password, totp(auth.secret, detachCounter * 30_000));
    await fillProof(
      manager,
      managerPassword,
      totp(managerAuth.secret, detachCounter * 30_000),
    );
    const unusedManagerCode = () => {
      const current = Math.floor(Date.now() / 30_000),
        counter = Math.max(current, managerAuth.lastCounter + 1);
      expect(
        counter <= current + 1,
        "unused proof inside real accepted window",
      ).toBe(true);
      return { counter, code: totp(managerAuth.secret, counter * 30_000) };
    };
    await page.route(
      "**/api/domains/test",
      async (route) => {
        expect(route.request().method()).toBe("POST");
        const sent = route.request().postDataJSON();
        expect(sent.domainId).toBe(domainId);
        expect(
          typeof sent.idempotencyKey === "string" &&
            sent.idempotencyKey.length > 0,
          "actual admission key",
        ).toBe(true);
        // Only the frozen DTO is written, never intercepted proof fields.
        await atomicJSON(controlDir, "barrier-arm.json", {
          version: 1,
          ticket,
          tenantId: "default",
          actorId: String(actorId),
          domainId,
          accountId,
          idempotencyKey: sent.idempotencyKey,
        });
        armed = true;
        await waitStatus(controlDir, ticket, (s) => s.armed);
        await route.continue();
      },
      { times: 1 },
    );
    const tested = await submit(page, "/api/domains/test", "确认检测保存版本");
    auth.lastCounter = detachCounter;
    expect(tested.value.task).toMatchObject({
      taskName: "domain.account_connection_test",
      maxAttempts: 1,
    });
    const taskId = tested.value.task.taskUUID as string;
    const assertOpened = (s: BarrierStatus, binding: number) => {
      expect(s).toMatchObject({
        ticket,
        ledgerLocked: true,
        lockHeld: true,
        released: false,
        quiesced: false,
      });
      expect(s.snapshot).toMatchObject({
        taskId,
        tenantId: "default",
        actorId: String(actorId),
        domainId,
        accountId,
        taskKind: "domain.account_connection_test",
        useState: "opened",
        openedAtPresent: true,
        openerPresent: true,
        openerUnchanged: true,
        quiescedAtPresent: false,
        quiescenceReason: "",
        accountRevision: "1",
        accountCredentialRevision: "1",
        accountDeleted: false,
        accountCredentialPresent: true,
        customCredentialCount: 0,
        bindingDependencyCount: binding,
        exactBindingDependencyCount: binding,
        taskDependencyCount: 1,
        exactTaskDependencyCount: 1,
        totalDependencyCount: binding + 1,
      });
    };
    const initial = await waitStatus(
      controlDir,
      ticket,
      (s) => s.lockHeld && s.snapshot !== null,
    );
    assertOpened(initial, 1);
    expect(initial.snapshot).toMatchObject({
      credentialSource: "operation_account",
      accountPointerPresent: true,
      accountPointerMatches: true,
    });
    const finished = await waitStatus(
      controlDir,
      ticket,
      (s) =>
        s.fixtureReached &&
        s.fixtureReleased &&
        s.fixtureResponseWritten &&
        s.fixtureHandlerClosed &&
        s.snapshot?.taskState === "succeeded",
    );
    assertOpened(finished, 1);
    expect(finished.snapshot).toMatchObject({
      diagnosticCode: "success",
      diagnosticSuccessful: true,
    });
    const actualTask = await readJSON(
      context,
      `/api/domains/test-result?taskUUID=${taskId}`,
    );
    expect(actualTask.task.state).toBe("succeeded");
    safe(actualTask);
    const detached = await submit(
      manager,
      "/api/domains/credential-source/detach",
      "保存凭据来源",
    );
    expect(detached.value).toMatchObject({
      credentialSource: "unconfigured",
      operation: "detach",
    });
    managerAuth.lastCounter = detachCounter;
    const afterDetach = await waitStatus(
      controlDir,
      ticket,
      (s) => s.snapshot?.bindingDependencyCount === 0,
    );
    assertOpened(afterDetach, 0);
    expect(afterDetach.snapshot).toMatchObject({
      credentialSource: "unconfigured",
      accountPointerPresent: false,
      accountPointerMatches: false,
    });
    expect(BigInt(afterDetach.snapshot!.connectionRevision)).toBe(
      BigInt(initial.snapshot!.connectionRevision) + 1n,
    );
    expect(BigInt(afterDetach.snapshot!.connectionCredentialGeneration)).toBe(
      BigInt(initial.snapshot!.connectionCredentialGeneration) + 1n,
    );
    const blocked: string[] = [];
    for (const [target, path, button] of [
      [replacer, "/api/operation-accounts/update", "保存登记修改"],
      [remover, "/api/operation-accounts/delete", "确认删除本地登记"],
    ] as const) {
      await fillProof(target, managerPassword, unusedManagerCode().code);
      const denied = await submit(target, path, button, 409);
      expect(denied.value).toEqual({ error: "account_in_use" });
      expect(denied.sent.expectedRevision).toBe("1");
      blocked.push(denied.sent.idempotencyKey);
      await expect(target.getByRole("alert")).toContainText(
        "此账户仍被连接引用或有尚未确认停止的使用",
      );
      // Rejected writes leave the snapshot unchanged, so the helper need not
      // publish a new sequence. Check its current held/opened status directly.
      const held = await status(controlDir);
      expect(held !== null, "barrier status remains available").toBe(true);
      assertOpened(held!, 0);
    }
    // Release promptly after both exact 409s. Receipt/audit reads can then avoid
    // consuming the bounded lock interval; no mutation retry occurs before them.
    await atomicJSON(controlDir, "barrier-release.json", {
      version: 1,
      ticket,
      phase: "release",
    });
    releaseRequested = true;
    const quiesced = await waitStatus(
      controlDir,
      ticket,
      (s) => s.released && s.quiesced,
      60_000,
    );
    expect(quiesced).toMatchObject({
      lockHeld: false,
      released: true,
      quiesced: true,
    });
    // This genuine post-release observation is causally after both rejected
    // writes. Its unchanged revisions, absent receipts and empty mutation audit
    // prove neither failed attempt committed before any successful retry.
    expect(quiesced.snapshot).toMatchObject({
      taskId,
      useState: "quiesced",
      taskState: "succeeded",
      openedAtPresent: true,
      openerPresent: true,
      openerUnchanged: true,
      quiescedAtPresent: true,
      quiescenceReason: "executor_returned",
      diagnosticCode: "success",
      diagnosticSuccessful: true,
      credentialSource: "unconfigured",
      accountPointerPresent: false,
      customCredentialCount: 0,
      bindingDependencyCount: 0,
      exactBindingDependencyCount: 0,
      taskDependencyCount: 0,
      exactTaskDependencyCount: 0,
      totalDependencyCount: 0,
      accountRevision: "1",
      accountCredentialRevision: "1",
      accountDeleted: false,
      accountCredentialPresent: true,
    });
    for (const key of blocked) {
      const receipt = await managerContext.request.get(
        `/api/operation-accounts/mutation?idempotencyKey=${encodeURIComponent(key)}`,
      );
      expect(receipt.status()).toBe(404);
      expect(await receipt.json()).toEqual({ error: "not_found" });
    }
    const audit = await readJSON(
      managerContext,
      "/api/audit?filterEvent=operation_account_update&filterEvent=operation_account_delete",
    );
    expect(audit.page.total).toBe(0);
    expect(audit.List).toEqual([]);
    await fillReplacement();
    const replacementProof = unusedManagerCode();
    await fillProof(replacer, managerPassword, replacementProof.code);
    const replaced = await submit(
      replacer,
      "/api/operation-accounts/update",
      "保存登记修改",
    );
    managerAuth.lastCounter = replacementProof.counter;
    expect(replaced.value).toMatchObject({
      operation: "update",
      accountId,
      revision: "2",
      credentialRevision: "2",
      deleted: false,
    });
    await accountForm(remover, label, "delete");
    const currentAccount = await readJSON(
      managerContext,
      `/api/operation-accounts/detail?accountId=${accountId}`,
    );
    expect(currentAccount.account).toMatchObject({
      revision: "2",
      credentialRevision: "2",
    });
    await remover
      .getByLabel("输入完整账户 ID 确认", { exact: true })
      .fill(accountId);
    await proof(remover, managerPassword, managerAuth);
    const removed = await submit(
      remover,
      "/api/operation-accounts/delete",
      "确认删除本地登记",
    );
    expect(removed.sent.expectedRevision).toBe("2");
    expect(removed.value).toMatchObject({
      operation: "delete",
      accountId,
      revision: "3",
      credentialRevision: "3",
      deleted: true,
    });
    const tombstone = await waitStatus(
      controlDir,
      ticket,
      (s) => s.snapshot?.accountDeleted === true,
    );
    expect(tombstone.snapshot).toMatchObject({
      taskId,
      useState: "quiesced",
      openerUnchanged: true,
      quiescedAtPresent: true,
      quiescenceReason: "executor_returned",
      totalDependencyCount: 0,
      accountRevision: "3",
      accountCredentialRevision: "3",
      accountDeleted: true,
      accountCredentialPresent: false,
    });
    safe({ initial, finished, afterDetach, quiesced, tombstone });
    await testInfo.attach("opened-use-barrier-evidence.json", {
      body: Buffer.from(
        JSON.stringify({ initial, finished, afterDetach, quiesced, tombstone }),
      ),
      contentType: "application/json",
    });
  } finally {
    if (armed && !releaseRequested)
      await atomicJSON(controlDir, "barrier-release.json", {
        version: 1,
        ticket,
        phase: "release",
      });
    await managerContext.close();
  }
});
