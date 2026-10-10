import { navigateTo } from "./navigation";
import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { createHmac } from "node:crypto";

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
  // Enrollment and every privileged write consume a counter. Use real time;
  // never freeze the browser clock or bypass the server's replay protection.
  const earliest = (authenticator.lastCounter + 1) * 30_000 + 1100;
  while (Date.now() < earliest)
    await new Promise((resolve) => setTimeout(resolve, earliest - Date.now()));
  const now = Date.now();
  authenticator.lastCounter = Math.floor(now / 30_000);
  return totp(authenticator.secret, now);
}

async function login(page: Page, username: string, password: string) {
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function changeInitialPassword(
  page: Page,
  initial: string,
  changed: string,
) {
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await page.getByLabel("当前密码", { exact: true }).fill(initial);
  await page.getByLabel("新密码", { exact: true }).fill(changed);
  await page.getByLabel("确认新密码", { exact: true }).fill(changed);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
}

async function enrollMfa(page: Page, password: string): Promise<Authenticator> {
  await navigateTo(page, "多因素认证");
  await page.getByLabel("当前密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "开始设置", exact: true }).click();
  const secret = await page
    .getByLabel("设置密钥", { exact: true })
    .inputValue();
  expect(secret.length).toBeGreaterThan(10);
  const now = Date.now();
  await page
    .getByLabel("认证器验证码", { exact: true })
    .fill(totp(secret, now));
  await page.getByRole("button", { name: "验证并启用", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用");
  return { secret, lastCounter: Math.floor(now / 30_000) };
}

async function readJSON(context: BrowserContext, path: string) {
  const response = await context.request.get(path);
  expect(response.status(), `GET ${path}`).toBe(200);
  return response.json();
}

async function resourceMutation(
  page: Page,
  path: string,
  button: string,
  revoked: boolean,
) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === path &&
        candidate.request().method() === "POST",
    ),
    page.getByRole("button", { name: button, exact: true }).click(),
  ]);
  expect(response.status(), `POST ${path}`).toBe(200);
  const result = await response.json();
  expect(result).toMatchObject({ result: "SUCCESS", sessionRevoked: revoked });
  return result;
}
async function proof(
  page: Page,
  password: string,
  authenticator: Authenticator,
) {
  await page.getByLabel("操作者当前密码", { exact: true }).fill(password);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(authenticator));
}
async function relogin(
  page: Page,
  username: string,
  password: string,
  authenticator: Authenticator,
) {
  await login(page, username, password);
  await page
    .getByLabel("二次认证验证码", { exact: true })
    .fill(await freshCode(authenticator));
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
}
async function openResources(page: Page) {
  await navigateTo(page, "资源与租户");
  await expect(
    page.getByRole("heading", { name: "当前数据权限", exact: true }),
  ).toBeVisible();
}
async function openGroup(page: Page, name: string) {
  await page.getByRole("button", { name: "资源组", exact: true }).click();
  await page.getByRole("button", { name: `查看 ${name}`, exact: true }).click();
  await expect(
    page.getByRole("heading", { name: `资源组详情：${name}`, exact: true }),
  ).toBeVisible();
}

