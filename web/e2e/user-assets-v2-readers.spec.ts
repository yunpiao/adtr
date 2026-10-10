import {
  test,
  expect,
  type BrowserContext,
  type Page,
  type Locator,
  type APIResponse,
  type Request,
  type Route,
} from "@playwright/test";
import { createHmac } from "node:crypto";
import { isIP } from "node:net";
import {
  directoryV2ResponseJSON,
  observeDirectoryV2Responses,
} from "./directory-v2-response-observer";
import {
  requireSameNativeWindow,
  useNativeTabLifecycle,
} from "./native-tab-lifecycle";
import type { DirectoryV2Input } from "../src/directory-v2-api";
import type { Permission } from "../src/access-api";
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
  await page.getByRole("button", { name: "多因素认证", exact: true }).click();
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
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
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
  await page.getByRole("button", { name: "用户资产", exact: true }).click();
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
// injected fetch transport can ignore AbortSignal; application generations and
// actor binding must still reject late deliveries. Network outcome is recorded.
async function bounded<T>(
  promise: Promise<T>,
  milliseconds: number,
  message: string,
): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error(message)), milliseconds);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}
async function holdAssetResponse(page: Page, path: string, actorId: number) {
  let ready!: () => void, release!: () => void, finish!: () => void;
  let reject!: (error: unknown) => void;
  const fetched = new Promise<void>((resolve, fail) => {
    ready = resolve;
    reject = fail;
  });
  void fetched.catch(() => {});
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const completed = new Promise<void>((resolve) => {
    finish = resolve;
  });
  let held: Request | undefined;
  let response: APIResponse | undefined;
  let error: unknown;
  let outcome = "pending";
  let releaseResult: Promise<string> | undefined;
  const ended = (candidate: Request) => {
    if (candidate === held) outcome = "delivered";
  };
  const failed = (candidate: Request) => {
    if (candidate === held)
      outcome = /aborted|cancelled|canceled/iu.test(
        candidate.failure()?.errorText ?? "",
      )
        ? "browser_cancelled"
        : "failed";
  };
  page.on("requestfinished", ended);
  page.on("requestfailed", failed);
  const predicate = (url: URL) => url.pathname === path;
  const handler = async (route: Route) => {
    try {
      held = route.request();
      response = await route.fetch({
        maxRedirects: 0,
        maxRetries: 0,
        timeout: 15_000,
      });
      expect(response.status()).toBe(200);
      expect(response.headers()["cache-control"]).toBe("no-store");
      expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId));
      const body = await response.json();
      expect(body.dictionaryVersion).toBe(2);
      expect(body.observationId).toBeTruthy();
      if (path.endsWith("/detail"))
        expect(body.object).toEqual(expectedUser(1));
      ready();
      await bounded(
        gate,
        120_000,
        "Held genuine user response was not released",
      );
      await route.fulfill({ response });
    } catch (cause) {
      error = cause;
      reject(cause);
      await route.abort().catch(() => {});
    } finally {
      try {
        await response?.dispose();
      } catch (cause) {
        error ??= cause;
      }
      finish();
    }
  };
  await page.route(predicate, handler, { times: 1 });
  return {
    fetched: () =>
      bounded(fetched, 20_000, "No genuine user asset response captured"),
    release() {
      if (!releaseResult)
        releaseResult = (async () => {
          release();
          try {
            if (!held) return "not_requested";
            await bounded(
              completed,
              20_000,
              "Held user asset route failed to finish",
            );
            if (error) throw error;
            await expect
              .poll(() => outcome, { timeout: 15_000 })
              .not.toBe("pending");
            return outcome;
          } finally {
            page.off("requestfinished", ended);
            page.off("requestfailed", failed);
            await page.unroute(predicate, handler);
          }
        })();
      return releaseResult;
    },
  };
}
async function ignoreAssetAbort(page: Page) {
  await page.addInitScript(() => {
    const original = window.fetch;
    window.fetch = function (input, init) {
      const url = new URL(
        input instanceof Request ? input.url : String(input),
        location.href,
      );
      if (
        url.origin === location.origin &&
        ["/api/user-assets/v2", "/api/user-assets/v2/detail"].includes(
          url.pathname,
        )
      )
        return original.call(this, input, { ...init, signal: undefined });
      return original.call(this, input, init);
    };
  });
}
async function fallbackTab(context: BrowserContext) {
  const page = await context.newPage();
  await page.addInitScript(() => {
    // Explicit transport-availability injection only. Neither authentication
    // responses nor document visibility/focus events are fabricated.
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
  await ignoreAssetAbort(page);
  await useNativeTabLifecycle(page);
  await page.goto("/");
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
        // Preserve event order and native state in failure output. This
        // witness contains no identity, credentials, request or DOM content.
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

process.env.PLAYWRIGHT_NO_COPY_PROMPT = "1";
test.use({ trace: "off", screenshot: "off", video: "off" });
test.beforeAll(() => requireFixture());
test.beforeEach(async ({ context }) => {
  await observeDirectoryV2Responses(context);
});
test("UserAssetsV2 real scoped reader revocation and native cross-tab held-response isolation", async ({
  page,
  context,
}, testInfo) => {
  test.setTimeout(900_000);
  let username = process.env.ADTR_E2E_USERNAME!;
  const initialPassword = process.env.ADTR_E2E_PASSWORD!;
  let password = "Changed Directory Reader Admin 123";
  const accountUser = process.env.ADTR_E2E_LDAP_USERNAME!;
  const accountPassword = process.env.ADTR_E2E_LDAP_PASSWORD!;
  const ldapIP = process.env.ADTR_E2E_LDAP_IP!;
  const domain = "synthetic.invalid";
  const groupName = "E2EDirectoryReadersScope";
  const accountLabel = "Synthetic private directory account";
  const readerUsername = "e2e.directory.reader";
  const readerInitial = "Initial Directory Data Reader 123";
  const readerPassword = "Changed Directory Data Reader 456";
  const roleName = "E2E Directory Data Reader";
  const producerUsername = "e2e.directory.producer";
  const producerInitial = "Initial Directory Producer 123";
  const producerPassword = "Changed Directory Producer 456";
  let roleID = "";
  let readerID = 0;
  const secrets = [
    initialPassword,
    password,
    accountUser,
    accountPassword,
    readerInitial,
    readerPassword,
    producerInitial,
    producerPassword,
  ];
  const authenticators: Authenticator[] = [];
  const safe = (value: unknown) => {
    const serialized = JSON.stringify(value);
    for (const secret of [
      ...secrets,
      ...authenticators.flatMap((auth) => [auth.secret, ...auth.codes]),
    ])
      expect(
        serialized.includes(secret),
        "Synthetic secret must stay write-only",
      ).toBe(false);
    expect(
      /"(?:actorPassword|totpCode|ciphertext|userInfo)"\s*:/u.test(serialized),
      "No secret-bearing public fields",
    ).toBe(false);
  };
  const consoles: string[] = [];
  const collect = (observed: Page) => {
    observed.on("console", (message) => consoles.push(message.text()));
    observed.on("pageerror", (error) => consoles.push(error.message));
  };
  collect(page);
  await ignoreAssetAbort(page);
  await useNativeTabLifecycle(page);
  await page.goto("/");
  await login(page, username, initialPassword);
  await changeInitialPassword(page, initialPassword, password);
  let auth = await enrollMfa(page, password);
  authenticators.push(auth);
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
  await loginMfa(page, username, password, auth);
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  let actorId = (await readJSON(context, "/api/auth/me")).ID as number;

  await test.step("create exactly two reader permissions and assign the producer's existing domain scope", async () => {
    await page.getByRole("button", { name: "访问管理", exact: true }).click();
    await page.getByRole("button", { name: "角色管理", exact: true }).click();
    await page.getByRole("button", { name: "新增角色", exact: true }).click();
    await page.getByLabel("角色名称", { exact: true }).fill(roleName);
    await page
      .getByLabel("角色备注", { exact: true })
      .fill("Synthetic two-permission directory reader");
    await page
      .getByRole("checkbox", { name: "域连接：读取", exact: true })
      .check();
    await page
      .getByRole("checkbox", { name: "目录资产：读取", exact: true })
      .check();
    await proof(page, password, auth);
    const role = await submit(page, "/api/access/roles/save", "保存角色");
    expect(role.value).toMatchObject({
      result: "SUCCESS",
      sessionRevoked: false,
    });
    roleID = role.value.roleID;
    expect(roleID).toMatch(/^[A-Za-z0-9_-]{24}$/u);
    const saved = await readJSON(
      context,
      `/api/access/permissions?roleID=${roleID}`,
    );
    const permissions = saved.permissions as Permission[];
    expect(
      permissions
        .filter((permission) => permission.auth.readable)
        .map((permission) => permission.mark)
        .sort(),
    ).toEqual(["directory_assets", "domains"]);
    expect(permissions.every((permission) => !permission.auth.writeable)).toBe(
      true,
    );
    for (const mark of ["tasks", "operation_accounts"])
      expect(
        permissions.find((permission) => permission.mark === mark)?.auth,
      ).toEqual({ readable: false, writeable: false });

    await page.getByRole("button", { name: "资源与租户", exact: true }).click();
    await page.getByRole("button", { name: "资源组", exact: true }).click();
    await page
      .getByRole("button", { name: `查看 ${groupName}`, exact: true })
      .click();
    await page
      .getByRole("button", { name: "调整关联角色", exact: true })
      .click();
    await expect(
      page.getByLabel("显式关联 platform_admin", { exact: true }),
    ).toBeChecked();
    await page.getByLabel("自定义角色 ID", { exact: true }).fill(roleID);
    await proof(page, password, auth);
    const assigned = await submit(
      page,
      "/api/resources/groups/assign",
      "保存角色关联",
    );
    expect(assigned.value).toMatchObject({
      result: "SUCCESS",
      sessionRevoked: true,
    });
    await loginMfa(page, username, password, auth);

    await page.getByRole("button", { name: "访问管理", exact: true }).click();
    await page.getByRole("button", { name: "用户管理", exact: true }).click();
    await page.getByRole("button", { name: "新增用户", exact: true }).click();
    await page.getByLabel("用户名", { exact: true }).fill(readerUsername);
    await fillSecret(
      page.getByLabel("初始密码", { exact: true }),
      readerInitial,
    );
    await fillSecret(
      page.getByLabel("确认初始密码", { exact: true }),
      readerInitial,
    );
    await page.getByLabel("角色", { exact: true }).fill(roleID);
    await proof(page, password, auth);
    const user = await submit(page, "/api/access/users/create", "创建用户");
    expect(user.value).toMatchObject({
      result: "SUCCESS",
      sessionRevoked: false,
    });
    readerID = user.value.ID;
    expect(readerID).toBeGreaterThan(0);
    expect(readerID).not.toBe(actorId);
  });

  // The real sensitive-action bucket is ten per actor per fifteen minutes.
  // Bootstrap uses exactly ten: password + MFA start/enable (3), domain/group/
  // first assignment (3), reader role/assignment/user (3), producer user (1).
  // A separately created producer uses ten: password + MFA start/enable (3),
  // operation account + connection grant + binding + v2 grant + sync (5),
  // and v2 grant revocation + reader scope withdrawal (2).
  // Login/logout never resets those limits; no SQL or clock bypass is used.
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
  expect(producer.value).toMatchObject({
    result: "SUCCESS",
    sessionRevoked: false,
  });
  expect(producer.value.ID).toBeGreaterThan(0);
  expect(producer.value.ID).not.toBe(actorId);
  expect(producer.value.ID).not.toBe(readerID);
  await logout(page);
  await login(page, producerUsername, producerInitial);
  await changeInitialPassword(page, producerInitial, producerPassword);
  auth = await enrollMfa(page, producerPassword);
  authenticators.push(auth);
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
  const effectivePath = `/api/directory-credential-use/v2/effective?accountId=${accountId}`;
  expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
    purpose: "domain.directory_read.v2",
    explicitlyGranted: false,
    eligible: false,
    grantRevision: "0",
    consumerEnabled: true,
  });

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
  const observed = await openAssets(page);
  const observation = observed.value;
  const selection = {
    domainId,
    revision: beforeConnection.revision,
    credentialRevision: beforeConnection.credentialRevision,
  };
  const common = {
    domainId,
    expectedRevision: selection.revision,
    expectedCredentialRevision: selection.credentialRevision,
    observationId: taskUUID,
  };
  expect(observation).toMatchObject({
    dictionaryVersion: 2,
    selection,
    available: true,
    observationId: taskUUID,
    list: Array.from({ length: 10 }, (_, i) => expectedUser(i + 1)),
    page: { pageIdx: 1, pageSize: 10, total: 12, totalPage: 2 },
    source: { pages: 2 },
  });
  // A separate context is used only to enroll the real reader and test its
  // authority. The account-switch scenario below uses shared cookies in the
  // original context, never injected storageState or synthetic session data.
  const readerContext = await context
    .browser()!
    .newContext({ baseURL: process.env.ADTR_E2E_BASE_URL });
  await observeDirectoryV2Responses(readerContext);
  const readerPage = await readerContext.newPage();
  collect(readerPage);
  let readerAuth!: Authenticator;
  try {
    await readerPage.goto("/");
    await login(readerPage, readerUsername, readerInitial);
    await changeInitialPassword(readerPage, readerInitial, readerPassword);
    readerAuth = await enrollMfa(readerPage, readerPassword);
    authenticators.push(readerAuth);
    const readerSession = (await readJSON(
      readerContext,
      "/api/auth/me",
    )) as Session;
    secrets.push(readerSession.csrfToken);
    expect(identity(readerSession)).toEqual({
      ID: readerID,
      username: readerUsername,
      hasMfa: true,
      needChangePwd: false,
    });
    const menu = await readJSON(readerContext, "/api/access/menu");
    expect(
      menu.menu
        .map((permission: Permission) => ({
          mark: permission.mark,
          auth: permission.auth,
        }))
        .sort((a: Permission, b: Permission) => a.mark.localeCompare(b.mark)),
    ).toEqual([
      { mark: "directory_assets", auth: { readable: true, writeable: false } },
      { mark: "domains", auth: { readable: true, writeable: false } },
    ]);
    const visible = await openAssets(readerPage);
    safe(visible.value);
    expect(visible.actorId).toBe(String(readerID));
    expect(visible.query.get("domainId")).toBe(domainId);
    expect(visible.value).toEqual(observation);
    await readerView(readerPage, readerUsername, accountLabel);
    const detail = await assetAction(
      readerPage,
      () =>
        readerPage
          .getByRole("button", { name: "查看用户 user-01", exact: true })
          .click(),
      true,
    );
    expect(detail.value).toEqual({
      dictionaryVersion: 2,
      selection,
      observationId: taskUUID,
      source: observation.source,
      object: expectedUser(1),
    });
    await readerPage
      .getByRole("button", { name: "关闭用户详情", exact: true })
      .click();
    const effective = await readJSON(readerContext, effectivePath, readerID);
    safe(effective);
    expect(effective).toMatchObject({
      accountId,
      domainId,
      purpose: "domain.directory_read.v2",
      explicitlyGranted: false,
      eligible: false,
      grantRevision: "0",
      consumerEnabled: true,
    });
    // Self-effective eligibility is deliberately allowed. Administrative grant
    // metadata, task ownership/receipts and mutation authority are not.
    for (const path of [
      `/api/directory/v2/task?taskUUID=${taskUUID}`,
      `/api/directory/v2/receipt?${new URLSearchParams({ domainId, idempotencyKey: intent.idempotencyKey })}`,
      `/api/tasks/detail?taskUUID=${taskUUID}`,
      `/api/tasks?domainId=${domainId}`,
      `/api/operation-accounts?filterDomain=${domain}`,
      `/api/operation-accounts/detail?accountId=${accountId}`,
      "/api/directory-credential-use/v2/accounts?pageIdx=1&pageSize=20",
      `/api/directory-credential-use/v2/grants?accountId=${accountId}`,
      `/api/directory-credential-use/v2/roles?accountId=${accountId}`,
      `/api/directory-credential-use/v2/mutation?idempotencyKey=${directoryGrant.sent.idempotencyKey}`,
    ])
      await forbidden(await readerContext.request.get(path));
    const account = (
      await readJSON(
        context,
        `/api/operation-accounts/detail?accountId=${accountId}`,
        actorId,
      )
    ).account;
    const headers = {
      Origin: new URL(readerPage.url()).origin,
      "X-CSRF-Token": readerSession.csrfToken,
    };
    for (const [path, data] of [
      [
        "/api/directory/v2/sync",
        {
          domainId,
          expectedRevision: beforeConnection.revision,
          expectedCredentialGeneration: beforeConnection.credentialRevision,
          idempotencyKey: "reader-forbidden-directory-sync",
        },
      ],
      ["/api/directory/v2/cancel", { taskUUID }],
      ...["grant", "revoke"].map(
        (operation) =>
          [
            `/api/directory-credential-use/v2/${operation}`,
            {
              accountId,
              // Grant targets the ungranted reader role; revoke targets the
              // actual producer grant. Revoke revision zero is invalid input
              // and would never reach the role-authority boundary under test.
              roleId: operation === "grant" ? roleID : "platform_admin",
              purpose: "domain.directory_read.v2",
              expectedAccountRevision: account.revision,
              expectedCredentialRevision: account.credentialRevision,
              expectedGrantRevision:
                operation === "grant"
                  ? "0"
                  : directoryGrant.value.grantRevision,
              idempotencyKey: `reader-forbidden-directory-${operation}`,
            },
          ] as const,
      ),
    ] as const) {
      // Genuine enrolled reader, current password, current unused TOTP, current
      // CSRF and correct domain/task pins isolate denial to role authority.
      const code = await freshCode(readerAuth);
      const response = await readerContext.request.post(path, {
        headers,
        data: { ...data, actorPassword: readerPassword, totpCode: code },
      });
      await forbidden(response);
    }
    expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
      explicitlyGranted: true,
      eligible: true,
      grantRevision: directoryGrant.value.grantRevision,
    });
    const reread = await readJSON(
      readerContext,
      `/api/user-assets/v2?${new URLSearchParams(common)}`,
      readerID,
    );
    safe(reread);
    expect(reread).toEqual(observation);
    expect(
      await readJSON(readerContext, effectivePath, readerID),
    ).toMatchObject({
      explicitlyGranted: false,
      eligible: false,
      grantRevision: "0",
    });
  } finally {
    await readerContext.close();
  }

  await test.step("native cross-tab account switch rejects held real user detail including notification fallback", async () => {
    const fallback = await fallbackTab(context);
    const switching = await context.newPage();
    await useNativeTabLifecycle(switching);
    await requireSameNativeWindow([page, fallback.page, switching]);
    const oldTabs = [page, fallback.page];
    const held: Awaited<ReturnType<typeof holdAssetResponse>>[] = [];
    const outcomes: string[] = [];
    const mutations: string[] = [];
    let monitor = false;
    const watchers = oldTabs.map((old) => {
      const listener = (request: Request) => {
        const path = new URL(request.url()).pathname;
        if (
          monitor &&
          request.method() !== "GET" &&
          request.method() !== "HEAD" &&
          path !== "/api/access/check"
        )
          mutations.push(`${request.method()} ${path}`);
      };
      old.on("request", listener);
      return { old, listener };
    });
    try {
      await switching.goto("/");
      await signedIn(switching, producerUsername);
      await openAssets(fallback.page);
      for (const old of oldTabs) {
        await old.bringToFront();
        const pending = await holdAssetResponse(
          old,
          "/api/user-assets/v2/detail",
          actorId,
        );
        held.push(pending);
        await old
          .getByRole("button", { name: "查看用户 user-01", exact: true })
          .click();
        await pending.fetched();
      }
      monitor = true;
      await switching.bringToFront();
      await expect
        .poll(() => fallback.lifecycle.evaluate((value) => value.read()))
        .toMatchObject({ visibility: "hidden", focused: false });
      await logout(switching);
      await expect(
        page.getByRole("heading", { name: "登录账户", exact: true }),
      ).toBeVisible();
      await expect(
        page.getByRole("table", { name: "用户资产列表", exact: true }),
      ).toHaveCount(0);
      await expect(
        page.getByRole("region", { name: "用户详情", exact: true }),
      ).toHaveCount(0);
      await loginMfa(switching, readerUsername, readerPassword, readerAuth);
      await signedIn(page, readerUsername);
      expect((await readJSON(context, "/api/auth/me")).ID).toBe(readerID);
      // This hidden tab really lacks both notifications. It keeps its old
      // actor until native visibility/focus provokes a current /auth/me read.
      await expect(fallback.page.locator(".signed-in strong")).toHaveText(
        producerUsername,
      );
      await fallback.lifecycle.evaluate((value) => value.reset());
      const authenticated = fallback.page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === "/api/auth/me" &&
          response.status() === 200,
      );
      await fallback.page.bringToFront();
      expect(identity(await (await authenticated).json())).toEqual({
        ID: readerID,
        username: readerUsername,
        hasMfa: true,
        needChangePwd: false,
      });
      await expect
        .poll(() => fallback.lifecycle.evaluate((value) => value.read()))
        .toEqual({
          visibility: "visible",
          focused: true,
          events: expect.arrayContaining([
            expect.objectContaining({ type: "focus", trusted: true }),
            { type: "visibilitychange", visible: true, trusted: true },
          ]),
        });
      await signedIn(fallback.page, readerUsername);
      for (const old of oldTabs) {
        await expect(
          old.getByRole("table", { name: "用户资产列表", exact: true }),
        ).toHaveCount(0);
        await expect(
          old.getByRole("region", { name: "用户详情", exact: true }),
        ).toHaveCount(0);
      }
      // Abort is ignored by the injected transport: these must actually finish
      // delivery after revalidation, not count cancellation as stale-body proof.
      for (const pending of held) outcomes.push(await pending.release());
      expect(outcomes).toEqual(["delivered", "delivered"]);
      for (const old of oldTabs) {
        await expect(
          old.getByRole("table", { name: "用户资产列表", exact: true }),
        ).toHaveCount(0);
        await expect(
          old.getByRole("region", { name: "用户详情", exact: true }),
        ).toHaveCount(0);
        await expect(old.getByText(guid(1), { exact: false })).toHaveCount(0);
        const reopened = await openAssets(old);
        expect(reopened.actorId).toBe(String(readerID));
        expect(reopened.value).toEqual(observation);
        await readerView(old, readerUsername, accountLabel);
      }
      expect(mutations).toEqual([]);
      await page.screenshot({
        path: testInfo.outputPath("user-assets-v2-reader-native-tab.png"),
        fullPage: true,
      });
      await testInfo.attach("user-assets-v2-native-transport-outcomes", {
        body: JSON.stringify({
          outcomes,
          nativeLifecycle: await fallback.lifecycle.evaluate((value) =>
            value.read(),
          ),
        }),
        contentType: "application/json",
      });
    } finally {
      monitor = false;
      for (const pending of held) await pending.release();
      for (const { old, listener } of watchers) old.off("request", listener);
      await fallback.lifecycle.evaluate((value) => value.stop());
      await fallback.page.close();
      await switching.close();
    }
  });

  await test.step("collection grant revocation preserves stored reads; domain-scope revocation clears them", async () => {
    const administrator = await context
      .browser()!
      .newContext({ baseURL: process.env.ADTR_E2E_BASE_URL });
    await observeDirectoryV2Responses(administrator);
    const adminPage = await administrator.newPage();
    let held: Awaited<ReturnType<typeof holdAssetResponse>> | undefined;
    try {
      await adminPage.goto("/");
      await loginMfa(adminPage, producerUsername, producerPassword, auth);
      await adminPage
        .getByRole("button", { name: "补充目录凭据授权", exact: true })
        .click();
      await adminPage
        .getByRole("button", { name: `清理 ${accountLabel}`, exact: true })
        .click();
      await adminPage
        .getByRole("button", { name: "撤销 platform_admin", exact: true })
        .click();
      await adminPage
        .getByRole("checkbox", {
          name: "我已核对目标角色，确认撤销覆盖当前及未来全部成员",
          exact: true,
        })
        .check();
      await proof(adminPage, producerPassword, auth);
      const revoked = await submit(
        adminPage,
        "/api/directory-credential-use/v2/revoke",
        "确认撤销角色使用权",
      );
      expect(revoked.value).toMatchObject({
        purpose: "domain.directory_read.v2",
        allowed: false,
        replayed: false,
      });
      expect(
        await readJSON(administrator, effectivePath, actorId),
      ).toMatchObject({ explicitlyGranted: false, eligible: false });
      expect(
        await readJSON(
          context,
          `/api/user-assets/v2?${new URLSearchParams(common)}`,
          readerID,
        ),
      ).toEqual(observation);
      const detail = await assetAction(
        page,
        () =>
          page
            .getByRole("button", { name: "查看用户 user-01", exact: true })
            .click(),
        true,
      );
      expect(detail.value.object).toEqual(expectedUser(1));
      await page
        .getByRole("button", { name: "关闭用户详情", exact: true })
        .click();
      held = await holdAssetResponse(
        page,
        "/api/user-assets/v2/detail",
        readerID,
      );
      await page
        .getByRole("button", { name: "查看用户 user-01", exact: true })
        .click();
      await held.fetched();
      await adminPage
        .getByRole("button", { name: "资源与租户", exact: true })
        .click();
      await adminPage
        .getByRole("button", { name: "资源组", exact: true })
        .click();
      await adminPage
        .getByRole("button", { name: `查看 ${groupName}`, exact: true })
        .click();
      await adminPage
        .getByRole("button", { name: "调整关联角色", exact: true })
        .click();
      await expect(
        adminPage.getByLabel("自定义角色 ID", { exact: true }),
      ).toHaveValue(roleID);
      await adminPage.getByLabel("自定义角色 ID", { exact: true }).fill("");
      await proof(adminPage, producerPassword, auth);
      const withdrawn = await submit(
        adminPage,
        "/api/resources/groups/assign",
        "保存角色关联",
      );
      expect(withdrawn.value).toMatchObject({
        result: "SUCCESS",
        sessionRevoked: true,
      });
      const expired = await context.request.get(
        `/api/user-assets/v2?${new URLSearchParams(common)}`,
      );
      expect(expired.status()).toBe(401);
      // A real rejected list read clears the old private view before held data
      // arrives; the late detail transport is intentionally still alive.
      await assetAction(
        page,
        () =>
          page
            .getByRole("button", { name: "刷新用户资产观测", exact: true })
            .click(),
        false,
        401,
      );
      await expect(
        page.getByRole("heading", { name: "登录账户", exact: true }),
      ).toBeVisible();
      expect(await held.release()).toBe("delivered");
      await expect(
        page.getByRole("table", { name: "用户资产列表", exact: true }),
      ).toHaveCount(0);
      await expect(
        page.getByRole("region", { name: "用户详情", exact: true }),
      ).toHaveCount(0);
      await loginMfa(page, readerUsername, readerPassword, readerAuth);
      for (const path of [
        `/api/user-assets/v2?${new URLSearchParams(common)}`,
        `/api/user-assets/v2/detail?${new URLSearchParams({ ...common, objectGUID: guid(1) })}`,
      ]) {
        const denied = await context.request.get(path);
        expect(denied.status()).toBe(404);
        expect(await denied.json()).toEqual({ error: "not_found" });
      }
      await page.getByRole("button", { name: "用户资产", exact: true }).click();
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
      await expect(page.getByText(taskUUID, { exact: false })).toHaveCount(0);
      await page.screenshot({
        path: testInfo.outputPath("user-assets-v2-reader-revoked.png"),
        fullPage: true,
      });
    } finally {
      await held?.release();
      await administrator.close();
    }
  });
  safe(consoles);
  expect(
    consoles.filter((message) => /Uncaught|unhandled/i.test(message)),
  ).toEqual([]);
});

async function readerView(page: Page, username: string, accountLabel: string) {
  await signedIn(page, username);
  await expect(
    page
      .getByRole("table", { name: "用户资产列表", exact: true })
      .locator("tbody tr"),
  ).toHaveCount(10);
  for (const name of [
    "提交目录同步",
    "请求取消目录同步",
    "补充目录凭据授权",
    "确认授予角色使用权",
    "确认撤销角色使用权",
    `清理 ${accountLabel}`,
  ])
    await expect(page.getByRole("button", { name, exact: true })).toHaveCount(
      0,
    );
  await expect(
    page.locator('input[name="actorPassword"], input[name="totpCode"]'),
  ).toHaveCount(0);
  await expect(page.getByText(accountLabel, { exact: false })).toHaveCount(0);
}
