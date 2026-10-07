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
  await page.getByRole("button", { name: "多因素认证", exact: true }).click();
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

async function mutation(
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
  await page.getByRole("button", { name: "资源与租户", exact: true }).click();
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

test("real browser → API → PostgreSQL synthetic resource isolation and tenant revocation", async ({
  page,
  context,
}) => {
  test.setTimeout(300_000);
  // This spec MUST run alone on a freshly migrated disposable bootstrap DB.
  // Parent runner seeds adtr.resource_domains(tenant_id,id,name,active) with
  // (default, synthetic-domain-a, Synthetic Domain A, true). No real AD is used.
  const username = process.env.ADTR_E2E_USERNAME,
    password = process.env.ADTR_E2E_PASSWORD;
  if (!username || !password)
    throw new Error(
      "ADTR_E2E_USERNAME and ADTR_E2E_PASSWORD must identify a fresh disposable resource-test bootstrap account",
    );
  const changed = "Changed Resource Password 123",
    domain = "synthetic-domain-a",
    name = "E2EEmptyResourceGroup",
    renamed = "E2ESyntheticResourceGroup";
  await page.goto("/");
  await login(page, username, password);
  await changeInitialPassword(page, password, changed);
  const authenticator = await enrollMfa(page, changed);
  await openResources(page);
  await expect(page.getByRole("alert")).toContainText("当前租户尚未配置");
  const unconfigured = await context.request.get("/api/resources/tenant");
  expect(unconfigured.status()).toBe(409);
  expect(await unconfigured.json()).toEqual({ error: "tenant_not_configured" });
  await page.getByRole("button", { name: "租户配置", exact: true }).click();
  await page.getByRole("button", { name: "编辑租户配置", exact: true }).click();
  await expect(page.getByLabel("活动域数量上限", { exact: true })).toHaveValue(
    "",
  );
  const expires = Math.floor(Date.now() / 1000) + 86400 * 365;
  await page.getByLabel("活动域数量上限", { exact: true }).fill("1");
  await page
    .getByLabel("失效时间（UTC Unix 秒）", { exact: true })
    .fill(String(expires));
  await page
    .getByLabel("客户 UID", { exact: true })
    .fill("synthetic-browser-customer");
  await page.getByLabel("租户名称", { exact: true }).fill("合成浏览器租户");
  await proof(page, changed, authenticator);
  await mutation(page, "/api/resources/tenant/save", "保存租户配置", true);
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  expect((await context.request.get("/api/auth/me")).status()).toBe(401);
  await relogin(page, username, changed, authenticator);
  expect(await readJSON(context, "/api/resources/tenant")).toEqual({
    maxAdCount: 1,
    expireTime: expires,
    uid: "synthetic-browser-customer",
    name: "合成浏览器租户",
  });
  await openResources(page);
  await expect(
    page.getByRole("status").filter({ hasText: "当前没有获授权的活动域" }),
  ).toBeVisible();
  expect(await readJSON(context, "/api/resources/grants")).toEqual({
    list: [],
    authorizationScopeOnly: true,
  });
  await page.getByLabel("待检查域 ID", { exact: true }).fill(domain);
  await page.getByRole("button", { name: "检查数据授权", exact: true }).click();
  await expect(
    page.getByRole("list", { name: "数据授权检查结果" }),
  ).toContainText(`不允许 · ${domain}`);

  await page.getByRole("button", { name: "资源组", exact: true }).click();
  await expect(
    page.getByRole("status").filter({ hasText: "没有匹配的资源组" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "新增资源组", exact: true }).click();
  await page.getByLabel("资源组名称", { exact: true }).fill(name);
  await page
    .getByLabel("资源组备注", { exact: true })
    .fill("Synthetic browser-only scope fixture");
  await page
    .getByRole("button", { name: "检查资源组名称", exact: true })
    .click();
  await expect(
    page.getByRole("status").filter({ hasText: "当前未占用" }),
  ).toBeVisible();
  await proof(page, changed, authenticator);
  const created = await mutation(
    page,
    "/api/resources/groups/create",
    "保存资源组",
    false,
  );
  expect(typeof created.id).toBe("string");
  expect(created.id.length).toBeGreaterThan(0);
  const id = encodeURIComponent(created.id);
  await expect(
    page.getByRole("status").filter({ hasText: "资源组已创建" }),
  ).toBeVisible();
  expect(
    (await readJSON(context, `/api/resources/groups/detail?id=${id}`)).meta,
  ).toMatchObject({
    id: created.id,
    name,
    datas: [],
    applyRoleCount: 0,
    mark: "Synthetic browser-only scope fixture",
  });
  expect(
    await readJSON(
      context,
      `/api/resources/groups/exists?name=${name.toLowerCase()}`,
    ),
  ).toEqual({ isExist: true });
  await page.reload();
  await openResources(page);
  await openGroup(page, name);
  await expect(
    page.getByText("空资源组，不授予任何域访问权限。"),
  ).toBeVisible();
  await page.getByRole("button", { name: "编辑资源组", exact: true }).click();
  await page.getByLabel("资源组名称", { exact: true }).fill(renamed);
  await page.getByLabel("域 ID", { exact: true }).fill(domain);
  await proof(page, changed, authenticator);
  await mutation(page, "/api/resources/groups/update", "保存资源组", false);
  await expect(
    page.getByRole("status").filter({ hasText: "资源组已更新" }),
  ).toBeVisible();
  expect(
    (await readJSON(context, `/api/resources/groups/detail?id=${id}`)).meta,
  ).toMatchObject({
    id: created.id,
    name: renamed,
    datas: [{ appName: "ad", resources: [domain] }],
    applyRoleCount: 0,
  });
  const searched = await readJSON(
    context,
    `/api/resources/groups?name=${renamed}&pageIdx=1&pageSize=10&sort=true`,
  );
  expect(searched.page.total).toBe(1);
  expect(searched.metas[0].id).toBe(created.id);
  expect(await readJSON(context, "/api/resources/grants")).toEqual({
    list: [],
    authorizationScopeOnly: true,
  });

  await page
    .getByRole("button", { name: `查看 ${renamed}`, exact: true })
    .click();
  await page.getByRole("button", { name: "调整关联角色", exact: true }).click();
  await expect(
    page.getByLabel("显式关联 platform_admin", { exact: true }),
  ).not.toBeChecked();
  await page.getByLabel("显式关联 platform_admin", { exact: true }).check();
  await proof(page, changed, authenticator);
  await mutation(page, "/api/resources/groups/assign", "保存角色关联", true);
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  expect((await context.request.get("/api/resources/grants")).status()).toBe(
    401,
  );
  await relogin(page, username, changed, authenticator);
  const roles = await readJSON(
    context,
    `/api/resources/groups/roles?id=${id}&pageIdx=1&pageSize=-1`,
  );
  expect(roles.details).toEqual([
    { id: "platform_admin", name: "platform_admin", mark: "" },
  ]);
  expect(roles.page.total).toBe(1);
  expect(roles.exhausted).toBe(true);
  expect(
    (await readJSON(context, `/api/resources/groups/detail?id=${id}`)).meta
      .applyRoleCount,
  ).toBe(1);
  expect(await readJSON(context, "/api/resources/grants")).toEqual({
    list: [{ application: "ad", resources: [domain] }],
    authorizationScopeOnly: true,
  });
  await openResources(page);
  await expect(
    page.getByRole("list", { name: "当前获授权的域" }),
  ).toContainText(domain);
  await page
    .getByLabel("待检查域 ID", { exact: true })
    .fill(`${domain}\nunknown-domain\n${domain},unknown-domain`);
  await page.getByRole("button", { name: "检查数据授权", exact: true }).click();
  const items = page
    .getByRole("list", { name: "数据授权检查结果" })
    .getByRole("listitem");
  await expect(items).toHaveCount(3);
  await expect(items.nth(0)).toContainText("允许（仅范围）");
  await expect(items.nth(1)).toContainText("不允许");
  await expect(items.nth(2)).toContainText("不允许");
  const session = await readJSON(context, "/api/auth/me"),
    headers = {
      Origin: new URL(page.url()).origin,
      "X-CSRF-Token": session.csrfToken,
    };
  for (const resourceType of [0, 1]) {
    const unsupported = await context.request.post("/api/resources/check", {
      headers,
      data: {
        resourceType,
        resources: [{ application: "ad", dataResource: [domain] }],
      },
    });
    expect(unsupported.status()).toBe(422);
    expect(await unsupported.json()).toEqual({
      error: "unsupported_resource_type",
    });
  }
  const numeric = await context.request.post("/api/resources/check", {
    headers,
    data: {
      resourceType: 2,
      resources: [{ application: 1, dataResource: [domain] }],
    },
  });
  expect(numeric.status()).toBe(400);
  expect(await numeric.json()).toEqual({ error: "invalid_input" });
  const forgedTenant = await context.request.post("/api/resources/check", {
    headers,
    data: {
      tenant: "another",
      resourceType: 2,
      resources: [{ application: "ad", dataResource: [domain] }],
    },
  });
  expect(forgedTenant.status()).toBe(400);
  expect(await forgedTenant.json()).toEqual({ error: "invalid_input" });

  await openGroup(page, renamed);
  await page.getByRole("button", { name: "删除资源组", exact: true }).click();
  await proof(page, changed, authenticator);
  await mutation(page, "/api/resources/groups/delete", "确认删除资源组", true);
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await relogin(page, username, changed, authenticator);
  expect(
    (
      await context.request.get(`/api/resources/groups/detail?id=${id}`)
    ).status(),
  ).toBe(404);
  expect((await readJSON(context, "/api/resources/groups")).page.total).toBe(0);
  expect(await readJSON(context, "/api/resources/grants")).toEqual({
    list: [],
    authorizationScopeOnly: true,
  });
  await openResources(page);
  await expect(
    page.getByRole("status").filter({ hasText: "当前没有获授权的活动域" }),
  ).toBeVisible();
  expect(
    await page.evaluate(() => localStorage.length + sessionStorage.length),
  ).toBe(0);
});
