import { navigateTo } from "./navigation";
import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { createHmac } from "node:crypto";

type Permission = {
  mark: string;
  auth: { readable: boolean; writeable: boolean };
};
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

async function submitMutation(page: Page, path: string, button: string) {
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
  expect(result).toMatchObject({ result: "SUCCESS", sessionRevoked: false });
  return result;
}

test("real browser → API → PostgreSQL users and function permissions", async ({
  page,
  context,
}) => {
  test.setTimeout(300_000);
  // Run only against the access runner's fresh disposable database. The auth
  // lifecycle spec changes its own bootstrap account and must run separately.
  const username = process.env.ADTR_E2E_USERNAME,
    password = process.env.ADTR_E2E_PASSWORD;
  if (!username || !password)
    throw new Error(
      "ADTR_E2E_USERNAME and ADTR_E2E_PASSWORD must identify a disposable synthetic bootstrap account",
    );
  const changed = "Changed E2E Password 123";
  const readerUsername = "e2e.viewer";
  const readerInitial = "Initial Reader Password 123";
  const readerChanged = "Changed Reader Password 456";
  const roleName = "E2E Users Read Only";
  const roleRemark = "Synthetic browser access test";
  const profile = {
    mobile: "13800000000",
    email: "e2e.viewer@example.invalid",
    remark: "Synthetic user created through the browser",
    address: "合成测试所在地",
    realName: "合成测试用户",
    department: "合成测试部门",
    post: "合成测试岗位",
  };

  await page.goto("/");
  await login(page, username, password);
  await changeInitialPassword(page, password, changed);
  const adminMfa = await enrollMfa(page, changed);
  await navigateTo(page, "访问管理");
  for (const name of ["用户管理", "角色管理", "功能权限"])
    await expect(page.getByRole("button", { name, exact: true })).toBeVisible();
  const adminMenu = await readJSON(context, "/api/access/menu");
  expect(adminMenu.menu.map((node: Permission) => node.mark).sort()).toEqual([
    "audit",
    "audit_exports",
    "directory_assets",
    "domains",
    "operation_accounts",
    "permissions",
    "roles",
    "schedules",
    "system",
    "system_logs",
    "task_archive",
    "tasks",
    "users",
  ]);

  await page.getByRole("button", { name: "角色管理", exact: true }).click();
  await page.getByRole("button", { name: "新增角色", exact: true }).click();
  await page.getByLabel("角色名称", { exact: true }).fill(roleName);
  await page.getByLabel("角色备注", { exact: true }).fill(roleRemark);
  await page
    .getByRole("checkbox", { name: "用户管理：读取", exact: true })
    .check();
  await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(adminMfa));
  const savedRole = await submitMutation(
    page,
    "/api/access/roles/save",
    "保存角色",
  );
  await expect(
    page.getByRole("status").filter({ hasText: "角色已保存" }),
  ).toBeVisible();
  expect(typeof savedRole.roleID).toBe("string");
  expect(savedRole.roleID.length).toBeGreaterThan(0);
  const roleID: string = savedRole.roleID;
  const persistedRole = await readJSON(
    context,
    `/api/access/roles/detail?roleID=${encodeURIComponent(roleID)}`,
  );
  expect(persistedRole.role).toMatchObject({
    id: roleID,
    name: roleName,
    remark: roleRemark,
    userNum: 0,
  });
  const permissions = await readJSON(
    context,
    `/api/access/permissions?roleID=${encodeURIComponent(roleID)}`,
  );
  expect(
    permissions.permissions.map(({ mark, auth }: Permission) => ({
      mark,
      auth,
    })),
  ).toEqual([
    { mark: "users", auth: { readable: true, writeable: false } },
    { mark: "roles", auth: { readable: false, writeable: false } },
    { mark: "permissions", auth: { readable: false, writeable: false } },
    { mark: "tasks", auth: { readable: false, writeable: false } },
    { mark: "audit", auth: { readable: false, writeable: false } },
    { mark: "audit_exports", auth: { readable: false, writeable: false } },
    { mark: "system", auth: { readable: false, writeable: false } },
    { mark: "schedules", auth: { readable: false, writeable: false } },
    { mark: "task_archive", auth: { readable: false, writeable: false } },
    { mark: "domains", auth: { readable: false, writeable: false } },
    { mark: "operation_accounts", auth: { readable: false, writeable: false } },
    { mark: "system_logs", auth: { readable: false, writeable: false } },
    { mark: "directory_assets", auth: { readable: false, writeable: false } },
  ]);
  expect(persistedRole.permissions).toEqual(permissions.permissions);

  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  await page.getByLabel("用户名", { exact: true }).fill(readerUsername);
  await page.getByLabel("初始密码", { exact: true }).fill(readerInitial);
  await page.getByLabel("确认初始密码", { exact: true }).fill(readerInitial);
  await page.getByLabel("角色", { exact: true }).fill(roleID);
  for (const [label, value] of [
    ["手机号", profile.mobile],
    ["邮箱", profile.email],
    ["备注", profile.remark],
    ["所在地", profile.address],
    ["真实姓名", profile.realName],
    ["部门", profile.department],
    ["岗位", profile.post],
  ])
    await page.getByLabel(label, { exact: true }).fill(value);
  await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(adminMfa));
  const savedUser = await submitMutation(
    page,
    "/api/access/users/create",
    "创建用户",
  );
  await expect(
    page.getByRole("status").filter({ hasText: "用户已创建" }),
  ).toBeVisible();
  await expect(
    page.getByRole("rowheader").filter({ hasText: readerUsername }),
  ).toBeVisible();
  expect(savedUser.ID).toBeGreaterThan(0);
  const users = await readJSON(
    context,
    `/api/access/users?${new URLSearchParams({
      search: readerUsername,
      filterRole: roleID,
      filterMfaStatus: "stop",
    })}`,
  );
  expect(users.page.total).toBe(1);
  expect(users.List).toHaveLength(1);
  expect(users.List[0]).toMatchObject({
    ...profile,
    ID: savedUser.ID,
    username: readerUsername,
    role: "viewer",
    priv: 2,
    roleID,
    roleName,
    hasMfa: false,
    disabled: false,
  });
  for (const secret of [
    "password",
    "passwordHash",
    "totpCode",
    "mfaSecret",
    "csrfToken",
  ])
    expect(users.List[0]).not.toHaveProperty(secret);
  expect(
    await readJSON(
      context,
      `/api/access/users/exists?username=${encodeURIComponent(readerUsername)}`,
    ),
  ).toEqual({ result: true });
  expect(
    (
      await readJSON(
        context,
        `/api/access/roles/detail?roleID=${encodeURIComponent(roleID)}`,
      )
    ).role.userNum,
  ).toBe(1);

  // A second browser context proves that an administrator reset revokes the
  // target's real cookie session, then requires a password change at next login.
  const readerContext = await context.browser()!.newContext({
    baseURL: process.env.ADTR_E2E_BASE_URL ?? "http://127.0.0.1:18080",
  });
  const readerPage = await readerContext.newPage();
  await readerPage.goto("/");
  await login(readerPage, readerUsername, readerInitial);
  await expect(
    readerPage.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  expect((await readJSON(readerContext, "/api/auth/me")).needChangePwd).toBe(
    true,
  );
  const readerReset = "Reset Reader Password 789";
  await navigateTo(page, "重置用户密码");
  await page.getByLabel("目标用户名", { exact: true }).fill(readerUsername);
  await page.getByLabel("用户新密码", { exact: true }).fill(readerReset);
  await page.getByLabel("确认用户新密码", { exact: true }).fill(readerReset);
  await page.getByLabel("管理员当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("管理员认证器验证码", { exact: true })
    .fill(await freshCode(adminMfa));
  const [resetResponse] = await Promise.all([
    page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === "/api/auth/reset-password" &&
        response.request().method() === "POST",
    ),
    page.getByRole("button", { name: "确认重置密码", exact: true }).click(),
  ]);
  expect(resetResponse.status()).toBe(200);
  expect(await resetResponse.json()).toEqual({ result: "SUCCESS" });
  await expect(page.getByRole("status")).toContainText("用户密码已重置");
  expect((await readerContext.request.get("/api/auth/me")).status()).toBe(401);
  await readerPage.reload();
  try {
    await login(readerPage, readerUsername, readerReset);
    await expect(
      readerPage.getByRole("heading", { name: "修改密码", exact: true }),
    ).toBeVisible();
    const firstReaderSession = await readJSON(readerContext, "/api/auth/me");
    expect(firstReaderSession).toMatchObject({
      username: readerUsername,
      needChangePwd: true,
    });
    const forcedAccess = await readerContext.request.get("/api/access/users");
    expect(forcedAccess.status()).toBe(403);
    expect(await forcedAccess.json()).toEqual({
      error: "password_change_required",
    });
    await changeInitialPassword(readerPage, readerReset, readerChanged);
    // Give the reader valid MFA proof too, so write denial demonstrates its
    // persisted role boundary rather than missing enrollment or invalid proof.
    const readerMfa = await enrollMfa(readerPage, readerChanged);
    await readerPage.reload();
    await expect(
      readerPage.getByRole("heading", { name: "账户概览", exact: true }),
    ).toBeVisible();
    await navigateTo(readerPage, "访问管理");
    await expect(
      readerPage.getByRole("button", { name: "用户管理", exact: true }),
    ).toBeVisible();
    await readerPage
      .getByRole("button", { name: "用户管理", exact: true })
      .click();
    await expect(
      readerPage.getByRole("rowheader").filter({ hasText: readerUsername }),
    ).toBeVisible();
    for (const name of ["角色管理", "功能权限", "新增用户"])
      await expect(
        readerPage.getByRole("button", { name, exact: true }),
      ).toHaveCount(0);
    const readerMenu = await readJSON(readerContext, "/api/access/menu");
    expect(readerMenu.menu.map((node: Permission) => node.mark)).toEqual([
      "users",
    ]);
    expect(readerMenu.menu[0].auth).toEqual({
      readable: true,
      writeable: false,
    });
    const readerSession = await readJSON(readerContext, "/api/auth/me");
    expect(readerSession.needChangePwd).toBe(false);
    expect(readerSession.hasMfa).toBe(true);
    const headers = {
      Origin: new URL(readerPage.url()).origin,
      "X-CSRF-Token": readerSession.csrfToken,
    };
    const checked = await readerContext.request.post("/api/access/check", {
      headers,
      data: {
        paths: [
          "GET /api/access/users",
          "POST /api/access/users/create",
          "GET /api/access/roles",
          "GET /api/access/permissions",
          "POST /api/access/assignments",
          "GET /api/access/not-registered",
          "POST /api/access/users",
        ],
      },
    });
    expect(checked.status()).toBe(200);
    expect(await checked.json()).toEqual({
      results: [true, false, false, false, false, false, false],
    });
    const allowedUsers = await readJSON(
      readerContext,
      `/api/access/users?search=${encodeURIComponent(readerUsername)}`,
    );
    expect(allowedUsers.List).toHaveLength(1);
    expect(allowedUsers.List[0]).toMatchObject({
      ID: savedUser.ID,
      username: readerUsername,
      roleID,
      roleName,
      ...profile,
    });
    for (const path of [
      "/api/access/roles",
      `/api/access/permissions?roleID=${encodeURIComponent(roleID)}`,
    ]) {
      const deniedRead = await readerContext.request.get(path);
      expect(deniedRead.status()).toBe(403);
      expect(await deniedRead.json()).toEqual({ error: "forbidden" });
    }
    const deniedWrite = await readerContext.request.post(
      "/api/access/users/create",
      {
        headers,
        data: {
          username: "e2e.forbidden",
          password: "Forbidden Synthetic Password 789",
          roleID: "viewer",
          actorPassword: readerChanged,
          totpCode: await freshCode(readerMfa),
        },
      },
    );
    expect(deniedWrite.status()).toBe(403);
    expect(await deniedWrite.json()).toEqual({ error: "forbidden" });
    expect(
      await readJSON(
        readerContext,
        "/api/access/users/exists?username=e2e.forbidden",
      ),
    ).toEqual({ result: false });
    expect(
      await readerPage.evaluate(
        () => localStorage.length + sessionStorage.length,
      ),
    ).toBe(0);
    await navigateTo(readerPage, "退出登录");
    await expect(
      readerPage.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    expect(
      (await readerContext.request.get("/api/access/users")).status(),
    ).toBe(401);
  } finally {
    await readerContext.close();
  }
});