// Actual browser, API, PostgreSQL, worker and synthetic TLS/LDAP fixture only.
// One transport fault drops a real committed response. No response mocks, real AD targets or real credentials.
test("real domain enrollment, explicit grant, TLS diagnostics, rotation and local deletion", async ({
  page,
  context,
}) => {
  test.setTimeout(420_000);
  const username = process.env.ADTR_E2E_USERNAME,
    password = process.env.ADTR_E2E_PASSWORD,
    ldapIP = process.env.ADTR_E2E_LDAP_IP,
    ldapUser = process.env.ADTR_E2E_LDAP_USERNAME,
    ldapPassword = process.env.ADTR_E2E_LDAP_PASSWORD;
  if (!username || !password || !ldapIP || !ldapUser || !ldapPassword)
    throw new Error(
      "Fresh synthetic bootstrap account and ADTR_E2E_LDAP_IP/USERNAME/PASSWORD are required",
    );
  const changed = "Changed Domain Password 123",
    domain = "synthetic.invalid",
    dc = "dc.synthetic.invalid",
    group = "E2EDomainConnection";
  const openDomains = async () => {
    await navigateTo(page, "域连接");
    await expect(
      page.getByRole("heading", { name: "域连接", exact: true }),
    ).toBeVisible();
  };
  const post = async (path: string, button: string) => {
    const [r] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === path && r.request().method() === "POST",
      ),
      page.getByRole("button", { name: button, exact: true }).click(),
    ]);
    expect(r.status(), path).toBe(200);
    return { value: await r.json(), sent: r.request().postDataJSON() };
  };
  const safe = (value: unknown) => {
    const text = JSON.stringify(value);
    expect(text).not.toContain(ldapPassword);
    expect(text).not.toContain(ldapUser);
    expect(text).not.toContain("ciphertext");
  };
  let submittedTests = 0,
    submittedCreates = 0;
  page.on("request", (request) => {
    if (request.method() !== "POST") return;
    const path = new URL(request.url()).pathname;
    if (path === "/api/domains/test") submittedTests++;
    if (path === "/api/domains/create") submittedCreates++;
  });
  const selectSavedSource = async (
    id: string,
    revision: string,
    credentialRevision: string,
    state: "unverified" | "verified",
    observedAt?: string,
  ) => {
    const submittedBefore = submittedTests;
    const actor = await readJSON(context, "/api/auth/me");
    const [choicesResponse] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === "/api/domain-selection" &&
          r.request().method() === "GET",
      ),
      page
        .getByRole("button", { name: "选择已授权数据源", exact: true })
        .click(),
    ]);
    expect(choicesResponse.status()).toBe(200);
    expect(choicesResponse.headers()["cache-control"]).toBe("no-store");
    expect(choicesResponse.headers()["x-adtr-user-id"]).toBe(String(actor.ID));
    const choices = await choicesResponse.json();
    expect(choices.List).toHaveLength(1);
    expect(choices.List[0]).toMatchObject({
      domainId: id,
      revision,
      credentialRevision,
      source: "configured_connection",
      connectionState: state,
    });
    expect(Object.keys(choices.List[0]).sort()).toEqual(
      [
        "domainId",
        "domain",
        "revision",
        "credentialRevision",
        "dcHostName",
        "ldapAddr",
        "port",
        "mode",
        "credentialConfigured",
        "connectionState",
        "source",
        "lastTest",
      ].sort(),
    );
    safe(choices);
    const sourceRadio = page.getByRole("radio", {
      name: `选择数据源 ${domain}`,
      exact: true,
    });
    const sourceRow = page
      .getByRole("table", { name: "已授权的保存连接配置", exact: true })
      .getByRole("row")
      .filter({ has: sourceRadio });
    await expect(sourceRow).toBeVisible();
    if (observedAt) {
      expect(choices.List[0].lastTest).toMatchObject({
        observedAt,
        code: "ok",
        dcHostName: dc,
      });
      // The status cell includes its nested diagnostic summary. Check both
      // pieces together, with the exact observation time in the same domain row.
      const historicalStatus = sourceRow
        .getByRole("cell")
        .filter({ hasText: "历史检测通过" });
      await expect(historicalStatus).toBeVisible();
      await expect(historicalStatus).toHaveText(
        /^历史检测通过\s*历史结果：TLS、凭据绑定及域命名上下文检测通过。$/u,
      );
      await expect(
        sourceRow.getByRole("cell", { name: observedAt, exact: true }),
      ).toBeVisible();
    } else expect(choices.List[0].lastTest).toBeNull();
    await sourceRadio.check();
    const [resolution, detailResponse] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === "/api/domain-selection/resolve" &&
          r.request().method() === "GET",
      ),
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === "/api/domains/detail" &&
          r.request().method() === "GET",
      ),
      page
        .getByRole("button", { name: "核对并打开连接详情", exact: true })
        .click(),
    ]);
    expect(resolution.status()).toBe(200);
    expect(resolution.headers()["cache-control"]).toBe("no-store");
    expect(resolution.headers()["x-adtr-user-id"]).toBe(String(actor.ID));
    expect(Object.fromEntries(new URL(resolution.url()).searchParams)).toEqual({
      domainId: id,
      expectedRevision: revision,
      expectedCredentialRevision: credentialRevision,
    });
    const checked = await resolution.json();
    expect(checked.selection).toMatchObject({
      domainId: id,
      revision,
      credentialRevision,
      connectionState: state,
    });
    safe(checked);
    expect(detailResponse.status()).toBe(200);
    await expect(
      page.getByRole("heading", { name: `域连接详情：${domain}`, exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(
        `数据源核对时间（UTC）：${checked.checkedAt}。连接详情按当前权限重新读取；检测仍需另行确认。`,
        { exact: true },
      ),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "检测已保存连接", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByLabel("操作者当前密码", { exact: true }),
    ).toHaveCount(0);
    expect(submittedTests).toBe(submittedBefore);
  };
  await page.goto("/");
  await login(page, username, password);
  await changeInitialPassword(page, password, changed);
  const auth = await enrollMfa(page, changed);
  const actor = await readJSON(context, "/api/auth/me");
  // Runner seeds an eligible default tenant, maxAdCount=2, no catalogue domains.
  expect((await readJSON(context, "/api/resources/tenant")).maxAdCount).toBe(2);
  await openDomains();
  await expect(page.getByText(/当前筛选下没有获授权的域连接/)).toBeVisible();
  await page.getByRole("button", { name: "新增域连接", exact: true }).click();
  await page.getByLabel("域 DNS 名称", { exact: true }).fill(domain);
  await page.getByLabel("域控 DNS 名称", { exact: true }).fill(dc);
  await page.getByLabel("连接 IP（可选）", { exact: true }).fill(ldapIP);
  await page.getByLabel("AD 用户名", { exact: true }).fill(ldapUser);
  await page.getByLabel("AD 密码", { exact: true }).fill(ldapPassword);
  await proof(page, changed, auth);
  // Forward the original browser POST to the real API, then discard its
  // committed response. No success or failure body is fabricated.
  type Committed = {
    value: {
      domainId: string;
      revision: string;
      result: string;
      requiresResourceAssignment: boolean;
      replayed: boolean;
    };
    key: string;
  };
  let committedResolve!: (value: Committed) => void,
    committedReject!: (error: unknown) => void;
  const committed = new Promise<Committed>((resolve, reject) => {
    committedResolve = resolve;
    committedReject = reject;
  });
  await page.route(
    "**/api/domains/create",
    async (route) => {
      try {
        const actual = await route.fetch({ maxRetries: 0 });
        expect(actual.status()).toBe(200);
        const value = await actual.json();
        const key = route.request().postDataJSON().idempotencyKey;
        await route.abort("connectionreset");
        committedResolve({ value, key });
      } catch (error) {
        committedReject(error);
        await route.abort().catch(() => {});
      }
    },
    { times: 1 },
  );
  await page.getByRole("button", { name: "保存本地连接", exact: true }).click();
  const created = await committed,
    id = created.value.domainId,
    key = created.key;
  await expect(
    page.getByText(/请返回列表，重新读取配置或查询原登记回执/),
  ).toBeVisible();
  await expect(page.getByLabel("AD 密码", { exact: true })).toHaveValue("");
  expect(created.value).toMatchObject({
    result: "SUCCESS",
    revision: "1",
    requiresResourceAssignment: true,
    replayed: false,
  });
  expect(id).not.toBe(domain);
  safe(created.value);
  await expect(page.getByLabel("新域 ID", { exact: true })).toHaveCount(0);
  expect(
    (await context.request.get(`/api/domains/detail?domainId=${id}`)).status(),
  ).toBe(404);
  expect((await readJSON(context, "/api/domains")).page.total).toBe(0);
  // Creation grants no domain scope, even to the platform administrator.
  // A filtered task list has no authorized scope until the explicit assignment.
  const ungrantedTasks = await context.request.get(
    `/api/tasks?taskName=domain.connection_test&domainId=${id}`,
  );
  expect(ungrantedTasks.status()).toBe(403);
  expect(await ungrantedTasks.json()).toEqual({ error: "forbidden" });
  expect(submittedTests).toBe(0);
  // Unknown outcomes retain only the original safe intent under its live actor.
  const storageKey = `adtr.domain-intent.v1:${actor.ID}:${encodeURIComponent(actor.username)}`;
  const pendingStorage = await page.evaluate(() => ({ ...sessionStorage }));
  expect(Object.keys(pendingStorage)).toEqual([storageKey]);
  const pending = JSON.parse(pendingStorage[storageKey]);
  expect(pending).toEqual({
    owner: `${actor.ID}:${actor.username}`,
    intent: { kind: "create", key, domain },
  });
  safe(pending);
  expect(submittedCreates).toBe(1);
  await expect(page).toHaveURL(/#domains\/create$/);
  const [recoveryResponse] = await Promise.all([
    page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        url.pathname === "/api/domains/creation" &&
        url.searchParams.get("idempotencyKey") === key &&
        response.request().method() === "GET"
      );
    }),
    page.reload(),
  ]);
  // Reload restores this workspace and recovers the original intent. Clicking
  // its navigation entry again can discard a receipt already recovered here.
  await expect(
    page.getByRole("button", { name: "域连接", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  await expect(
    page.getByRole("heading", { name: "域连接", exact: true }),
  ).toBeVisible();
  expect(recoveryResponse.status()).toBe(200);
  expect(recoveryResponse.headers()["x-adtr-user-id"]).toBe(String(actor.ID));
  await expect(page.getByLabel("新域 ID", { exact: true })).toHaveValue(id);
  const receipt = await recoveryResponse.json();
  expect(receipt.receipt).toEqual({
    domainId: id,
    domain,
    revision: "1",
    deleted: false,
    requiresResourceAssignment: true,
  });
  safe(receipt);
  expect(submittedCreates).toBe(1);
  expect(await page.evaluate(() => sessionStorage.length)).toBe(0);
  await openResources(page);
  await page.getByRole("button", { name: "资源组", exact: true }).click();
  await page.getByRole("button", { name: "新增资源组", exact: true }).click();
  await page.getByLabel("资源组名称", { exact: true }).fill(group);
  await page.getByLabel("域 ID", { exact: true }).fill(id);
  await proof(page, changed, auth);
  const groupCreated = await resourceMutation(
    page,
    "/api/resources/groups/create",
    "保存资源组",
    false,
  );
  await openGroup(page, group);
  await page.getByRole("button", { name: "调整关联角色", exact: true }).click();
  await page.getByLabel("显式关联 platform_admin", { exact: true }).check();
  await proof(page, changed, auth);
  await resourceMutation(
    page,
    "/api/resources/groups/assign",
    "保存角色关联",
    true,
  );
  await relogin(page, username, changed, auth);
  // Now assert creation/recovery did not submit a diagnostic. This kind is not
  // owner/epoch-filtered, so the grant's epoch change cannot hide an earlier task.
  const initialTasks = await readJSON(
    context,
    `/api/tasks?taskName=domain.connection_test&domainId=${id}`,
  );
  expect(initialTasks.page.total).toBe(0);
  expect(initialTasks.tasks).toEqual([]);
  await openDomains();
  await selectSavedSource(id, "1", "1", "unverified");
  expect(submittedTests).toBe(0);
  await expect(
    page.getByRole("heading", { name: `域连接详情：${domain}`, exact: true }),
  ).toBeVisible();
  const initial = (
    await readJSON(context, `/api/domains/detail?domainId=${id}`)
  ).connection;
  expect(initial).toMatchObject({
    domainId: id,
    domain,
    dcHostName: dc,
    ldapAddr: ldapIP,
    port: "389",
    mode: "starttls",
    revision: "1",
    credentialRevision: "1",
    credentialConfigured: true,
    connectionState: "unverified",
    lastDiagnostic: null,
    latestTaskUUID: "",
  });
  safe(initial);
  await page
    .getByRole("button", { name: "检测已保存连接", exact: true })
    .click();
  await proof(page, changed, auth);
  const first = await post("/api/domains/test", "确认检测保存版本");
  expect(Object.keys(first.sent).sort()).toEqual(
    [
      "domainId",
      "expectedRevision",
      "idempotencyKey",
      "actorPassword",
      "totpCode",
    ].sort(),
  );
  expect(first.value.task).toMatchObject({
    taskName: "domain.connection_test",
    domainId: id,
    maxAttempts: 1,
  });
  await expect(
    page.getByText("TLS、凭据绑定及域命名上下文检测通过。", { exact: true }),
  ).toBeVisible({ timeout: 30_000 });
  const result1 = await readJSON(
    context,
    `/api/domains/test-result?taskUUID=${first.value.task.taskUUID}`,
  );
  expect(result1.task.state).toBe("succeeded");
  expect(result1.diagnostic).toMatchObject({
    code: "ok",
    stage: "complete",
    revision: "1",
    credentialRevision: "1",
    dcHostName: dc,
  });
  safe(result1);
  await page.reload();
  await openDomains();
  await selectSavedSource(
    id,
    "1",
    "1",
    "verified",
    result1.diagnostic.observedAt,
  );
  await page.getByRole("button", { name: "编辑域连接", exact: true }).click();
  await expect(page.getByLabel("AD 用户名", { exact: true })).toHaveCount(0);
  await page.getByLabel("替换凭据", { exact: true }).check();
  await expect(page.getByLabel("AD 密码", { exact: true })).toHaveValue("");
  await page.getByLabel("AD 用户名", { exact: true }).fill(ldapUser);
  await page.getByLabel("AD 密码", { exact: true }).fill(ldapPassword);
  await page.getByLabel("加密连接方式", { exact: true }).selectOption("636");
  await proof(page, changed, auth);
  await post("/api/domains/update", "保存域修改");
  const updated = (
    await readJSON(context, `/api/domains/detail?domainId=${id}`)
  ).connection;
  expect(updated).toMatchObject({
    revision: "2",
    credentialRevision: "2",
    port: "636",
    mode: "ldaps",
    connectionState: "unverified",
    lastDiagnostic: null,
    latestTaskUUID: "",
  });
  safe(updated);
  const stale = await context.request.get(
    `/api/domains/test-result?taskUUID=${first.value.task.taskUUID}`,
  );
  if (stale.status() === 200) {
    const s = await stale.json();
    expect(s.diagnostic).toBeNull();
  } else expect([403, 404, 409]).toContain(stale.status());
  await page
    .getByRole("button", { name: "检测已保存连接", exact: true })
    .click();
  await proof(page, changed, auth);
  const second = await post("/api/domains/test", "确认检测保存版本");
  expect(second.value.task.taskUUID).not.toBe(first.value.task.taskUUID);
  await expect(
    page.getByText("TLS、凭据绑定及域命名上下文检测通过。", { exact: true }),
  ).toBeVisible({ timeout: 30_000 });
  const result2 = await readJSON(
    context,
    `/api/domains/test-result?taskUUID=${second.value.task.taskUUID}`,
  );
  expect(result2.task).toMatchObject({
    state: "succeeded",
    attempt: 1,
    maxAttempts: 1,
  });
  expect(result2.diagnostic).toMatchObject({
    revision: "2",
    credentialRevision: "2",
    code: "ok",
  });
  safe(result2);
  const generic = await readJSON(
    context,
    `/api/tasks/detail?taskUUID=${second.value.task.taskUUID}`,
  );
  expect(generic.task.result).toEqual({});
  expect(generic.task.cursor).toEqual({});
  await page
    .getByRole("button", { name: "返回域连接列表", exact: true })
    .click();
  await page
    .getByRole("button", { name: `查看 ${domain}`, exact: true })
    .click();
  await page.getByRole("button", { name: "删除域连接", exact: true }).click();
  await page.getByLabel("输入域名确认删除", { exact: true }).fill(domain);
  await proof(page, changed, auth);
  await post("/api/domains/delete", "确认删除本地连接");
  await expect(page.getByText(/当前筛选下没有获授权的域连接/)).toBeVisible();
  expect(
    (await context.request.get(`/api/domains/detail?domainId=${id}`)).status(),
  ).toBe(404);
  expect((await readJSON(context, "/api/resources/grants")).list).toEqual([]);
  expect(
    (
      await readJSON(
        context,
        `/api/resources/groups/detail?id=${groupCreated.id}`,
      )
    ).meta.datas,
  ).toEqual([]);
  expect(
    (await readJSON(context, `/api/domains/creation?idempotencyKey=${key}`))
      .receipt.deleted,
  ).toBe(true);
  expect(
    await page.evaluate(() => localStorage.length + sessionStorage.length),
  ).toBe(0);
});
