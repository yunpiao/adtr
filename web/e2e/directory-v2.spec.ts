import {
  test,
  expect,
  type BrowserContext,
  type Locator,
  type Page,
  type Route,
} from "@playwright/test";
import { createHmac } from "node:crypto";
import { directoryV2ResponseRegressions } from "./directory-v2-response-regressions";
import {
  directoryV2ResponseJSON,
  observeDirectoryV2Responses,
} from "./directory-v2-response-observer";
import { isIP } from "node:net";
import type { DirectoryV2Input } from "../src/directory-v2-api";
import type { Task } from "../src/task-api";

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
// worker and TLS LDAPFixture(directory_enabled=True, directory_v2=True). The fixture has 30 objects
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
    process.env.ADTR_E2E_LDAP_DIRECTORY_V2 !== "true"
  )
    throw new Error(
      "Directory acceptance requires scripts/test_auth_e2e.py --suite directory-v2: an isolated fresh database, real worker, synthetic paged TLS LDAP fixture, ADTR_E2E_LDAP_DIRECTORY_MODE=true, ADTR_E2E_LDAP_DIRECTORY_V2=true and all ADTR_E2E auth/LDAP variables. No fixture means acceptance is blocked, not skipped.",
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
  await page.getByRole("button", { name: "多因素认证", exact: true }).click();
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

async function loseSyncResponse(
  page: Page,
  actorId: number,
  username: string,
  safe: (value: unknown) => void,
) {
  const owner = `${actorId}:${encodeURIComponent(username)}`;
  const storageKey = `adtr.directory-sync-intent.v2:${owner}`;
  // The real receipt can arrive before a locator observes this transient panel.
  // Observe its actual DOM commit without delaying or replacing any GET.
  const witness = await page.evaluateHandle((key) => {
    let snapshot: {
      text: string;
      proofInputs: number;
      intentKeys: string[];
      intent: DirectoryV2Input;
    } | null = null;
    const observer = new MutationObserver(() => {
      const region = document.querySelector(
        'section[aria-label="核对目录同步回执"]',
      );
      if (!region || !region.getClientRects().length) return;
      const stored = JSON.parse(sessionStorage.getItem(key) ?? "null");
      snapshot = {
        text: region.textContent ?? "",
        proofInputs: region.querySelectorAll(
          'input[name="actorPassword"], input[name="totpCode"]',
        ).length,
        intentKeys: Object.keys(stored?.intent ?? {}).sort(),
        intent: {
          domainId: stored?.intent?.domainId,
          expectedRevision: stored?.intent?.expectedRevision,
          expectedCredentialGeneration:
            stored?.intent?.expectedCredentialGeneration,
          idempotencyKey: stored?.intent?.idempotencyKey,
        },
      };
      observer.disconnect();
    });
    observer.observe(document.body, { childList: true, subtree: true });
    return { read: () => snapshot, stop: () => observer.disconnect() };
  }, storageKey);
  type Committed = { task: Task; intent: DirectoryV2Input };
  let resolveCommit!: (value: Committed) => void;
  let rejectCommit!: (error: unknown) => void;
  const commit = new Promise<Committed>((resolve, reject) => {
    resolveCommit = resolve;
    rejectCommit = reject;
  });
  // Attach immediately so a route failure cannot become an unhandled rejection.
  void commit.catch(() => {});
  const syncURL = (url: URL) => url.pathname === "/api/directory/v2/sync";
  const dropDelivery = async (route: Route) => {
    let response;
    try {
      expect(route.request().method()).toBe("POST");
      const sent = route.request().postDataJSON();
      expect(Object.keys(sent).sort()).toEqual(
        [
          "actorPassword",
          "domainId",
          "expectedCredentialGeneration",
          "expectedRevision",
          "idempotencyKey",
          "totpCode",
        ].sort(),
      );
      // Keep only the nonsecret original intent; never retain or print proof.
      const intent: DirectoryV2Input = {
        domainId: sent.domainId,
        expectedRevision: sent.expectedRevision,
        expectedCredentialGeneration: sent.expectedCredentialGeneration,
        idempotencyKey: sent.idempotencyKey,
      };
      safe(intent);
      response = await route.fetch({
        maxRedirects: 0,
        maxRetries: 0,
        timeout: 15_000,
      });
      expect(
        response.status(),
        "real sync must commit before delivery is lost",
      ).toBe(200);
      expect(response.headers()["cache-control"]).toBe("no-store");
      expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId));
      const value = await response.json();
      safe(value);
      expect(Object.keys(value).sort()).toEqual(["replayed", "task"]);
      expect(value).toMatchObject({
        replayed: false,
        task: {
          taskName: "domain.directory_read.v2",
          domainId: intent.domainId,
          payloadVersion: 1,
          maxAttempts: 1,
          result: {},
          cursor: {},
        },
      });
      expect(typeof value.task.taskUUID).toBe("string");
      // The authenticated API has replied successfully. Lose only transport
      // delivery to the browser, not the server transaction; never fulfill data.
      await route.abort("failed");
      resolveCommit({ task: value.task as Task, intent });
    } catch (error) {
      rejectCommit(error);
      await route.abort("failed").catch(() => {});
    } finally {
      await response?.dispose();
    }
  };
  try {
    await page.route(syncURL, dropDelivery, { times: 1 });
    const [committed, receipt] = await Promise.all([
      commit,
      page.waitForResponse(
        (candidate) =>
          new URL(candidate.url()).pathname === "/api/directory/v2/receipt" &&
          candidate.request().method() === "GET",
        { timeout: 15_000 },
      ),
      page.waitForRequest(
        (request) =>
          syncURL(new URL(request.url())) && request.method() === "POST",
        { timeout: 15_000 },
      ),
      page.getByRole("button", { name: "提交目录同步", exact: true }).click(),
    ]);
    const unknown = await witness.evaluate((value) => value.read());
    safe(unknown);
    expect(unknown).not.toBeNull();
    expect(unknown!.text).toContain("原操作可能已提交");
    expect(unknown!.text).toContain("查询不会新建任务");
    expect(unknown!.proofInputs).toBe(0);
    expect(unknown!.intentKeys).toEqual(Object.keys(committed.intent).sort());
    expect(unknown!.intent).toEqual(committed.intent);
    expect(receipt.status()).toBe(200);
    expect(receipt.headers()["cache-control"]).toBe("no-store");
    expect(receipt.headers()["x-adtr-user-id"]).toBe(String(actorId));
    expect([...new URL(receipt.url()).searchParams.entries()].sort()).toEqual(
      [
        ["domainId", committed.intent.domainId],
        ["idempotencyKey", committed.intent.idempotencyKey],
      ].sort(),
    );
    const recovered = await directoryV2ResponseJSON(receipt);
    safe(recovered);
    expect(Object.keys(recovered)).toEqual(["task"]);
    expect(recovered.task).toMatchObject({
      taskUUID: committed.task.taskUUID,
      taskName: committed.task.taskName,
      domainId: committed.intent.domainId,
      payloadVersion: 1,
      maxAttempts: 1,
    });
    await expect(
      page.getByRole("region", { name: "目录同步任务", exact: true }),
    ).toContainText(committed.task.taskUUID);
    await expect(
      page.getByRole("button", {
        name: "使用原编号和版本重试同步",
        exact: true,
      }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "提交目录同步", exact: true }),
    ).toHaveCount(0);
    const saved = await page.evaluate(
      (key) => JSON.parse(sessionStorage.getItem(key) ?? "null"),
      storageKey,
    );
    safe(saved);
    expect(saved).toEqual({
      owner,
      intent: { ...committed.intent, taskUUID: committed.task.taskUUID },
    });
    expect(
      await page.evaluate(
        (key) => sessionStorage.getItem(key),
        `adtr.directory-sync-intent.v1:${owner}`,
      ),
      "The v2 recovery must not populate the v1 key",
    ).toBeNull();
    return committed;
  } finally {
    await page.unroute(syncURL, dropDelivery);
    await witness.evaluate((value) => value.stop());
    await witness.dispose();
  }
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
  await page.getByRole("button", { name: "补充目录资产", exact: true }).click();
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

