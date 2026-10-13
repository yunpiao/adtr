import { navigateTo } from "./navigation";
import {
  test,
  expect,
  type BrowserContext,
  type Locator,
  type Page,
} from "@playwright/test";
import { createHmac, createHash } from "node:crypto";
import { isIP } from "node:net";
import { readFile, writeFile } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { directoryV2ResponseJSON } from "./directory-v2-response-observer";
const run = promisify(execFile);
type Authenticator = { secret: string; lastCounter: number };
type DirectoryObject = {
  objectGUID: string;
  distinguishedName: string;
  kind: "user" | "group" | "computer";
  objectClass: string[];
  samAccountName: string | null;
  userAccountControl: number | null;
  objectSid: string | null;
  mail: string | null;
  description: string[] | null;
  whenCreated: string | null;
};

// Run only through the directory-v2 suite on its owned disposable database, real
// worker and TLS LDAPFixture(directory_enabled=True, directory_v4=True). The fixture has 30 objects
// on two wire pages; it must never resolve or contact a real AD server.
// A missing fixture is a failed acceptance run, never a skipped/passing test.
function requireFixture() {
  const names = [
    "ADTR_E2E_BASE_URL",
    "ADTR_E2E_USERNAME",
    "ADTR_E2E_PASSWORD",
    "ADTR_E2E_LDAP_IP",
    "ADTR_E2E_LDAP_USERNAME",
    "ADTR_E2E_LDAP_PASSWORD",
  ] as const;
  if (
    names.some((name) => !process.env[name]) ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_MODE !== "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_V4 !== "true"
  )
    throw new Error(
      "Directory acceptance requires scripts/test_auth_e2e.py --suite directory-v4-backend: an isolated fresh database, real worker, synthetic paged TLS LDAP fixture, ADTR_E2E_LDAP_DIRECTORY_MODE=true, ADTR_E2E_LDAP_DIRECTORY_V4=true and all ADTR_E2E auth/LDAP variables. No fixture means acceptance is blocked, not skipped.",
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
  // Setup also creates another disposable actor; never return its initial
  // password or the acting user's proof to assertion reporters.
  return { value: await directoryV2ResponseJSON(response) };
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

function expectedObjects(): DirectoryObject[] {
  const objects: DirectoryObject[] = [];
  for (let n = 1; n <= 30; n++) {
    const kind = n <= 12 ? "user" : n <= 24 ? "group" : "computer";
    const index = n <= 12 ? n : n <= 24 ? n - 12 : n - 24;
    const name = `${kind}-${String(index).padStart(2, "0")}`;
    const unit =
      kind === "user" ? "Users" : kind === "group" ? "Groups" : "Computers";
    const hex = n.toString(16).padStart(2, "0");
    objects.push({
      objectGUID: `${hex.repeat(4)}-${hex.repeat(2)}-${hex.repeat(2)}-${hex.repeat(2)}-${hex.repeat(6)}`,
      distinguishedName: `CN=${name},OU=${unit},DC=synthetic,DC=invalid`,
      kind,
      objectClass:
        kind === "group"
          ? ["group", "top"]
          : kind === "computer"
            ? ["computer", "organizationalperson", "person", "top", "user"]
            : ["organizationalperson", "person", "top", "user"],
      samAccountName: n === 24 ? null : kind === "computer" ? `${name}$` : name,
      userAccountControl:
        kind === "group"
          ? null
          : kind === "computer"
            ? 4096
            : n === 1
              ? 0
              : 512,
      objectSid: n === 24 ? null : `S-1-5-21-1-2-3-${1000 + n}`,
      mail:
        n === 24
          ? null
          : n === 1
            ? " Mixed@example.test "
            : `row-${String(n).padStart(2, "0")}@example.test`,
      description:
        n === 24
          ? null
          : [
              n === 1
                ? "Line 1\u0000\n\t😀\u202e"
                : `Synthetic row ${String(n).padStart(2, "0")}`,
            ],
      whenCreated:
        n === 24
          ? null
          : n === 1
            ? "0001-01-01T00:00:00Z"
            : "2026-10-01T02:03:04Z",
    });
  }
  return objects;
}

async function provenance() {
  const git = async (...args: string[]) =>
    (await run("git", args)).stdout.trim();
  const root = await git("rev-parse", "--show-toplevel");
  const checkoutCommit = await git("rev-parse", "HEAD");
  const sourceDigests: Record<string, string> = {};
  for (const path of [
    ".github/workflows/ci.yml",
    "internal/auth/group_members_v4_http.go",
    "internal/directoryassets/objects_v4.go",
    "internal/domainconfig/config.go",
    "internal/domains/directory_observation_v4_schema.go",
    "internal/domains/directory_v4_observation_codec.go",
    "internal/domains/group_members_v4.go",
    "internal/ldapconnection/directory_protocol_v4.go",
    "internal/ldapconnection/directory_v4.go",
    "internal/store/directory_v4_migration.go",
    "internal/testldapfixture/directory_v4.go",
    "scripts/ldap_e2e_fixture.py",
    "scripts/test_auth_e2e.py",
    "scripts/validate_directory_v4_evidence.py",
    "web/e2e/directory-v4-backend.spec.ts",
  ])
    sourceDigests[path] = createHash("sha256")
      .update(await readFile(`${root}/${path}`))
      .digest("hex");
  return {
    checkoutCommit,
    checkoutTree: await git("rev-parse", "HEAD^{tree}"),
    eventHeadCommit: process.env.ADTR_EVENT_HEAD_SHA ?? checkoutCommit,
    eventBaseCommit:
      process.env.ADTR_EVENT_BASE_SHA ?? (await git("rev-parse", "HEAD^")),
    workflowCommit: process.env.GITHUB_SHA ?? checkoutCommit,
    runId: process.env.GITHUB_RUN_ID ?? "local",
    runAttempt: process.env.GITHUB_RUN_ATTEMPT ?? "1",
    eventName: process.env.GITHUB_EVENT_NAME ?? "local",
    sourceDigests,
  };
}

async function storedEvidence(taskUUID: string) {
  if (!/^[A-Za-z0-9_.-]{1,128}$/u.test(taskUUID))
    throw new Error("Invalid fixture task identity");
  const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container))
    throw new Error("Owned isolated database required");
  const sql = `SELECT jsonb_build_object('bodyBase64',replace(encode(o.body,'base64'),chr(10),''),'sha256',o.sha256,'dictionaryVersion',o.dictionary_version,'count',o.object_count,'taskState',t.state,'purpose',u.purpose,'useState',u.state,'reason',u.quiescence_reason,'dependencies',(SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.consumer_kind=u.purpose AND d.object_id=u.task_id)) FROM adtr.domain_directory_observations o JOIN adtr.tasks t ON t.task_id=o.task_id JOIN adtr.domain_directory_task_uses u ON u.task_id=o.task_id WHERE o.tenant_id='default' AND o.task_id='${taskUUID}'`;
  let stdout: string;
  try {
    ({ stdout } = await run(
      "docker",
      [
        "exec",
        "-e",
        "PGOPTIONS=-c statement_timeout=2000",
        container,
        "psql",
        "-U",
        "postgres",
        "-d",
        "adtr_e2e",
        "-X",
        "-A",
        "-t",
        "-v",
        "ON_ERROR_STOP=1",
        "-c",
        sql,
      ],
      { encoding: "utf8", timeout: 5000, maxBuffer: 1 << 20 },
    ));
  } catch {
    throw new Error("Read-only isolated dictionary4 evidence query failed");
  }
  return stdout.trim() ? JSON.parse(stdout) : null;
}
// Existing UI is used only to provision isolated accounts/domain. New v4 facts
// and mutations are exercised through real HTTP; no v4 UI delivery is claimed.
test.use({ trace: "off", screenshot: "off", video: "off" });
test.beforeAll(() => requireFixture());
test("dictionary4 real backend Worker TLS LDAP PostgreSQL API facts", async ({
  page,
  context,
}, testInfo) => {
  test.setTimeout(630_000);
  let username = process.env.ADTR_E2E_USERNAME!;
  const initialPassword = process.env.ADTR_E2E_PASSWORD!;
  let password = "Changed Directory Password 123";
  const producerUsername = "directory-producer";
  const producerInitial = "Initial Directory Producer Password 123";
  const producerPassword = "Changed Directory Producer Password 123";
  const accountUser = process.env.ADTR_E2E_LDAP_USERNAME!;
  const accountPassword = process.env.ADTR_E2E_LDAP_PASSWORD!;
  const ldapIP = process.env.ADTR_E2E_LDAP_IP!;
  const domain = "synthetic.invalid";
  const accountLabel = "Synthetic directory reader";
  const groupName = "E2EDirectoryScope";
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
  const bootstrap = { username, password, auth };
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
  // producer creation (1) = 7. The assignment revokes
  // the bootstrap session; create the producer only after that UI re-login.
  // Producer: password/MFA (3), account/connection grant/binding (3), denied
  // v4 sync (1), v4 grant (1), committed v4 sync (1), v4 revoke (1) = 10.
  // The forbidden sync still consumes rate budget. Login/logout never resets it.
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
  username = producerUsername;
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

  // Auth/me identifies the actor in its JSON body; unlike domain APIs it
  // does not emit X-ADTR-User-ID. Keep an explicit identity assertion.
  const me = await readJSON(context, "/api/auth/me");
  expect(me.ID).toBe(actorId);
  expect(typeof me.csrfToken).toBe("string");
  expect(me.csrfToken.length).toBeGreaterThan(0);
  secrets.push(me.csrfToken);
  const headers = {
    Origin: new URL(page.url()).origin,
    "X-CSRF-Token": me.csrfToken,
  };
  const post = async (
    path: string,
    data: Record<string, unknown>,
    status = 200,
  ) => {
    const code = await freshCode(auth);
    let response;
    try {
      response = await context.request.post(path, {
        headers,
        data: { ...data, actorPassword: password, totpCode: code },
        maxRedirects: 0,
        timeout: 15000,
      });
    } catch {
      throw new Error("Synthetic proof-bearing request failed");
    }
    expect(response.status(), `POST ${path}`).toBe(status);
    const value = await response.json();
    safe(value);
    return value;
  };
  const sync = {
    domainId,
    expectedRevision: beforeConnection.revision,
    expectedCredentialGeneration: beforeConnection.credentialRevision,
    idempotencyKey: "v4-real-wire",
  };
  const effectivePath = `/api/directory-credential-use/v4/effective?accountId=${accountId}`;
  expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
    purpose: "domain.directory_read.v4",
    explicitlyGranted: false,
    eligible: false,
    consumerEnabled: true,
  });
  // A real connection-test grant already exists; it cannot authorize v4.
  const denied = await post("/api/directory/v4/sync", sync, 403);
  expect(denied).toEqual({ error: "forbidden" });
  const account = (
    await readJSON(
      context,
      `/api/operation-accounts/detail?accountId=${accountId}`,
      actorId,
    )
  ).account;
  const mutation = {
    accountId,
    roleId: "platform_admin",
    purpose: "domain.directory_read.v4",
    expectedAccountRevision: account.revision,
    expectedCredentialRevision: account.credentialRevision,
    expectedGrantRevision: "0",
    idempotencyKey: "v4-explicit-grant",
  };
  const granted = await post(
    "/api/directory-credential-use/v4/grant",
    mutation,
  );
  expect(granted).toMatchObject({
    purpose: "domain.directory_read.v4",
    allowed: true,
    replayed: false,
    consumerEnabled: true,
  });
  for (const prefix of [
    "/api/directory-credential-use",
    "/api/directory-credential-use/v2",
    "/api/directory-credential-use/v3",
  ]) {
    expect(
      await readJSON(
        context,
        `${prefix}/effective?accountId=${accountId}`,
        actorId,
      ),
    ).toMatchObject({ explicitlyGranted: false, eligible: false });
  }
  const initial = await readJSON(
    context,
    `/api/directory/v4/observation?domainId=${domainId}`,
    actorId,
  );
  expect(initial).toMatchObject({
    dictionaryVersion: 4,
    available: false,
    list: [],
  });
  const submitted = await post("/api/directory/v4/sync", sync);
  const taskUUID = submitted.task.taskUUID;
  expect(submitted).toMatchObject({
    replayed: false,
    task: {
      taskName: "domain.directory_read.v4",
      payloadVersion: 1,
      maxAttempts: 1,
      result: {},
      cursor: {},
    },
  });
  const taskPath = `/api/directory/v4/task?taskUUID=${taskUUID}`;
  await expect
    .poll(async () => (await readJSON(context, taskPath, actorId)).task.state, {
      timeout: 30000,
    })
    .toBe("succeeded");
  const receipt = await readJSON(
    context,
    `/api/directory/v4/receipt?domainId=${domainId}&idempotencyKey=${sync.idempotencyKey}`,
    actorId,
  );
  expect(receipt.task.taskUUID).toBe(taskUUID);
  const observed = await readJSON(
    context,
    `/api/directory/v4/observation?domainId=${domainId}&observationId=${taskUUID}&pageSize=25`,
    actorId,
  );
  const second = await readJSON(
    context,
    `/api/directory/v4/observation?domainId=${domainId}&observationId=${taskUUID}&pageSize=25&pageIdx=2`,
    actorId,
  );
  expect(observed).toMatchObject({
    dictionaryVersion: 4,
    available: true,
    observationId: taskUUID,
    page: { total: 30, totalPage: 2 },
  });
  expect(observed.list).toHaveLength(25);
  expect(second.list).toHaveLength(5);
  const expected = expectedObjects().map((object, index) => {
    const n = index + 1;
    const present = (n <= 12 && n !== 12) || n > 24;
    return {
      ...object,
      rawMember:
        n <= 13 || n > 24
          ? null
          : n === 14
            ? []
            : n === 15
              ? [
                  "CN=user-01,OU=Users,DC=synthetic,DC=invalid",
                  "CN=group-02,OU=Groups,DC=synthetic,DC=invalid",
                  "CN=S-1-5-21-foreign,CN=ForeignSecurityPrincipals,DC=elsewhere,DC=invalid",
                  "CN=raw\u0000\n😀<script>,DC=synthetic,DC=invalid",
                ]
              : [
                  `CN=member-${String(n).padStart(2, "0")},OU=Users,DC=synthetic,DC=invalid`,
                ],
      returnedMemberCount:
        n <= 13 || n > 24 ? null : n === 14 ? 0 : n === 15 ? 4 : 1,
      memberReadState: n <= 13 || n > 24 ? "not_returned" : "returned",
      primaryGroupID: present
        ? n === 2
          ? 0
          : n === 3
            ? 4294967295
            : 513
        : null,
      servicePrincipalName: present
        ? n === 1
          ? ["HOST/Z", "host/a\u0000\n😀<script>", "HOST/a"]
          : [
              `HOST/row-${String(n).padStart(2, "0")}.synthetic.invalid`,
              `HTTP/row-${String(n).padStart(2, "0")}.synthetic.invalid`,
            ]
        : null,
    };
  });
  expect([...observed.list, ...second.list]).toEqual(expected);
  safe(observed);
  safe(second);
  const params = new URLSearchParams({
    domainId,
    expectedRevision: beforeConnection.revision,
    expectedCredentialRevision: beforeConnection.credentialRevision,
    observationId: taskUUID,
    objectGUID: expected[14].objectGUID,
  });
  const detail = await readJSON(
    context,
    `/api/group-assets/v4/members?${params}`,
    actorId,
  );
  expect(detail.dictionaryVersion).toBe(4);
  expect(detail.object).toEqual(expected[14]);
  for (const index of [12, 13, 14, 23]) {
    params.set("objectGUID", expected[index].objectGUID);
    const member = await readJSON(
      context,
      `/api/group-assets/v4/members?${params}`,
      actorId,
    );
    expect(member.object).toEqual(expected[index]);
    expect(member.selection).toMatchObject({
      domainId,
      revision: beforeConnection.revision,
      credentialRevision: beforeConnection.credentialRevision,
    });
    expect(member.source).toEqual(observed.source);
  }
  params.set("objectGUID", expected[14].objectGUID);
  for (const key of [
    "observationId",
    "objectGUID",
    "expectedRevision",
    "expectedCredentialRevision",
  ]) {
    const missing = new URLSearchParams(params);
    missing.delete(key);
    expect(
      (
        await context.request.get(`/api/group-assets/v4/members?${missing}`)
      ).status(),
    ).toBe(400);
  }
  const stale = new URLSearchParams(params);
  stale.set("expectedRevision", "999999");
  expect(
    (
      await context.request.get(`/api/group-assets/v4/members?${stale}`)
    ).status(),
  ).toBe(409);
  const unknown = new URLSearchParams(params);
  unknown.set("observationId", "never-observed");
  expect(
    (
      await context.request.get(`/api/group-assets/v4/members?${unknown}`)
    ).status(),
  ).toBe(409);
  const wrongOld = await context.request.get(
    `/api/user-assets/v2/detail?${params}`,
  );
  expect(wrongOld.status()).toBe(409);
  for (const prefix of [
    "/api/directory",
    "/api/directory/v2",
    "/api/directory/v3",
  ]) {
    const wrong = await context.request.get(
      `${prefix}/observation?domainId=${domainId}&observationId=${taskUUID}`,
    );
    expect(wrong.status()).toBe(409);
  }
  params.set("objectGUID", expected[0].objectGUID);
  expect(
    (
      await context.request.get(`/api/group-assets/v4/members?${params}`)
    ).status(),
  ).toBe(404);
  await expect
    .poll(async () => (await storedEvidence(taskUUID))?.useState, {
      timeout: 10000,
    })
    .toBe("quiesced");
  const evidence = await storedEvidence(taskUUID);
  expect(evidence).toMatchObject({
    dictionaryVersion: 4,
    count: 30,
    taskState: "succeeded",
    purpose: "domain.directory_read.v4",
    useState: "quiesced",
    reason: "executor_returned",
    dependencies: 0,
  });
  const bytes = Buffer.from(evidence.bodyBase64, "base64");
  expect(createHash("sha256").update(bytes).digest("hex")).toBe(
    evidence.sha256,
  );
  const stored = JSON.parse(bytes.toString("utf8"));
  expect(stored.dictionaryVersion).toBe(4);
  expect(stored.objects).toHaveLength(30);
  for (let i = 0; i < 30; i++) {
    const identity = stored.objects[i].identity;
    const want = expected[i];
    expect(
      identity.primaryGroupIDBytes === null
        ? null
        : Buffer.from(identity.primaryGroupIDBytes, "base64").toString("utf8"),
    ).toBe(want.primaryGroupID === null ? null : String(want.primaryGroupID));
    expect(
      identity.servicePrincipalNameBytes === null
        ? null
        : identity.servicePrincipalNameBytes.map((v: string) =>
            Buffer.from(v, "base64").toString("utf8"),
          ),
    ).toEqual(want.servicePrincipalName);
    const membership = stored.objects[i].membership;
    expect(Object.keys(membership)).toEqual(["memberBytes"]);
    expect(
      membership.memberBytes === null
        ? null
        : membership.memberBytes.map((v: string) =>
            Buffer.from(v, "base64").toString("utf8"),
          ),
    ).toEqual(want.rawMember);
    expect(want.returnedMemberCount).toBe(
      want.rawMember === null ? null : want.rawMember.length,
    );
  }
  // Revoke the actual v4 grant. Read authority is independent of use authority.
  const revoked = await post("/api/directory-credential-use/v4/revoke", {
    ...mutation,
    expectedGrantRevision: granted.grantRevision,
    idempotencyKey: "v4-explicit-revoke",
  });
  expect(revoked.allowed).toBe(false);
  expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
    explicitlyGranted: false,
    eligible: false,
  });
  params.set("objectGUID", expected[14].objectGUID);
  expect(
    (await readJSON(context, `/api/group-assets/v4/members?${params}`, actorId))
      .object,
  ).toEqual(expected[14]);
  safe(consoles);
  await writeFile(
    testInfo.outputPath("directory-v4-backend-evidence.json"),
    Buffer.from(
      JSON.stringify(
        {
          format: 1,
          provenance: await provenance(),
          taskUUID,
          sha256: evidence.sha256,
          bodyBase64: evidence.bodyBase64,
          source: observed.source,
          purpose: evidence.purpose,
          reason: evidence.reason,
          dependencies: evidence.dependencies,
          objects: [...observed.list, ...second.list],
          storedMembership: stored.objects.map(
            (o: { membership: unknown }) => o.membership,
          ),
          storedIdentity: stored.objects.map(
            (o: { identity: unknown }) => o.identity,
          ),
        },
        null,
        2,
      ),
    ),
  );
});
