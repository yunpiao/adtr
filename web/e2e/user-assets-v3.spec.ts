import {
  checkpoint,
  diagnosticStep,
  observeSafePageRequests,
} from "./safe-request-diagnostics";
import { navigateTo } from "./navigation";
import {
  test,
  expect,
  type BrowserContext,
  type Locator,
  type Page,
  type Request,
  type Response,
} from "@playwright/test";
import { createHmac, createHash } from "node:crypto";
import { isIP } from "node:net";
import { writeFile, readFile } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import {
  directoryV2ResponseJSON,
  observeDirectoryV2Responses,
} from "./directory-v2-response-observer";
import { holdAssetResponse } from "./user-assets-v2-held-response";
import { installUserAssetAbortIsolation } from "./user-assets-v2-abort-isolation";
import {
  useNativeTabLifecycle,
  requireSameNativeWindow,
} from "./native-tab-lifecycle";
import { fixtureGET } from "./fixture-get";
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

// Run only through user-assets-v3 on its owned disposable database, real
// worker and TLS LDAPFixture(directory_enabled=True, directory_v3=True). The fixture has 30 objects
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
    "ADTR_E2E_DB_CONTAINER",
  ] as const;
  if (
    names.some((name) => !process.env[name]) ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_MODE !== "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_V3 !== "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_V2 === "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_EMPTY === "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_SLOW === "true"
  )
    throw new Error(
      "Directory acceptance requires scripts/test_auth_e2e.py --suite user-assets-v3: an isolated fresh database, real worker, synthetic paged TLS LDAP fixture, ADTR_E2E_LDAP_DIRECTORY_MODE=true, ADTR_E2E_LDAP_DIRECTORY_V3=true and all ADTR_E2E auth/LDAP variables. No fixture means acceptance is blocked, not skipped.",
    );
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(process.env.ADTR_E2E_DB_CONTAINER!))
    throw new Error("User assets v3 requires its owned isolated database");
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
    return totp(auth.secret, now);
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
    throw new Error("Read-only isolated dictionary3 evidence query failed");
  }
  return stdout.trim() ? JSON.parse(stdout) : null;
}
const browserReads: {
  path: string;
  status: number;
  actorId: string | undefined;
  query: Record<string, string>;
  value: any;
}[] = [];
async function assetAction(
  page: Page,
  action: () => Promise<unknown>,
  detail = false,
  status = 200,
) {
  const path = detail ? "/api/user-assets/v3/detail" : "/api/user-assets/v3";
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === path &&
        candidate.request().method() === "GET",
    ),
    action(),
  ]);
  return recordAssetResponse(response, detail, status);
}
async function recordAssetResponse(
  response: Response,
  detail = false,
  status = 200,
) {
  const path = detail ? "/api/user-assets/v3/detail" : "/api/user-assets/v3";
  expect(new URL(response.url()).pathname).toBe(path);
  expect(response.request().method()).toBe("GET");
  expect(
    response.status(),
    "V3 workspace must reach the actual authenticated API",
  ).toBe(status);
  expect(response.headers()["cache-control"]).toBe("no-store");
  const value = await directoryV2ResponseJSON(response);
  const query = new URL(response.url()).searchParams;
  const actorId = response.headers()["x-adtr-user-id"];
  if (status === 200) {
    expect(value.dictionaryVersion).toBe(3);
    expect(value.selection).toEqual({
      domainId: query.get("domainId"),
      revision: query.get("expectedRevision"),
      credentialRevision: query.get("expectedCredentialRevision"),
    });
    expect(actorId).toMatch(/^[1-9][0-9]*$/u);
    if (query.has("observationId"))
      expect(value.observationId).toBe(query.get("observationId"));
    if (detail) expect(value.object.objectGUID).toBe(query.get("objectGUID"));
  }
  browserReads.push({
    path,
    status,
    actorId,
    query: Object.fromEntries(query),
    value,
  });
  return { value, query, actorId };
}
async function selectAssets(page: Page) {
  await expect(
    page.getByRole("link", { name: "字典 3 · 主组 RID / SPN", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  await diagnosticStep(page, "source-select", () =>
    page
      .getByRole("radio", { name: "选择数据源 synthetic.invalid", exact: true })
      .check(),
  );
  return assetAction(page, () =>
    page
      .getByRole("button", { name: "核对并打开连接详情", exact: true })
      .click(),
  );
}
async function openAssets(page: Page, requireDefaultV2 = false) {
  await navigateTo(page, "用户资产");
  await expect(page).toHaveURL(/#user-assets-v[23]$/u);
  if (requireDefaultV2) await expect(page).toHaveURL(/#user-assets-v2$/u);
  // The sidebar keeps an already-explicit v3 choice. Other workspace entries
  // retain the old default v2; cross-version navigation is always an actual link.
  if (new URL(page.url()).hash === "#user-assets-v2")
    await page
      .getByRole("link", { name: "字典 3 · 主组 RID / SPN", exact: true })
      .click();
  await expect(page).toHaveURL(/#user-assets-v3$/u);
  await expect(
    page.getByRole("table", { name: "用户资产列表", exact: true }),
  ).toHaveCount(0);
  return selectAssets(page);
}
async function searchAssets(page: Page, value: string) {
  await page.getByLabel("用户资产关键词", { exact: true }).fill(value);
  return assetAction(page, () =>
    page.getByRole("button", { name: "搜索用户", exact: true }).click(),
  );
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
  await expect(page.locator(".signed-in strong")).toHaveText(username);
}
async function fallbackTab(context: BrowserContext) {
  const page = await context.newPage();
  await page.addInitScript(() => {
    // Only disable notification transports. Native visibility/focus and all
    // authentication and asset responses remain real.
    Object.defineProperty(window, "BroadcastChannel", {
      configurable: true,
      value: class {
        constructor() {
          throw new DOMException("Disabled by E2E", "SecurityError");
        }
      },
    });
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      get() {
        throw new DOMException("Disabled by E2E", "SecurityError");
      },
    });
  });
  await useNativeTabLifecycle(page);
  await page.goto("/#user-assets-v3");
  const lifecycle = await page.evaluateHandle(() => {
    let events: { type: string; trusted: boolean; visible: boolean }[] = [];
    const record = (event: Event) => {
      if (event.type === "focus" && event.target !== window) return;
      events.push({
        type: event.type,
        trusted: event.isTrusted,
        visible: document.visibilityState === "visible",
      });
    };
    window.addEventListener("focus", record);
    document.addEventListener("visibilitychange", record);
    return {
      reset() {
        events = [];
      },
      read() {
        return {
          visibility: document.visibilityState,
          focused: document.hasFocus(),
          events,
        };
      },
      stop() {
        window.removeEventListener("focus", record);
        document.removeEventListener("visibilitychange", record);
      },
    };
  });
  return { page, lifecycle };
}

// All browser facts come from the real registered producer and stored API.
// Authentication forms are never captured. Only explicit clean asset screens
// and metadata/actual fact evidence below may become artifacts.
test.use({ trace: "off", screenshot: "off", video: "off" });
test.beforeAll(() => requireFixture());
const stopDiagnostics = new WeakMap<Page, () => void>();
test.beforeEach(async ({ context, page }) => {
  stopDiagnostics.set(page, observeSafePageRequests(page));
  await checkpoint(page, "test-start", "start");
  await observeDirectoryV2Responses(context);
  await context.addInitScript(installUserAssetAbortIsolation);
});
test.afterEach(async ({ page }, info) => {
  try {
    await checkpoint(
      page,
      "test-finish",
      info.status === info.expectedStatus ? "complete" : "failed",
    );
  } finally {
    stopDiagnostics.get(page)?.();
    stopDiagnostics.delete(page);
  }
});
test("UserAssetsV3 real workspace, pins, identity facts and native reader invalidation", async ({
  page,
  context,
}, testInfo) => {
  test.setTimeout(900_000);
  await useNativeTabLifecycle(page);
  await page.setViewportSize({ width: 1440, height: 900 });
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

  await diagnosticStep(page, "administrator-login", async () => {
    await page.goto("/");
    await login(page, username, initialPassword);
  });
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
  const createdGroup = await submit(
    page,
    "/api/resources/groups/create",
    "保存资源组",
  );
  expect(createdGroup.value).toMatchObject({ result: "SUCCESS" });
  const groupId = createdGroup.value.id as string;
  expect(groupId).toBeTruthy();
  await page
    .getByRole("button", { name: `查看 ${groupName}`, exact: true })
    .click();
  await page.getByRole("button", { name: "调整关联角色", exact: true }).click();
  await page.getByLabel("显式关联 platform_admin", { exact: true }).check();
  await proof(page, password, auth);
  expect(
    (await submit(page, "/api/resources/groups/assign", "保存角色关联")).value,
  ).toMatchObject({ result: "SUCCESS", sessionRevoked: true });
  let actorId = await diagnosticStep(page, "administrator-login", async () => {
    await login(page, username, password);
    await fillSecret(
      page.getByLabel("二次认证验证码", { exact: true }),
      await freshCode(auth),
    );
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "账户概览", exact: true }),
    ).toBeVisible();
    return (await readJSON(context, "/api/auth/me")).ID as number;
  });

  // Production allows ten sensitive actions per actor per fifteen minutes.
  // Bootstrap: password/MFA start/enable (3), domain/group/assignment (3),
  // producer creation (1), source edit (1), connection-grant cleanup (1),
  // final scope withdrawal (1) = 10. The assignment revokes
  // the bootstrap session; create the producer only after that UI re-login.
  // Producer: password/MFA (3), account/connection grant/binding (3),
  // v3 grant (1), first/second real sync (2), v3 revoke (1) = 10.
  // No rate-limit changes, database authorization edits or clock bypass.
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
    idempotencyKey: "v3-real-wire",
  };
  const effectivePath = `/api/directory-credential-use/v3/effective?accountId=${accountId}`;
  expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
    purpose: "domain.directory_read.v3",
    explicitlyGranted: false,
    eligible: false,
    consumerEnabled: true,
  });
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
  const observed = await readJSON(
    context,
    `/api/directory/v3/observation?domainId=${domainId}&observationId=${taskUUID}&pageSize=25`,
    actorId,
  );
  const second = await readJSON(
    context,
    `/api/directory/v3/observation?domainId=${domainId}&observationId=${taskUUID}&pageSize=25&pageIdx=2`,
    actorId,
  );
  expect(observed).toMatchObject({
    dictionaryVersion: 3,
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
    objectGUID: expected[0].objectGUID,
  });
  const detail = await readJSON(
    context,
    `/api/user-assets/v3/detail?${params}`,
    actorId,
  );
  expect(detail.dictionaryVersion).toBe(3);
  expect(detail.object).toEqual(expected[0]);
  const wrongOld = await context.request.get(
    `/api/user-assets/v2/detail?${params}`,
  );
  expect(wrongOld.status()).toBe(409);
  for (const prefix of ["/api/directory", "/api/directory/v2"]) {
    const wrong = await context.request.get(
      `${prefix}/observation?domainId=${domainId}&observationId=${taskUUID}`,
    );
    expect(wrong.status()).toBe(409);
  }
  params.set("objectGUID", expected[12].objectGUID);
  expect(
    (
      await context.request.get(`/api/user-assets/v3/detail?${params}`)
    ).status(),
  ).toBe(404);
  await expect
    .poll(async () => (await storedEvidence(taskUUID))?.useState, {
      timeout: 10000,
    })
    .toBe("quiesced");
  const evidence = await storedEvidence(taskUUID);
  expect(evidence).toMatchObject({
    dictionaryVersion: 3,
    count: 30,
    taskState: "succeeded",
    purpose: "domain.directory_read.v3",
    useState: "quiesced",
    reason: "executor_returned",
    dependencies: 0,
  });
  const bytes = Buffer.from(evidence.bodyBase64, "base64");
  expect(createHash("sha256").update(bytes).digest("hex")).toBe(
    evidence.sha256,
  );
  const stored = JSON.parse(bytes.toString("utf8"));
  expect(stored.dictionaryVersion).toBe(3);
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
  }
  const users = expected.slice(0, 12);
  const selection = {
    domainId,
    revision: beforeConnection.revision,
    credentialRevision: beforeConnection.credentialRevision,
  };
  const table = page.getByRole("table", { name: "用户资产列表", exact: true });
  const panel = page.getByRole("region", { name: "用户详情", exact: true });
  const fact = (label: string) =>
    panel
      .getByText(label, { exact: true })
      .locator("xpath=following-sibling::dd[1]");
  const screenshots: unknown[] = [];
  const transport: unknown[] = [];
  const observations = new Map<string, { sha256: string; source: unknown }>([
    [taskUUID, { sha256: evidence.sha256, source: observed.source }],
  ]);
  const capture = async (filename: string, list: any, object?: any) => {
    // Bind each clean image to browser-consumed bytes, task/source identity and
    // the independently hashed PostgreSQL observation. Never capture proof UI.
    expect(list.dictionaryVersion).toBe(3);
    expect(list.available).toBe(true);
    const stored = observations.get(list.observationId);
    expect(stored).toBeDefined();
    expect(list.source).toEqual(stored!.source);
    if (object) {
      expect(object.observationId).toBe(list.observationId);
      expect(object.source).toEqual(list.source);
      expect(object.selection).toEqual(list.selection);
    }
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    safe(await page.locator("body").innerText());
    const path = testInfo.outputPath(filename);
    await page.screenshot({ path, fullPage: false });
    screenshots.push({
      filename,
      synthetic: true,
      viewport: page.viewportSize(),
      taskUUID: list.observationId,
      observationId: list.observationId,
      dictionaryVersion: 3,
      selection: list.selection,
      source: list.source,
      observationSHA256: stored!.sha256,
      screenshotSHA256: createHash("sha256")
        .update(await readFile(path))
        .digest("hex"),
      actualList: list,
      actualDetail: object ?? null,
    });
    await writeFile(
      testInfo.outputPath("user-assets-v3-screenshot-bindings.json"),
      JSON.stringify({ partial: true, screenshots }, null, 2),
    );
  };
  const captureSPNGeometry = async (values: Locator, filename: string) => {
    // Only layout numbers/styles, never document text or authentication inputs.
    const geometry = await values.evaluate(async (element) => {
      const rect = (value: DOMRectReadOnly) => ({
        x: value.x,
        y: value.y,
        width: value.width,
        height: value.height,
        top: value.top,
        right: value.right,
        bottom: value.bottom,
        left: value.left,
      });
      const intersection = await new Promise<unknown>((resolve) => {
        const observer = new IntersectionObserver(([entry]) => {
          observer.disconnect();
          resolve({
            ratio: entry.intersectionRatio,
            bounding: rect(entry.boundingClientRect),
            intersection: rect(entry.intersectionRect),
            root: entry.rootBounds ? rect(entry.rootBounds) : null,
          });
        });
        observer.observe(element);
      });
      const boxes = [];
      for (
        let node: Element | null = element;
        node;
        node = node.parentElement
      ) {
        const style = getComputedStyle(node);
        boxes.push({
          tag: node.tagName,
          className: node.className,
          rect: rect(node.getBoundingClientRect()),
          clientWidth: node.clientWidth,
          clientHeight: node.clientHeight,
          scrollWidth: node.scrollWidth,
          scrollHeight: node.scrollHeight,
          scrollTop: node.scrollTop,
          scrollLeft: node.scrollLeft,
          overflowX: style.overflowX,
          overflowY: style.overflowY,
          borderTop: style.borderTopWidth,
          borderRight: style.borderRightWidth,
          borderBottom: style.borderBottomWidth,
          borderLeft: style.borderLeftWidth,
        });
      }
      return {
        viewport: { width: innerWidth, height: innerHeight },
        intersection,
        boxes,
      };
    });
    console.log("SPN layout geometry", JSON.stringify(geometry));
    await writeFile(
      testInfo.outputPath(filename),
      JSON.stringify(geometry, null, 2),
    );
  };
  const first = await openAssets(page, true);
  expect(first.actorId).toBe(String(actorId));
  expect(first.query.has("observationId")).toBe(false);
  expect(first.value).toMatchObject({
    dictionaryVersion: 3,
    available: true,
    selection,
    observationId: taskUUID,
    list: users.slice(0, 10),
    source: observed.source,
    page: { pageIdx: 1, pageSize: 10, total: 12, totalPage: 2 },
  });
  await expect(table.locator("tbody tr")).toHaveCount(10);
  await expect(table).not.toContainText("group-");
  await expect(table).not.toContainText("computer-");
  await expect(
    page.getByRole("button", { name: "提交目录同步", exact: true }),
  ).toHaveCount(0);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  const firstBounds = await table.locator("tbody tr").first().boundingBox();
  // Preserve source-bound partial pixels if the unchanged layout gate fails.
  await capture("user-assets-v3-desktop-list.png", first.value);
  expect(firstBounds).not.toBeNull();
  expect(firstBounds!.y + firstBounds!.height).toBeLessThanOrEqual(900);

  await test.step("actual ordered raw SPN, hostile controls, zero, max and null facts", async () => {
    for (const row of [1, 2, 3]) {
      const actual = await assetAction(
        page,
        () =>
          page
            .getByRole("button", {
              name: `查看用户 user-${String(row).padStart(2, "0")}`,
              exact: true,
            })
            .click(),
        true,
      );
      expect(actual.value.object).toEqual(users[row - 1]);
      expect(actual.query.get("observationId")).toBe(taskUUID);
      expect(actual.query.get("objectGUID")).toBe(users[row - 1].objectGUID);
      await expect(fact("主组 RID（primaryGroupID）")).toHaveText(
        String(users[row - 1].primaryGroupID),
      );
      await expect(fact("采集字典版本")).toHaveText("3");
      await expect(fact("观测编号")).toHaveText(taskUUID);
      if (row === 1) {
        const values = panel.getByRole("list", {
          name: "完整 SPN 原始值（展示转义）",
          exact: true,
        });
        await expect(values.locator("li")).toHaveText([
          "HOST/Z",
          "host/a\\u0000\\u000a😀<script>",
          "HOST/a",
        ]);
        await expect(values.locator("script,a,img")).toHaveCount(0);
        await expect(fact("描述")).toHaveText(
          "Line 1\\u0000\\u000a\\u0009😀\\u202e",
        );
        await expect(fact("账户控制值（原始整数）")).toHaveText("0");
        // Center the real scroll target: nearest-edge scrolling can leave a
        // fractional bottom border behind the drawer scrollport (run 141).
        await values.evaluate((element) =>
          element.scrollIntoView({ block: "center", inline: "nearest" }),
        );
        await captureSPNGeometry(
          values,
          "user-assets-v3-desktop-spn-geometry.json",
        );
        await capture(
          "user-assets-v3-desktop-ordered-spn.png",
          first.value,
          actual.value,
        );
        await expect(values).toBeInViewport({ ratio: 1 });
      } else {
        await fact("主组 RID（primaryGroupID）").scrollIntoViewIfNeeded();
        await capture(
          `user-assets-v3-desktop-rid-${row === 2 ? "zero" : "max"}.png`,
          first.value,
          actual.value,
        );
      }
      await page
        .getByRole("button", { name: "关闭用户详情", exact: true })
        .click();
      await expect(panel).toHaveCount(0);
      await expect(
        page.getByRole("button", {
          name: `查看用户 user-${String(row).padStart(2, "0")}`,
          exact: true,
        }),
      ).toBeFocused();
    }
    const secondPage = await assetAction(page, () =>
      page.getByRole("button", { name: "用户资产下一页", exact: true }).click(),
    );
    expect(secondPage.query.get("observationId")).toBe(taskUUID);
    expect(secondPage.value.list).toEqual(users.slice(10));
    const nullDetail = await assetAction(
      page,
      () =>
        page
          .getByRole("button", { name: "查看用户 user-12", exact: true })
          .click(),
      true,
    );
    expect(nullDetail.value.object).toEqual(users[11]);
    await expect(fact("主组 RID（primaryGroupID）")).toHaveText("未返回");
    await expect(fact("服务主体名称（SPN）")).toHaveText("未返回");
    await fact("主组 RID（primaryGroupID）").scrollIntoViewIfNeeded();
    await capture(
      "user-assets-v3-desktop-null-facts.png",
      secondPage.value,
      nullDetail.value,
    );
    await page.keyboard.press("Escape");
    await expect(panel).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "查看用户 user-12", exact: true }),
    ).toBeFocused();
    const back = await assetAction(page, () =>
      page.getByRole("button", { name: "用户资产上一页", exact: true }).click(),
    );
    expect(back.value).toEqual(first.value);
  });

  await test.step("literal search, exact SAM, paired UTC creation bounds and stable ordering keep the pin", async () => {
    for (const [query, wanted] of [
      ["USER-01", [users[0]]],
      [" Mixed", [users[0]]],
      ["S-1-5-21-1-2-3-1012", [users[11]]],
      ["OU=Users", users.slice(0, 10)],
      ["HOST/Z", []],
    ] as const) {
      const found = await searchAssets(page, query);
      expect(found.query.get("search")).toBe(query);
      expect(found.query.get("observationId")).toBe(taskUUID);
      expect(found.value.list).toEqual(wanted);
    }
    await expect(
      page.getByText("此观测在当前筛选下没有匹配用户。", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("当前数据源没有可用的字典 3 观测；尚不能确认用户数量。", {
        exact: true,
      }),
    ).toHaveCount(0);
    await assetAction(page, () =>
      page.getByRole("button", { name: "清除用户筛选", exact: true }).click(),
    );
    await page.getByText("精确账户与创建时间筛选", { exact: true }).click();
    await page.getByLabel("精确账户名（SAM）", { exact: true }).fill("user-11");
    await page
      .getByLabel("创建时间开始（含）", { exact: true })
      .fill("2026-10-01T10:03:04+08:00");
    await page
      .getByLabel("创建时间结束（不含）", { exact: true })
      .fill("2026-10-01T02:03:04.000000001Z");
    await page.getByLabel("创建时间排序", { exact: true }).selectOption("desc");
    const filtered = await assetAction(page, () =>
      page.getByRole("button", { name: "搜索用户", exact: true }).click(),
    );
    expect(filtered.value.list).toEqual([users[10]]);
    expect(filtered.query.get("sAMAccountName")).toBe("user-11");
    expect(filtered.query.get("startTm")).toBe("2026-10-01T10:03:04+08:00");
    expect(filtered.query.get("endTm")).toBe("2026-10-01T02:03:04.000000001Z");
    expect(filtered.query.get("creatTm")).toBe("desc");
    expect(filtered.query.get("observationId")).toBe(taskUUID);
    await capture("user-assets-v3-desktop-exact-filters.png", filtered.value);
    await page.getByLabel("精确账户名（SAM）", { exact: true }).fill("USER-11");
    expect(
      (
        await assetAction(page, () =>
          page.getByRole("button", { name: "搜索用户", exact: true }).click(),
        )
      ).value.list,
    ).toEqual([]);
    await page.getByLabel("精确账户名（SAM）", { exact: true }).fill("");
    await page.getByLabel("创建时间开始（含）", { exact: true }).fill("");
    await page.getByLabel("创建时间结束（不含）", { exact: true }).fill("");
    const descending = await assetAction(page, () =>
      page.getByRole("button", { name: "搜索用户", exact: true }).click(),
    );
    expect(descending.value.list).toEqual(users.slice(1, 11));
    const descendingLast = await assetAction(page, () =>
      page.getByRole("button", { name: "用户资产下一页", exact: true }).click(),
    );
    expect(descendingLast.value.list).toEqual([users[11], users[0]]);
    const reset = await assetAction(page, () =>
      page.getByRole("button", { name: "清除用户筛选", exact: true }).click(),
    );
    expect(reset.query.get("observationId")).toBe(taskUUID);
    for (const key of ["sAMAccountName", "startTm", "endTm", "creatTm"])
      expect(reset.query.has(key)).toBe(false);
    await page.getByText("精确账户与创建时间筛选", { exact: true }).click();
    for (const size of [20, 30, 40, 50, 10]) {
      const resized = await assetAction(page, () =>
        page
          .getByLabel("用户资产每页条数", { exact: true })
          .selectOption(String(size)),
      );
      expect(resized.query.get("observationId")).toBe(taskUUID);
      expect(resized.value.list).toEqual(users.slice(0, size));
    }
  });

  await test.step("UAC disabled-bit draft, real filtered reads, reset and late original response keep the pin", async () => {
    const control = page.getByLabel("本次观测的禁用位", { exact: true });
    const apply = () =>
      page.getByRole("button", { name: "搜索用户", exact: true }).click();
    const clear = () =>
      page.getByRole("button", { name: "清除用户筛选", exact: true }).click();
    const next = () =>
      page.getByRole("button", { name: "用户资产下一页", exact: true }).click();
    const originalUsers = [...observed.list, ...second.list].filter(
      (object) => object.kind === "user",
    );
    type Bit = "" | "clear" | "set" | "not_returned";
    const assertRead = (
      actual: Awaited<ReturnType<typeof assetAction>>,
      bit: Bit,
      pageIdx = 1,
    ) => {
      const query = {
        domainId,
        expectedRevision: selection.revision,
        expectedCredentialRevision: selection.credentialRevision,
        observationId: taskUUID,
        search: "",
        pageIdx: String(pageIdx),
        pageSize: "10",
        ...(bit ? { uacDisableBit: bit } : {}),
      };
      // Compare all original query entries: neither duplicate parameters nor
      // an unexpected filter can hide behind Object.fromEntries().
      expect([...actual.query].sort()).toEqual(Object.entries(query).sort());
      expect(actual.query.getAll("uacDisableBit")).toEqual(bit ? [bit] : []);
      expect(actual.actorId).toBe(String(actorId));
      const empty = bit === "set" || bit === "not_returned";
      expect(actual.value).toEqual({
        ...first.value,
        list: empty
          ? []
          : originalUsers.slice((pageIdx - 1) * 10, pageIdx * 10),
        page: {
          pageIdx,
          pageSize: 10,
          total: empty ? 0 : 12,
          totalPage: empty ? 0 : 2,
        },
      });
    };
    const assertEmpty = async (label: string) => {
      await expect(
        page.getByText(`已应用禁用位：${label}`, { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText("此观测在当前筛选下没有匹配用户。", { exact: true }),
      ).toBeVisible();
      await expect(table).toHaveCount(0);
      await expect(page.locator(".asset-page-count")).toHaveText(
        "第 1 页 · 共 0 页 · 匹配用户 0 个",
      );
      await expect(
        page.getByText(`观测来源 · ${taskUUID}`, { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: "用户资产上一页", exact: true }),
      ).toBeDisabled();
      await expect(
        page.getByText(
          "当前数据源没有可用的字典 3 观测；尚不能确认用户数量。",
          { exact: true },
        ),
      ).toHaveCount(0);
      await expect(
        page.getByRole("button", { name: "用户资产下一页", exact: true }),
      ).toBeDisabled();
    };
    // The actual producer fixture has UAC 0/512 for all 12 users. It proves
    // nonempty clear and empty set/not_returned only, not nonempty set/null LDAP.
    expect(users.map((user) => user.userAccountControl)).toEqual([
      0,
      ...Array(11).fill(512),
    ]);
    assertRead(await assetAction(page, next), "", 2);
    let draftReads = 0;
    const recordDraftRead = (request: Request) => {
      if (new URL(request.url()).pathname === "/api/user-assets/v3")
        draftReads++;
    };
    page.on("request", recordDraftRead);
    try {
      await control.selectOption("clear");
      await expect(
        page.getByText("筛选已编辑，尚未应用；请提交搜索后查看详情或翻页。", {
          exact: true,
        }),
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: "用户资产上一页", exact: true }),
      ).toBeDisabled();
      await expect(
        page.getByRole("button", { name: "用户资产下一页", exact: true }),
      ).toBeDisabled();
      expect(draftReads).toBe(0);
    } finally {
      page.off("request", recordDraftRead);
    }
    const clearFirst = await assetAction(page, apply);
    assertRead(clearFirst, "clear");
    await expect(table.locator("tbody tr")).toHaveCount(10);
    await expect(
      page.getByText("已应用禁用位：禁用位未设置", { exact: true }),
    ).toBeVisible();
    await capture("user-assets-v3-desktop-uac-clear.png", clearFirst.value);
    const clearLast = await assetAction(page, next);
    assertRead(clearLast, "clear", 2);
    expect([...clearFirst.value.list, ...clearLast.value.list]).toEqual(
      originalUsers,
    );
    await expect(table.locator("tbody tr")).toHaveCount(2);
    assertRead(await assetAction(page, clear), "");
    await expect(control).toHaveValue("");
    await expect(table.locator("tbody tr")).toHaveCount(10);

    for (const [bit, label] of [
      ["set", "禁用位已设置"],
      ["not_returned", "UAC未返回"],
    ] as const) {
      await control.selectOption(bit);
      const empty = await assetAction(page, apply);
      assertRead(empty, bit);
      await assertEmpty(label);
      await capture(`user-assets-v3-desktop-uac-${bit}.png`, empty.value);
    }
    // Choosing unrestricted must omit the parameter, not send an empty value.
    await control.selectOption("");
    assertRead(await assetAction(page, apply), "");
    await expect(table.locator("tbody tr")).toHaveCount(10);

    const held = await holdAssetResponse(
      page,
      "/api/user-assets/v3",
      actorId,
      users[0],
    );
    try {
      await control.selectOption("clear");
      const [heldRequest] = await Promise.all([
        page.waitForRequest(
          (request) =>
            new URL(request.url()).pathname === "/api/user-assets/v3" &&
            request.method() === "GET",
        ),
        apply(),
      ]);
      await held.fetched();
      await control.selectOption("set");
      const current = await assetAction(page, apply);
      assertRead(current, "set");
      await assertEmpty("禁用位已设置");
      // Bind to the old browser Request, never let the newer same-path GET
      // satisfy its waiter. The helper replays only genuine upstream bytes.
      const [originalResponse, outcome] = await Promise.all([
        page.waitForResponse((response) => response.request() === heldRequest),
        held.release(),
      ]);
      assertRead(await recordAssetResponse(originalResponse), "clear");
      expect(outcome.application).toBe("complete-consumed");
      transport.push({ scenario: "uac-clear-superseded-by-set", ...outcome });
      await expect(control).toHaveValue("set");
      await assertEmpty("禁用位已设置");
    } finally {
      await held.release();
    }
    const restored = await assetAction(page, clear);
    assertRead(restored, "");
    expect(restored.value).toEqual(first.value);
    await expect(control).toHaveValue("");
    await expect(table.locator("tbody tr")).toHaveCount(10);
    await expect(
      page.getByText("已应用禁用位：不限", { exact: true }),
    ).toBeVisible();
  });

  await test.step("native clipboard and Chromium drag events reject keyword CR/LF without a transformed query", async () => {
    const field = page.getByLabel("用户资产关键词", { exact: true });
    let reads = 0;
    const recordRead = (request: Request) => {
      if (new URL(request.url()).pathname === "/api/user-assets/v3") reads++;
    };
    page.on("request", recordRead);
    const cdp = await context.newCDPSession(page);
    await context.grantPermissions(["clipboard-read", "clipboard-write"], {
      origin: new URL(page.url()).origin,
    });
    try {
      for (const kind of ["paste", "drop"] as const) {
        for (const raw of ["user-\n11", "\r\n"]) {
          await field.fill("user-01");
          await field.evaluate((element, kind) => {
            (window as any).__v3TransferWitness = null;
            element.addEventListener(
              kind,
              (event) => {
                setTimeout(() => {
                  (window as any).__v3TransferWitness = {
                    trusted: event.isTrusted,
                    prevented: event.defaultPrevented,
                  };
                });
              },
              { once: true },
            );
          }, kind);
          if (kind === "paste") {
            await page.evaluate(
              (raw) => navigator.clipboard.writeText(raw),
              raw,
            );
            await field.focus();
            await page.keyboard.press("Control+V");
          } else {
            await field.scrollIntoViewIfNeeded();
            const bounds = await field.boundingBox();
            expect(bounds).not.toBeNull();
            const data = {
              items: [{ mimeType: "text/plain", data: raw }],
              dragOperationsMask: 1,
            };
            for (const type of ["dragEnter", "dragOver", "drop"] as const)
              await cdp.send("Input.dispatchDragEvent", {
                type,
                x: bounds!.x + bounds!.width / 2,
                y: bounds!.y + bounds!.height / 2,
                data,
              });
          }
          await expect
            .poll(() =>
              page.evaluate(() => (window as any).__v3TransferWitness),
            )
            .toEqual({ trusted: true, prevented: true });
          await expect(field).toHaveValue("user-01");
          await expect(field).toHaveAttribute("aria-invalid", "true");
          await expect(
            page.getByRole("button", { name: "搜索用户", exact: true }),
          ).toBeDisabled();
          await page
            .locator("form.asset-search-toolbar")
            .evaluate((form) => (form as HTMLFormElement).requestSubmit());
          expect(reads).toBe(0);
          await field.fill("x");
          await field.fill("");
          await expect(field).toHaveAttribute("aria-invalid", "false");
        }
      }
    } finally {
      page.off("request", recordRead);
      await cdp.detach();
      await context.clearPermissions();
      await page.evaluate(() => {
        delete (window as any).__v3TransferWitness;
      });
    }
    await assetAction(page, () =>
      page.getByRole("button", { name: "清除用户筛选", exact: true }).click(),
    );
  });

  await test.step("mobile real data and drawer remain bounded at 390 by 844", async () => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.evaluate(() => scrollTo(0, 0));
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await capture("user-assets-v3-mobile-list.png", first.value);
    const actual = await assetAction(
      page,
      () =>
        page
          .getByRole("button", { name: "查看用户 user-01", exact: true })
          .click(),
      true,
    );
    expect(actual.value.object).toEqual(users[0]);
    await capture(
      "user-assets-v3-mobile-detail.png",
      first.value,
      actual.value,
    );
    await fact("主组 RID（primaryGroupID）").scrollIntoViewIfNeeded();
    const visibleSPN = panel.getByRole("list", {
      name: "完整 SPN 原始值（展示转义）",
      exact: true,
    });
    await visibleSPN.evaluate((element) =>
      element.scrollIntoView({ block: "center", inline: "nearest" }),
    );
    await captureSPNGeometry(
      visibleSPN,
      "user-assets-v3-mobile-spn-geometry.json",
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await capture(
      "user-assets-v3-mobile-ordered-spn.png",
      first.value,
      actual.value,
    );
    await expect(visibleSPN).toBeInViewport({ ratio: 1 });
    await page
      .getByRole("button", { name: "关闭用户详情", exact: true })
      .click();
    await page.setViewportSize({ width: 1440, height: 900 });
  });

  await test.step("interrupted actual list retries exact source, filter and observation", async () => {
    let failedQuery: URLSearchParams | undefined;
    await page.route(
      (url) => url.pathname === "/api/user-assets/v3",
      async (route) => {
        failedQuery = new URL(route.request().url()).searchParams;
        await route.abort("connectionreset");
      },
      { times: 1 },
    );
    await page.getByLabel("用户资产关键词", { exact: true }).fill("user-02");
    await page.getByRole("button", { name: "搜索用户", exact: true }).click();
    await expect(
      page.getByRole("button", { name: "重试用户资产读取", exact: true }),
    ).toBeVisible();
    expect(failedQuery).toBeDefined();
    expect(failedQuery!.get("observationId")).toBe(taskUUID);
    const retried = await assetAction(page, () =>
      page
        .getByRole("button", { name: "重试用户资产读取", exact: true })
        .click(),
    );
    expect([...retried.query]).toEqual([...failedQuery!]);
    expect(retried.value.list).toEqual([users[1]]);
    await assetAction(page, () =>
      page.getByRole("button", { name: "清除用户筛选", exact: true }).click(),
    );
  });

  await test.step("late genuine detail cannot reopen a closed panel; late list cannot replace newer search", async () => {
    const held = await holdAssetResponse(
      page,
      "/api/user-assets/v3/detail",
      actorId,
      users[0],
    );
    await page
      .getByRole("button", { name: "查看用户 user-01", exact: true })
      .click();
    try {
      await held.fetched();
      await page
        .getByRole("button", { name: "关闭用户详情", exact: true })
        .click();
      const outcome = await held.release();
      expect(outcome.application).toBe("complete-consumed");
      transport.push({ scenario: "closed-detail", ...outcome });
      await expect(panel).toHaveCount(0);
    } finally {
      await held.release();
    }
    const old = await holdAssetResponse(
      page,
      "/api/user-assets/v3",
      actorId,
      users[0],
    );
    await page.getByLabel("用户资产关键词", { exact: true }).fill("user-02");
    await page.getByRole("button", { name: "搜索用户", exact: true }).click();
    try {
      await old.fetched();
      await page.getByLabel("用户资产关键词", { exact: true }).fill("user-11");
      const current = await assetAction(page, () =>
        page.getByLabel("用户资产关键词", { exact: true }).press("Enter"),
      );
      expect(current.value.list).toEqual([users[10]]);
      const outcome = await old.release();
      expect(outcome.application).toBe("complete-consumed");
      transport.push({ scenario: "superseded-list", ...outcome });
      await expect(table).toContainText("user-11");
      await expect(table).not.toContainText("user-02");
    } finally {
      await old.release();
    }
    await assetAction(page, () =>
      page.getByRole("button", { name: "清除用户筛选", exact: true }).click(),
    );
  });

  await test.step("version links, Back, Forward and reload require a clean source choice", async () => {
    const held = await holdAssetResponse(
      page,
      "/api/user-assets/v3",
      actorId,
      users[0],
    );
    await page.getByLabel("用户资产关键词", { exact: true }).fill("user-02");
    await page.getByRole("button", { name: "搜索用户", exact: true }).click();
    try {
      await held.fetched();
      await diagnosticStep(page, "version-switch", async () => {
        await page
          .getByRole("link", { name: "字典 2 · 基础身份", exact: true })
          .click();
        await expect(page).toHaveURL(/#user-assets-v2$/u);
      });
      const outcome = await held.release();
      expect(outcome.application).toBe("complete-consumed");
      transport.push({ scenario: "version-switch", ...outcome });
      await expect(table).toHaveCount(0);
      await expect(panel).toHaveCount(0);
    } finally {
      await held.release();
    }
    await diagnosticStep(page, "source-select", () =>
      page
        .getByRole("radio", {
          name: "选择数据源 synthetic.invalid",
          exact: true,
        })
        .check(),
    );
    const [v2Response] = await Promise.all([
      page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === "/api/user-assets/v2" &&
          response.request().method() === "GET",
      ),
      page
        .getByRole("button", { name: "核对并打开连接详情", exact: true })
        .click(),
    ]);
    expect(v2Response.status()).toBe(200);
    expect(await directoryV2ResponseJSON(v2Response)).toMatchObject({
      dictionaryVersion: 2,
      available: false,
      list: [],
    });
    await expect(table).toHaveCount(0);
    await page.goBack();
    await expect(page).toHaveURL(/#user-assets-v3$/u);
    await expect(table).toHaveCount(0);
    await expect(
      page
        .getByRole("status")
        .filter({ hasText: "切换版本或返回页面后需重新选择" }),
    ).toBeVisible();
    await page.goForward();
    await expect(page).toHaveURL(/#user-assets-v2$/u);
    await expect(table).toHaveCount(0);
    await page
      .getByRole("link", { name: "字典 3 · 主组 RID / SPN", exact: true })
      .click();
    const fresh = await selectAssets(page);
    expect(fresh.query.has("observationId")).toBe(false);
    expect(fresh.query.get("search") ?? "").toBe("");
    expect(fresh.value.list).toEqual(users.slice(0, 10));
    await page.reload();
    await expect(page).toHaveURL(/#user-assets-v3$/u);
    await expect(table).toHaveCount(0);
    await expect(panel).toHaveCount(0);
    await expect(
      page
        .getByRole("status")
        .filter({ hasText: "切换版本或返回页面后需重新选择" }),
    ).toBeVisible();
    expect((await selectAssets(page)).value).toEqual(first.value);
  });

  // A second real producer task provides a distinct immutable observation.
  // The existing browser pin must survive until the explicit refresh click.
  const secondSubmitted = await post("/api/directory/v3/sync", {
    ...sync,
    idempotencyKey: "v3-workspace-second-real-wire",
  });
  const newerId = secondSubmitted.task.taskUUID as string;
  expect(newerId).not.toBe(taskUUID);
  await expect
    .poll(
      async () =>
        (
          await readJSON(
            context,
            `/api/directory/v3/task?taskUUID=${newerId}`,
            actorId,
          )
        ).task.state,
      { timeout: 30000 },
    )
    .toBe("succeeded");
  await expect
    .poll(async () => (await storedEvidence(newerId))?.useState, {
      timeout: 10000,
    })
    .toBe("quiesced");
  const newerStored = await storedEvidence(newerId);
  expect(newerStored).toMatchObject({
    dictionaryVersion: 3,
    count: 30,
    taskState: "succeeded",
    useState: "quiesced",
    dependencies: 0,
  });
  const newerBytes = Buffer.from(newerStored.bodyBase64, "base64");
  expect(createHash("sha256").update(newerBytes).digest("hex")).toBe(
    newerStored.sha256,
  );
  const oldPin = await searchAssets(page, "user-01");
  expect(oldPin.query.get("observationId")).toBe(taskUUID);
  expect(oldPin.value.observationId).toBe(taskUUID);
  const refreshed = await assetAction(page, () =>
    page.getByRole("button", { name: "刷新用户资产观测", exact: true }).click(),
  );
  expect(refreshed.query.has("observationId")).toBe(false);
  expect(refreshed.query.get("search")).toBe("user-01");
  expect(refreshed.value.observationId).toBe(newerId);
  expect(refreshed.value.list).toEqual([users[0]]);
  observations.set(newerId, {
    sha256: newerStored.sha256,
    source: refreshed.value.source,
  });
  const latest = await assetAction(page, () =>
    page.getByRole("button", { name: "清除用户筛选", exact: true }).click(),
  );
  expect(latest.query.get("observationId")).toBe(newerId);
  await capture("user-assets-v3-desktop-explicit-refresh.png", latest.value);

  // Real producer authorization withdrawal leaves currently authorized stored
  // reads intact; it cannot be used as a shortcut to fabricate a denial.
  const revoked = await post("/api/directory-credential-use/v3/revoke", {
    ...mutation,
    expectedGrantRevision: granted.grantRevision,
    idempotencyKey: "v3-workspace-revoke-producer",
  });
  expect(revoked.allowed).toBe(false);
  expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
    explicitlyGranted: false,
    eligible: false,
  });
  expect((await searchAssets(page, "user-01")).value.list).toEqual([users[0]]);

  await test.step("native cross-tab logout clears both notification and real-focus fallback readers", async () => {
    const fallback = await fallbackTab(context);
    const switching = await context.newPage();
    await useNativeTabLifecycle(switching);
    const pending: Awaited<ReturnType<typeof holdAssetResponse>>[] = [];
    try {
      await switching.goto("/");
      await expect(switching.locator(".signed-in strong")).toHaveText(
        producerUsername,
      );
      await requireSameNativeWindow([page, fallback.page, switching]);
      for (const old of [page, fallback.page]) {
        await old.bringToFront();
        await openAssets(old);
        const held = await holdAssetResponse(
          old,
          "/api/user-assets/v3/detail",
          actorId,
          users[0],
        );
        pending.push(held);
        await old
          .getByRole("button", { name: "查看用户 user-01", exact: true })
          .click();
        await held.fetched();
      }
      await switching.bringToFront();
      await expect
        .poll(() => fallback!.lifecycle.evaluate((value) => value.read()))
        .toMatchObject({ visibility: "hidden", focused: false });
      await navigateTo(switching, "退出登录");
      await expect(
        page.getByRole("heading", { name: "登录账户", exact: true }),
      ).toBeVisible();
      await expect(table).toHaveCount(0);
      await expect(panel).toHaveCount(0);
      // Notification-disabled document stays unchanged until native focus.
      await expect(fallback.page.locator(".signed-in strong")).toHaveText(
        producerUsername,
      );
      await fallback.lifecycle.evaluate((value) => value.reset());
      const expired = fallback.page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === "/api/auth/me" &&
          response.status() === 401,
      );
      await fallback.page.bringToFront();
      expect(await (await expired).json()).toEqual({
        error: "unauthenticated",
      });
      await expect
        .poll(() => fallback!.lifecycle.evaluate((value) => value.read()))
        .toMatchObject({
          visibility: "visible",
          focused: true,
          events: expect.arrayContaining([
            expect.objectContaining({ type: "focus", trusted: true }),
            { type: "visibilitychange", visible: true, trusted: true },
          ]),
        });
      const lifecycle = await fallback.lifecycle.evaluate((value) =>
        value.read(),
      );
      expect(lifecycle).toMatchObject({
        visibility: "visible",
        focused: true,
        events: expect.arrayContaining([
          expect.objectContaining({ type: "focus", trusted: true }),
          { type: "visibilitychange", visible: true, trusted: true },
        ]),
      });
      for (const [index, held] of pending.entries()) {
        const outcome = await held.release();
        expect(outcome.application).toBe("complete-consumed");
        transport.push({
          scenario: index === 0 ? "notification-logout" : "native-focus-logout",
          lifecycle: index === 1 ? lifecycle : null,
          ...outcome,
        });
      }
      for (const old of [page, fallback.page]) {
        await expect(
          old.getByRole("table", { name: "用户资产列表", exact: true }),
        ).toHaveCount(0);
        await expect(
          old.getByRole("region", { name: "用户详情", exact: true }),
        ).toHaveCount(0);
      }
      await switching.bringToFront();
      await loginMfa(switching, producerUsername, producerPassword, auth);
    } finally {
      for (const held of pending) await held.release();
      await fallback.lifecycle.evaluate((value) => value.stop());
      await fallback.lifecycle.dispose();
      await fallback.page.close();
      await switching.close();
    }
    await page.bringToFront();
    await expect(page.locator(".signed-in strong")).toHaveText(
      producerUsername,
    );
    await expect(table).toHaveCount(0);
    await openAssets(page);
  });

  await test.step("current source revision mismatch resets selection; native scope revocation drops late protected bytes", async () => {
    const administrator = await context
      .browser()!
      .newContext({ baseURL: process.env.ADTR_E2E_BASE_URL });
    const adminPage = await administrator.newPage();
    let fallback: Awaited<ReturnType<typeof fallbackTab>> | undefined;
    let held: Awaited<ReturnType<typeof holdAssetResponse>> | undefined;
    let primaryFailure: unknown;
    let failed = false;
    let stage = "administrator login";
    try {
      await adminPage.goto("/");
      await loginMfa(
        adminPage,
        bootstrap.username,
        bootstrap.password,
        bootstrap.auth,
      );
      const adminMe = await readJSON(administrator, "/api/auth/me");
      secrets.push(adminMe.csrfToken);
      const adminPost = async (path: string, data: Record<string, unknown>) => {
        let response;
        try {
          response = await administrator.request.post(path, {
            headers: {
              Origin: new URL(page.url()).origin,
              "X-CSRF-Token": adminMe.csrfToken,
            },
            data: {
              ...data,
              actorPassword: bootstrap.password,
              totpCode: await freshCode(bootstrap.auth),
            },
            maxRedirects: 0,
            timeout: 15000,
          });
        } catch {
          throw new Error("Synthetic administrative proof request failed");
        }
        const value = await response.json();
        safe(value);
        // Only the validated public error code accompanies a status failure.
        const errorCode =
          typeof value.error === "string" && /^[a-z_]{1,80}$/u.test(value.error)
            ? value.error
            : "none";
        expect(response.status(), `POST ${path}: ${errorCode}`).toBe(200);
        return value;
      };
      fallback = await fallbackTab(context);
      await fallback.page.bringToFront();
      await selectAssets(fallback.page);
      held = await holdAssetResponse(
        fallback.page,
        "/api/user-assets/v3/detail",
        actorId,
        users[0],
      );
      await fallback.page
        .getByRole("button", { name: "查看用户 user-01", exact: true })
        .click();
      await held.fetched();
      await held.diagnose("genuine detail captured");
      stage = "background held detail and foreground source";
      await page.bringToFront();
      await requireSameNativeWindow([page, fallback.page]);
      await openAssets(page);
      await expect
        .poll(() => fallback!.lifecycle.evaluate((value) => value.read()))
        .toMatchObject({ visibility: "hidden", focused: false });
      stage = "update current source revision";
      const changed = await adminPost("/api/domains/update", {
        domainId,
        expectedRevision: beforeConnection.revision,
        dcHostName: "changed.synthetic.invalid",
        ldapAddr: ldapIP,
        port: "636",
      });
      expect(changed.result).toBe("SUCCESS");
      expect(changed.revision).not.toBe(beforeConnection.revision);
      stage = "source conflict clears selected view";
      const stale = await assetAction(
        page,
        () =>
          page
            .getByRole("button", { name: "刷新用户资产观测", exact: true })
            .click(),
        false,
        409,
      );
      expect(stale.value).toEqual({ error: "selection_changed" });
      await expect(table).toHaveCount(0);
      await expect(panel).toHaveCount(0);
      await expect(
        page
          .getByRole("status")
          .filter({ hasText: "切换版本或返回页面后需重新选择" }),
      ).toBeVisible();
      stage = "select changed source without current observation";
      const unavailable = await selectAssets(page);
      expect(unavailable.query.has("observationId")).toBe(false);
      expect(unavailable.value).toMatchObject({
        dictionaryVersion: 3,
        available: false,
        list: [],
      });
      expect(unavailable.value.selection.revision).toBe(changed.revision);
      await expect(
        page.getByText(
          "当前数据源没有可用的字典 3 观测；尚不能确认用户数量。",
          { exact: true },
        ),
      ).toBeVisible();
      // Preserve the governance invariant: the last administrator scope
      // cannot be removed while any explicit use grant remains allowed.
      // The v3 collection grant was already revoked; the setup connection-test
      // grant is independent and requires its own genuine, version-pinned revoke.
      stage = "revoke remaining connection-test grant";
      const connectionRevoked = await adminPost("/api/credential-use/revoke", {
        ...mutation,
        purpose: "domain.connection_test",
        expectedGrantRevision: connectionGrant.value.grantRevision,
        idempotencyKey: "v3-workspace-connection-cleanup",
      });
      expect(connectionRevoked).toMatchObject({
        result: "SUCCESS",
        purpose: "domain.connection_test",
        allowed: false,
        replayed: false,
      });
      const remainingConnection = await readJSON(
        administrator,
        `/api/credential-use/effective?accountId=${accountId}`,
        adminMe.ID,
      );
      expect(remainingConnection).toMatchObject({
        purpose: "domain.connection_test",
        explicitlyGranted: false,
        eligible: false,
      });
      stage = "revoke current scope";
      const withdrawn = await adminPost("/api/resources/groups/assign", {
        id: groupId,
        roleIds: [],
      });
      expect(withdrawn).toMatchObject({
        result: "SUCCESS",
        sessionRevoked: true,
      });
      const currentDenied = await context.request.get(
        `/api/user-assets/v3?${new URLSearchParams({ domainId, expectedRevision: changed.revision, expectedCredentialRevision: beforeConnection.credentialRevision })}`,
      );
      expect(currentDenied.status()).toBe(401);
      expect(await currentDenied.json()).toEqual({ error: "unauthenticated" });
      await held.diagnose("API list401 observed");
      stage = "native revalidation and private-view cleanup";
      await fallback.lifecycle.evaluate((value) => value.reset());
      const revalidated = fallback.page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === "/api/auth/me" &&
          response.status() === 401,
      );
      await fallback.page.bringToFront();
      expect(await (await revalidated).json()).toEqual({
        error: "unauthenticated",
      });
      await expect
        .poll(() => fallback!.lifecycle.evaluate((value) => value.read()))
        .toMatchObject({
          visibility: "visible",
          focused: true,
          events: expect.arrayContaining([
            expect.objectContaining({ type: "focus", trusted: true }),
            { type: "visibilitychange", visible: true, trusted: true },
          ]),
        });
      const lifecycle = await fallback.lifecycle.evaluate((value) =>
        value.read(),
      );
      expect(lifecycle).toMatchObject({
        visibility: "visible",
        focused: true,
        events: expect.arrayContaining([
          expect.objectContaining({ type: "focus", trusted: true }),
          { type: "visibilitychange", visible: true, trusted: true },
        ]),
      });
      // A response event precedes React's private-view cleanup. Wait for
      // the actual invalidated screen before delivering the old genuine bytes.
      await expect(
        fallback.page.getByRole("heading", { name: "登录账户", exact: true }),
      ).toBeVisible();
      await expect(
        fallback.page.getByRole("table", { name: "用户资产列表", exact: true }),
      ).toHaveCount(0);
      await expect(
        fallback.page.getByRole("region", { name: "用户详情", exact: true }),
      ).toHaveCount(0);
      await held.diagnose("native session401 observed");
      stage = "release old genuine detail after invalidation";
      const outcome = await held.release();
      expect(outcome.application).toBe("complete-consumed");
      transport.push({
        scenario: "source-conflict-and-native-scope-revocation",
        oldRevision: beforeConnection.revision,
        changedRevision: changed.revision,
        listDenialStatus: currentDenied.status(),
        lifecycle,
        ...outcome,
      });
      await expect(
        fallback.page.getByRole("heading", { name: "登录账户", exact: true }),
      ).toBeVisible();
      await expect(
        fallback.page.getByRole("table", { name: "用户资产列表", exact: true }),
      ).toHaveCount(0);
      await expect(
        fallback.page.getByRole("region", { name: "用户详情", exact: true }),
      ).toHaveCount(0);
    } catch (error) {
      failed = true;
      primaryFailure = error;
      // Static stage only: never print privileged request data or proof inputs.
      console.log("V3 authority scenario failed at stage", stage);
    } finally {
      const cleanupErrors: unknown[] = [];
      const cleanup = async (action: () => Promise<unknown>) => {
        try {
          await action();
        } catch (error) {
          cleanupErrors.push(error);
        }
      };
      if (held) await cleanup(() => held!.release());
      if (fallback) {
        await cleanup(() =>
          fallback!.lifecycle.evaluate((value) => value.stop()),
        );
        await cleanup(() => fallback!.lifecycle.dispose());
        await cleanup(() => fallback!.page.close());
      }
      await cleanup(() => administrator.close());
      // Keep the first assertion failure observable even when releasing a
      // not-yet-invalidated held response also fails its strict abort proof.
      if (failed)
        throw new AggregateError(
          [primaryFailure, ...cleanupErrors],
          `V3 authority scenario failed at ${stage}`,
          { cause: primaryFailure },
        );
      if (cleanupErrors.length)
        throw new AggregateError(
          cleanupErrors,
          "V3 authority scenario cleanup failed",
        );
    }
  });
  safe(consoles);
  for (const read of browserReads)
    if (read.status === 200) expect(read.actorId).toBe(String(actorId));
  const artifact = {
    synthetic: true,
    chain:
      "registered Worker -> isolated paged TLS LDAP -> PostgreSQL -> actual API -> real browser",
    dictionaryVersion: 3,
    taskUUID,
    observationId: taskUUID,
    sha256: evidence.sha256,
    source: observed.source,
    purpose: evidence.purpose,
    reason: evidence.reason,
    dependencies: evidence.dependencies,
    actualUsers: [...observed.list, ...second.list].filter(
      (object) => object.kind === "user",
    ),
    browserReads,
    storedIdentity: stored.objects.map(
      (object: { identity: unknown }) => object.identity,
    ),
    observations: [...observations].map(([observationId, value]) => ({
      observationId,
      ...value,
    })),
    screenshots,
    transport,
  };
  safe(artifact);
  const output = testInfo.outputPath("user-assets-v3-workspace-evidence.json");
  await writeFile(output, JSON.stringify(artifact, null, 2));
  await testInfo.attach("user-assets-v3-workspace-evidence", {
    path: output,
    contentType: "application/json",
  });
});
