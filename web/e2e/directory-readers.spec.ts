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
import type { Task } from "../src/task-api";
import type {
  DirectoryInput,
  DirectoryObservation,
} from "../src/directory-api";
import type { Permission } from "../src/access-api";

type Authenticator = { secret: string; lastCounter: number; codes: string[] };
type Session = {
  ID: number;
  username: string;
  csrfToken: string;
  hasMfa: boolean;
  needChangePwd: boolean;
};

// Run only on the directory-readers suite’s fresh owned database, real worker
// and TLS LDAPFixture(directory_enabled=True, directory_empty=True,
// directory_slow=False): one actual empty wire page, without page delay.
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
    process.env.ADTR_E2E_LDAP_DIRECTORY_EMPTY !== "true" ||
    process.env.ADTR_E2E_LDAP_DIRECTORY_SLOW !== "false"
  )
    throw new Error(
      "Directory reader acceptance requires scripts/test_auth_e2e.py --suite directory-readers: an isolated fresh database, real worker, one-page empty/fast synthetic TLS LDAP fixture, ADTR_E2E_LDAP_DIRECTORY_MODE=true, ADTR_E2E_LDAP_DIRECTORY_EMPTY=true, ADTR_E2E_LDAP_DIRECTORY_SLOW=false, ADTR_E2E_DB_CONTAINER and all ADTR_E2E auth/LDAP variables. No fixture means acceptance is blocked, not skipped.",
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
    value: await response.json(),
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
        new URL(candidate.url()).pathname === "/api/directory/observation" &&
        candidate.request().method() === "GET",
    ),
    action(),
  ]);
  expect(response.status(), "UI observation read must reach the real API").toBe(
    200,
  );
  expect(response.headers()["cache-control"]).toBe("no-store");
  return {
    value: await response.json(),
    query: new URL(response.url()).searchParams,
    actorId: response.headers()["x-adtr-user-id"],
  };
}

async function openDirectory(page: Page) {
  await page.getByRole("button", { name: "目录资产", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "目录资产", exact: true }),
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

async function readerView(
  page: Page,
  readerUsername: string,
  accountLabel: string,
  observationId: string,
) {
  await signedIn(page, readerUsername);
  const region = page.getByRole("region", { name: "目录观察", exact: true });
  await expect(region).toContainText(`观察编号：${observationId}`);
  await expect(region.getByRole("status")).toHaveText(
    "已成功观察，当前筛选结果为空。",
  );
  await expect(
    page
      .getByRole("status")
      .filter({ hasText: "当前为目录只读访问，不能提交同步。" }),
  ).toBeVisible();
  // observationId is authorized data, even though it equals the producer task
  // UUID. Check the private task UI and controls rather than hiding that ID.
  for (const name of [
    "目录同步任务",
    "目录同步恢复",
    "核对目录同步回执",
    "目录读取凭据使用授权",
  ])
    await expect(page.getByRole("region", { name, exact: true })).toHaveCount(
      0,
    );
  for (const name of [
    "提交目录同步",
    "使用原编号和版本重试同步",
    "刷新目录任务",
    "请求取消目录同步",
    "查询原同步回执",
    "目录读取授权",
    "确认授予角色使用权",
    "确认撤销角色使用权",
    `清理 ${accountLabel}`,
  ])
    await expect(page.getByRole("button", { name, exact: true })).toHaveCount(
      0,
    );
  await expect(
    page.getByRole("table", {
      name: "具有已保存目录读取授权的账户",
      exact: true,
    }),
  ).toHaveCount(0);
  await expect(page.getByText(accountLabel, { exact: false })).toHaveCount(0);
  await expect(
    page.locator('input[name="actorPassword"], input[name="totpCode"]'),
  ).toHaveCount(0);
}

async function bounded<T>(
  promise: Promise<T>,
  timeout: number,
  message: string,
): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error(message)), timeout);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

