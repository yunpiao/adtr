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
import { writeFile } from "node:fs/promises";
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

// Run only through group-assets-v3 / group-assets-v3-readers, or the explicit
// user-asset-export-v3 option on the owned disposable database, real
// worker and TLS LDAPFixture(directory_enabled=True, directory_v3=True). The fixture has 30 objects
// on two wire pages; it must never resolve or contact a real AD server.
// A missing fixture is a failed acceptance run, never a skipped/passing test.
function requireFixture(
  userAssetExport = false,
  userUACProfile?: "mixed-33-v1",
) {
  const uac = userUACProfile === "mixed-33-v1";
  if (userUACProfile !== undefined && !uac)
    throw new Error("uac_evidence_invalid:configuration");
  if (uac) {
    if (
      userAssetExport ||
      process.env.ADTR_E2E_USER_ASSET_EXPORT_V3_UAC !== "true" ||
      process.env.ADTR_E2E_USER_ASSET_EXPORT_V3 === "true" ||
      process.env.ADTR_E2E_LDAP_DIRECTORY_USER_UAC_V3_PROFILE !==
        "mixed-33-v1" ||
      !/^[0-9a-f]{32}$/u.test(
        process.env.ADTR_LDAP_FIXTURE_UAC_RUN_NONCE ?? "",
      ) ||
      !process.env.ADTR_E2E_LDAP_UAC_EVIDENCE_DIR
    )
      throw new Error("uac_evidence_invalid:configuration");
  } else if (
    process.env.ADTR_E2E_LDAP_DIRECTORY_USER_UAC_V3_PROFILE ||
    process.env.ADTR_E2E_USER_ASSET_EXPORT_V3_UAC === "true"
  )
    throw new Error("uac_evidence_invalid:configuration");
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
    process.env.ADTR_E2E_LDAP_DIRECTORY_V3 !== "true" ||
    (userAssetExport || uac
      ? (!uac && process.env.ADTR_E2E_USER_ASSET_EXPORT_V3 !== "true") ||
        [
          "GROUP_ASSETS_V3",
          "COMPUTER_ASSETS_V3",
          "V2",
          "V4",
          "EMPTY",
          "SLOW",
          "USER_ASSETS_V2",
          "GROUP_MEMBERS_V4_REFRESH",
          "GROUP_MEMBERS_V4_PAGINATION",
        ].some(
          (mode) =>
            ![undefined, "false"].includes(
              process.env[`ADTR_E2E_LDAP_DIRECTORY_${mode}`],
            ),
        ) ||
        Boolean(process.env.ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR)
      : process.env.ADTR_E2E_LDAP_DIRECTORY_GROUP_ASSETS_V3 !== "true")
  )
    throw new Error(
      userAssetExport
        ? "User export acceptance requires its dedicated isolated runner, default dictionary 3, real Worker, PostgreSQL and synthetic TLS LDAP; conflicting fixture modes are forbidden."
        : "Directory acceptance requires scripts/test_auth_e2e.py --suite group-assets-v3 or group-assets-v3-readers: an isolated fresh database, real worker, synthetic paged TLS LDAP fixture, ADTR_E2E_LDAP_DIRECTORY_MODE=true, ADTR_E2E_LDAP_DIRECTORY_V3=true and all ADTR_E2E auth/LDAP variables. No fixture means acceptance is blocked, not skipped.",
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

export async function login(page: Page, username: string, password: string) {
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

export async function proof(
  page: Page,
  password: string,
  auth: Authenticator,
  privateSecrets?: string[],
) {
  await fillSecret(
    page.getByLabel("操作者当前密码", { exact: true }),
    password,
  );
  const code = await freshCode(auth);
  // C supplies its private per-run scanner list. Register before filling so a
  // reflected proof is rejected in every later response, candidate and PNG.
  // Default callers omit this argument and keep their existing behavior.
  if (privateSecrets) privateSecrets.push(code);
  await fillSecret(
    page.getByLabel("未使用的认证器验证码", { exact: true }),
    code,
  );
}

export async function readJSON(
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

async function storedEvidence(taskUUID: string) {
  if (!/^[A-Za-z0-9_.-]{1,128}$/u.test(taskUUID))
    throw new Error("Invalid fixture task identity");
  const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container))
    throw new Error("Owned isolated database required");
  const sql = `SELECT jsonb_build_object('taskUUID',o.task_id,'actorId',o.actor_id::text,'selection',jsonb_build_object('domainId',o.domain_id,'revision',u.connection_revision::text,'credentialRevision',u.connection_credential_generation::text),'sourcePins',jsonb_build_object('accountId',u.account_id,'accountCredentialRevision',u.account_credential_revision::text,'policyRevision',u.policy_revision,'grantRoleId',u.grant_role_id,'grantRevision',u.grant_revision::text),'bodyBase64',replace(encode(o.body,'base64'),chr(10),''),'sha256',o.sha256,'dictionaryVersion',o.dictionary_version,'count',o.object_count,'taskState',t.state,'taskName',t.kind,'payloadVersion',t.payload_version,'maxAttempts',t.max_attempts,'attempt',t.attempt,'purpose',u.purpose,'useState',u.state,'reason',u.quiescence_reason,'dependencies',(SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.consumer_kind=u.purpose AND d.object_id=u.task_id)) FROM adtr.domain_directory_observations o JOIN adtr.tasks t ON t.task_id=o.task_id JOIN adtr.domain_directory_task_uses u ON u.task_id=o.task_id WHERE o.tenant_id='default' AND o.task_id='${taskUUID}'`;
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
    throw new Error("Read-only isolated dictionary3 evidence query failed");
  }
  return stdout.trim() ? JSON.parse(stdout) : null;
}
export async function setupGroupFixture(
  page: Page,
  context: BrowserContext,
  options: { emptyRefresh?: boolean; userAssetExport?: boolean } = {},
) {
  return setupGroupFixtureMode(page, context, options);
}