// Disable credential-bearing traces/screenshots, including on setup failure.
// Only the successful sync has a transport fault injected after its real API
// reply. All API data, including receipt recovery, comes from the real server.
test.use({ trace: "off", screenshot: "off", video: "off" });
test.beforeAll(() => requireFixture());
test.beforeEach(async ({ context }) => {
  await observeDirectoryV2Responses(context);
});
directoryV2ResponseRegressions();
test("real dictionary-v2 UI → authenticated API → worker → paged synthetic LDAP → PostgreSQL observation", async ({
  page,
  context,
}) => {
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
  let syncPosts = 0;
  context.on("request", (request) => {
    if (
      request.method() === "POST" &&
      new URL(request.url()).pathname === "/api/directory/v2/sync"
    )
      syncPosts++;
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
  const bootstrap = { username, password, auth };
  secrets.push(auth.secret);
  const tenant = await readJSON(context, "/api/resources/tenant");
  expect(tenant.maxAdCount).toBe(2);
  expect(tenant.expireTime).toBeGreaterThan(Math.floor(Date.now() / 1000));

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

  await page.getByRole("button", { name: "资源与租户", exact: true }).click();
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
  // producer creation (1), later source edit (1) = 8. The assignment revokes
  // the bootstrap session; create the producer only after that UI re-login.
  // Producer: password/MFA (3), account/connection grant/binding (3), v1 grant
  // (1), denied v2 sync (1), v2 grant (1), committed v2 sync (1) = 10.
  // The forbidden sync still consumes rate budget. Login/logout never resets it.
  await page.getByRole("button", { name: "访问管理", exact: true }).click();
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
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
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

  await page.getByRole("button", { name: "管理操作账户", exact: true }).click();
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

  await page.getByRole("button", { name: "域连接", exact: true }).click();
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
  // An actual, explicitly saved v1 grant must not authorize the v2 profile.
  // Both grants target the same account and role through the production UI.
  await page.getByRole("button", { name: "管理操作账户", exact: true }).click();
  const v1BackToAccounts = page.getByRole("button", {
    name: "返回操作账户列表",
    exact: true,
  });
  if (await v1BackToAccounts.isVisible()) await v1BackToAccounts.click();
  await page
    .getByRole("button", { name: `查看 ${accountLabel}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "查看目录读取凭据使用授权", exact: true })
    .click();
  const v1Grant = await grant(
    page,
    password,
    auth,
    "/api/directory-credential-use",
  );
  safe(v1Grant.value);
  expect(v1Grant.value).toMatchObject({
    purpose: "domain.directory_read",
    allowed: true,
    replayed: false,
  });
  expect(
    await readJSON(
      context,
      `/api/directory-credential-use/effective?accountId=${accountId}`,
      actorId,
    ),
  ).toMatchObject({
    purpose: "domain.directory_read",
    explicitlyGranted: true,
    eligible: true,
    grantRevision: v1Grant.value.grantRevision,
  });
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
  await proof(page, password, auth);
  const deniedUse = await submit(
    page,
    "/api/directory/v2/sync",
    "提交目录同步",
    403,
  );
  expect(deniedUse.value).toEqual({ error: "forbidden" });
  expect(
    (await readJSON(context, "/api/tasks?pageIdx=1&pageSize=20")).page.total,
  ).toBe(0);

  await page.getByRole("button", { name: "管理操作账户", exact: true }).click();
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

  await page
    .getByRole("button", { name: "补充目录凭据授权", exact: true })
    .click();
  await expect(
    page.getByRole("table", {
      name: "具有已保存目录读取授权的账户",
      exact: true,
    }),
  ).toContainText(accountLabel);
  await openDirectory(page);
  await proof(page, password, auth);
  const submitted = await loseSyncResponse(page, actorId, username, safe);
  expect(syncPosts).toBe(2); // Denied intent, then the single committed intent.
  const { intent } = submitted;
  expect(intent).toMatchObject({
    domainId,
    expectedRevision: beforeConnection.revision,
    expectedCredentialGeneration: beforeConnection.credentialRevision,
  });
  const taskUUID = submitted.task.taskUUID;
  expect(typeof taskUUID).toBe("string");
  expect(typeof intent.idempotencyKey).toBe("string");

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
        completed = value.task as Task;
        if (
          ["failed", "partial_failed", "dead_letter", "cancelled"].includes(
            completed.state,
          )
        )
          throw new Error(
            `Synthetic directory worker ended ${completed.state}: ${completed.error}`,
          );
        return completed.state;
      },
      {
        timeout: 60_000,
        message:
          "The real paged LDAP worker must successfully publish the observation",
      },
    )
    .toBe("succeeded");
  expect(completed).toMatchObject({
    taskUUID,
    state: "succeeded",
    attempt: 1,
    result: {},
    cursor: {},
  });
  const taskRegion = page.getByRole("region", {
    name: "目录同步任务",
    exact: true,
  });
  await expect(taskRegion.getByRole("status")).toContainText(
    "服务器状态：已成功",
  );
  await expect(taskRegion).toContainText(taskUUID);
  const receipt = await readJSON(
    context,
    `/api/directory/v2/receipt?${new URLSearchParams({ domainId, idempotencyKey: intent.idempotencyKey })}`,
    actorId,
  );
  expect(receipt.task).toEqual(completed);
  // Fixed route selection must never reinterpret a v2 task/receipt as v1.
  for (const path of [
    `/api/directory/task?taskUUID=${taskUUID}`,
    `/api/directory/receipt?${new URLSearchParams({ domainId, idempotencyKey: intent.idempotencyKey })}`,
  ]) {
    const response = await context.request.get(path);
    expect(response.status()).toBe(404);
    expect(await response.json()).toEqual({ error: "not_found" });
  }
  const untouchedV1 = await readJSON(
    context,
    `/api/directory/observation?domainId=${domainId}`,
    actorId,
  );
  expect(untouchedV1).toEqual({
    available: false,
    list: [],
    page: { pageIdx: 1, pageSize: 50, total: 0, totalPage: 0 },
  });
  const generic = await readJSON(
    context,
    `/api/tasks/detail?taskUUID=${taskUUID}`,
  );
  expect(generic.task).toEqual(completed);
  expect(JSON.stringify(generic)).not.toMatch(
    /objectGUID|samAccountName|objectSid|whenCreated|Mixed@example|grantRevision|accountCredentialRevision|connectionCredentialGeneration/u,
  );
  safe(generic);

  const refreshed = await observationAction(page, () =>
    page.getByRole("button", { name: "刷新目录观察", exact: true }).click(),
  );
  const expected = expectedObjects();
  expect(refreshed.actorId).toBe(String(actorId));
  expect(refreshed.value).toMatchObject({
    dictionaryVersion: 2,
    available: true,
    observationId: taskUUID,
    page: { pageIdx: 1, pageSize: 50, total: 30, totalPage: 1 },
    source: {
      server_name: "dc.synthetic.invalid",
      dc_host_name: "dc.synthetic.invalid",
      domain,
      naming_context: "dc=synthetic,dc=invalid",
      pages: 2,
    },
  });
  expect(refreshed.value.list).toEqual(expected);
  expect(
    Date.parse(refreshed.value.source.completed_at),
  ).toBeGreaterThanOrEqual(Date.parse(refreshed.value.source.started_at));
  expect(refreshed.query.has("observationId")).toBe(false);
  const table = page.getByRole("table", { name: "目录对象观察", exact: true });
  await expect(table.locator("tbody tr")).toHaveCount(30);
  await expect(table).toContainText("user-01");
  await expect(table).toContainText("computer-06$");
  await expect(
    table.locator("tbody tr").nth(0).getByRole("cell").nth(5),
  ).toHaveText("0");
  await expect(
    table.locator("tbody tr").nth(23).getByRole("cell").nth(2),
  ).toHaveText("未返回");
  await expect(
    table.locator("tbody tr").nth(23).getByRole("cell").nth(5),
  ).toHaveText("未返回");
  for (const field of [
    "objectSid",
    "mail",
    "description",
    "whenCreated（UTC）",
  ])
    await expect(
      table.getByRole("columnheader", { name: field, exact: true }),
    ).toBeVisible();
  const factual = table.locator("tbody tr").nth(0).getByRole("cell");
  await expect(factual.nth(6)).toHaveText("S-1-5-21-1-2-3-1001");
  // Compare textContent, because locator text matchers intentionally normalize
  // whitespace whereas mail must retain its original accepted bytes.
  expect(await factual.nth(7).textContent()).toBe(" Mixed@example.test ");
  await expect(factual.nth(7)).toHaveCSS("white-space", "pre-wrap");
  expect(await factual.nth(8).textContent()).toBe(
    "Line 1\\u0000\\u000a\\u0009😀\\u202e",
  );
  await expect(factual.nth(9)).toHaveText("0001-01-01T00:00:00Z");
  for (const index of [6, 7, 8, 9])
    await expect(
      table.locator("tbody tr").nth(23).getByRole("cell").nth(index),
    ).toHaveText("未返回");
  await expect(table.locator('a[href^="mailto:"]')).toHaveCount(0);
  expect(await table.textContent()).not.toMatch(/[\u0000\u202e]/u);

  const firstPage = await observationAction(page, () =>
    page.getByLabel("目录每页条数", { exact: true }).selectOption("25"),
  );
  expect(firstPage.value.list).toEqual(expected.slice(0, 25));
  expect(firstPage.value.page).toEqual({
    pageIdx: 1,
    pageSize: 25,
    total: 30,
    totalPage: 2,
  });
  await expect(table.locator("tbody tr")).toHaveCount(25);
  const pagination = page.getByLabel("目录观察分页", { exact: true });
  const secondPage = await observationAction(page, () =>
    pagination.getByRole("button", { name: "下一页", exact: true }).click(),
  );
  expect(secondPage.query.get("observationId")).toBe(taskUUID);
  expect(secondPage.query.get("pageIdx")).toBe("2");
  expect(secondPage.value.list).toEqual(expected.slice(25));
  await expect(table.locator("tbody tr")).toHaveCount(5);
  await expect(
    pagination.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();

  for (const [kind, count] of [
    ["user", 12],
    ["group", 12],
    ["computer", 6],
  ] as const) {
    const filtered = await observationAction(page, () =>
      page.getByLabel("目录对象类型", { exact: true }).selectOption(kind),
    );
    expect(filtered.query.get("kind")).toBe(kind);
    expect(filtered.query.get("pageIdx")).toBe("1");
    expect(filtered.query.has("observationId")).toBe(false);
    expect(filtered.value.list).toEqual(
      expected.filter((object) => object.kind === kind),
    );
    expect(filtered.value.page).toEqual({
      pageIdx: 1,
      pageSize: 25,
      total: count,
      totalPage: 1,
    });
    await expect(table.locator("tbody tr")).toHaveCount(count);
  }
  // This is an out-of-range page of a populated observation. Actual empty
  // observations, cancellation and reader isolation have independent real
  // suites with their own disposable fixtures and sensitive-action budgets.
  const emptyPage = await readJSON(
    context,
    `/api/directory/v2/observation?${new URLSearchParams({ domainId, observationId: taskUUID, kind: "user", pageIdx: "2", pageSize: "25" })}`,
    actorId,
  );
  expect(emptyPage).toMatchObject({
    dictionaryVersion: 2,
    available: true,
    observationId: taskUUID,
    list: [],
    page: { pageIdx: 2, pageSize: 25, total: 12, totalPage: 1 },
  });
  const invalidPage = await context.request.get(
    `/api/directory/v2/observation?domainId=${domainId}&pageIdx=2&pageSize=25`,
  );
  expect(invalidPage.status()).toBe(400);
  expect(await invalidPage.json()).toEqual({ error: "invalid_input" });

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

  expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
    explicitlyGranted: true,
    eligible: true,
    grantRevision: directoryGrant.value.grantRevision,
  });
  expect(
    await readJSON(
      context,
      `/api/directory-credential-use/effective?accountId=${accountId}`,
      actorId,
    ),
  ).toMatchObject({
    explicitlyGranted: true,
    eligible: true,
    grantRevision: v1Grant.value.grantRevision,
  });
  expect(
    await readJSON(
      context,
      `/api/credential-use/effective?accountId=${accountId}`,
      actorId,
    ),
  ).toMatchObject({
    explicitlyGranted: true,
    grantRevision: connectionGrant.value.grantRevision,
  });
  await page.reload();
  const afterReload = await openDirectory(page);
  expect(afterReload.value).toMatchObject({
    dictionaryVersion: 2,
    available: true,
    observationId: taskUUID,
    list: expected,
  });
  await expect(table.locator("tbody tr")).toHaveCount(30);
  expect(syncPosts).toBe(2); // One rejected intent and one actual task, no automatic write replay.

  // Change the actual current source through another authorized actor's UI.
  // This consumes that actor's eighth sensitive action. No new LDAP call is
  // submitted; changing a synthetic host only invalidates the old source pin.
  await observationAction(page, () =>
    page.getByLabel("目录每页条数", { exact: true }).selectOption("25"),
  );
  const editor = await context
    .browser()!
    .newContext({ baseURL: process.env.ADTR_E2E_BASE_URL });
  const editorPage = await editor.newPage();
  try {
    await editorPage.goto("/");
    await login(editorPage, bootstrap.username, bootstrap.password);
    await fillSecret(
      editorPage.getByLabel("二次认证验证码", { exact: true }),
      await freshCode(bootstrap.auth),
    );
    await editorPage.getByRole("button", { name: "登录", exact: true }).click();
    await expect(
      editorPage.getByRole("heading", { name: "账户概览", exact: true }),
    ).toBeVisible();
    await editorPage
      .getByRole("button", { name: "域连接", exact: true })
      .click();
    await editorPage
      .getByRole("button", { name: `查看 ${domain}`, exact: true })
      .click();
    await editorPage
      .getByRole("button", { name: "编辑域连接", exact: true })
      .click();
    await editorPage
      .getByLabel("域控 DNS 名称", { exact: true })
      .fill("dc2.synthetic.invalid");
    await proof(editorPage, bootstrap.password, bootstrap.auth);
    const changed = await submit(
      editorPage,
      "/api/domains/update",
      "保存域修改",
    );
    safe(changed.value);
    expect(changed.value).toMatchObject({ result: "SUCCESS" });
  } finally {
    await editor.close();
  }
  const [stalePin] = await Promise.all([
    page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === "/api/directory/v2/observation" &&
        response.request().method() === "GET",
    ),
    pagination.getByRole("button", { name: "下一页", exact: true }).click(),
  ]);
  expect(new URL(stalePin.url()).searchParams.get("observationId")).toBe(
    taskUUID,
  );
  expect(new URL(stalePin.url()).searchParams.get("pageIdx")).toBe("2");
  expect(stalePin.status()).toBe(409);
  expect(await directoryV2ResponseJSON(stalePin)).toEqual({
    error: "directory_observation_unavailable",
  });
  await expect(page.getByRole("alert")).toContainText(
    "原目录观察已不可用。请明确刷新目录观察，从第一页重新读取。",
  );
  await expect(table).toHaveCount(0);
  const currentSource = await observationAction(page, () =>
    page.getByRole("button", { name: "刷新目录观察", exact: true }).click(),
  );
  expect(currentSource.query.has("observationId")).toBe(false);
  expect(currentSource.query.get("pageIdx")).toBe("1");
  expect(currentSource.value).toEqual({
    dictionaryVersion: 2,
    available: false,
    list: [],
    page: { pageIdx: 1, pageSize: 25, total: 0, totalPage: 0 },
  });
  await expect(
    page.getByRole("status").filter({
      hasText: "尚无当前配置的成功目录观察。此状态不表示目录为空。",
    }),
  ).toBeVisible();
  expect(syncPosts).toBe(2);
  const storage = await page.evaluate(() => ({
    local: { ...localStorage },
    session: { ...sessionStorage },
    history: history.state,
    url: location.href,
  }));
  safe(storage);
  expect(JSON.stringify(storage)).not.toMatch(
    /objectGUID|samAccountName|objectSid|whenCreated|Mixed@example|Line 1|user-01|computer-06/u,
  );
  safe(consoles);
  expect(
    consoles.filter((message) => /Uncaught|unhandled/i.test(message)),
  ).toEqual([]);

  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await expect(table).toHaveCount(0);
  expect(
    (
      await context.request.get(
        `/api/directory/v2/observation?domainId=${domainId}`,
      )
    ).status(),
  ).toBe(401);
});
