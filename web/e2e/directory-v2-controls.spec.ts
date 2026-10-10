import { navigateTo } from "./navigation";
import {
  test,
  expect,
  type BrowserContext,
  type Page,
  type Locator,
  type Response,
} from "@playwright/test";
import { createHmac } from "node:crypto";
import {
  directoryV2ResponseJSON,
  observeDirectoryV2Responses,
} from "./directory-v2-response-observer";
import { isIP } from "node:net";
import type { Task } from "../src/task-api";
import { readDirectoryV2Use } from "./directory-v2-ledger-observer";

type Authenticator = { secret: string; lastCounter: number };

// Run only on the directory-v2-controls suite’s fresh owned database, real worker
// and TLS LDAPFixture(directory_enabled=True, directory_v2=True, directory_empty=True,
// directory_slow=True): five valid empty wire pages, two seconds per page.
// A missing fixture is a failed acceptance run, never a skipped/passing test.
function requireFixture() {
  const names = [
    "ADTR_E2E_BASE_URL",
    "ADTR_E2E_USERNAME",
    "ADTR_E2E_PASSWORD",
    "ADTR_E2E_LDAP_IP",
    "ADTR_E2E_LDAP_USERNAME",
    "ADTR_E2E_LDAP_PASSWORD",
    "ADTR_E2E_DB_CONTAINER",
  ] as const;
  if (
    names.some((name) => !process.env[name]) ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_MODE !== "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_V2 !== "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_EMPTY !== "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_SLOW !== "true"
  )
    throw new Error(
      "Directory controls acceptance requires scripts/test_auth_e2e.py --suite directory-v2-controls: an isolated fresh database, real worker, five-page empty/slow synthetic TLS LDAP fixture, ADTR_E2E_LDAP_DIRECTORY_MODE=true, ADTR_E2E_LDAP_DIRECTORY_V2=true, ADTR_E2E_LDAP_DIRECTORY_EMPTY=true, ADTR_E2E_LDAP_DIRECTORY_SLOW=true, ADTR_E2E_DB_CONTAINER and all ADTR_E2E auth/LDAP variables. No fixture means acceptance is blocked, not skipped.",
    );
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(process.env.ADTR_E2E_DB_CONTAINER!))
    throw new Error(
      "Directory controls require the owned isolated database container",
    );
  const base = new URL(process.env.ADTR_E2E_BASE_URL!);
  if (
    base.protocol !== "http:" ||
    !["127.0.0.1", "[::1]"].includes(base.hostname) ||
    base.username ||
    base.password ||
    base.search ||
    base.hash ||
    base.pathname !== "/"
  )
    throw new Error("Directory acceptance requires the owned loopback API URL");
  const address = process.env.ADTR_E2E_LDAP_IP!;
  const [a, b] = address.split(".").map(Number);
  if (
    isIP(address) !== 4 ||
    !(
      a === 10 ||
      (a === 172 && b >= 16 && b <= 31) ||
      (a === 192 && b === 168)
    ) ||
    process.env.ADTR_E2E_LDAP_USERNAME !== "fixture@synthetic.invalid"
  )
    throw new Error("Only the isolated synthetic LDAP fixture is allowed");
}

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

async function freshCode(auth: Authenticator) {
  // Enrollment, privileged writes and MFA login consume real counters. Never
  // bypass replay protection or use browser clock changes as server evidence.
  const earliest = (auth.lastCounter + 1) * 30_000 + 1100;
  while (Date.now() < earliest)
    await new Promise((resolve) => setTimeout(resolve, earliest - Date.now()));
  const now = Date.now();
  auth.lastCounter = Math.floor(now / 30_000);
  return totp(auth.secret, now);
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
  expect(response.headers()["cache-control"]).toBe("no-store");
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
  expect(response.headers()["cache-control"]).toBe("no-store");
  // Never return the password/TOTP-bearing POST body to assertion reporters.
  const sent = response.request().postDataJSON();
  return {
    value: await directoryV2ResponseJSON(response),
    sent: {
      domainId: sent.domainId,
      expectedRevision: sent.expectedRevision,
      expectedCredentialGeneration: sent.expectedCredentialGeneration,
      idempotencyKey: sent.idempotencyKey,
      taskUUID: sent.taskUUID,
    },
  };
}

