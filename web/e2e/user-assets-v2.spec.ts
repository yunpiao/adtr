import { navigateTo } from "./navigation";
import {
  test,
  expect,
  type BrowserContext,
  type Page,
  type Locator,
  type APIResponse,
  type Request,
} from "@playwright/test";
import { createHmac } from "node:crypto";
import { fixtureGET } from "./fixture-get";
import { holdAssetResponse as holdGenuineAssetResponse } from "./user-assets-v2-held-response";
import { installUserAssetAbortIsolation } from "./user-assets-v2-abort-isolation";
import { isIP } from "node:net";
import {
  directoryV2ResponseJSON,
  observeDirectoryV2Responses,
} from "./directory-v2-response-observer";
import type { DirectoryV2Input } from "../src/directory-v2-api";
import { readDirectoryV2Use } from "./directory-v2-ledger-observer";
import type { Task } from "../src/task-api";
type Authenticator = { secret: string; lastCounter: number; codes: string[] };
type Session = {
  ID: number;
  username: string;
  csrfToken: string;
  hasMfa: boolean;
  needChangePwd: boolean;
};

// Run only with the owned user-assets-v2 fixture: two actual TLS LDAP pages
// containing twelve users, twelve groups and six computer inheritance decoys.
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
    process.env.ADTR_E2E_LDAP_DIRECTORY_USER_ASSETS_V2 !== "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_EMPTY === "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_SLOW === "true"
  )
    throw new Error(
      "UserAssetsV2 acceptance requires scripts/test_auth_e2e.py --suite user-assets-v2 or user-assets-v2-readers and its isolated nonempty fast user-assets-v2 TLS LDAP fixture, real worker and fresh owned database. Missing fixture is blocked, never skipped.",
    );
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(process.env.ADTR_E2E_DB_CONTAINER!))
    throw new Error(
      "Directory readers require the owned isolated database container",
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
  for (;;) {
    const now = Date.now();
    const earliest = Math.max(
      (auth.lastCounter + 1) * 30_000 + 1100,
      now % 30_000 >= 27_000
        ? (Math.floor(now / 30_000) + 1) * 30_000 + 1100
        : now,
    );
    if (now < earliest) {
      await new Promise((resolve) => setTimeout(resolve, earliest - now));
      continue;
    }
    auth.lastCounter = Math.floor(now / 30_000);
    const code = totp(auth.secret, now);
    auth.codes.push(code);
    return code;
  }
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
  const auth: Authenticator = { secret, lastCounter: -1, codes: [] };
  await fillSecret(
    page.getByLabel("认证器验证码", { exact: true }),
    await freshCode(auth),
  );
  await page.getByRole("button", { name: "验证并启用", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用");
  return auth;
}

function identity({ ID, username, hasMfa, needChangePwd }: Session) {
  // Never hand a CSRF-bearing session object to an assertion reporter.
  return { ID, username, hasMfa, needChangePwd };
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
  const response = await fixtureGET(context.request, path);
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

async function changeInitialPassword(
  page: Page,
  initial: string,
  changed: string,
) {
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await fillSecret(page.getByLabel("当前密码", { exact: true }), initial);
  await fillSecret(page.getByLabel("新密码", { exact: true }), changed);
  await fillSecret(page.getByLabel("确认新密码", { exact: true }), changed);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
}

async function loginMfa(
  page: Page,
  username: string,
  password: string,
  auth: Authenticator,
) {
  await login(page, username, password);
  await fillSecret(
    page.getByLabel("二次认证验证码", { exact: true }),
    await freshCode(auth),
  );
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await signedIn(page, username);
}

async function signedIn(page: Page, username: string) {
  await expect(page.locator(".signed-in strong")).toHaveText(username);
  await expect(
    page.getByRole("status").filter({ hasText: "正在核验当前会话" }),
  ).toHaveCount(0);
}

async function logout(page: Page) {
  await navigateTo(page, "退出登录");
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
}

async function forbidden(response: APIResponse) {
  expect(response.status(), "Authenticated reader must be forbidden").toBe(403);
  expect(response.headers()["cache-control"]).toBe("no-store");
  expect(await response.json()).toEqual({ error: "forbidden" });
}

function guid(row: number) {
  const hex = row.toString(16).padStart(2, "0").repeat(16);
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
function expectedUser(row: number) {
  const name = `user-${String(row).padStart(2, "0")}`;
  return {
    objectGUID: guid(row),
    distinguishedName: `CN=${name},OU=Users,DC=synthetic,DC=invalid`,
    kind: "user",
    objectClass: ["organizationalperson", "person", "top", "user"],
    samAccountName: row === 12 ? null : name,
    userAccountControl: row === 12 ? null : row === 1 ? 0 : 512,
    objectSid: row === 12 ? null : `S-1-5-21-1-2-3-${1000 + row}`,
    mail:
      row === 12
        ? null
        : row === 1
          ? " Mixed+%_&*?'\"\\()[]@example.test \x00\n\t😀\u202e"
          : `row-${String(row).padStart(2, "0")}@example.test`,
    description:
      row === 12
        ? null
        : [
            row === 1
              ? "<img src=x onerror=alert(1)>\x00\n\t😀\u202e"
              : `Synthetic row ${String(row).padStart(2, "0")}`,
          ],
    whenCreated:
      row === 12
        ? null
        : row === 1
          ? "0001-01-01T00:00:00Z"
          : "2026-10-01T02:03:04Z",
  };
}
async function assetAction(
  page: Page,
  action: () => Promise<unknown>,
  detail = false,
  status = 200,
) {
  const path = detail ? "/api/user-assets/v2/detail" : "/api/user-assets/v2";
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === path &&
        candidate.request().method() === "GET",
    ),
    action(),
  ]);
  expect(
    response.status(),
    "User asset UI must read the actual authenticated API",
  ).toBe(status);
  expect(response.headers()["cache-control"]).toBe("no-store");
  const value = await directoryV2ResponseJSON(response);
  return {
    value,
    query: new URL(response.url()).searchParams,
    actorId: response.headers()["x-adtr-user-id"],
  };
}
async function openAssets(page: Page) {
  await navigateTo(page, "用户资产");
  await expect(
    page.getByRole("heading", { name: "用户资产", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("radio", { name: "选择数据源 synthetic.invalid", exact: true })
    .check();
  return assetAction(page, () =>
    page
      .getByRole("button", { name: "核对并打开连接详情", exact: true })
      .click(),
  );
}
async function searchAssets(page: Page, text: string) {
  await page.getByLabel("用户资产关键词", { exact: true }).fill(text);
  return assetAction(page, () =>
    page.getByRole("button", { name: "搜索用户", exact: true }).click(),
  );
}

// Hold genuine authenticated bytes, not a fabricated success body. A narrowly
// armed controller can ignore cancellation; the genuine body still crosses the
// production actor check and bounded parser. UI generations reject late values.
// Exact original-reader consumption is required on both engines; the fixed
// engine additionally requires native completion. Facts are never conflated.
const holdAssetResponse = (page: Page, path: string, actorId: number) =>
  holdGenuineAssetResponse(page, path, actorId, expectedUser(1));
async function ignoreAssetAbort(page: Page) {
  await page.addInitScript(installUserAssetAbortIsolation);
}

process.env.PLAYWRIGHT_NO_COPY_PROMPT = "1";
test.use({ trace: "off", screenshot: "off", video: "off" });
test.beforeAll(() => requireFixture());
test.beforeEach(async ({ context }) => {
  await observeDirectoryV2Responses(context);
});
test("UserAssetsV2 real worker TLS LDAP PostgreSQL search and pinned detail", async ({
  page,
  context,
}, testInfo) => {
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
  let syncPosts = 0;
  context.on("request", (request) => {
    if (
      request.method() === "POST" &&
      new URL(request.url()).pathname === "/api/directory/v2/sync"
    )
      syncPosts++;
  });

  await ignoreAssetAbort(page);
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
  const bootstrap = { username, password, auth };
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
  // producer creation (1), final detach/account-delete/domain-delete (3) = 10. The resource assignment above really revokes
  // the bootstrap session; create the producer only after that UI re-login.
  // Producer: password/MFA (3), account/connection grant/binding/directory
  // grant (4), first sync/second sync (2) = 9. No limit or clock bypass.
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

  const beforeObservation = await openDirectory(page);
  expect(beforeObservation.value).toEqual({
    dictionaryVersion: 2,
    available: false,
    list: [],
    page: { pageIdx: 1, pageSize: 50, total: 0, totalPage: 0 },
  });
  await proof(page, password, auth);
  const submitted = await submit(
    page,
    "/api/directory/v2/sync",
    "提交目录同步",
  );
  safe(submitted.value);
  const intent = submitted.sent as DirectoryV2Input;
  expect(intent).toMatchObject({
    domainId,
    expectedRevision: beforeConnection.revision,
    expectedCredentialGeneration: beforeConnection.credentialRevision,
  });
  expect(submitted.value.replayed).toBe(false);
  const taskUUID = submitted.value.task.taskUUID as string;
  let completed: Task | undefined;
  await expect
    .poll(
      async () => {
        const value = await readJSON(
          context,
          `/api/directory/v2/task?taskUUID=${taskUUID}`,
          actorId,
        );
        safe(value);
        completed = value.task;
        return completed?.state;
      },
      {
        timeout: 60_000,
        message:
          "Real nonempty LDAP worker must successfully publish its observation",
      },
    )
    .toBe("succeeded");
  expect(completed).toMatchObject({
    taskUUID,
    domainId,
    state: "succeeded",
    attempt: 1,
    result: {},
    cursor: {},
  });
  await expect
    .poll(() => readDirectoryV2Use(taskUUID), { timeout: 15_000 })
    .toEqual({
      state: "quiesced",
      reason: "executor_returned",
      dependencies: 0,
      taskState: "succeeded",
    });
  const expected = Array.from({ length: 12 }, (_, index) =>
    expectedUser(index + 1),
  );
  const first = await openAssets(page);
  const selection = {
    domainId,
    revision: beforeConnection.revision,
    credentialRevision: beforeConnection.credentialRevision,
  };
  expect(first.actorId).toBe(String(actorId));
  expect(first.query.has("observationId")).toBe(false);
  expect(first.value).toMatchObject({
    dictionaryVersion: 2,
    selection,
    available: true,
    observationId: taskUUID,
    list: expected.slice(0, 10),
    page: { pageIdx: 1, pageSize: 10, total: 12, totalPage: 2 },
    source: {
      server_name: "dc.synthetic.invalid",
      dc_host_name: "dc.synthetic.invalid",
      domain,
      naming_context: "dc=synthetic,dc=invalid",
      pages: 2,
    },
  });
  const table = page.getByRole("table", {
    name: "用户资产列表",
    exact: true,
    includeHidden: true,
  });
  await expect(table.locator("tbody tr")).toHaveCount(10);
  await expect(table).not.toContainText("group-");
  await expect(table).not.toContainText("computer-");
  await expect(
    page.getByRole("button", { name: "提交目录同步", exact: true }),
  ).toHaveCount(0);
  await test.step("SOC desktop and mobile use real disclosures, routes and asset data", async () => {
    const soc = await context.newPage();
    try {
      await soc.setViewportSize({ width: 1440, height: 900 });
      await soc.goto("/#user-assets-v2");
      await signedIn(soc, producerUsername);
      for (const name of ["用户资产", "后台任务", "操作审计"])
        await expect(
          soc.getByRole("button", { name, exact: true }),
        ).toBeVisible();
      for (const [group, label] of [
        ["management", "平台管理"],
        ["collection", "采集与接入"],
        ["identity", "账户与身份"],
      ]) {
        const details = soc.locator(
          `details[data-navigation-group="${group}"]`,
        );
        await expect(details.locator("summary")).toContainText(label);
        await expect(details).not.toHaveAttribute("open", "");
      }
      await expect(
        soc.getByRole("button", { name: "访问管理", exact: true }),
      ).toHaveCount(0);
      const desktop = await openAssets(soc);
      expect(desktop.value).toEqual(first.value);
      const socTable = soc.getByRole("table", {
        name: "用户资产列表",
        exact: true,
      });
      await expect(socTable.locator("tbody tr")).toHaveCount(10);
      // Measure the unscrolled first screen; the first complete user row must
      // fit at the approved desktop viewport without scrolling it into view.
      expect(await soc.evaluate(() => scrollY)).toBe(0);
      const firstRowBounds = await socTable
        .locator("tbody tr")
        .first()
        .boundingBox();
      expect(firstRowBounds).not.toBeNull();
      expect(firstRowBounds!.y).toBeGreaterThanOrEqual(0);
      expect(firstRowBounds!.y + firstRowBounds!.height).toBeLessThanOrEqual(
        900,
      );
      await expect(
        soc.getByRole("button", { name: "更换数据源", exact: true }),
      ).toBeVisible();
      await expect(soc.locator("#asset-source-picker")).toBeHidden();
      expect(
        await soc.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      await soc.screenshot({
        path: testInfo.outputPath(
          "user-assets-v2-soc-desktop-user-assets-list.png",
        ),
        fullPage: true,
      });
      await soc.screenshot({
        path: testInfo.outputPath(
          "user-assets-v2-soc-desktop-user-assets-first-screen.png",
        ),
        fullPage: false,
      });

      const desktopDetail = await assetAction(
        soc,
        () =>
          soc
            .getByRole("button", { name: "查看用户 user-01", exact: true })
            .click(),
        true,
      );
      expect(desktopDetail.value.object).toEqual(expected[0]);
      const desktopDrawer = soc.getByRole("dialog", {
        name: "用户详情抽屉",
        exact: true,
      });
      await expect(desktopDrawer).toHaveAttribute("aria-modal", "true");
      const desktopBounds = await desktopDrawer.boundingBox();
      expect(desktopBounds).toEqual({ x: 920, y: 0, width: 520, height: 900 });
      await soc.screenshot({
        path: testInfo.outputPath(
          "user-assets-v2-soc-desktop-user-asset-detail.png",
        ),
        fullPage: false,
      });
      await soc
        .getByRole("button", { name: "返回用户列表", exact: true })
        .click();
      await expect(desktopDrawer).toHaveCount(0);
      await expect(
        soc.getByRole("button", { name: "查看用户 user-01", exact: true }),
      ).toBeFocused();

      // Visit an actual destination in each secondary group. Native history
      // still revalidates the session and keeps the matching group reachable.
      for (const name of [
        "后台任务",
        "操作审计",
        "访问管理",
        "域连接",
        "账户概览",
      ]) {
        await navigateTo(soc, name);
        await expect(
          soc.getByRole("heading", { name, exact: true }),
        ).toBeVisible();
        await expect(
          soc.getByRole("button", { name, exact: true }),
        ).toHaveAttribute("aria-current", "page");
      }
      await soc.goBack();
      await expect(
        soc.getByRole("heading", { name: "域连接", exact: true }),
      ).toBeVisible();
      await soc.goForward();
      await expect(
        soc.getByRole("heading", { name: "账户概览", exact: true }),
      ).toBeVisible();

      await soc.setViewportSize({ width: 390, height: 844 });
      const toggle = soc.getByRole("button", { name: "打开导航", exact: true });
      await expect(toggle).toBeVisible();
      await expect(soc.locator("#soc-navigation-panel")).toBeHidden();
      await toggle.click();
      const navigation = soc.getByRole("dialog", {
        name: "工作区导航",
        exact: true,
      });
      await expect(navigation).toBeVisible();
      await expect(
        navigation.getByRole("button", { name: "用户资产", exact: true }),
      ).toBeVisible();
      const management = soc.locator(
        'details[data-navigation-group="management"]',
      );
      await expect(management).toHaveAttribute("open", "");
      await management.locator("summary").click();
      await expect(management).not.toHaveAttribute("open", "");
      await soc.screenshot({
        path: testInfo.outputPath(
          "user-assets-v2-soc-mobile-navigation-open.png",
        ),
        fullPage: false,
      });
      await soc.keyboard.press("Escape");
      await expect(navigation).toHaveCount(0);
      await expect(toggle).toBeFocused();
      await navigateTo(soc, "系统健康");
      await expect(
        soc.getByRole("heading", { name: "系统健康", exact: true }),
      ).toBeVisible();
      await expect(soc.locator("#soc-navigation-panel")).toBeHidden();
      await expect(management).toHaveAttribute("open", "");
      const mobile = await openAssets(soc);
      expect(mobile.value).toEqual(first.value);
      await expect(soc.locator("#soc-navigation-panel")).toBeHidden();
      await expect(socTable.locator("tbody tr")).toHaveCount(10);
      // A complete first SAM value must be readable on the untouched mobile
      // first screen, not just the first card's border below the toolbar.
      expect(await soc.evaluate(() => scrollY)).toBe(0);
      const firstMobileSAM = socTable
        .locator("tbody tr")
        .first()
        .locator('td[data-label="SAM"]');
      await expect(firstMobileSAM).toHaveText("user-01");
      await expect(firstMobileSAM).toBeVisible();
      const firstMobileSAMBounds = await firstMobileSAM.boundingBox();
      expect(firstMobileSAMBounds).not.toBeNull();
      expect(firstMobileSAMBounds!.y).toBeGreaterThanOrEqual(0);
      expect(
        firstMobileSAMBounds!.y + firstMobileSAMBounds!.height,
      ).toBeLessThanOrEqual(844);
      expect(
        await soc.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      await soc.screenshot({
        path: testInfo.outputPath(
          "user-assets-v2-soc-mobile-user-assets-list.png",
        ),
        fullPage: true,
      });
      await soc.screenshot({
        path: testInfo.outputPath(
          "user-assets-v2-soc-mobile-user-assets-first-screen.png",
        ),
        fullPage: false,
      });

      const mobileDetail = await assetAction(
        soc,
        () =>
          soc
            .getByRole("button", { name: "查看用户 user-01", exact: true })
            .click(),
        true,
      );
      expect(mobileDetail.value.object).toEqual(expected[0]);
      const drawer = soc.getByRole("dialog", {
        name: "用户详情抽屉",
        exact: true,
      });
      await expect(drawer).toHaveAttribute("aria-modal", "true");
      const bounds = await drawer.boundingBox();
      expect(bounds).not.toBeNull();
      expect(bounds!.x).toBeGreaterThanOrEqual(0);
      expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
      expect(bounds!.height).toBe(844);
      // Playwright 1.56's role selector still matches inert descendants.
      // Prove the browser's actual focus barrier, rather than DOM absence.
      const searchForm = soc.locator("form.asset-search-toolbar");
      await expect(searchForm).toHaveAttribute("inert", "");
      await expect(searchForm).toHaveJSProperty("inert", true);
      const backgroundSearch = searchForm.getByRole("button", {
        name: "搜索用户",
        exact: true,
      });
      const identity = soc.getByRole("region", {
        name: "用户身份",
        exact: true,
      });
      await identity.focus();
      await expect(identity).toBeFocused();
      await backgroundSearch.evaluate((button) =>
        (button as HTMLButtonElement).focus(),
      );
      await expect(identity).toBeFocused();
      expect(
        await drawer.evaluate((element) =>
          element.contains(document.activeElement),
        ),
      ).toBe(true);
      expect(
        await soc.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      await soc.screenshot({
        path: testInfo.outputPath(
          "user-assets-v2-soc-mobile-user-asset-detail.png",
        ),
        fullPage: false,
      });
      const close = soc.getByRole("button", {
        name: "关闭用户详情",
        exact: true,
      });
      await identity.focus();
      await soc.keyboard.press("Shift+Tab");
      await expect(
        soc.getByRole("button", { name: "返回用户列表", exact: true }),
      ).toBeFocused();
      await soc.keyboard.press("Tab");
      await expect(identity).toBeFocused();
      await soc.keyboard.press("Tab");
      await expect(close).toBeFocused();
      await soc.keyboard.press("Escape");
      await expect(drawer).toHaveCount(0);
      await expect(
        soc.getByRole("button", { name: "查看用户 user-01", exact: true }),
      ).toBeFocused();
      await soc
        .getByRole("button", { name: "更换数据源", exact: true })
        .click();
      await expect(soc.locator("#asset-source-picker")).toBeVisible();
      await expect(
        soc.getByRole("radio", {
          name: "选择数据源 synthetic.invalid",
          exact: true,
        }),
      ).toBeVisible();
      await soc
        .getByRole("button", { name: "收起数据源选择", exact: true })
        .click();
      await expect(soc.locator("#asset-source-picker")).toBeHidden();
      await expect(socTable.locator("tbody tr")).toHaveCount(10);
    } finally {
      await soc.close();
      await page.bringToFront();
      await signedIn(page, producerUsername);
    }
  });
  const privateTaskCount = (
    await readJSON(context, "/api/tasks?pageIdx=1&pageSize=20")
  ).page.total;
  const firstDetail = await assetAction(
    page,
    () =>
      page
        .getByRole("button", { name: "查看用户 user-01", exact: true })
        .click(),
    true,
  );
  expect(firstDetail.value).toEqual({
    dictionaryVersion: 2,
    selection,
    observationId: taskUUID,
    source: first.value.source,
    object: expected[0],
  });
  expect(firstDetail.query.get("objectGUID")).toBe(guid(1));
  expect(firstDetail.query.get("observationId")).toBe(taskUUID);
  const panel = page.getByRole("region", { name: "用户详情", exact: true });
  await expect(panel).toContainText("0001-01-01T00:00:00Z");
  await expect(panel).toContainText(
    "<img src=x onerror=alert(1)>\\u0000\\u000a\\u0009😀\\u202e",
  );
  await expect(panel.locator("img, a[href^='mailto:']")).toHaveCount(0);
  expect(await panel.textContent()).not.toMatch(/[\u0000\u202e]/u);
  await page.screenshot({
    path: testInfo.outputPath("user-assets-v2-hostile-detail.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "关闭用户详情", exact: true }).focus();
  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "查看用户 user-01", exact: true }),
  ).toBeFocused();

  const second = await assetAction(page, () =>
    page.getByRole("button", { name: "用户资产下一页", exact: true }).click(),
  );
  expect(second.query.get("observationId")).toBe(taskUUID);
  expect(second.value.list).toEqual(expected.slice(10));
  expect(second.value.page).toEqual({
    pageIdx: 2,
    pageSize: 10,
    total: 12,
    totalPage: 2,
  });
  const nullDetail = await assetAction(
    page,
    () =>
      page
        .getByRole("button", { name: `查看用户 ${guid(12)}`, exact: true })
        .click(),
    true,
  );
  expect(nullDetail.value.object).toEqual(expected[11]);
  await expect(
    panel.locator("dd").filter({ hasText: /^未返回$/u }),
  ).toHaveCount(6);
  await page.screenshot({
    path: testInfo.outputPath("user-assets-v2-null-detail.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "关闭用户详情", exact: true }).click();

  await test.step("overlong native input remains intact and sends no read; exact UTF-16 boundaries pass", async () => {
    const field = page.getByLabel("用户资产关键词", { exact: true });
    let reads = 0;
    const observe = (request: Request) => {
      if (
        request.method() === "GET" &&
        new URL(request.url()).pathname.startsWith("/api/user-assets/v2")
      )
        reads++;
    };
    page.on("request", observe);
    try {
      for (const [overlong, boundary] of [
        ["x".repeat(51), "x".repeat(50)],
        ["😀".repeat(26), "😀".repeat(25)],
      ]) {
        await field.fill("");
        await field.pressSequentially(overlong);
        await expect(field).toHaveValue(overlong);
        const before = reads;
        await field.press("Enter");
        await expect(page.getByRole("alert")).toContainText(
          "搜索最多 50 个 UTF-16 单元",
        );
        await expect(field).toHaveValue(overlong);
        // Flush the browser's submitted-form microtasks and one rendered frame;
        // the visible validation witness above settles the synchronous reject.
        await page.evaluate(
          () =>
            new Promise<void>((resolve) =>
              requestAnimationFrame(() => resolve()),
            ),
        );
        expect(reads).toBe(before);
        await field.fill(boundary);
        const accepted = await assetAction(page, () => field.press("Enter"));
        expect(accepted.query.get("search")).toBe(boundary);
        expect(accepted.query.get("observationId")).toBe(taskUUID);
        expect(accepted.value).toMatchObject({
          available: true,
          list: [],
          page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 },
        });
        await expect(field).toHaveValue(boundary);
        await expect(page.getByRole("alert")).toHaveCount(0);
      }
    } finally {
      page.off("request", observe);
    }
  });

  await test.step("SAM SID mail and DN search scans beyond first unfiltered page", async () => {
    for (const query of [
      "UsEr-11",
      "S-1-5-21-1-2-3-1011",
      "row-11@example.test",
      "CN=user-11,OU=Users",
    ]) {
      const found = await searchAssets(page, query);
      expect(found.query.get("search")).toBe(query);
      expect(found.query.get("observationId")).toBe(taskUUID);
      expect(found.value.list).toEqual([expected[10]]);
      expect(found.value.page).toEqual({
        pageIdx: 1,
        pageSize: 10,
        total: 1,
        totalPage: 1,
      });
      await expect(table.locator("tbody tr")).toHaveCount(1);
    }
    for (const query of ["+%_&*?'\"\\()[]", " Mixed", "😀", "@example.test "]) {
      const found = await searchAssets(page, query);
      expect(found.query.get("search")).toBe(query); // URL encoding exactly once.
      expect(found.value.list).toEqual([expected[0]]);
    }
    for (const query of [
      "<img",
      guid(1),
      "' OR 1=1 --",
      "*)(objectClass=*)",
      "\\u0000",
    ]) {
      const none = await searchAssets(page, query);
      expect(none.value).toMatchObject({
        available: true,
        observationId: taskUUID,
        list: [],
        page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 },
      });
    }
  });
  const cleared = await assetAction(page, () =>
    page.getByRole("button", { name: "清除用户搜索", exact: true }).click(),
  );
  expect(cleared.query.get("observationId")).toBe(taskUUID);
  expect(cleared.value.list).toEqual(expected.slice(0, 10));
  for (const size of [20, 30, 40, 50, 10]) {
    const resized = await assetAction(page, () =>
      page
        .getByLabel("用户资产每页条数", { exact: true })
        .selectOption(String(size)),
    );
    expect(resized.query.get("observationId")).toBe(taskUUID);
    expect(resized.value.list).toEqual(expected.slice(0, size));
    expect(resized.value.page).toEqual({
      pageIdx: 1,
      pageSize: size,
      total: 12,
      totalPage: Math.ceil(12 / size),
    });
  }
  const common = {
    domainId,
    expectedRevision: selection.revision,
    expectedCredentialRevision: selection.credentialRevision,
    observationId: taskUUID,
  };
  for (const query of ["\x00", "\n", "\u202e"]) {
    const raw = await readJSON(
      context,
      `/api/user-assets/v2?${new URLSearchParams({ ...common, search: query })}`,
      actorId,
    );
    expect(raw.list).toEqual([expected[0]]);
  }
  const pastEnd = await readJSON(
    context,
    `/api/user-assets/v2?${new URLSearchParams({ ...common, pageIdx: "10000" })}`,
    actorId,
  );
  expect(pastEnd.page).toEqual({
    pageIdx: 10000,
    pageSize: 10,
    total: 12,
    totalPage: 2,
  });
  expect(pastEnd.list).toEqual([]);
  for (const query of [
    "userStatus=active",
    "pageSize=-1",
    "search=" + encodeURIComponent("😀".repeat(26)),
    "search=a&search=b",
    "unknown=1",
  ]) {
    const invalid = await context.request.get(
      `/api/user-assets/v2?${new URLSearchParams(common)}&${query}`,
    );
    expect(invalid.status()).toBe(query.startsWith("userStatus") ? 422 : 400);
    expect(await invalid.json()).toEqual({
      error: query.startsWith("userStatus")
        ? "unsupported_user_asset_filter"
        : "invalid_input",
    });
  }
  for (const row of [13, 25, 99]) {
    const absent = await context.request.get(
      `/api/user-assets/v2/detail?${new URLSearchParams({ ...common, objectGUID: guid(row) })}`,
    );
    expect(absent.status()).toBe(404);
    expect(await absent.json()).toEqual({ error: "not_found" });
  }
  expect(
    (await readJSON(context, "/api/tasks?pageIdx=1&pageSize=20")).page.total,
  ).toBe(privateTaskCount);
  expect(syncPosts).toBe(1);

  await test.step("late genuine detail cannot reopen a closed panel when transport ignores abort", async () => {
    const held = await holdAssetResponse(
      page,
      "/api/user-assets/v2/detail",
      actorId,
    );
    await page
      .getByRole("button", { name: "查看用户 user-01", exact: true })
      .click();
    try {
      await held.fetched();
      await page
        .getByRole("button", { name: "关闭用户详情", exact: true })
        .click();
      expect(await held.release()).toMatchObject({
        application: "complete-consumed",
      });
      await expect(panel).toHaveCount(0);
    } finally {
      await held.release();
    }
  });
  await test.step("late list cannot replace a newer search", async () => {
    const held = await holdAssetResponse(page, "/api/user-assets/v2", actorId);
    await page.getByLabel("用户资产关键词", { exact: true }).fill("user-02");
    await page.getByRole("button", { name: "搜索用户", exact: true }).click();
    try {
      await held.fetched();
      await page.getByLabel("用户资产关键词", { exact: true }).fill("user-11");
      const current = await assetAction(page, () =>
        page.getByLabel("用户资产关键词", { exact: true }).press("Enter"),
      );
      expect(current.value.list).toEqual([expected[10]]);
      expect(await held.release()).toMatchObject({
        application: "complete-consumed",
      });
      await expect(table).toContainText("user-11");
      await expect(table).not.toContainText("user-02");
    } finally {
      await held.release();
    }
  });

  // A second genuine producer run is the ninth sensitive action for producer.
  // Keep this reader page open so its captured observation cannot silently move.
  const producerPage = await context.newPage();
  let newerId = "";
  try {
    await producerPage.goto("/");
    await signedIn(producerPage, producerUsername);
    await openDirectory(producerPage);
    await proof(producerPage, password, auth);
    const newer = await submit(
      producerPage,
      "/api/directory/v2/sync",
      "提交目录同步",
    );
    newerId = newer.value.task.taskUUID;
    expect(newerId).not.toBe(taskUUID);
    await expect
      .poll(
        async () =>
          (
            await readJSON(
              context,
              `/api/directory/v2/task?taskUUID=${newerId}`,
              actorId,
            )
          ).task.state,
        { timeout: 60_000 },
      )
      .toBe("succeeded");
  } finally {
    await producerPage.close();
  }
  const pinned = await assetAction(page, () =>
    page.getByRole("button", { name: "清除用户搜索", exact: true }).click(),
  );
  expect(pinned.value.observationId).toBe(taskUUID);
  const pinnedDetail = await assetAction(
    page,
    () =>
      page
        .getByRole("button", { name: "查看用户 user-01", exact: true })
        .click(),
    true,
  );
  expect(pinnedDetail.value.observationId).toBe(taskUUID);
  await page.getByRole("button", { name: "关闭用户详情", exact: true }).click();
  const refreshed = await assetAction(page, () =>
    page.getByRole("button", { name: "刷新用户资产观测", exact: true }).click(),
  );
  expect(refreshed.query.has("observationId")).toBe(false);
  expect(refreshed.value.observationId).toBe(newerId);
  await expect(panel).toHaveCount(0);
  await expect(table.locator("tbody tr")).toHaveCount(10);
  await page.screenshot({
    path: testInfo.outputPath("user-assets-v2-nonempty-list.png"),
    fullPage: true,
  });

  await assetAction(
    page,
    () =>
      page
        .getByRole("button", { name: "查看用户 user-01", exact: true })
        .click(),
    true,
  );
  await page.getByRole("button", { name: "关闭用户详情", exact: true }).click();
  await navigateTo(page, "账户概览");
  await page.goBack();
  await expect(
    page.getByRole("heading", { name: "用户资产", exact: true }),
  ).toBeVisible();
  await expect(panel).toHaveCount(0);
  await page.goForward();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  await test.step("a genuine detail404 after production source deletion clears the selected private source", async () => {
    const current = await openAssets(page);
    expect(current.value.observationId).toBe(newerId);
    await assetAction(
      page,
      () =>
        page
          .getByRole("button", { name: "查看用户 user-01", exact: true })
          .click(),
      true,
    );
    await expect(table.locator("tbody tr")).toHaveCount(10);
    await expect(panel).toContainText(guid(1));
    await expect
      .poll(() => readDirectoryV2Use(newerId), { timeout: 15_000 })
      .toEqual({
        state: "quiesced",
        reason: "executor_returned",
        dependencies: 0,
        taskState: "succeeded",
      });
    const administrator = await context
      .browser()!
      .newContext({ baseURL: process.env.ADTR_E2E_BASE_URL });
    await observeDirectoryV2Responses(administrator);
    const adminPage = await administrator.newPage();
    try {
      await adminPage.goto("/");
      await loginMfa(
        adminPage,
        bootstrap.username,
        bootstrap.password,
        bootstrap.auth,
      );
      // Existing bootstrap's eighth, ninth and tenth sensitive actions. Use
      // production dependency/credential guards, never SQL state or guard edits.
      await navigateTo(adminPage, "域连接");
      await adminPage
        .getByRole("button", { name: `查看 ${domain}`, exact: true })
        .click();
      await adminPage
        .getByRole("button", { name: "管理凭据来源", exact: true })
        .click();
      await adminPage
        .getByLabel("新的凭据来源", { exact: true })
        .selectOption("detach");
      await proof(adminPage, bootstrap.password, bootstrap.auth);
      const detached = await submit(
        adminPage,
        "/api/domains/credential-source/detach",
        "保存凭据来源",
      );
      expect(detached.value).toMatchObject({
        result: "SUCCESS",
        operation: "detach",
        credentialSource: "unconfigured",
      });
      await navigateTo(adminPage, "管理操作账户");
      await adminPage
        .getByRole("button", { name: `查看 ${accountLabel}`, exact: true })
        .click();
      await adminPage
        .getByRole("button", { name: "删除本地登记", exact: true })
        .click();
      await adminPage
        .getByLabel("输入完整账户 ID 确认", { exact: true })
        .fill(accountId);
      await proof(adminPage, bootstrap.password, bootstrap.auth);
      const deletedAccount = await submit(
        adminPage,
        "/api/operation-accounts/delete",
        "确认删除本地登记",
      );
      expect(deletedAccount.value).toMatchObject({
        result: "SUCCESS",
        operation: "delete",
        accountId,
        deleted: true,
      });
      await navigateTo(adminPage, "域连接");
      const back = adminPage.getByRole("button", {
        name: "返回域连接列表",
        exact: true,
      });
      if (await back.isVisible()) await back.click();
      await adminPage
        .getByRole("button", { name: `查看 ${domain}`, exact: true })
        .click();
      await adminPage
        .getByRole("button", { name: "删除域连接", exact: true })
        .click();
      await adminPage
        .getByLabel("输入域名确认删除", { exact: true })
        .fill(domain);
      await proof(adminPage, bootstrap.password, bootstrap.auth);
      const deletedDomain = await submit(
        adminPage,
        "/api/domains/delete",
        "确认删除本地连接",
      );
      expect(deletedDomain.value).toMatchObject({
        result: "SUCCESS",
        domainId,
      });
      // Domain lifecycle removes current scope without logging this reader out.
      // Do not substitute a 401/session-clear test for the ambiguous detail404.
      expect((await readJSON(context, "/api/auth/me")).ID).toBe(actorId);
      await expect(table.locator("tbody tr")).toHaveCount(10);
      await expect(panel).toContainText(guid(1));
      await page
        .getByRole("button", { name: "关闭用户详情", exact: true })
        .click();
      const unavailable = await assetAction(
        page,
        () =>
          page
            .getByRole("button", { name: "查看用户 user-02", exact: true })
            .click(),
        true,
        404,
      );
      expect(unavailable.value).toEqual({ error: "not_found" });
      expect(unavailable.query.get("observationId")).toBe(newerId);
      expect(unavailable.query.get("objectGUID")).toBe(guid(2));
      await expect(page.getByRole("alert")).toHaveText(
        "当前无法读取此用户，请重新选择数据源。",
      );
      await expect(table).toHaveCount(0);
      await expect(panel).toHaveCount(0);
      await expect(
        page.getByRole("region", { name: "已选数据源用户资产", exact: true }),
      ).toHaveCount(0);
      await expect(
        page
          .getByRole("status")
          .filter({ hasText: "当前筛选下没有已授权数据源。" }),
      ).toBeVisible();
      await expect(
        page.getByRole("radio", {
          name: "选择数据源 synthetic.invalid",
          exact: true,
        }),
      ).toHaveCount(0);
      await expect(page.getByText("已选择域：", { exact: false })).toHaveCount(
        0,
      );
      await expect(page.getByText(newerId, { exact: false })).toHaveCount(0);
      await expect(page.getByText("已删除", { exact: false })).toHaveCount(0);
      await page.screenshot({
        path: testInfo.outputPath("user-assets-v2-source-detail404.png"),
        fullPage: true,
      });
    } finally {
      await administrator.close();
    }
  });
  expect(syncPosts).toBe(2);
  safe(consoles);
  expect(
    consoles.filter((message) => /Uncaught|unhandled/i.test(message)),
  ).toEqual([]);
  const storage = await page.evaluate(() => ({
    local: { ...localStorage },
    session: { ...sessionStorage },
    history: history.state,
    url: location.href,
  }));
  safe(storage);
  expect(JSON.stringify(storage)).not.toMatch(
    /objectGUID|samAccountName|Mixed\+|onerror|user-01/u,
  );
  await logout(page);
  expect(
    (
      await context.request.get(
        `/api/user-assets/v2?${new URLSearchParams(common)}`,
      )
    ).status(),
  ).toBe(401);
});