// Delayed-real-response transport injection. Fetch from the authenticated API
// once, hold its genuine response, and release the same bytes after the switch.
// Chromium may abort delivery when the old component unmounts. That is a safe
// cancellation, not evidence that a late payload ignored AbortSignal.
async function delayGrantAccounts(
  page: Page,
  actorId: number,
  accountId: string,
  accountLabel: string,
  safe: (value: unknown) => void,
) {
  const path = (url: URL) =>
    url.pathname === "/api/directory-credential-use/accounts";
  let unblock!: () => void;
  const gate = new Promise<void>((resolve) => {
    unblock = resolve;
  });
  let ready!: () => void;
  let fail!: (error: unknown) => void;
  const fetched = new Promise<void>((resolve, reject) => {
    ready = resolve;
    fail = reject;
  });
  void fetched.catch(() => {});
  let finish!: () => void;
  const finished = new Promise<void>((resolve) => {
    finish = resolve;
  });
  let ended!: () => void;
  const terminal = new Promise<void>((resolve) => {
    ended = resolve;
  });
  let claimed = false;
  let held: Request | undefined;
  const transport: {
    outcome: "pending" | "delivered" | "browser_cancelled" | "failed";
  } = { outcome: "pending" };
  let failure: unknown;
  let failed = false;
  let releaseAttempted = false;
  let response: APIResponse | undefined;
  const succeeded = (candidate: Request) => {
    if (candidate === held) {
      transport.outcome = "delivered";
      ended();
    }
  };
  const rejected = (candidate: Request) => {
    if (candidate === held) {
      transport.outcome = /aborted|cancelled|canceled/i.test(
        candidate.failure()?.errorText ?? "",
      )
        ? "browser_cancelled"
        : "failed";
      ended();
    }
  };
  page.on("requestfinished", succeeded);
  page.on("requestfailed", rejected);
  const handler = async (route: Route) => {
    try {
      claimed = true;
      held = route.request();
      expect(held.method()).toBe("GET");
      response = await route.fetch({
        maxRedirects: 0,
        maxRetries: 0,
        timeout: 15_000,
      });
      expect(response.status()).toBe(200);
      expect(response.headers()["cache-control"]).toBe("no-store");
      expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId));
      const value = await response.json();
      safe(value);
      expect(value.List).toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            accountId,
            label: accountLabel,
            domain: "synthetic.invalid",
          }),
        ]),
      );
      ready();
      await bounded(
        gate,
        120_000,
        "Held directory response was not released in time",
      );
      releaseAttempted = true;
      try {
        await route.fulfill({ response });
      } catch (error) {
        // Only the real browser's recorded cancellation permits this outcome.
        if (
          !/aborted|cancelled|canceled/i.test(held.failure()?.errorText ?? "")
        )
          throw error;
      }
    } catch (error) {
      failed = true;
      failure = error;
      fail(error);
      await route.abort().catch(() => {});
    } finally {
      try {
        await response?.dispose();
      } catch (error) {
        failed = true;
        failure ??= error;
      }
      finish();
    }
  };
  await page.route(path, handler, { times: 1 });
  return {
    fetched: () =>
      bounded(
        fetched,
        20_000,
        "No genuine admin directory account response was captured",
      ),
    async release() {
      unblock();
      await bounded(
        finished,
        20_000,
        "Delayed directory handler did not finish",
      );
      if (failed) throw failure;
      await bounded(
        terminal,
        10_000,
        "The held browser request had no terminal event",
      );
      if (
        transport.outcome !== "delivered" &&
        transport.outcome !== "browser_cancelled"
      )
        throw new Error(
          "Held response ended with an unexpected browser transport failure",
        );
      expect(releaseAttempted).toBe(true);
      await page.evaluate(
        () => new Promise<void>((resolve) => setTimeout(resolve, 0)),
      );
      return { outcome: transport.outcome };
    },
    async cleanup() {
      unblock();
      try {
        await bounded(
          page.unroute(path, handler),
          5_000,
          "Unable to remove delayed response route",
        );
        if (claimed)
          await bounded(
            finished,
            20_000,
            "Delayed response cleanup did not finish",
          );
        if (failed) throw failure;
      } finally {
        page.off("requestfinished", succeeded);
        page.off("requestfailed", rejected);
      }
    },
  };
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
  const cdp = await context.newCDPSession(page);
  // Restore real Chromium tab lifecycle instead of Playwright's default
  // always-focused emulation, as in session-invalidation.spec.ts.
  await cdp.send("Emulation.setFocusEmulationEnabled", { enabled: false });
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
      verified() {
        return ["focus", "visibilitychange"].every((type) =>
          events.some(
            (event) => event.type === type && event.trusted && event.visible,
          ),
        );
      },
      stop() {
        window.removeEventListener("focus", record);
        document.removeEventListener("visibilitychange", record);
      },
    };
  });
  return { page, cdp, lifecycle };
}