async function grant(
  page: Page,
  password: string,
  auth: Authenticator,
  prefix: string,
) {
  await page
    .getByLabel("授权目标角色", { exact: true })
    .selectOption("platform_admin");
  await page.getByRole("button", { name: "审阅角色授权", exact: true }).click();
  await page
    .getByRole("checkbox", {
      name: "我已核对目标角色，确认授权覆盖当前及未来全部成员",
      exact: true,
    })
    .check();
  await proof(page, password, auth);
  return submit(page, `${prefix}/grant`, "确认授予角色使用权");
}

async function observationAction(page: Page, action: () => Promise<unknown>) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === "/api/directory/v2/observation" &&
        candidate.request().method() === "GET",
    ),
    action(),
  ]);
  expect(response.status(), "UI observation read must reach the real API").toBe(
    200,
  );
  expect(response.headers()["cache-control"]).toBe("no-store");
  const value = await directoryV2ResponseJSON(response);
  expect(
    value.dictionaryVersion,
    "Every v2 observation carries its fixed profile",
  ).toBe(2);
  return {
    value,
    query: new URL(response.url()).searchParams,
    actorId: response.headers()["x-adtr-user-id"],
  };
}

async function openDirectory(page: Page) {
  await navigateTo(page, "补充目录资产");
  await expect(
    page.getByRole("heading", { name: "补充目录资产", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("radio", { name: "选择数据源 synthetic.invalid", exact: true })
    .check();
  return observationAction(page, () =>
    page
      .getByRole("button", { name: "核对并打开连接详情", exact: true })
      .click(),
  );
}

async function lateFreshCode(auth: Authenticator) {
  for (;;) {
    const now = Date.now();
    const counter = Math.floor(now / 30_000);
    if (
      counter > auth.lastCounter &&
      now % 30_000 >= 26_000 &&
      now % 30_000 < 27_000
    ) {
      auth.lastCounter = counter;
      return totp(auth.secret, now);
    }
    const targetCounter = Math.max(
      auth.lastCounter + 1,
      counter + (now % 30_000 >= 27_000 ? 1 : 0),
    );
    await new Promise((resolve) =>
      setTimeout(resolve, Math.max(1, targetCounter * 30_000 + 26_000 - now)),
    );
  }
}

async function waitForTerminal(
  context: BrowserContext,
  taskUUID: string,
  actorId: number,
  expected: "succeeded" | "cancelled",
  safe: (value: unknown) => void,
) {
  let task: Task | undefined;
  await expect
    .poll(
      async () => {
        const value = await readJSON(
          context,
          `/api/directory/v2/task?taskUUID=${taskUUID}`,
          actorId,
        );
        safe(value);
        task = value.task as Task;
        if (
          [
            "succeeded",
            "failed",
            "partial_failed",
            "dead_letter",
            "cancelled",
          ].includes(task.state) &&
          task.state !== expected
        )
          throw new Error(
            `Synthetic directory worker ended ${task.state} instead of ${expected}`,
          );
        return task.state;
      },
      {
        timeout: 60_000,
        message: `Real directory executor must reach ${expected}`,
      },
    )
    .toBe(expected);
  return task!;
}

// Only actual UI requests, real worker/LDAP I/O, and a SELECT-only observer.
// Data-reader/custom-actor and cross-tab browser coverage remain separate.
// Capture is disabled before setup so synthetic credentials never enter artifacts.
// Playwright 1.56 also captures failure DOM snapshots when trace is off.
process.env.PLAYWRIGHT_NO_COPY_PROMPT = "1";
test.use({ trace: "off", screenshot: "off", video: "off" });
test.beforeAll(() => requireFixture());
test.beforeEach(async ({ context }) => {
  await observeDirectoryV2Responses(context);
});
test("real empty dictionary-v2 observation and cancellation during paged I/O", async ({
  page,
  context,
}) => {
  test.setTimeout(900_000);
  const username = process.env.ADTR_E2E_USERNAME!;
  const initialPassword = process.env.ADTR_E2E_PASSWORD!;
  let password = "Changed Directory Controls Password 123";
  const producerUsername = "directory-controls-producer";
  const producerInitial = "Initial Directory Controls Producer Password 123";
  const producerPassword = "Changed Directory Controls Producer Password 123";
  const accountUser = process.env.ADTR_E2E_LDAP_USERNAME!;
  const accountPassword = process.env.ADTR_E2E_LDAP_PASSWORD!;
  const ldapIP = process.env.ADTR_E2E_LDAP_IP!;
  const domain = "synthetic.invalid";
  const accountLabel = "Synthetic empty directory reader";
  const groupName = "E2EDirectoryControlsScope";
  const secrets = [
    initialPassword,
    password,
    producerInitial,
    producerPassword,
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
      /"(?:actorPassword|totpCode|ciphertext|userInfo)"\s*:/u.test(serialized),
      "secret-bearing public field",
    ).toBe(false);
  };
  const consoles: string[] = [];
  page.on("console", (message) => consoles.push(message.text()));
  page.on("pageerror", (error) => consoles.push(error.message));
  const requests: { path: string; at: number; currentCode: boolean }[] = [];
  let authenticator: Authenticator | undefined;
  let syncPosts = 0;
  context.on("request", (request) => {
    if (
      request.method() === "POST" &&
      ["/api/directory/v2/sync", "/api/directory/v2/cancel"].includes(
        new URL(request.url()).pathname,
      )
    ) {
      const path = new URL(request.url()).pathname;
      if (path === "/api/directory/v2/sync") syncPosts++;
      const at = Date.now();
      requests.push({
        path,
        at,
        currentCode:
          !!authenticator &&
          request.postDataJSON().totpCode === totp(authenticator.secret, at),
      });
    }
  });

  await page.goto("/");
  await login(page, username, initialPassword);
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await fillSecret(
    page.getByLabel("当前密码", { exact: true }),
    initialPassword,
  );
  await fillSecret(page.getByLabel("新密码", { exact: true }), password);
  await fillSecret(page.getByLabel("确认新密码", { exact: true }), password);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  let auth = await enrollMfa(page, password);
  secrets.push(auth.secret);
  const tenant = await readJSON(context, "/api/resources/tenant");
  expect(tenant.maxAdCount).toBe(2);
  expect(tenant.expireTime).toBeGreaterThan(Math.floor(Date.now() / 1000));

  await navigateTo(page, "域连接");
  await page.getByRole("button", { name: "新增域连接", exact: true }).click();
  await page.getByLabel("域 DNS 名称", { exact: true }).fill(domain);
  await page
    .getByLabel("域控 DNS 名称", { exact: true })
    .fill("dc.synthetic.invalid");
  await page.getByLabel("连接 IP（可选）", { exact: true }).fill(ldapIP);
  await page
    .getByLabel("初始凭据来源", { exact: true })
    .selectOption("unconfigured");
  await proof(page, password, auth);
  const createdDomain = await submit(
    page,
    "/api/domains/create-unconfigured",
    "保存本地连接",
  );
  expect(createdDomain.value).toMatchObject({
    result: "SUCCESS",
    revision: "1",
    requiresResourceAssignment: true,
  });
  safe(createdDomain.value);
  const domainId = createdDomain.value.domainId as string;
  const deniedScope = await context.request.get(
    `/api/directory/v2/observation?domainId=${domainId}`,
  );
  expect(deniedScope.status()).toBe(404);
  expect(await deniedScope.json()).toEqual({ error: "not_found" });

  await navigateTo(page, "资源与租户");
  await page.getByRole("button", { name: "资源组", exact: true }).click();
  await page.getByRole("button", { name: "新增资源组", exact: true }).click();
  await page.getByLabel("资源组名称", { exact: true }).fill(groupName);
  await page.getByLabel("域 ID", { exact: true }).fill(domainId);
  await proof(page, password, auth);
  expect(
    (await submit(page, "/api/resources/groups/create", "保存资源组")).value,
  ).toMatchObject({ result: "SUCCESS" });
  await page
    .getByRole("button", { name: `查看 ${groupName}`, exact: true })
    .click();
  await page.getByRole("button", { name: "调整关联角色", exact: true }).click();
  await page.getByLabel("显式关联 platform_admin", { exact: true }).check();
  await proof(page, password, auth);
  expect(
    (await submit(page, "/api/resources/groups/assign", "保存角色关联")).value,
  ).toMatchObject({ result: "SUCCESS", sessionRevoked: true });
  await login(page, username, password);
  await fillSecret(
    page.getByLabel("二次认证验证码", { exact: true }),
    await freshCode(auth),
  );
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  let actorId = (await readJSON(context, "/api/auth/me")).ID as number;

  // Production allows ten sensitive actions per actor per fifteen minutes.
  // Bootstrap: password/MFA start/enable (3), domain/group/assignment (3),
  // producer creation (1) = 7. The resource assignment above really revokes
  // the bootstrap session; create the producer only after that UI re-login.
  // Producer: password/MFA (3), account/connection grant/binding/directory
  // grant (4), first sync/second sync/cancel (3) = 10. No limit or clock bypass.
  await navigateTo(page, "访问管理");
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  await page.getByLabel("用户名", { exact: true }).fill(producerUsername);
  await fillSecret(
    page.getByLabel("初始密码", { exact: true }),
    producerInitial,
  );
  await fillSecret(
    page.getByLabel("确认初始密码", { exact: true }),
    producerInitial,
  );
  await page.getByLabel("角色", { exact: true }).fill("platform_admin");
  await proof(page, password, auth);
  const producer = await submit(page, "/api/access/users/create", "创建用户");
  safe(producer.value);
  expect(producer.value).toMatchObject({
    result: "SUCCESS",
    sessionRevoked: false,
  });
  expect(producer.value.ID).toBeGreaterThan(0);
  expect(producer.value.ID).not.toBe(actorId);
  await navigateTo(page, "退出登录");
  await login(page, producerUsername, producerInitial);
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await fillSecret(
    page.getByLabel("当前密码", { exact: true }),
    producerInitial,
  );
  await fillSecret(
    page.getByLabel("新密码", { exact: true }),
    producerPassword,
  );
  await fillSecret(
    page.getByLabel("确认新密码", { exact: true }),
    producerPassword,
  );
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  auth = await enrollMfa(page, producerPassword);
  secrets.push(auth.secret);
  authenticator = auth;
  password = producerPassword;
  actorId = (await readJSON(context, "/api/auth/me")).ID as number;
  expect(actorId).toBe(producer.value.ID);

  await navigateTo(page, "管理操作账户");
  await page.getByRole("button", { name: "新增操作账户", exact: true }).click();
  await page
    .getByRole("button", { name: `选择 ${domain}`, exact: true })
    .click();
  await page.getByLabel("登记标签（可选）", { exact: true }).fill(accountLabel);
  await fillSecret(page.getByLabel("AD 用户名", { exact: true }), accountUser);
  await fillSecret(
    page.getByLabel("AD 密码", { exact: true }),
    accountPassword,
  );
  await proof(page, password, auth);
  const createdAccount = await submit(
    page,
    "/api/operation-accounts/create",
    "保存本地登记",
  );
  safe(createdAccount.value);
  const accountId = createdAccount.value.accountId as string;
  await page
    .getByRole("button", { name: "查看凭据使用授权", exact: true })
    .click();
  const connectionGrant = await grant(
    page,
    password,
    auth,
    "/api/credential-use",
  );
  expect(connectionGrant.value).toMatchObject({
    result: "SUCCESS",
    purpose: "domain.connection_test",
    allowed: true,
  });
  safe(connectionGrant.value);

  await navigateTo(page, "域连接");
  const backToDomains = page.getByRole("button", {
    name: "返回域连接列表",
    exact: true,
  });
  if (await backToDomains.isVisible()) await backToDomains.click();
  await page
    .getByRole("button", { name: `查看 ${domain}`, exact: true })
    .click();
  await page.getByRole("button", { name: "管理凭据来源", exact: true }).click();
  await page
    .getByLabel("新的凭据来源", { exact: true })
    .selectOption("reference");
  await page
    .getByRole("button", { name: `选择账户 ${accountLabel}`, exact: true })
    .click();
  await proof(page, password, auth);
  expect(
    (
      await submit(
        page,
        "/api/domains/credential-source/reference",
        "保存凭据来源",
      )
    ).value,
  ).toMatchObject({
    result: "SUCCESS",
    credentialSource: "operation_account",
    replayed: false,
  });
  const beforeConnection = (
    await readJSON(context, `/api/domains/detail?domainId=${domainId}`, actorId)
  ).connection;
  const effectivePath = `/api/directory-credential-use/v2/effective?accountId=${accountId}`;
  expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
    purpose: "domain.directory_read.v2",
    explicitlyGranted: false,
    eligible: false,
    grantRevision: "0",
    consumerEnabled: true,
  });

  const initial = await openDirectory(page);
  expect(initial.actorId).toBe(String(actorId));
  expect(initial.value).toEqual({
    dictionaryVersion: 2,
    available: false,
    list: [],
    page: { pageIdx: 1, pageSize: 50, total: 0, totalPage: 0 },
  });
  await expect(
    page.getByRole("status").filter({
      hasText: "尚无当前配置的成功目录观察。此状态不表示目录为空。",
    }),
  ).toBeVisible();
  await navigateTo(page, "管理操作账户");
  const backToAccounts = page.getByRole("button", {
    name: "返回操作账户列表",
    exact: true,
  });
  if (await backToAccounts.isVisible()) await backToAccounts.click();
  await page
    .getByRole("button", { name: `查看 ${accountLabel}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "查看补充目录读取凭据使用授权", exact: true })
    .click();
  const directoryGrant = await grant(
    page,
    password,
    auth,
    "/api/directory-credential-use/v2",
  );
  expect(directoryGrant.value).toMatchObject({
    result: "SUCCESS",
    purpose: "domain.directory_read.v2",
    allowed: true,
    replayed: false,
  });
  safe(directoryGrant.value);
  expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
    purpose: "domain.directory_read.v2",
    explicitlyGranted: true,
    eligible: true,
  });
  expect(
    await readJSON(
      context,
      `/api/credential-use/effective?accountId=${accountId}`,
      actorId,
    ),
  ).toMatchObject({
    purpose: "domain.connection_test",
    explicitlyGranted: true,
    grantRevision: connectionGrant.value.grantRevision,
  });

  await navigateTo(page, "补充目录凭据授权");
  await expect(
    page.getByRole("table", {
      name: "具有已保存目录读取授权的账户",
      exact: true,
    }),
  ).toContainText(accountLabel);
  await openDirectory(page);

  const unavailable = "尚无当前配置的成功目录观察。此状态不表示目录为空。";
  const empty = "已成功观察，当前筛选结果为空。";
  const observation = page.getByRole("region", {
    name: "目录观察",
    exact: true,
  });
  await expect(observation).toContainText(unavailable);
  await expect(observation.getByText(empty, { exact: true })).toHaveCount(0);

  await proof(page, password, auth);
  const first = await submit(page, "/api/directory/v2/sync", "提交目录同步");
  safe(first.value);
  expect(first.value).toMatchObject({
    replayed: false,
    task: {
      taskName: "domain.directory_read.v2",
      domainId,
      maxAttempts: 1,
      result: {},
      cursor: {},
    },
  });
  expect(first.sent).toMatchObject({
    domainId,
    expectedRevision: beforeConnection.revision,
    expectedCredentialGeneration: beforeConnection.credentialRevision,
  });
  const successfulUUID = first.value.task.taskUUID as string;
  const completed = await waitForTerminal(
    context,
    successfulUUID,
    actorId,
    "succeeded",
    safe,
  );
  const taskRegion = page.getByRole("region", {
    name: "目录同步任务",
    exact: true,
  });
  await expect(taskRegion.getByRole("status")).toContainText(
    "服务器状态：已成功",
  );
  expect(completed).toMatchObject({
    taskUUID: successfulUUID,
    state: "succeeded",
    attempt: 1,
    progress: 100,
    result: {},
    cursor: {},
  });
  await expect
    .poll(() => readDirectoryV2Use(successfulUUID), {
      timeout: 15_000,
      message:
        "Successful real executor must release its actual directory dependencies",
    })
    .toEqual({
      state: "quiesced",
      reason: "executor_returned",
      dependencies: 0,
      taskState: "succeeded",
    });

  const refreshed = await observationAction(page, () =>
    page.getByRole("button", { name: "刷新目录观察", exact: true }).click(),
  );
  safe(refreshed.value);
  expect(refreshed.actorId).toBe(String(actorId));
  expect(refreshed.value).toMatchObject({
    dictionaryVersion: 2,
    available: true,
    observationId: successfulUUID,
    list: [],
    page: { pageIdx: 1, pageSize: 50, total: 0, totalPage: 0 },
    source: {
      server_name: "dc.synthetic.invalid",
      dc_host_name: "dc.synthetic.invalid",
      domain,
      naming_context: "dc=synthetic,dc=invalid",
      pages: 5,
    },
  });
  expect(
    Date.parse(refreshed.value.source.completed_at),
  ).toBeGreaterThanOrEqual(Date.parse(refreshed.value.source.started_at));
  expect(refreshed.value.source.elapsed_milliseconds).toBeGreaterThanOrEqual(
    10_000,
  );
  expect(refreshed.query.has("observationId")).toBe(false);
  await expect(observation.getByText(empty, { exact: true })).toBeVisible();
  await expect(observation.getByText(unavailable, { exact: true })).toHaveCount(
    0,
  );
  await expect(observation).toContainText(`观察编号：${successfulUUID}`);
  await expect(observation).toContainText("LDAP 页数 5");
  await expect(observation.getByRole("table")).toHaveCount(0);
  const pagination = observation.getByLabel("目录观察分页", { exact: true });
  await expect(
    pagination.getByRole("button", { name: "上一页", exact: true }),
  ).toBeDisabled();
  await expect(
    pagination.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();
  await taskRegion
    .getByRole("button", { name: "结束此任务查看", exact: true })
    .click();

  // Capture only real browser task polls. No route interception, fake responses,
  // clock edits, lifecycle writes or fabricated executor-return witnesses.
  const polled: { task: Task; receivedAt: number }[] = [];
  const pendingReads = new Set<Promise<void>>();
  let pollFailure = false;
  let scenarioFailed = false;
  let scenarioError: unknown;
  let responsesDrained = false;
  const onResponse = (response: Response) => {
    if (
      new URL(response.url()).pathname !== "/api/directory/v2/task" ||
      response.request().method() !== "GET"
    )
      return;
    const pending = (async () => {
      if (response.status() !== 200)
        throw new Error("Real directory UI poll failed");
      const value = await directoryV2ResponseJSON(response);
      safe(value);
      polled.push({ task: value.task as Task, receivedAt: Date.now() });
    })().catch(() => {
      pollFailure = true;
    });
    pendingReads.add(pending);
    void pending.finally(() => pendingReads.delete(pending));
  };
  page.on("response", onResponse);
  try {
    await fillSecret(
      page.getByLabel("操作者当前密码", { exact: true }),
      password,
    );
    await fillSecret(
      page.getByLabel("未使用的认证器验证码", { exact: true }),
      await lateFreshCode(auth),
    );
    const second = await submit(page, "/api/directory/v2/sync", "提交目录同步");
    safe(second.value);
    const cancelledUUID = second.value.task.taskUUID as string;
    expect(second.value).toMatchObject({
      replayed: false,
      task: {
        taskName: "domain.directory_read.v2",
        domainId,
        maxAttempts: 1,
        result: {},
        cursor: {},
      },
    });
    expect(cancelledUUID).not.toBe(successfulUUID);
    expect(second.sent.idempotencyKey).not.toBe(first.sent.idempotencyKey);
    const admitted = requests
      .filter((request) => request.path === "/api/directory/v2/sync")
      .at(-1)!;
    expect(
      admitted.currentCode,
      "The admitted sync must use the actual current TOTP",
    ).toBe(true);
    expect(
      admitted.at % 30_000,
      "Submit late in this real counter window",
    ).toBeGreaterThanOrEqual(26_000);
    expect(
      admitted.at % 30_000,
      "Leave time for the admitted request before rollover",
    ).toBeLessThan(29_000);

    await expect(
      taskRegion
        .getByRole("status")
        .filter({ hasText: "服务器状态：执行中 · 进度 41%" }),
    ).toBeVisible({ timeout: 15_000 });
    const passwordField = taskRegion.getByLabel("操作者当前密码", {
      exact: true,
    });
    const codeField = taskRegion.getByLabel("未使用的认证器验证码", {
      exact: true,
    });
    await fillSecret(passwordField, password);
    // This draft is generated from the actual current counter. It may still
    // be the counter consumed by sync, so retain it only as form-state proof;
    // never submit it before replacing it with the unused current counter.
    const draftCode = totp(auth.secret, Date.now());
    await fillSecret(codeField, draftCode);
    const typedAt = Date.now();
    const beforeHeartbeat = polled
      .filter(
        ({ task }) =>
          task.taskUUID === cancelledUUID &&
          task.state === "running" &&
          task.progress === 41,
      )
      .at(-1);
    expect(
      beforeHeartbeat,
      "UI progress must come from a real task response",
    ).toBeDefined();
    // Same version/progress but a newer updatedAt is the worker heartbeat,
    // not a different page authorization or a synthetic browser update.
    await expect
      .poll(
        () => {
          const duringProof = polled.filter(
            (entry) =>
              (entry === beforeHeartbeat || entry.receivedAt >= typedAt) &&
              entry.task.taskUUID === cancelledUUID &&
              entry.task.state === "running" &&
              entry.task.progress === 41,
          );
          return duringProof.some(({ task }, index) =>
            duringProof
              .slice(index + 1)
              .some(
                ({ task: later }) =>
                  later.resultVersion === task.resultVersion &&
                  Date.parse(later.updatedAt) > Date.parse(task.updatedAt),
              ),
          );
        },
        {
          timeout: 3_000,
          intervals: [100],
          message:
            "Observe the real worker heartbeat while the cancellation proof is entered",
        },
      )
      .toBe(true);
    expect(
      await passwordField.evaluate(
        (input, expected) => (input as HTMLInputElement).value === expected,
        password,
      ),
      "Password proof survives a real heartbeat",
    ).toBe(true);
    expect(
      await codeField.evaluate(
        (input, expected) => (input as HTMLInputElement).value === expected,
        draftCode,
      ),
      "TOTP draft survives a real heartbeat",
    ).toBe(true);
    await expect(
      taskRegion
        .getByRole("status")
        .filter({ hasText: "服务器状态：执行中 · 进度 42%" }),
    ).toBeVisible({ timeout: 5_000 });
    expect(
      await passwordField.evaluate(
        (input, expected) => (input as HTMLInputElement).value === expected,
        password,
      ),
      "Password proof survives the page-four progress update",
    ).toBe(true);
    expect(Date.now()).toBeGreaterThan(typedAt);
    expect(
      await codeField.evaluate(
        (input, expected) => (input as HTMLInputElement).value === expected,
        draftCode,
      ),
      "TOTP draft survives the page-four progress update",
    ).toBe(true);
    // Progress 42 is the real fourth-page authorization (~6 seconds), so the
    // next counter is now current and unused. Do not wait through another page
    // window, mint a future code, or weaken the server's replay protection.
    const now = Date.now();
    const cancelCounter = Math.floor(now / 30_000);
    expect(
      cancelCounter,
      "Cancellation must use a new real current counter",
    ).toBeGreaterThan(auth.lastCounter);
    auth.lastCounter = cancelCounter;
    await fillSecret(codeField, totp(auth.secret, now));
    const cancelled = await submit(
      page,
      "/api/directory/v2/cancel",
      "请求取消目录同步",
    );
    safe(cancelled.value);
    expect(cancelled.sent.taskUUID).toBe(cancelledUUID);
    expect(cancelled.value.task).toMatchObject({
      taskUUID: cancelledUUID,
      state: "cancel_requested",
      result: {},
      cursor: {},
    });
    const cancelRequest = requests.find(
      (request) => request.path === "/api/directory/v2/cancel",
    )!;
    expect(
      cancelRequest.currentCode,
      "Cancellation proof must be current at actual dispatch",
    ).toBe(true);
    expect(
      cancelRequest.at - admitted.at,
      "Cancellation must arrive during the bounded five-page read",
    ).toBeLessThan(10_000);
    expect(cancelled.value.task.progress).toBeGreaterThanOrEqual(42);
    expect(cancelled.value.task.progress).toBeLessThan(95);

    const stopped = await waitForTerminal(
      context,
      cancelledUUID,
      actorId,
      "cancelled",
      safe,
    );
    expect(stopped).toMatchObject({
      taskUUID: cancelledUUID,
      state: "cancelled",
      attempt: 1,
      result: {},
      cursor: {},
    });
    expect(stopped.progress).toBeLessThan(95);
    await expect(taskRegion.getByRole("status")).toContainText(
      "服务器状态：已取消",
    );
    // Terminal task state is insufficient: the real return witness must have
    // quiesced the use and released every task dependency in the owned DB.
    await expect
      .poll(() => readDirectoryV2Use(cancelledUUID), {
        timeout: 15_000,
        message:
          "Cancellation must reach real executor_returned quiescence and zero dependencies",
      })
      .toEqual({
        state: "quiesced",
        reason: "executor_returned",
        dependencies: 0,
        taskState: "cancelled",
      });
    await expect(observation.getByText(empty, { exact: true })).toBeVisible();
    await expect(observation).toContainText(`观察编号：${successfulUUID}`);
    const afterCancel = await observationAction(page, () =>
      page.getByRole("button", { name: "刷新目录观察", exact: true }).click(),
    );
    safe(afterCancel.value);
    expect(afterCancel.value).toEqual(refreshed.value);
    await expect(observation.getByText(empty, { exact: true })).toBeVisible();
    await expect(
      observation.getByText(unavailable, { exact: true }),
    ).toHaveCount(0);
    await expect(observation).not.toContainText(cancelledUUID);
    const cancelledSnapshot = await context.request.get(
      `/api/directory/v2/observation?${new URLSearchParams({ domainId, observationId: cancelledUUID })}`,
    );
    expect(cancelledSnapshot.status()).toBe(409);
    expect(await cancelledSnapshot.json()).toEqual({
      error: "directory_observation_unavailable",
    });
    const original = await readJSON(
      context,
      `/api/directory/v2/observation?${new URLSearchParams({ domainId, observationId: successfulUUID })}`,
      actorId,
    );
    safe(original);
    expect(original).toEqual(refreshed.value);
    expect(syncPosts).toBe(2);
    expect(
      requests.filter((request) => request.path === "/api/directory/v2/cancel"),
    ).toHaveLength(1);
    expect(
      requests.every((request) => request.currentCode),
      "Every directory write used its current counter",
    ).toBe(true);
  } catch (error) {
    scenarioFailed = true;
    scenarioError = error;
  } finally {
    page.off("response", onResponse);
    // Response parsing has a bounded drain; the outer runner alone owns all
    // fixture/container cleanup, including any failure before cancellation.
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      responsesDrained = await Promise.race([
        Promise.allSettled([...pendingReads]).then(() => true),
        new Promise<boolean>((resolve) => {
          timer = setTimeout(() => resolve(false), 2_000);
        }),
      ]);
    } finally {
      clearTimeout(timer);
    }
  }
  // Soft collection assertions preserve their own diagnostics while allowing
  // Playwright to report the original scenario assertion and source location.
  expect
    .soft(
      responsesDrained,
      "Real browser response parsing drained within two seconds",
    )
    .toBe(true);
  expect.soft(pollFailure, "All observed browser polls succeeded").toBe(false);
  if (scenarioFailed) throw scenarioError;

  const afterConnection = (
    await readJSON(context, `/api/domains/detail?domainId=${domainId}`, actorId)
  ).connection;
  expect(afterConnection.latestTaskUUID).toBe(beforeConnection.latestTaskUUID);
  expect(afterConnection.lastDiagnostic).toEqual(
    beforeConnection.lastDiagnostic,
  );
  expect(afterConnection.connectionState).toBe(
    beforeConnection.connectionState,
  );
  const storage = await page.evaluate(() => ({
    local: { ...localStorage },
    session: { ...sessionStorage },
    history: history.state,
    url: location.href,
  }));
  safe(storage);
  safe(consoles);
  expect(
    consoles.filter((message) => /Uncaught|unhandled/i.test(message)),
  ).toEqual([]);
  await navigateTo(page, "退出登录");
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await expect(observation).toHaveCount(0);
  expect(
    (
      await context.request.get(
        `/api/directory/v2/observation?domainId=${domainId}`,
      )
    ).status(),
  ).toBe(401);
});
