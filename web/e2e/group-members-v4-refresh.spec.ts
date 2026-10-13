import { publishEmptyFixtureMarker } from "./asset-empty-transition-marker";
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
import { observeGroupMembersV4Responses } from "./group-members-v4-response-observer";
import { openMembers } from "./group-members-v4-helpers";
import {
  refreshMembershipProvenance,
  captureRefreshMembers,
} from "./group-members-v4-refresh-helpers";
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
type Permission = {
  mark: string;
  auth: { readable: boolean; writeable: boolean };
};
type Authenticator = { secret: string; lastCounter: number };
// Independent refresh suite: two real Worker collections from the same TLS fixture.
// Explicit refresh mode publishes a private marker between authenticated collections.
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
    process.env.ADTR_E2E_LDAP_DIRECTORY_V4 !== "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_GROUP_MEMBERS_V4_REFRESH !== "true" ||
    !process.env.ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_EMPTY === "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_SLOW === "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_GROUP_MEMBERS_V4_PAGINATION === "true"
  )
    throw new Error(
      "Group membership acceptance requires scripts/test_auth_e2e.py --suite group-members-v4-refresh: an isolated fresh database, real worker, synthetic paged TLS LDAP fixture, ADTR_E2E_LDAP_DIRECTORY_MODE=true, ADTR_E2E_LDAP_DIRECTORY_V4=true, ADTR_E2E_LDAP_DIRECTORY_GROUP_MEMBERS_V4_REFRESH=true, ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR and all ADTR_E2E auth/LDAP variables. No fixture means acceptance is blocked, not skipped.",
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

async function storedEvidence(taskUUID: string) {
  if (!/^[A-Za-z0-9_.-]{1,128}$/u.test(taskUUID))
    throw new Error("Invalid fixture task identity");
  const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container))
    throw new Error("Owned isolated database required");
  const sql = `BEGIN READ ONLY; SELECT jsonb_build_object('bodyBase64',replace(encode(o.body,'base64'),chr(10),''),'sha256',o.sha256,'dictionaryVersion',o.dictionary_version,'count',o.object_count,'taskState',t.state,'purpose',u.purpose,'useState',u.state,'reason',u.quiescence_reason,'dependencies',(SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.consumer_kind=u.purpose AND d.object_id=u.task_id)) FROM adtr.domain_directory_observations o JOIN adtr.tasks t ON t.task_id=o.task_id JOIN adtr.domain_directory_task_uses u ON u.task_id=o.task_id WHERE o.tenant_id='default' AND o.task_id='${taskUUID}'; COMMIT;`;
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
        "-q",
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
async function readerSnapshot(
  actorId: number,
  roleId: string,
  taskUUID: string,
  oldTaskUUID: string,
) {
  if (
    !/^[A-Za-z0-9_.-]{1,128}$/u.test(oldTaskUUID) ||
    !Number.isSafeInteger(actorId) ||
    actorId <= 0 ||
    !/^[A-Za-z0-9_-]{24}$/u.test(roleId) ||
    !/^[A-Za-z0-9_.-]{1,128}$/u.test(taskUUID)
  )
    throw new Error("Invalid synthetic reader identity");
  const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container))
    throw new Error("Owned isolated database required");
  const sql = `BEGIN READ ONLY; SELECT jsonb_build_object(
    'actorId',u.id,'roleId',u.role_id,'baseRole',u.role,'mfaLastStep',u.mfa_last_step,
    'readable',COALESCE((SELECT jsonb_agg(mark ORDER BY mark) FROM adtr.access_permissions WHERE tenant_id=u.tenant_id AND role_id=u.role_id AND readable),'[]'::jsonb),
    'writeable',COALESCE((SELECT jsonb_agg(mark ORDER BY mark) FROM adtr.access_permissions WHERE tenant_id=u.tenant_id AND role_id=u.role_id AND writeable),'[]'::jsonb),
    'domainIds',COALESCE((SELECT jsonb_agg(domain_id ORDER BY domain_id) FROM (SELECT DISTINCT m.domain_id FROM adtr.resource_role_groups g JOIN adtr.resource_group_members m USING(tenant_id,group_id) WHERE g.tenant_id=u.tenant_id AND g.role_id=u.role_id) d),'[]'::jsonb),
    'useGrants',(SELECT count(*) FROM adtr.operation_account_use_grants WHERE tenant_id=u.tenant_id AND role_id=u.role_id),
    'taskCount',(SELECT count(*) FROM adtr.tasks),
    'useCount',(SELECT count(*) FROM adtr.domain_directory_task_uses),
    'observationCount',(SELECT count(*) FROM adtr.domain_directory_observations),
    'oldObservationSha256',(SELECT encode(sha256(body),'hex') FROM adtr.domain_directory_observations WHERE tenant_id=u.tenant_id AND task_id='${oldTaskUUID}'),
    'observationSha256',(SELECT encode(sha256(body),'hex') FROM adtr.domain_directory_observations WHERE tenant_id=u.tenant_id AND task_id='${taskUUID}')
  ) FROM adtr.users u WHERE u.tenant_id='default' AND u.id=${actorId} AND u.role_id='${roleId}'; COMMIT;`;
  try {
    const { stdout } = await run(
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
        "-q",
        "-v",
        "ON_ERROR_STOP=1",
        "-c",
        sql,
      ],
      { encoding: "utf8", timeout: 5000, maxBuffer: 1 << 20 },
    );
    return JSON.parse(stdout.trim());
  } catch {
    throw new Error("Read-only isolated reader authority query failed");
  }
}
async function producerAllowedGrants(taskUUID: string) {
  if (!/^[A-Za-z0-9_.-]{1,128}$/u.test(taskUUID))
    throw new Error("Invalid fixture task identity");
  const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container))
    throw new Error("Owned isolated database required");
  const sql = `BEGIN READ ONLY; SELECT count(*) FROM adtr.operation_account_use_grants g JOIN adtr.domain_directory_task_uses u ON u.tenant_id=g.tenant_id AND u.account_id=g.account_id AND u.purpose=g.purpose WHERE u.tenant_id='default' AND u.task_id='${taskUUID}' AND g.role_id='platform_admin' AND g.allowed; COMMIT;`;
  try {
    const { stdout } = await run(
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
        "-q",
        "-v",
        "ON_ERROR_STOP=1",
        "-c",
        sql,
      ],
      { encoding: "utf8", timeout: 5000, maxBuffer: 1 << 20 },
    );
    if (!/^\d+$/u.test(stdout.trim())) throw new Error("Invalid grant count");
    return Number(stdout.trim());
  } catch {
    throw new Error("Read-only isolated producer grant query failed");
  }
}
// Existing setup provisions real isolated accounts/domain and registered Worker.
// New checks consume saved v4 facts through the production membership route.
test.use({ trace: "off", screenshot: "off", video: "off" });
test.beforeAll(() => requireFixture());
test("group members dictionary4 refresh real Worker TLS LDAP PostgreSQL read-only UI", async ({
  page: producerPage,
  context: producerContext,
  browser,
}, testInfo) => {
  test.setTimeout(630_000);
  let username = process.env.ADTR_E2E_USERNAME!;
  const initialPassword = process.env.ADTR_E2E_PASSWORD!;
  let password = "Changed Directory Password 123";
  const readerUsername = "membership-reader";
  const readerInitial = "Initial Membership Reader Password 123";
  const readerPassword = "Changed Membership Reader Password 123";
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
    readerInitial,
    readerPassword,
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
  producerPage.on("console", (message) => consoles.push(message.text()));
  producerPage.on("pageerror", (error) => consoles.push(error.message));

  await observeGroupMembersV4Responses(producerContext);
  await producerPage.goto("/");
  await login(producerPage, username, initialPassword);
  await expect(
    producerPage.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await fillSecret(
    producerPage.getByLabel("当前密码", { exact: true }),
    initialPassword,
  );
  await fillSecret(
    producerPage.getByLabel("新密码", { exact: true }),
    password,
  );
  await fillSecret(
    producerPage.getByLabel("确认新密码", { exact: true }),
    password,
  );
  await producerPage
    .getByRole("button", { name: "更新密码", exact: true })
    .click();
  await expect(
    producerPage.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  let auth = await enrollMfa(producerPage, password);
  secrets.push(auth.secret);
  const tenant = await readJSON(producerContext, "/api/resources/tenant");
  expect(tenant.maxAdCount).toBe(2);
  expect(tenant.expireTime).toBeGreaterThan(Math.floor(Date.now() / 1000));

  await navigateTo(producerPage, "域连接");
  await producerPage
    .getByRole("button", { name: "新增域连接", exact: true })
    .click();
  await producerPage.getByLabel("域 DNS 名称", { exact: true }).fill(domain);
  await producerPage
    .getByLabel("域控 DNS 名称", { exact: true })
    .fill("dc.synthetic.invalid");
  await producerPage
    .getByLabel("连接 IP（可选）", { exact: true })
    .fill(ldapIP);
  await producerPage
    .getByLabel("初始凭据来源", { exact: true })
    .selectOption("unconfigured");
  await proof(producerPage, password, auth);
  const createdDomain = await submit(
    producerPage,
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
  const deniedScope = await producerContext.request.get(
    `/api/directory/v2/observation?domainId=${domainId}`,
  );
  expect(deniedScope.status()).toBe(404);
  expect(await deniedScope.json()).toEqual({ error: "not_found" });

  await navigateTo(producerPage, "资源与租户");
  await producerPage
    .getByRole("button", { name: "资源组", exact: true })
    .click();
  await producerPage
    .getByRole("button", { name: "新增资源组", exact: true })
    .click();
  await producerPage.getByLabel("资源组名称", { exact: true }).fill(groupName);
  await producerPage.getByLabel("域 ID", { exact: true }).fill(domainId);
  await proof(producerPage, password, auth);
  expect(
    (await submit(producerPage, "/api/resources/groups/create", "保存资源组"))
      .value,
  ).toMatchObject({ result: "SUCCESS" });
  await producerPage
    .getByRole("button", { name: `查看 ${groupName}`, exact: true })
    .click();
  await producerPage
    .getByRole("button", { name: "调整关联角色", exact: true })
    .click();
  await producerPage
    .getByLabel("显式关联 platform_admin", { exact: true })
    .check();
  await proof(producerPage, password, auth);
  expect(
    (await submit(producerPage, "/api/resources/groups/assign", "保存角色关联"))
      .value,
  ).toMatchObject({ result: "SUCCESS", sessionRevoked: true });
  await login(producerPage, username, password);
  await fillSecret(
    producerPage.getByLabel("二次认证验证码", { exact: true }),
    await freshCode(auth),
  );
  await producerPage.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    producerPage.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  let actorId = (await readJSON(producerContext, "/api/auth/me")).ID as number;

  // Ten real sensitive actions per bootstrap: password/MFA (3), domain/group/
  // assignment (3), reader role/assignment/user (3), producer creation (1).
  // Producer remains at ten; no bucket resets or authentication shortcuts.
  await navigateTo(producerPage, "访问管理");
  await producerPage
    .getByRole("button", { name: "角色管理", exact: true })
    .click();
  await producerPage
    .getByRole("button", { name: "新增角色", exact: true })
    .click();
  await producerPage
    .getByLabel("角色名称", { exact: true })
    .fill("E2EMembershipReader");
  for (const name of ["域连接：读取", "目录资产：读取"])
    await producerPage.getByRole("checkbox", { name, exact: true }).check();
  await proof(producerPage, password, auth);
  const readerRole = await submit(
    producerPage,
    "/api/access/roles/save",
    "保存角色",
  );
  expect(readerRole.value).toMatchObject({
    result: "SUCCESS",
    sessionRevoked: false,
  });
  const roleId = readerRole.value.roleID as string;
  expect(roleId).toMatch(/^[A-Za-z0-9_-]{24}$/u);
  const permissions = (
    await readJSON(producerContext, `/api/access/permissions?roleID=${roleId}`)
  ).permissions.map(({ mark, auth }: Permission) => ({ mark, auth }));
  expect(
    permissions
      .filter((p: Permission) => p.auth.readable)
      .map((p: Permission) => p.mark)
      .sort(),
  ).toEqual(["directory_assets", "domains"]);
  expect(permissions.every((p: Permission) => !p.auth.writeable)).toBe(true);
  await navigateTo(producerPage, "资源与租户");
  await producerPage
    .getByRole("button", { name: "资源组", exact: true })
    .click();
  await producerPage
    .getByRole("button", { name: `查看 ${groupName}`, exact: true })
    .click();
  await producerPage
    .getByRole("button", { name: "调整关联角色", exact: true })
    .click();
  await expect(
    producerPage.getByLabel("显式关联 platform_admin", { exact: true }),
  ).toBeChecked();
  await producerPage.getByLabel("自定义角色 ID", { exact: true }).fill(roleId);
  await proof(producerPage, password, auth);
  expect(
    (await submit(producerPage, "/api/resources/groups/assign", "保存角色关联"))
      .value,
  ).toMatchObject({ result: "SUCCESS", sessionRevoked: true });
  await login(producerPage, username, password);
  await fillSecret(
    producerPage.getByLabel("二次认证验证码", { exact: true }),
    await freshCode(auth),
  );
  await producerPage.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    producerPage.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  await navigateTo(producerPage, "访问管理");
  await producerPage
    .getByRole("button", { name: "用户管理", exact: true })
    .click();
  await producerPage
    .getByRole("button", { name: "新增用户", exact: true })
    .click();
  await producerPage.getByLabel("用户名", { exact: true }).fill(readerUsername);
  await fillSecret(
    producerPage.getByLabel("初始密码", { exact: true }),
    readerInitial,
  );
  await fillSecret(
    producerPage.getByLabel("确认初始密码", { exact: true }),
    readerInitial,
  );
  await producerPage.getByLabel("角色", { exact: true }).fill(roleId);
  await proof(producerPage, password, auth);
  const readerUser = await submit(
    producerPage,
    "/api/access/users/create",
    "创建用户",
  );
  expect(readerUser.value).toMatchObject({
    result: "SUCCESS",
    sessionRevoked: false,
  });
  const readerId = readerUser.value.ID as number;
  expect(readerId).toBeGreaterThan(0);
  expect(readerId).not.toBe(actorId);

  await navigateTo(producerPage, "访问管理");
  await producerPage
    .getByRole("button", { name: "用户管理", exact: true })
    .click();
  await producerPage
    .getByRole("button", { name: "新增用户", exact: true })
    .click();
  await producerPage
    .getByLabel("用户名", { exact: true })
    .fill(producerUsername);
  await fillSecret(
    producerPage.getByLabel("初始密码", { exact: true }),
    producerInitial,
  );
  await fillSecret(
    producerPage.getByLabel("确认初始密码", { exact: true }),
    producerInitial,
  );
  await producerPage.getByLabel("角色", { exact: true }).fill("platform_admin");
  await proof(producerPage, password, auth);
  const producer = await submit(
    producerPage,
    "/api/access/users/create",
    "创建用户",
  );
  safe(producer.value);
  expect(producer.value).toMatchObject({
    result: "SUCCESS",
    sessionRevoked: false,
  });
  expect(producer.value.ID).toBeGreaterThan(0);
  expect(producer.value.ID).not.toBe(actorId);
  await navigateTo(producerPage, "退出登录");
  await login(producerPage, producerUsername, producerInitial);
  await expect(
    producerPage.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await fillSecret(
    producerPage.getByLabel("当前密码", { exact: true }),
    producerInitial,
  );
  await fillSecret(
    producerPage.getByLabel("新密码", { exact: true }),
    producerPassword,
  );
  await fillSecret(
    producerPage.getByLabel("确认新密码", { exact: true }),
    producerPassword,
  );
  await producerPage
    .getByRole("button", { name: "更新密码", exact: true })
    .click();
  await expect(
    producerPage.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  auth = await enrollMfa(producerPage, producerPassword);
  secrets.push(auth.secret);
  username = producerUsername;
  password = producerPassword;
  actorId = (await readJSON(producerContext, "/api/auth/me")).ID as number;
  expect(actorId).toBe(producer.value.ID);

  await navigateTo(producerPage, "管理操作账户");
  await producerPage
    .getByRole("button", { name: "新增操作账户", exact: true })
    .click();
  await producerPage
    .getByRole("button", { name: `选择 ${domain}`, exact: true })
    .click();
  await producerPage
    .getByLabel("登记标签（可选）", { exact: true })
    .fill(accountLabel);
  await fillSecret(
    producerPage.getByLabel("AD 用户名", { exact: true }),
    accountUser,
  );
  await fillSecret(
    producerPage.getByLabel("AD 密码", { exact: true }),
    accountPassword,
  );
  await proof(producerPage, password, auth);
  const createdAccount = await submit(
    producerPage,
    "/api/operation-accounts/create",
    "保存本地登记",
  );
  safe(createdAccount.value);
  const accountId = createdAccount.value.accountId as string;
  await producerPage
    .getByRole("button", { name: "查看凭据使用授权", exact: true })
    .click();
  const connectionGrant = await grant(
    producerPage,
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

  await navigateTo(producerPage, "域连接");
  const backToDomains = producerPage.getByRole("button", {
    name: "返回域连接列表",
    exact: true,
  });
  if (await backToDomains.isVisible()) await backToDomains.click();
  await producerPage
    .getByRole("button", { name: `查看 ${domain}`, exact: true })
    .click();
  await producerPage
    .getByRole("button", { name: "管理凭据来源", exact: true })
    .click();
  await producerPage
    .getByLabel("新的凭据来源", { exact: true })
    .selectOption("reference");
  await producerPage
    .getByRole("button", { name: `选择账户 ${accountLabel}`, exact: true })
    .click();
  await proof(producerPage, password, auth);
  expect(
    (
      await submit(
        producerPage,
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
    await readJSON(
      producerContext,
      `/api/domains/detail?domainId=${domainId}`,
      actorId,
    )
  ).connection;

  // Auth/me identifies the actor in its JSON body; unlike domain APIs it
  // does not emit X-ADTR-User-ID. Keep an explicit identity assertion.
  const me = await readJSON(producerContext, "/api/auth/me");
  expect(me.ID).toBe(actorId);
  expect(typeof me.csrfToken).toBe("string");
  expect(me.csrfToken.length).toBeGreaterThan(0);
  secrets.push(me.csrfToken);
  const headers = {
    Origin: new URL(producerPage.url()).origin,
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
      response = await producerContext.request.post(path, {
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
  expect(await readJSON(producerContext, effectivePath, actorId)).toMatchObject(
    {
      purpose: "domain.directory_read.v4",
      explicitlyGranted: false,
      eligible: false,
      consumerEnabled: true,
    },
  );
  // A real connection-test grant already exists; it cannot authorize v4.
  const denied = await post("/api/directory/v4/sync", sync, 403);
  expect(denied).toEqual({ error: "forbidden" });
  const account = (
    await readJSON(
      producerContext,
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
        producerContext,
        `${prefix}/effective?accountId=${accountId}`,
        actorId,
      ),
    ).toMatchObject({ explicitlyGranted: false, eligible: false });
  }
  const initial = await readJSON(
    producerContext,
    `/api/directory/v4/observation?domainId=${domainId}`,
    actorId,
  );
  expect(initial).toMatchObject({
    dictionaryVersion: 4,
    available: false,
    list: [],
  });
  await producerPage.setViewportSize({ width: 1440, height: 900 });
  const noObservation = await openMembers(producerPage, actorId);
  expect(noObservation.value).toMatchObject({
    dictionaryVersion: 4,
    available: false,
    list: [],
  });
  await expect(producerPage.getByRole("status")).toContainText(
    "暂无可读取的字典 4",
  );
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
    .poll(
      async () =>
        (await readJSON(producerContext, taskPath, actorId)).task.state,
      {
        timeout: 30000,
      },
    )
    .toBe("succeeded");
  const receipt = await readJSON(
    producerContext,
    `/api/directory/v4/receipt?domainId=${domainId}&idempotencyKey=${sync.idempotencyKey}`,
    actorId,
  );
  expect(receipt.task.taskUUID).toBe(taskUUID);
  const observed = await readJSON(
    producerContext,
    `/api/directory/v4/observation?domainId=${domainId}&observationId=${taskUUID}&pageSize=25`,
    actorId,
  );
  const second = await readJSON(
    producerContext,
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
  expect(stored.source).toEqual(observed.source);
  expect(stored.objects).toHaveLength(30);
  const backend = {
    taskUUID,
    ...evidence,
    source: observed.source,
    objects: expected,
  };
  const context = await browser.newContext({
    baseURL: process.env.ADTR_E2E_BASE_URL,
  });
  await observeGroupMembersV4Responses(context);
  const page = await context.newPage();
  page.on("console", (message) => consoles.push(message.text()));
  page.on("pageerror", (error) => consoles.push(error.message));
  await page.goto("/");
  await login(page, readerUsername, readerInitial);
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await fillSecret(page.getByLabel("当前密码", { exact: true }), readerInitial);
  await fillSecret(page.getByLabel("新密码", { exact: true }), readerPassword);
  await fillSecret(
    page.getByLabel("确认新密码", { exact: true }),
    readerPassword,
  );
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  const readerAuth = await enrollMfa(page, readerPassword);
  secrets.push(readerAuth.secret);
  // Prove an actual MFA login, not only enrollment in the initial session.
  await navigateTo(page, "退出登录");
  await login(page, readerUsername, readerPassword);
  await fillSecret(
    page.getByLabel("二次认证验证码", { exact: true }),
    await freshCode(readerAuth),
  );
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  const readerMe = await readJSON(context, "/api/auth/me");
  expect(readerMe).toMatchObject({
    ID: readerId,
    username: readerUsername,
    role: "viewer",
    needChangePwd: false,
    hasMfa: true,
  });
  expect(readerId).not.toBe(actorId);
  secrets.push(readerMe.csrfToken);
  const readerHeaders = {
    Origin: new URL(page.url()).origin,
    "X-CSRF-Token": readerMe.csrfToken,
  };
  const menu = (await readJSON(context, "/api/access/menu")).menu
    .map(({ mark, auth }: Permission) => ({ mark, auth }))
    .sort((a: Permission, b: Permission) => a.mark.localeCompare(b.mark));
  expect(menu).toEqual([
    { mark: "directory_assets", auth: { readable: true, writeable: false } },
    { mark: "domains", auth: { readable: true, writeable: false } },
  ]);
  const checks = [
    ...[
      "/api/domain-selection",
      "/api/domain-selection/resolve",
      "/api/directory/v4/observation",
      "/api/group-assets/v4/members",
    ].map((path) => ({ method: "GET", path, allowed: true })),
    ...[
      "/api/directory/v4/sync",
      "/api/directory/v4/cancel",
      "/api/directory-credential-use/v4/grant",
      "/api/directory-credential-use/v4/revoke",
    ].map((path) => ({ method: "POST", path, allowed: false })),
  ];
  const checked = await context.request.post("/api/access/check", {
    headers: readerHeaders,
    data: { paths: checks.map((c) => `${c.method} ${c.path}`) },
  });
  expect(checked.status()).toBe(200);
  expect(await checked.json()).toEqual({
    results: checks.map((c) => c.allowed),
  });
  const { ui, transition } = await captureRefreshMembers(
    page,
    readerId,
    domainId,
    backend,
    testInfo,
    async () => {
      await publishEmptyFixtureMarker();
      const nextSync = {
        ...sync,
        idempotencyKey: "v4-refresh-empty-real-wire",
      };
      const submitted = await post("/api/directory/v4/sync", nextSync);
      const emptyTaskUUID = submitted.task.taskUUID;
      expect(emptyTaskUUID).not.toBe(taskUUID);
      expect(submitted.replayed).toBe(false);
      await expect
        .poll(
          async () =>
            (
              await readJSON(
                producerContext,
                `/api/directory/v4/task?taskUUID=${emptyTaskUUID}`,
                actorId,
              )
            ).task.state,
          { timeout: 30000 },
        )
        .toBe("succeeded");
      const receipt = await readJSON(
        producerContext,
        `/api/directory/v4/receipt?domainId=${domainId}&idempotencyKey=${nextSync.idempotencyKey}`,
        actorId,
      );
      expect(receipt.task.taskUUID).toBe(emptyTaskUUID);
      const emptyObserved = await readJSON(
        producerContext,
        `/api/directory/v4/observation?domainId=${domainId}&observationId=${emptyTaskUUID}&pageSize=25`,
        actorId,
      );
      expect(emptyObserved).toMatchObject({
        dictionaryVersion: 4,
        available: true,
        observationId: emptyTaskUUID,
        list: [],
        page: { pageIdx: 1, pageSize: 25, total: 0, totalPage: 0 },
        source: { pages: 1 },
      });
      await expect
        .poll(async () => (await storedEvidence(emptyTaskUUID))?.useState, {
          timeout: 10000,
        })
        .toBe("quiesced");
      const emptyEvidence = await storedEvidence(emptyTaskUUID);
      expect(emptyEvidence).toMatchObject({
        dictionaryVersion: 4,
        count: 0,
        taskState: "succeeded",
        purpose: "domain.directory_read.v4",
        useState: "quiesced",
        reason: "executor_returned",
        dependencies: 0,
      });
      const emptyBytes = Buffer.from(emptyEvidence.bodyBase64, "base64");
      expect(createHash("sha256").update(emptyBytes).digest("hex")).toBe(
        emptyEvidence.sha256,
      );
      expect(JSON.parse(emptyBytes.toString("utf8"))).toEqual({
        dictionaryVersion: 4,
        objects: [],
        source: emptyObserved.source,
      });
      for (const key of [
        "server_name",
        "dc_host_name",
        "domain",
        "naming_context",
      ])
        expect(emptyObserved.source[key]).toBe(observed.source[key]);
      // Revoke the actual v4 grant. Read authority is independent of use authority.
      const revoked = await post("/api/directory-credential-use/v4/revoke", {
        ...mutation,
        expectedGrantRevision: granted.grantRevision,
        idempotencyKey: "v4-explicit-revoke",
      });
      expect(revoked.allowed).toBe(false);
      const producerEffective = await readJSON(
        producerContext,
        effectivePath,
        actorId,
      );
      expect(producerEffective).toMatchObject({
        explicitlyGranted: false,
        eligible: false,
      });

      const v4AllowedGrants = await producerAllowedGrants(emptyTaskUUID);
      expect(v4AllowedGrants).toBe(0);
      const before = await readerSnapshot(
        readerId,
        roleId,
        emptyTaskUUID,
        taskUUID,
      );
      expect(before).toMatchObject({
        actorId: readerId,
        roleId,
        baseRole: "viewer",
        readable: ["directory_assets", "domains"],
        writeable: [],
        domainIds: [domainId],
        useGrants: 0,
        observationSha256: emptyEvidence.sha256,
        oldObservationSha256: evidence.sha256,
        observationCount: 2,
      });
      return {
        emptyBackend: {
          taskUUID: emptyTaskUUID,
          ...emptyEvidence,
          source: emptyObserved.source,
          objects: [],
        },
        producer: {
          actorId,
          grantRevoked: !revoked.allowed,
          v4AllowedGrants,
          explicitlyGranted: producerEffective.explicitlyGranted,
          eligible: producerEffective.eligible,
        },
        before,
      };
    },
  );
  const { emptyBackend, producer: producerEvidence, before } = transition;
  const after = await readerSnapshot(
    readerId,
    roleId,
    emptyBackend.taskUUID,
    taskUUID,
  );
  expect(after).toEqual(before);
  expect(await storedEvidence(taskUUID)).toEqual(evidence);
  const {
    taskUUID: _,
    source: __,
    objects: ___,
    ...emptyStored
  } = emptyBackend;
  expect(await storedEvidence(emptyBackend.taskUUID)).toEqual(emptyStored);
  const reader = {
    actorId: readerId,
    producerActorId: actorId,
    roleId,
    permissions,
    menu,
    checks,
    authentication: {
      role: readerMe.role,
      hasMfa: readerMe.hasMfa,
      needChangePwd: readerMe.needChangePwd,
      loginMfa: true,
    },
    before,
    after,
  };
  const result = {
    format: 1,
    provenance: await refreshMembershipProvenance(),
    noObservation,
    backend,
    emptyBackend,
    producer: producerEvidence,
    reader,
    ui,
  };
  safe(result);
  safe(consoles);
  await context.close();
  await writeFile(
    testInfo.outputPath("group-members-v4-refresh-evidence.json"),
    JSON.stringify(result, null, 2),
  );
});