async function watchReaderDOM(
  page: Page,
  readerUsername: string,
  accountLabel: string,
) {
  return page.evaluateHandle(
    ({ readerUsername, accountLabel }) => {
      let privateReturned = false;
      let identityReturned = false;
      const scan = () => {
        const current =
          document.querySelector(".signed-in strong")?.textContent;
        identityReturned ||= !!current && current !== readerUsername;
        privateReturned ||=
          (document.body.textContent ?? "").includes(accountLabel) ||
          !!document.querySelector(
            '.directory-credential-use-workspace, section[aria-label="目录同步任务"], section[aria-label="目录同步恢复"], input[name="actorPassword"], input[name="totpCode"]',
          ) ||
          Array.from(document.querySelectorAll("button")).some((button) =>
            [
              "目录读取授权",
              "提交目录同步",
              "请求取消目录同步",
              "确认授予角色使用权",
              "确认撤销角色使用权",
            ].includes(button.textContent?.trim() ?? ""),
          );
      };
      const observer = new MutationObserver(scan);
      observer.observe(document.body, {
        subtree: true,
        childList: true,
        characterData: true,
        attributes: true,
      });
      scan();
      return {
        read() {
          scan();
          return { privateReturned, identityReturned };
        },
        stop() {
          observer.disconnect();
        },
      };
    },
    { readerUsername, accountLabel },
  );
}