// Typed, explicit C-only entry point; the default public options stay intact.
export async function setupUACGroupFixture(
  page: Page,
  context: BrowserContext,
  profile: "mixed-33-v1",
) {
  if (profile !== "mixed-33-v1")
    throw new Error("uac_evidence_invalid:configuration");
  return setupGroupFixtureMode(page, context, {}, profile);
}

async function setupGroupFixtureMode(
  page: Page,
  context: BrowserContext,
  options: { emptyRefresh?: boolean; userAssetExport?: boolean },
  userUACProfile?: "mixed-33-v1",
) {
  if (options.emptyRefresh && !process.env.ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR)
    throw new Error(
      "Empty refresh setup requires its isolated transition fixture",
    );
  if (options.emptyRefresh && options.userAssetExport)
    throw new Error("Export fixture cannot use group empty-refresh mode");
  if (userUACProfile) {
    if (options.emptyRefresh || options.userAssetExport)
      throw new Error("uac_evidence_invalid:configuration");
    requireFixture(false, userUACProfile);
  } else requireFixture(options.userAssetExport);
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
  if (userUACProfile)
    await page.getByLabel("加密连接方式", { exact: true }).selectOption("389");
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

  // Production permits ten sensitive actions per actor per fifteen minutes.
  // Default readers preserve the original sequence: bootstrap7; producer
  // password/MFA3 + account/grant/binding3 + denied sync1 + v3 grant1 + sync1
  // =9, leaving the reader's existing source update as its tenth action.
  // Only primary emptyRefresh moves the SAME account create/connection grant
  // to the already authorized bootstrap BEFORE producer creation:
  // bootstrap password/MFA3 + domain/group/assignment3 + account/grant2 +
  // producer creation1 =9; producer password/MFA3 + binding1 + denied sync1 +
  // v3 grant1 + THREE real syncs3 =9. No new account, grant, clock or rate edit.
  // A forbidden sync consumes budget; login/logout never resets it.
  // The existing bootstrap may provision these same two scoped fixtures in
  // primary empty-refresh runs. Every proof is generated and consumed in the
  // same current actor session; readers retain the original producer ordering.
  const provisionAccount = async (
    provisionActor: number,
    actorPassword: string,
    actorAuth: Authenticator,
  ) => {
    if (options.emptyRefresh)
      expect((await readJSON(context, "/api/auth/me")).ID).toBe(provisionActor);
    await navigateTo(page, "管理操作账户");
    await page
      .getByRole("button", { name: "新增操作账户", exact: true })
      .click();
    await page
      .getByRole("button", { name: `选择 ${domain}`, exact: true })
      .click();
    await page
      .getByLabel("登记标签（可选）", { exact: true })
      .fill(accountLabel);
    await fillSecret(
      page.getByLabel("AD 用户名", { exact: true }),
      accountUser,
    );
    await fillSecret(
      page.getByLabel("AD 密码", { exact: true }),
      accountPassword,
    );
    await proof(page, actorPassword, actorAuth);
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
      actorPassword,
      actorAuth,
      "/api/credential-use",
    );
    expect(connectionGrant.value).toMatchObject({
      result: "SUCCESS",
      purpose: "domain.connection_test",
      allowed: true,
    });
    safe(connectionGrant.value);

    if (options.emptyRefresh) {
      expect(createdAccount.value).toMatchObject({
        domainId,
        accountId,
        verificationState: "unverified",
      });
      expect((await readJSON(context, "/api/auth/me")).ID).toBe(provisionActor);
      expect(
        (
          await readJSON(
            context,
            `/api/operation-accounts/detail?accountId=${accountId}`,
            provisionActor,
          )
        ).account,
      ).toMatchObject({
        accountId,
        domainId,
        domain,
        credentialConfigured: true,
        verificationState: "unverified",
      });
    }
    return accountId;
  };
  const earlyAccountId = options.emptyRefresh
    ? await provisionAccount(actorId, password, auth)
    : null;

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

  const accountId =
    earlyAccountId ?? (await provisionAccount(actorId, password, auth));
  if (options.emptyRefresh) {
    // Producer authority is checked by the real endpoint before its binding.
    expect((await readJSON(context, "/api/auth/me")).ID).toBe(actorId);
    expect(
      (
        await readJSON(
          context,
          `/api/operation-accounts/detail?accountId=${accountId}`,
          actorId,
        )
      ).account,
    ).toMatchObject({
      accountId,
      domainId,
      domain,
      credentialConfigured: true,
    });
  }

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

  if (userUACProfile)
    expect(beforeConnection).toMatchObject({ port: "389", mode: "starttls" });

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
    idempotencyKey: "v3-real-wire",
  };
  const effectivePath = `/api/directory-credential-use/v3/effective?accountId=${accountId}`;
  expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
    purpose: "domain.directory_read.v3",
    explicitlyGranted: false,
    eligible: false,
    consumerEnabled: true,
  });
  // A real connection-test grant already exists; it cannot authorize v3.
  const denied = await post("/api/directory/v3/sync", sync, 403);
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
    purpose: "domain.directory_read.v3",
    expectedAccountRevision: account.revision,
    expectedCredentialRevision: account.credentialRevision,
    expectedGrantRevision: "0",
    idempotencyKey: "v3-explicit-grant",
  };
  const granted = await post(
    "/api/directory-credential-use/v3/grant",
    mutation,
  );
  expect(granted).toMatchObject({
    purpose: "domain.directory_read.v3",
    allowed: true,
    replayed: false,
    consumerEnabled: true,
  });
  for (const prefix of [
    "/api/directory-credential-use",
    "/api/directory-credential-use/v2",
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
    `/api/directory/v3/observation?domainId=${domainId}`,
    actorId,
  );
  expect(initial).toMatchObject({
    dictionaryVersion: 3,
    available: false,
    list: [],
  });
  const submitted = await post("/api/directory/v3/sync", sync);
  const taskUUID = submitted.task.taskUUID;
  expect(submitted).toMatchObject({
    replayed: false,
    task: {
      taskName: "domain.directory_read.v3",
      payloadVersion: 1,
      maxAttempts: 1,
      result: {},
      cursor: {},
    },
  });
  const taskPath = `/api/directory/v3/task?taskUUID=${taskUUID}`;
  await expect
    .poll(async () => (await readJSON(context, taskPath, actorId)).task.state, {
      timeout: 30000,
    })
    .toBe("succeeded");
  const receipt = await readJSON(
    context,
    `/api/directory/v3/receipt?domainId=${domainId}&idempotencyKey=${sync.idempotencyKey}`,
    actorId,
  );
  expect(receipt.task.taskUUID).toBe(taskUUID);
  return {
    actorId,
    domainId,
    domain,
    taskUUID,
    beforeConnection,
    post,
    sync,
    safe,
    secrets,
    consoles,
    accountId,
    mutation,
    bootstrap,
    producer: { username, password, auth },
    read: (path: string) => readJSON(context, path, actorId),
    evidence: (observationTask = taskUUID) => storedEvidence(observationTask),
  };
}
