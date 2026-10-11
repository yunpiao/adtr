import { navigateTo } from "./navigation";
import {
  test,
  expect,
  type BrowserContext,
  type Locator,
  type Page,
  type APIResponse,
  type Request,
  type Route,
} from "@playwright/test";
import { createHmac, createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

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
  // Each privileged write and MFA login gets a new real counter, including
  // rejected writes. Never bypass replay protection or freeze the server clock.
  const earliest = (authenticator.lastCounter + 1) * 30_000 + 1100;
  while (Date.now() < earliest)
    await new Promise((resolve) => setTimeout(resolve, earliest - Date.now()));
  const now = Date.now();
  authenticator.lastCounter = Math.floor(now / 30_000);
  return totp(authenticator.secret, now);
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

// Hold one genuine authenticated read until a different user owns the shared
// cookie. A browser cancellation is valid evidence of safe cancellation, not
// proof that a payload was delivered. No body, actor header or status is forged.
async function holdSourceRead(
  page: Page,
  path: (url: URL) => boolean,
  actorId: number,
  check: (value: unknown) => void,
) {
  let unblock!: () => void;
  const gate = new Promise<void>((resolve) => {
    unblock = resolve;
  });
  let ready!: () => void, rejectReady!: (error: unknown) => void;
  const fetched = new Promise<void>((resolve, reject) => {
    ready = resolve;
    rejectReady = reject;
  });
  void fetched.catch(() => {});
  let finish!: () => void, end!: () => void;
  const finished = new Promise<void>((resolve) => {
    finish = resolve;
  });
  const terminal = new Promise<void>((resolve) => {
    end = resolve;
  });
  let held: Request | undefined, actual: APIResponse | undefined;
  let failure: unknown;
  let failed = false,
    releaseAttempted = false;
  const transport: {
    outcome: "pending" | "delivered" | "browser_cancelled" | "failed";
  } = { outcome: "pending" };
  const succeeded = (request: Request) => {
    if (request !== held) return;
    transport.outcome = "delivered";
    end();
  };
  const rejected = (request: Request) => {
    if (request !== held) return;
    transport.outcome = /aborted|cancelled|canceled/i.test(
      request.failure()?.errorText ?? "",
    )
      ? "browser_cancelled"
      : "failed";
    end();
  };
  page.on("requestfinished", succeeded);
  page.on("requestfailed", rejected);
  const handler = async (route: Route) => {
    try {
      held = route.request();
      expect(held.method()).toBe("GET");
      actual = await route.fetch({
        maxRedirects: 0,
        maxRetries: 0,
        timeout: 15_000,
      });
      expect(actual.status()).toBe(200);
      expect(actual.headers()["cache-control"]).toBe("no-store");
      expect(actual.headers()["x-adtr-user-id"]).toBe(String(actorId));
      check(await actual.json());
      ready();
      await bounded(gate, 120_000, "Source response was not released in time");
      releaseAttempted = true;
      try {
        await route.fulfill({ response: actual });
      } catch (error) {
        if (
          !/aborted|cancelled|canceled/i.test(held.failure()?.errorText ?? "")
        )
          throw error;
      }
    } catch (error) {
      failed = true;
      failure = error;
      rejectReady(error);
      await route.abort().catch(() => {});
    } finally {
      try {
        await actual?.dispose();
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
      bounded(fetched, 20_000, "No genuine source response was captured"),
    async release() {
      unblock();
      await bounded(finished, 20_000, "Source response handler did not finish");
      if (failed) throw failure;
      await bounded(terminal, 10_000, "Source request had no terminal event");
      expect(releaseAttempted).toBe(true);
      expect(["delivered", "browser_cancelled"]).toContain(transport.outcome);
      await page.evaluate(
        () => new Promise((resolve) => setTimeout(resolve, 0)),
      );
      return { outcome: transport.outcome };
    },
    async cleanup() {
      unblock();
      try {
        await bounded(
          page.unroute(path, handler),
          5_000,
          "Source unroute timed out",
        );
        if (held)
          await bounded(
            finished,
            20_000,
            "Source route cleanup did not finish",
          );
        if (failed) throw failure;
      } finally {
        page.off("requestfinished", succeeded);
        page.off("requestfailed", rejected);
      }
    },
  };
}

async function watchSourceIsolation(
  page: Page,
  nextUsername: string,
  accountLabel: string,
  accountId: string,
  key: string,
) {
  return page.evaluateHandle(
    ({ nextUsername, accountLabel, accountId, key }) => {
      let oldIdentity = false,
        oldSource = false;
      const scan = () => {
        const actor = document.querySelector(".signed-in strong")?.textContent;
        oldIdentity ||= !!actor && actor !== nextUsername;
        const text = document.body.textContent ?? "";
        oldSource ||=
          [accountLabel, accountId, key].some((value) =>
            text.includes(value),
          ) ||
          !!document.querySelector(
            'section[aria-label="凭据来源"], section[aria-label="核对凭据来源回执"], section[aria-label="同域操作账户"], input[name="actorPassword"], input[name="totpCode"]',
          ) ||
          Array.from(document.querySelectorAll("input")).some((input) =>
            [accountLabel, accountId, key].includes(input.value),
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
          return { oldIdentity, oldSource };
        },
        stop: () => observer.disconnect(),
      };
    },
    { nextUsername, accountLabel, accountId, key },
  );
}

// Requires the real migrated database, worker and isolated trusted TLS LDAP fixture.
// Fault injection loses an actual committed response, never fabricates API outcomes.
// Screenshots are captured only after checking that every write-only field is clear.
test.use({ trace: "off", screenshot: "off", video: "off" });
test("unconfigured bootstrap, explicit own-role reference, real LDAP consumption and source recovery", async ({
  page,
  context,
}, testInfo) => {
  test.setTimeout(510_000);
  let username = process.env.ADTR_E2E_USERNAME;
  const bootstrapPassword = process.env.ADTR_E2E_PASSWORD;
  if (!username || !bootstrapPassword || !process.env.ADTR_E2E_BASE_URL)
    throw new Error(
      "A fresh synthetic bootstrap account and ADTR_E2E_BASE_URL are required",
    );
  let password = "Changed Governance Password 123";
  const consumerUsername = "reference-consumer";
  const consumerInitial = "Initial Reference Consumer 123";
  const consumerPassword = "Changed Reference Consumer 456";
  const domain = "synthetic.invalid",
    group = "E2ECredentialGovernance",
    label = "Governed synthetic credential";
  const domainUser = "domain-fixture@synthetic.invalid",
    domainPassword = "Synthetic domain credential 481";
  const accountUser = process.env.ADTR_E2E_LDAP_USERNAME,
    accountPassword = process.env.ADTR_E2E_LDAP_PASSWORD,
    ldapIP = process.env.ADTR_E2E_LDAP_IP;
  if (!accountUser || !accountPassword || !ldapIP)
    throw new Error(
      "Real synthetic LDAP fixture credentials and IP are required",
    );
  const secrets = [
    bootstrapPassword,
    password,
    consumerInitial,
    consumerPassword,
    domainUser,
    domainPassword,
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
      /"(?:username|password|actorPassword|totpCode|ciphertext|userInfo)"\s*:/u.test(
        serialized,
      ),
      "secret-bearing public field",
    ).toBe(false);
  };
  const safeBrowser = async (target = page) => {
    const state = await target.evaluate(async () => ({
      url: location.href,
      history: history.state,
      local: { ...localStorage },
      session: { ...sessionStorage },
      databases: await indexedDB.databases(),
      text: document.body.innerText,
      inputs: Array.from(document.querySelectorAll("input")).map(
        (input) => input.value,
      ),
    }));
    safe(state);
    expect(state.local).toEqual({});
    expect(state.databases).toEqual([]);
    return state;
  };
  const consoles: string[] = [];
  page.on("console", (m) => consoles.push(m.text()));
  page.on("pageerror", (e) => consoles.push(e.message));
  let grantPosts = 0,
    revokePosts = 0;
  const remoteRequests: string[] = [];
  context.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    if (request.method() !== "POST") return;
    if (path === "/api/credential-use/grant") grantPosts++;
    if (path === "/api/credential-use/revoke") revokePosts++;
    if (
      path === "/api/domains/test" ||
      path.startsWith("/api/tasks/") ||
      path === "/api/credential-use/execute"
    )
      remoteRequests.push(path);
  });
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
  let auth = await enrollMfa(page, password);
  secrets.push(auth.secret);
  const tenant = await readJSON(context, "/api/resources/tenant");
  expect(tenant.maxAdCount).toBe(2);
  expect(tenant.expireTime).toBeGreaterThan(Math.floor(Date.now() / 1000));
  expect(
    (await readJSON(context, "/api/credential-use/accounts")).List,
  ).toEqual([]);
  expect((await readJSON(context, "/api/operation-accounts")).List).toEqual([]);

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
  await navigateTo(page, "资源与租户");
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
  let actorId = (await readJSON(context, "/api/auth/me")).ID as number;
  await navigateTo(page, "管理操作账户");
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

  // Keep the production ten-action window intact, including the stale CAS.
  // Bootstrap: password/MFA (3), domain/group/assignment (3), account/grant (2),
  // consumer creation (1) = 9. Consumer: password/MFA (3), metadata/stale source/
  // committed reference/test/revoke/detach/custom (7) = 10. Create the consumer
  // after the one-member grant review to retain its current/future-member check.
  // Logout does not reset a bucket; the real second user has its own budget.
  const bootstrapActorId = actorId;
  const bootstrapActor = { username, password, auth };
  await navigateTo(page, "访问管理");
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  await page.getByLabel("用户名", { exact: true }).fill(consumerUsername);
  await fillSecret(
    page.getByLabel("初始密码", { exact: true }),
    consumerInitial,
  );
  await fillSecret(
    page.getByLabel("确认初始密码", { exact: true }),
    consumerInitial,
  );
  await page.getByLabel("角色", { exact: true }).fill("platform_admin");
  await proof(page, password, auth);
  const consumer = await submit(page, "/api/access/users/create", "创建用户");
  safe(consumer.value);
  expect(consumer.value).toMatchObject({
    result: "SUCCESS",
    sessionRevoked: false,
  });
  expect(consumer.value.ID).toBeGreaterThan(0);
  expect(consumer.value.ID).not.toBe(bootstrapActorId);
  await navigateTo(page, "退出登录");
  await login(page, consumerUsername, consumerInitial);
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await fillSecret(
    page.getByLabel("当前密码", { exact: true }),
    consumerInitial,
  );
  await fillSecret(
    page.getByLabel("新密码", { exact: true }),
    consumerPassword,
  );
  await fillSecret(
    page.getByLabel("确认新密码", { exact: true }),
    consumerPassword,
  );
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  auth = await enrollMfa(page, consumerPassword);
  secrets.push(auth.secret);
  username = consumerUsername;
  password = consumerPassword;
  actorId = (await readJSON(context, "/api/auth/me")).ID as number;
  expect(actorId).toBe(consumer.value.ID);
  expect(await getUse(`/effective?accountId=${accountId}`)).toMatchObject({
    explicitlyGranted: true,
    eligible: true,
    grantRevision: granted.value.grantRevision,
  });
  expect((await getUse(`/roles?accountId=${accountId}`)).roles).toContainEqual({
    roleId: "platform_admin",
    roleName: "platform_admin",
    memberCount: 2,
  });
  const openSource = async (target = page) => {
    await navigateTo(target, "域连接");
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
  const sourceRead = async () => {
    const response = await context.request.get(
      `/api/domains/credential-source?domainId=${domainId}`,
    );
    expect(response.status()).toBe(200);
    expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId));
    const value = await response.json();
    safe(value);
    return value;
  };
  await openSource();
  expect(await sourceRead()).toMatchObject({
    credentialSource: "unconfigured",
    credentialConfigured: false,
    testEligible: false,
    reference: null,
  });
  await safeBrowser();
  await page.screenshot({
    path: testInfo.outputPath("source-unconfigured.png"),
    fullPage: true,
  });
  // Deliver a real source read after leaving its view. Back/Forward must not
  // resurrect account pointers or a stale source editor.
  let readArrived!: () => void, releaseRead!: () => void;
  const fetchedRead = new Promise<void>((resolve) => {
    readArrived = resolve;
  });
  const release = new Promise<void>((resolve) => {
    releaseRead = resolve;
  });
  await page.route(
    "**/api/domains/credential-source?*",
    async (route) => {
      const actual = await route.fetch({ maxRetries: 0 });
      expect(actual.status()).toBe(200);
      safe(await actual.json());
      readArrived();
      await release;
      await route.fulfill({ response: actual }).catch(() => {});
    },
    { times: 1 },
  );
  await page
    .getByRole("button", { name: "重新读取凭据来源", exact: true })
    .click();
  await fetchedRead;
  await page.goBack();
  await expect(
    page.getByRole("button", { name: `查看 ${domain}`, exact: true }),
  ).toBeVisible();
  releaseRead();
  await page.goForward();
  await expect(
    page.getByRole("button", { name: `查看 ${domain}`, exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("新的凭据来源", { exact: true })).toHaveCount(0);
  await openSource();
  await page
    .getByLabel("新的凭据来源", { exact: true })
    .selectOption("reference");
  const choose = page.getByRole("button", {
    name: `选择账户 ${label}`,
    exact: true,
  });
  await choose.click();
  await expect(
    page.getByText("本角色显式授权：已授予", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "保存凭据来源", exact: true }),
  ).toBeEnabled();
  const candidates = await readJSON(
    context,
    `/api/operation-accounts?filterDomain=${domain}`,
  );
  expect(
    candidates.List.every((a: { domainId: string }) => a.domainId === domainId),
  ).toBe(true);
  // A real concurrent endpoint edit advances the parent revision but leaves the
  // credential generation intact. The stale source form must fail closed.
  const beforeEdit = await sourceRead();
  const editingActor = await readJSON(context, "/api/auth/me");
  const edited = await context.request.post("/api/domains/update", {
    headers: {
      Origin: new URL(process.env.ADTR_E2E_BASE_URL!).origin,
      "X-CSRF-Token": editingActor.csrfToken,
    },
    data: {
      domainId,
      expectedRevision: beforeEdit.revision,
      dcHostName: "dc.synthetic.invalid",
      ldapAddr: ldapIP,
      port: "636",
      actorPassword: password,
      totpCode: await freshCode(auth),
    },
  });
  expect(edited.status()).toBe(200);
  safe(await edited.json());
  await proof(page, password, auth);
  const stale = await submit(
    page,
    "/api/domains/credential-source/reference",
    "保存凭据来源",
    409,
  );
  expect(stale.value).toEqual({ error: "revision_conflict" });
  await expect(
    page.getByRole("button", { name: "保存凭据来源", exact: true }),
  ).toBeDisabled();
  await expect(page.getByLabel("操作者当前密码", { exact: true })).toHaveCount(
    0,
  );
  await page
    .getByRole("button", { name: "重新核对来源版本", exact: true })
    .click();
  await page
    .getByLabel("新的凭据来源", { exact: true })
    .selectOption("reference");
  await page
    .getByRole("button", { name: `选择账户 ${label}`, exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "保存凭据来源", exact: true }),
  ).toBeEnabled();
  const afterEdit = await sourceRead();
  expect(BigInt(afterEdit.revision)).toBe(BigInt(beforeEdit.revision) + 1n);
  expect(afterEdit.connectionCredentialGeneration).toBe(
    beforeEdit.connectionCredentialGeneration,
  );
  let sourcePosts = 0;
  context.on("request", (request) => {
    if (
      request.method() === "POST" &&
      new URL(request.url()).pathname ===
        "/api/domains/credential-source/reference"
    )
      sourcePosts++;
  });
  let committedKey = "";
  let finishCommit!: () => void;
  const committed = new Promise<void>((resolve) => {
    finishCommit = resolve;
  });
  await page.route(
    "**/api/domains/credential-source/mutation?*",
    async (route) => {
      const actual = await route.fetch({ maxRetries: 0 });
      expect(actual.status()).toBe(200);
      expect(actual.headers()["x-adtr-user-id"]).toBe(String(actorId));
      safe(await actual.json());
      await route.abort("connectionreset");
    },
  );
  await page.route(
    "**/api/domains/credential-source/reference",
    async (route) => {
      const actual = await route.fetch({ maxRetries: 0 });
      expect(actual.status()).toBe(200);
      expect(actual.headers()["x-adtr-user-id"]).toBe(String(actorId));
      const value = await actual.json();
      safe(value);
      expect(value).toMatchObject({
        operation: "reference",
        credentialSource: "operation_account",
        replayed: false,
      });
      const sent = route.request().postDataJSON();
      expect(sent.accountId).toBe(accountId);
      expect(Object.keys(sent).sort()).toEqual(
        [
          "domainId",
          "expectedRevision",
          "expectedConnectionCredentialGeneration",
          "accountId",
          "expectedAccountRevision",
          "expectedAccountCredentialRevision",
          "expectedGrantRevision",
          "idempotencyKey",
          "actorPassword",
          "totpCode",
        ].sort(),
      );
      committedKey = sent.idempotencyKey;
      await route.abort("connectionreset");
      finishCommit();
    },
    { times: 1 },
  );
  await proof(page, password, auth);
  await page.getByRole("button", { name: "保存凭据来源", exact: true }).click();
  await committed;
  await expect(
    page.getByRole("heading", {
      name: "核对未确认的凭据来源变更",
      exact: true,
    }),
  ).toBeVisible();
  const pending = await safeBrowser();
  const storageKey = `adtr.domain-source-intent.v1:${actorId}:${username}`;
  expect(Object.keys(pending.session)).toEqual([storageKey]);
  expect(JSON.parse(pending.session[storageKey])).toEqual({
    owner: `${actorId}:${username}`,
    intent: {
      operation: "reference",
      domainId,
      expectedRevision: afterEdit.revision,
      expectedConnectionCredentialGeneration:
        afterEdit.connectionCredentialGeneration,
      idempotencyKey: committedKey,
    },
  });
  const serialized = JSON.stringify(pending.session);
  expect(serialized).toContain(committedKey);
  expect(serialized).not.toContain(accountId);
  expect(serialized).not.toMatch(
    /accountId|grantRevision|accountRevision|accountCredentialRevision/u,
  );
  await expect(page.getByLabel("操作者当前密码", { exact: true })).toHaveCount(
    0,
  );
  await test.step("isolate an unresolved source receipt, candidate draft and late reads across real account switching", async () => {
    // Both existing accounts have this domain's explicit scope. The boundary
    // under test is the original operator's receipt and browser draft, not a
    // fabricated denial of B's independently authorized domain reads.
    const draft = await context.newPage();
    const detail = await context.newPage();
    const switching = await context.newPage();
    const oldTabs = [page, draft, detail];
    const held: Awaited<ReturnType<typeof holdSourceRead>>[] = [];
    const witnesses: Awaited<ReturnType<typeof watchSourceIsolation>>[] = [];
    const listeners: { tab: Page; listener: (request: Request) => void }[] = [];
    const browserWrites: string[] = [],
      foreignReceiptReads: string[] = [];
    const outcomes: { outcome: string }[] = [];
    const cleanupErrors: unknown[] = [];
    let switched = false;
    let scenarioFailed = false,
      scenarioFailure: unknown;
    for (const extra of [draft, detail, switching]) {
      extra.on("console", (message) => consoles.push(message.text()));
      extra.on("pageerror", (error) => consoles.push(error.message));
    }
    try {
      const receipt = await holdSourceRead(
        page,
        (url) => url.pathname === "/api/domains/credential-source/mutation",
        actorId,
        (value) => {
          safe(value);
          expect(value).toMatchObject({
            receipt: {
              operation: "reference",
              domainId,
              credentialSource: "operation_account",
              replayed: true,
            },
          });
        },
      );
      held.push(receipt);
      // The real aborted receipt read retries through the normal read hook.
      // Hold that result while A's unresolved intent remains in the editor.
      await receipt.fetched();
      await expect(
        page.getByRole("heading", {
          name: "核对未确认的凭据来源变更",
          exact: true,
        }),
      ).toBeVisible();

      await draft.goto("/");
      await expect(draft.locator(".signed-in strong")).toHaveText(username);
      await openSource(draft);
      await draft
        .getByLabel("新的凭据来源", { exact: true })
        .selectOption("reference");
      await draft
        .getByRole("button", {
          name: `选择账户 ${label}`,
          exact: true,
        })
        .click();
      await expect(
        draft.getByRole("button", {
          name: "保存凭据来源",
          exact: true,
        }),
      ).toBeEnabled();
      await fillSecret(
        draft.getByLabel("操作者当前密码", { exact: true }),
        password,
      );
      // This is only an unsubmitted proof draft. Do not consume another real
      // counter or weaken the server's replay protection to test its clearing.
      await fillSecret(
        draft.getByLabel("未使用的认证器验证码", { exact: true }),
        totp(auth.secret),
      );
      const candidates = await holdSourceRead(
        draft,
        (url) => url.pathname === "/api/operation-accounts",
        actorId,
        (value) => {
          safe(value);
          expect(value).toMatchObject({
            List: expect.arrayContaining([
              expect.objectContaining({ accountId, domainId, label }),
            ]),
          });
        },
      );
      held.push(candidates);
      await draft
        .getByRole("button", { name: "刷新同域账户", exact: true })
        .click();
      await candidates.fetched();
      expect(
        await draft
          .getByLabel("操作者当前密码", { exact: true })
          .evaluate((input) => (input as HTMLInputElement).value.length > 0),
      ).toBe(true);
      await expect(
        draft.getByRole("button", {
          name: `选择账户 ${label}`,
          exact: true,
        }),
      ).toHaveAttribute("aria-pressed", "true");

      await detail.goto("/");
      await expect(detail.locator(".signed-in strong")).toHaveText(username);
      await openSource(detail);
      const source = await holdSourceRead(
        detail,
        (url) => url.pathname === "/api/domains/credential-source",
        actorId,
        (value) => {
          safe(value);
          expect(value).toMatchObject({
            domainId,
            credentialSource: "operation_account",
            reference: {
              accountId,
              label,
              explicitlyGranted: true,
              eligible: true,
            },
          });
        },
      );
      held.push(source);
      await detail
        .getByRole("button", { name: "重新读取凭据来源", exact: true })
        .click();
      await source.fetched();
      await switching.goto("/");
      await expect(switching.locator(".signed-in strong")).toHaveText(username);
      for (const tab of oldTabs) {
        const listener = (request: Request) => {
          const url = new URL(request.url());
          if (
            !["GET", "HEAD"].includes(request.method()) &&
            url.pathname !== "/api/access/check"
          )
            browserWrites.push(`${request.method()} ${url.pathname}`);
          if (
            switched &&
            url.pathname === "/api/domains/credential-source/mutation"
          )
            foreignReceiptReads.push(url.pathname);
        };
        tab.on("request", listener);
        listeners.push({ tab, listener });
      }
      // Real logout/login changes the shared cookie and publishes the app's
      // native cross-tab notification. No synthetic focus/session event is used.
      await navigateTo(switching, "退出登录");
      for (const tab of oldTabs) {
        await expect(
          tab.getByRole("heading", { name: "登录账户", exact: true }),
        ).toBeVisible();
        await safeBrowser(tab);
        witnesses.push(
          await watchSourceIsolation(
            tab,
            bootstrapActor.username,
            label,
            accountId,
            committedKey,
          ),
        );
      }
      await login(switching, bootstrapActor.username, bootstrapActor.password);
      await fillSecret(
        switching.getByLabel("二次认证验证码", { exact: true }),
        await freshCode(bootstrapActor.auth),
      );
      await switching
        .getByRole("button", { name: "登录", exact: true })
        .click();
      await expect(switching.locator(".signed-in strong")).toHaveText(
        bootstrapActor.username,
      );
      expect((await readJSON(context, "/api/auth/me")).ID).toBe(
        bootstrapActorId,
      );
      switched = true;
      for (const tab of oldTabs)
        await expect(tab.locator(".signed-in strong")).toHaveText(
          bootstrapActor.username,
        );

      const denied = await context.request.get(
        `/api/domains/credential-source/mutation?idempotencyKey=${committedKey}`,
      );
      expect(denied.status()).toBe(404);
      expect(denied.headers()["cache-control"]).toBe("no-store");
      expect(await denied.json()).toEqual({ error: "not_found" });
      const ownRead = await context.request.get(
        `/api/domains/credential-source?domainId=${domainId}`,
      );
      expect(ownRead.status()).toBe(200);
      expect(ownRead.headers()["x-adtr-user-id"]).toBe(
        String(bootstrapActorId),
      );
      const ownSource = await ownRead.json();
      safe(ownSource);
      expect(ownSource).toMatchObject({
        domainId,
        revision: (BigInt(afterEdit.revision) + 1n).toString(),
        connectionCredentialGeneration: (
          BigInt(afterEdit.connectionCredentialGeneration) + 1n
        ).toString(),
        credentialSource: "operation_account",
        reference: { accountId, explicitlyGranted: true, eligible: true },
      });
      for (const response of held) outcomes.push(await response.release());
      for (const tab of oldTabs) {
        await expect(
          tab.getByLabel("新的凭据来源", { exact: true }),
        ).toHaveCount(0);
        await expect(
          tab.getByRole("heading", {
            name: "核对未确认的凭据来源变更",
            exact: true,
          }),
        ).toHaveCount(0);
        await expect(tab.getByText(/原凭据来源操作已确认/)).toHaveCount(0);
        await safeBrowser(tab);
      }
      // A's metadata-only recovery intent stays owner-keyed for A's return. B
      // neither claims it nor creates a copy under B's own identity.
      expect((await safeBrowser()).session).toEqual(pending.session);
      await navigateTo(draft, "域连接");
      await expect(
        draft.getByRole("button", { name: `查看 ${domain}`, exact: true }),
      ).toBeVisible();
      await safeBrowser(draft);
      const switchScreenshot = await draft.screenshot({
        path: testInfo.outputPath("source-account-switch.png"),
        fullPage: true,
      });
      for (const witness of witnesses)
        expect(await witness.evaluate((value) => value.read())).toEqual({
          oldIdentity: false,
          oldSource: false,
        });
      expect(
        browserWrites,
        "Old source tabs never submit under the new account",
      ).toEqual([]);
      expect(
        foreignReceiptReads,
        "B never automatically queries A's persisted intent",
      ).toEqual([]);
      expect(sourcePosts).toBe(1);
      const root = execFileSync("git", ["rev-parse", "--show-toplevel"], {
        encoding: "utf8",
      }).trim();
      const hashFile = (file: string) =>
        createHash("sha256")
          .update(readFileSync(resolve(root, file)))
          .digest("hex");
      writeFileSync(
        testInfo.outputPath("source-account-switch-transport.json"),
        JSON.stringify(
          {
            receiptOwnerDenied: true,
            sourcePosts,
            lateReads: outcomes,
            checkoutCommit: execFileSync("git", ["rev-parse", "HEAD"], {
              encoding: "utf8",
            }).trim(),
            eventHead: process.env.ADTR_EVENT_HEAD_SHA ?? null,
            eventBase: process.env.ADTR_EVENT_BASE_SHA ?? null,
            workflowRun: process.env.GITHUB_RUN_ID ?? null,
            workflowAttempt: process.env.GITHUB_RUN_ATTEMPT ?? null,
            screenshotSHA256: createHash("sha256")
              .update(switchScreenshot)
              .digest("hex"),
            sourceSHA256: Object.fromEntries(
              [
                "web/e2e/account-references.spec.ts",
                "web/src/App.tsx",
                "web/src/DomainCredentialSource.tsx",
                "web/src/domain-source-intent.ts",
                "web/src/domain-source-api.ts",
              ].map((file) => [file, hashFile(file)]),
            ),
          },
          null,
          2,
        ),
      );
      for (const witness of witnesses) {
        await witness.evaluate((value) => value.stop());
        await witness.dispose();
      }
      witnesses.length = 0;
      switched = false;
      await navigateTo(switching, "退出登录");
      await login(switching, username, password);
      await fillSecret(
        switching.getByLabel("二次认证验证码", { exact: true }),
        await freshCode(auth),
      );
      await switching
        .getByRole("button", { name: "登录", exact: true })
        .click();
      await expect(page.locator(".signed-in strong")).toHaveText(username);
      expect((await readJSON(context, "/api/auth/me")).ID).toBe(actorId);
    } catch (error) {
      scenarioFailed = true;
      scenarioFailure = error;
    } finally {
      const cleanup = async (operation: () => Promise<unknown>) => {
        try {
          await bounded(
            operation(),
            25_000,
            "Source isolation cleanup timed out",
          );
        } catch (error) {
          cleanupErrors.push(error);
        }
      };
      for (const response of held) await cleanup(() => response.cleanup());
      for (const witness of witnesses) {
        await cleanup(() => witness.evaluate((value) => value.stop()));
        await cleanup(() => witness.dispose());
      }
      for (const { tab, listener } of listeners) tab.off("request", listener);
      for (const extra of [draft, detail, switching])
        await cleanup(() => extra.close());
    }
    if (scenarioFailed || cleanupErrors.length)
      throw new AggregateError(
        [...(scenarioFailed ? [scenarioFailure] : []), ...cleanupErrors],
        "Real source account-switch isolation failed",
      );
  });
  // Finish the genuine receipt read/abort before removing interception and
  // reloading; otherwise unroute can handle an in-flight route first.
  await expect(page).toHaveURL(/#domains\/credential-source$/);
  await page.unrouteAll({ behavior: "wait" });
  await page.reload();
  // Reload recovers the original source intent in the restored workspace.
  // A second navigation could clear its already-confirmed receipt notice.
  await expect(
    page.getByRole("button", { name: "域连接", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  await expect(
    page.getByRole("heading", { name: "域连接", exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/原凭据来源操作已确认/)).toBeVisible();
  expect(sourcePosts).toBe(1);
  expect((await safeBrowser()).session).toEqual({});
  const recovered = await context.request.get(
    `/api/domains/credential-source/mutation?idempotencyKey=${committedKey}`,
  );
  expect(recovered.status()).toBe(200);
  expect(recovered.headers()["x-adtr-user-id"]).toBe(String(actorId));
  const recovery = await recovered.json();
  safe(recovery);
  expect(recovery.receipt).toMatchObject({
    operation: "reference",
    domainId,
    credentialSource: "operation_account",
    deleted: false,
  });
  await page
    .getByRole("button", { name: `查看 ${domain}`, exact: true })
    .click();
  await page.getByRole("button", { name: "管理凭据来源", exact: true }).click();
  await expect(page.getByText(/已引用：/)).toBeVisible();
  expect(await sourceRead()).toMatchObject({
    credentialSource: "operation_account",
    credentialConfigured: true,
    testEligible: true,
    reference: { accountId, explicitlyGranted: true, eligible: true },
  });
  await safeBrowser();
  await page.screenshot({
    path: testInfo.outputPath("source-reference.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "关闭来源编辑", exact: true }).click();
  await page
    .getByRole("button", { name: "检测已保存连接", exact: true })
    .click();
  await proof(page, password, auth);
  const tested = await submit(page, "/api/domains/test", "确认检测保存版本");
  expect(tested.value.task).toMatchObject({
    taskName: "domain.account_connection_test",
    maxAttempts: 1,
    result: {},
    cursor: {},
  });
  await expect(
    page.getByText("TLS、凭据绑定及域命名上下文检测通过。", { exact: true }),
  ).toBeVisible({ timeout: 30_000 });
  const result = await readJSON(
    context,
    `/api/domains/test-result?taskUUID=${tested.value.task.taskUUID}`,
  );
  expect(result.task.state).toBe("succeeded");
  expect(result.diagnostic.code).toBe("ok");
  safe(result);
  const ordinary = await readJSON(
    context,
    `/api/domains/detail?domainId=${domainId}`,
  );
  expect(ordinary.connection).toMatchObject({
    credentialSource: "operation_account",
    credentialConfigured: true,
    connectionState: "verified",
  });
  expect(JSON.stringify(ordinary)).not.toContain(accountId);
  const taskView = await readJSON(
    context,
    `/api/tasks/detail?taskUUID=${tested.value.task.taskUUID}`,
  );
  expect(taskView.task.result).toEqual({});
  expect(taskView.task.cursor).toEqual({});
  expect(JSON.stringify(taskView)).not.toContain(accountId);
  safe(taskView);
  await navigateTo(page, "管理操作账户");
  await page
    .getByRole("button", { name: `查看 ${label}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "查看凭据使用授权", exact: true })
    .click();
  await page
    .getByRole("button", { name: "撤销 platform_admin", exact: true })
    .click();
  await page
    .getByRole("checkbox", {
      name: "我已核对目标角色，确认撤销覆盖当前及未来全部成员",
    })
    .check();
  await proof(page, password, auth);
  const revoked = await submit(
    page,
    "/api/credential-use/revoke",
    "确认撤销角色使用权",
  );
  expect(revoked.value.currentAllowed).toBe(false);
  await openSource();
  expect(await sourceRead()).toMatchObject({
    testEligible: false,
    reference: { explicitlyGranted: false, eligible: false },
  });
  await page.getByRole("button", { name: "关闭来源编辑", exact: true }).click();
  await page
    .getByRole("button", { name: "检测已保存连接", exact: true })
    .click();
  await expect(page.getByText(/当前不可提交检测/)).toBeVisible();
  await expect(
    page.getByRole("button", { name: "确认检测保存版本", exact: true }),
  ).toHaveCount(0);
  await openSource();
  await page.getByLabel("新的凭据来源", { exact: true }).selectOption("detach");
  await proof(page, password, auth);
  const detached = await submit(
    page,
    "/api/domains/credential-source/detach",
    "保存凭据来源",
  );
  expect(detached.value).toMatchObject({
    credentialSource: "unconfigured",
    operation: "detach",
  });
  await openSource();
  await page.getByLabel("新的凭据来源", { exact: true }).selectOption("custom");
  await expect(page.getByLabel("AD 用户名", { exact: true })).toHaveValue("");
  await expect(page.getByLabel("AD 密码", { exact: true })).toHaveValue("");
  await page.getByLabel("AD 用户名", { exact: true }).fill(accountUser);
  await page.getByLabel("AD 密码", { exact: true }).fill(accountPassword);
  await proof(page, password, auth);
  const custom = await submit(
    page,
    "/api/domains/credential-source/custom",
    "保存凭据来源",
  );
  expect(custom.value).toMatchObject({
    credentialSource: "custom",
    operation: "custom",
  });
  await openSource();
  expect(await sourceRead()).toMatchObject({
    credentialSource: "custom",
    credentialConfigured: true,
    reference: null,
  });
  await safeBrowser();
  await page.screenshot({
    path: testInfo.outputPath("source-custom.png"),
    fullPage: true,
  });
  expect(grantPosts).toBe(1);
  expect(revokePosts).toBe(1);
  expect(sourcePosts).toBe(1);
  expect(
    remoteRequests.filter((path) => path === "/api/domains/test"),
  ).toHaveLength(1);
  safe(consoles);
});
