import { test, expect } from "@playwright/test";
import { createHmac } from "node:crypto";
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
test("real browser → API → PostgreSQL password and MFA lifecycle", async ({
  page,
  context,
}) => {
  const username = process.env.ADTR_E2E_USERNAME,
    password = process.env.ADTR_E2E_PASSWORD;
  if (!username || !password)
    throw new Error(
      "ADTR_E2E_USERNAME and ADTR_E2E_PASSWORD must identify a disposable synthetic bootstrap account",
    );
  const changed = "Changed E2E Password 123";
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page
    .getByLabel("密码", { exact: true })
    .fill("Incorrect Synthetic Password");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("凭据或验证码不正确");
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "多因素认证", exact: true }),
  ).toBeDisabled();
  const initialCookies = await context.cookies();
  expect(
    initialCookies.some((c) => c.httpOnly && c.sameSite === "Strict"),
  ).toBe(true);
  const me = await context.request.get("/api/auth/me");
  expect(me.status()).toBe(200);
  const initialProfile = await me.json();
  expect(initialProfile.needChangePwd).toBe(true);
  expect(initialProfile.username).toBe(username.toLowerCase());
  const noCsrf = await context.request.post("/api/auth/logout", {
    data: {},
    headers: { Origin: new URL(page.url()).origin },
  });
  expect(noCsrf.status()).toBe(403);
  const forcedMfa = await context.request.get("/api/auth/mfa");
  expect(forcedMfa.status()).toBe(403);
  await page.getByLabel("当前密码", { exact: true }).fill(password);
  await page.getByLabel("新密码", { exact: true }).fill(changed);
  await page.getByLabel("确认新密码", { exact: true }).fill(changed);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  const updatedProfile = await (
    await context.request.get("/api/auth/me")
  ).json();
  expect(updatedProfile.needChangePwd).toBe(false);
  expect(updatedProfile.csrfToken).not.toBe(initialProfile.csrfToken);
  const stale = await context.request.get("/api/auth/me", {
    headers: {
      Cookie: initialCookies.map((c) => `${c.name}=${c.value}`).join("; "),
    },
  });
  expect(stale.status()).toBe(401);
  await page.getByRole("button", { name: "多因素认证", exact: true }).click();
  await page.getByLabel("当前密码", { exact: true }).fill(changed);
  await page.getByRole("button", { name: "开始设置", exact: true }).click();
  const secret = await page
    .getByLabel("设置密钥", { exact: true })
    .inputValue();
  expect(secret.length).toBeGreaterThan(10);
  const confirmedNow = Date.now();
  const confirmedCounter = Math.floor(confirmedNow / 30_000);
  await page
    .getByLabel("认证器验证码", { exact: true })
    .fill(totp(secret, confirmedNow));
  await page.getByRole("button", { name: "验证并启用", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用");
  const mfa = await context.request.get("/api/auth/mfa");
  expect(mfa.status()).toBe(200);
  expect(await mfa.json()).toEqual({ hasMfa: true });
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  expect((await context.request.get("/api/auth/me")).status()).toBe(401);
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(changed);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByLabel("二次认证验证码", { exact: true }),
  ).toBeVisible();
  // Confirmation consumes a TOTP counter. Wait for a new window, never bypass replay protection.
  if (Math.floor(Date.now() / 30_000) <= confirmedCounter)
    await new Promise((resolve) =>
      setTimeout(resolve, (confirmedCounter + 1) * 30_000 - Date.now() + 1100),
    );
  await page.getByLabel("二次认证验证码", { exact: true }).fill(totp(secret));
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(() => localStorage.length + sessionStorage.length),
  ).toBe(0);
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
});