// The suite owns its disposable fixture and never provisions a database here.
// Trace, video, screenshots and failure DOM snapshots stay disabled throughout.
process.env.PLAYWRIGHT_NO_COPY_PROMPT = "1";
test.use({ trace: "off", screenshot: "off", video: "off" });
test.beforeAll(() => requireFixture());
test("real empty directory reader and same-context account-switch isolation", async ({
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
    `/api/directory/observation?domainId=${domainId}`,
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
  // A separately created producer uses eight: password + MFA start/enable (3),
  // operation account + connection grant + binding + directory grant + sync (5).
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
  const effectivePath = `/api/directory-credential-use/effective?accountId=${accountId}`;
  expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
    purpose: "domain.directory_read",
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
    .getByRole("button", { name: "查看目录读取凭据使用授权", exact: true })
    .click();
  const directoryGrant = await grant(
    page,
    password,
    auth,
    "/api/directory-credential-use",
  );
  expect(directoryGrant.value).toMatchObject({
    result: "SUCCESS",
    purpose: "domain.directory_read",
    allowed: true,
    replayed: false,
  });
  safe(directoryGrant.value);
  expect(await readJSON(context, effectivePath, actorId)).toMatchObject({
    purpose: "domain.directory_read",
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
    available: false,
    list: [],
    page: { pageIdx: 1, pageSize: 50, total: 0, totalPage: 0 },
  });
  await proof(page, password, auth);
  const submitted = await submit(page, "/api/directory/sync", "提交目录同步");
  safe(submitted.value);
  const intent = submitted.sent as DirectoryInput;
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
          `/api/directory/task?taskUUID=${taskUUID}`,
          actorId,
        );
        safe(value);
        completed = value.task;
        return completed?.state;
      },
      {
        timeout: 60_000,
        message:
          "Real empty LDAP worker must successfully publish its observation",
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
  const observed = await observationAction(page, () =>
    page.getByRole("button", { name: "刷新目录观察", exact: true }).click(),
  );
  safe(observed.value);
  const observation = observed.value as DirectoryObservation;
  expect(observed.actorId).toBe(String(actorId));
  expect(observation).toMatchObject({
    available: true,
    observationId: taskUUID,
    list: [],
    page: { pageIdx: 1, pageSize: 50, total: 0, totalPage: 0 },
    source: {
      server_name: "dc.synthetic.invalid",
      dc_host_name: "dc.synthetic.invalid",
      domain,
      naming_context: "dc=synthetic,dc=invalid",
      pages: 1,
    },
  });
  expect(Date.parse(observation.source!.completed_at)).toBeGreaterThanOrEqual(
    Date.parse(observation.source!.started_at),
  );
  await expect(
    page
      .getByRole("status")
      .filter({ hasText: "已成功观察，当前筛选结果为空。" }),
  ).toBeVisible();

  // A separate context is used only to enroll the real reader and test its
  // authority. The account-switch scenario below uses shared cookies in the
  // original context, never injected storageState or synthetic session data.
  const readerContext = await context
    .browser()!
    .newContext({ baseURL: process.env.ADTR_E2E_BASE_URL });
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
    const visible = await openDirectory(readerPage);
    safe(visible.value);
    expect(visible.actorId).toBe(String(readerID));
    expect(visible.query.get("domainId")).toBe(domainId);
    expect(visible.value).toEqual(observation);
    await readerView(readerPage, readerUsername, accountLabel, taskUUID);
    const effective = await readJSON(readerContext, effectivePath, readerID);
    safe(effective);
    expect(effective).toMatchObject({
      accountId,
      domainId,
      purpose: "domain.directory_read",
      explicitlyGranted: false,
      eligible: false,
      grantRevision: "0",
      consumerEnabled: true,
    });
    // Self-effective eligibility is deliberately allowed. Administrative grant
    // metadata, task ownership/receipts and mutation authority are not.
    for (const path of [
      `/api/directory/task?taskUUID=${taskUUID}`,
      `/api/directory/receipt?${new URLSearchParams({ domainId, idempotencyKey: intent.idempotencyKey })}`,
      `/api/tasks/detail?taskUUID=${taskUUID}`,
      `/api/tasks?domainId=${domainId}`,
      `/api/operation-accounts?filterDomain=${domain}`,
      `/api/operation-accounts/detail?accountId=${accountId}`,
      "/api/directory-credential-use/accounts?pageIdx=1&pageSize=20",
      `/api/directory-credential-use/grants?accountId=${accountId}`,
      `/api/directory-credential-use/roles?accountId=${accountId}`,
      `/api/directory-credential-use/mutation?idempotencyKey=${directoryGrant.sent.idempotencyKey}`,
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
        "/api/directory/sync",
        {
          domainId,
          expectedRevision: beforeConnection.revision,
          expectedCredentialGeneration: beforeConnection.credentialRevision,
          idempotencyKey: "reader-forbidden-directory-sync",
        },
      ],
      ["/api/directory/cancel", { taskUUID }],
      ...["grant", "revoke"].map(
        (operation) =>
          [
            `/api/directory-credential-use/${operation}`,
            {
              accountId,
              roleId: roleID,
              purpose: "domain.directory_read",
              expectedAccountRevision: account.revision,
              expectedCredentialRevision: account.credentialRevision,
              expectedGrantRevision: "0",
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
    const reread = await readJSON(
      readerContext,
      `/api/directory/observation?domainId=${domainId}`,
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

  await test.step("release genuine privileged account metadata only after the shared session is the enrolled reader", async () => {
    secrets.push((await readJSON(context, "/api/auth/me")).csrfToken);
    const fallback = await fallbackTab(context);
    const switching = await context.newPage();
    collect(fallback.page);
    collect(switching);
    const oldTabs = [page, fallback.page];
    const delayed: Awaited<ReturnType<typeof delayGrantAccounts>>[] = [];
    const witnesses: Awaited<ReturnType<typeof watchReaderDOM>>[] = [];
    const handlers: { page: Page; listener: (request: Request) => void }[] = [];
    const browserWrites: string[] = [];
    let monitorWrites = false;
    let scenarioFailure: unknown;
    let scenarioFailed = false;
    const cleanupErrors: unknown[] = [];
    const outcomes: { outcome: "delivered" | "browser_cancelled" }[] = [];
    const notification = await page.evaluateHandle(() => {
      let count = 0;
      let invalid = false;
      const channel = new BroadcastChannel("adtr.session-invalidation.v1");
      const listener = (event: MessageEvent<unknown>) => {
        count++;
        invalid ||=
          typeof event.data !== "string" || !/^[0-9a-f]{32}$/u.test(event.data);
      };
      channel.addEventListener("message", listener);
      return {
        read: () => ({ count, invalid }),
        stop: () => {
          channel.removeEventListener("message", listener);
          channel.close();
        },
      };
    });
    try {
      await switching.goto("/");
      await signedIn(switching, username);
      for (const old of oldTabs) {
        await old.bringToFront();
        await old
          .getByRole("button", { name: "目录读取授权", exact: true })
          .click();
        await expect(
          old.getByRole("table", {
            name: "具有已保存目录读取授权的账户",
            exact: true,
          }),
        ).toContainText(accountLabel);
        await expect(
          old.getByRole("button", {
            name: `清理 ${accountLabel}`,
            exact: true,
          }),
        ).toBeVisible();
        const listener = (request: Request) => {
          // /access/check is read-only permission introspection over POST.
          if (
            monitorWrites &&
            request.method() !== "GET" &&
            request.method() !== "HEAD" &&
            new URL(request.url()).pathname !== "/api/access/check"
          )
            browserWrites.push(
              `${request.method()} ${new URL(request.url()).pathname}`,
            );
        };
        old.on("request", listener);
        handlers.push({ page: old, listener });
        const held = await delayGrantAccounts(
          old,
          actorId,
          accountId,
          accountLabel,
          safe,
        );
        delayed.push(held);
        await old
          .getByRole("button", {
            name: "刷新目录读取授权清理目录",
            exact: true,
          })
          .click();
        await held.fetched();
      }
      monitorWrites = true;
      await switching.bringToFront();
      await expect
        .poll(() => fallback.page.evaluate(() => document.visibilityState))
        .toBe("hidden");
      await logout(switching);
      // The ordinary tab clears through a real same-origin notification while
      // still in the background. Neither click nor reload causes this check.
      await expect(
        page.getByRole("heading", { name: "登录账户", exact: true }),
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: `清理 ${accountLabel}`, exact: true }),
      ).toHaveCount(0);
      await expect(page.getByText(accountLabel, { exact: false })).toHaveCount(
        0,
      );
      await loginMfa(switching, readerUsername, readerPassword, readerAuth);
      const current = (await readJSON(context, "/api/auth/me")) as Session;
      secrets.push(current.csrfToken);
      expect(identity(current)).toEqual({
        ID: readerID,
        username: readerUsername,
        hasMfa: true,
        needChangePwd: false,
      });
      await signedIn(page, readerUsername);
      witnesses.push(await watchReaderDOM(page, readerUsername, accountLabel));

      // No transports on the hidden fallback tab: it retains the old identity
      // until real focus/visibility starts a current authenticated /me read.
      await expect(fallback.page.locator(".signed-in strong")).toHaveText(
        username,
      );
      await fallback.lifecycle.evaluate((value) => value.reset());
      const reread = fallback.page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === "/api/auth/me" &&
          response.request().method() === "GET" &&
          response.status() === 200,
      );
      await fallback.page.bringToFront();
      const read = await reread;
      expect(identity(await read.json())).toEqual({
        ID: readerID,
        username: readerUsername,
        hasMfa: true,
        needChangePwd: false,
      });
      await expect
        .poll(() => fallback.lifecycle.evaluate((value) => value.verified()))
        .toBe(true);
      await signedIn(fallback.page, readerUsername);
      witnesses.push(
        await watchReaderDOM(fallback.page, readerUsername, accountLabel),
      );
      for (const old of oldTabs) {
        await expect(
          old.getByRole("table", {
            name: "具有已保存目录读取授权的账户",
            exact: true,
          }),
        ).toHaveCount(0);
        await expect(old.getByText(accountLabel, { exact: false })).toHaveCount(
          0,
        );
        await expect(
          old.getByRole("button", { name: "目录读取授权", exact: true }),
        ).toHaveCount(0);
      }
      // Release only now: both old views and shared-cookie /me have proved
      // reader identity, and the reader's genuine MFA login has completed.
      for (const held of delayed) outcomes.push(await held.release());
      for (const old of oldTabs) {
        await signedIn(old, readerUsername);
        const visible = await openDirectory(old);
        safe(visible.value);
        expect(visible.actorId).toBe(String(readerID));
        expect(visible.query.get("domainId")).toBe(domainId);
        expect(visible.value).toEqual(observation);
        await readerView(old, readerUsername, accountLabel, taskUUID);
        const stored = await old.evaluate(() => {
          let local: Record<string, string> = {};
          try {
            local = { ...localStorage };
          } catch {
            /* fallback deliberately has no localStorage */
          }
          return { local, session: { ...sessionStorage } };
        });
        safe(stored);
      }
      for (const witness of witnesses)
        expect(await witness.evaluate((value) => value.read())).toEqual({
          privateReturned: false,
          identityReturned: false,
        });
      expect(
        browserWrites,
        "Old tabs never replay private mutations during or after account switch",
      ).toEqual([]);
      const notices = await notification.evaluate((value) => value.read());
      expect(notices.count).toBeGreaterThan(0);
      expect(notices.invalid, "Cross-tab notifications carry nonce only").toBe(
        false,
      );
      await forbidden(
        await context.request.get(
          "/api/directory-credential-use/accounts?pageIdx=1&pageSize=20",
        ),
      );
      await forbidden(
        await context.request.get(`/api/directory/task?taskUUID=${taskUUID}`),
      );
      expect((await readJSON(context, "/api/auth/me")).ID).toBe(readerID);
      safe(consoles);
    } catch (error) {
      scenarioFailed = true;
      scenarioFailure = error;
    } finally {
      // All observers/routes are drained with bounds, including failure paths.
      // Late handler errors are preserved alongside any original assertion.
      const cleanup = async (operation: () => Promise<unknown>) => {
        try {
          await bounded(
            operation(),
            25_000,
            "Directory reader cleanup exceeded its bound",
          );
        } catch (error) {
          cleanupErrors.push(error);
        }
      };
      for (const held of delayed) await cleanup(() => held.cleanup());
      for (const witness of witnesses) {
        await cleanup(() => witness.evaluate((value) => value.stop()));
        await cleanup(() => witness.dispose());
      }
      for (const handler of handlers)
        handler.page.off("request", handler.listener);
      await cleanup(() => notification.evaluate((value) => value.stop()));
      await cleanup(() => notification.dispose());
      await cleanup(() => fallback.lifecycle.evaluate((value) => value.stop()));
      await cleanup(() => fallback.lifecycle.dispose());
      await cleanup(() => fallback.cdp.detach());
      await cleanup(() => fallback.page.close());
      await cleanup(() => switching.close());
    }
    if (scenarioFailed || cleanupErrors.length)
      throw new AggregateError(
        [...(scenarioFailed ? [scenarioFailure] : []), ...cleanupErrors],
        "Directory reader/session assertions or bounded cleanup failed",
      );
    await testInfo.attach("directory-reader-session-scope", {
      contentType: "application/json",
      body: JSON.stringify(
        {
          fixture:
            "one-page empty synthetic TLS LDAP; owned disposable API/worker/PostgreSQL",
          scenarios: [
            "custom role: only domains.read and directory_assets.read",
            "no producer credential grant or private task access",
            "same-context switch with delayed real admin grant-account responses",
            "native notification and trusted focus/visibility fallback",
          ],
          responseOutcomes: outcomes,
          limitations: [
            "Cancelled browser delivery is not ignored-AbortSignal coverage",
            "No real AD or product acceptance",
          ],
        },
        null,
        2,
      ),
    });
  });
});
