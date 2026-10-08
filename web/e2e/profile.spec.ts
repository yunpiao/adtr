import {
  test,
  expect,
  type APIResponse,
  type BrowserContext,
  type Page,
} from "@playwright/test";
import { createHmac } from "node:crypto";

const avatarPath = "/api/profile/avatar";
const successMessage = "头像已更新，已重新读取服务器保存的图片。";
const recoveryMessage =
  "上传结果尚未确认，请重新读取当前头像后再决定是否上传。";
type Upload = { name: string; mimeType: string; buffer: Buffer };
type Session = { ID: number; csrfToken: string; hasMfa: boolean };

function totp(secret: string, now: number): string {
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

async function readJSON(context: BrowserContext, path: string) {
  const response = await context.request.get(path);
  expect(response.status(), `GET ${path}`).toBe(200);
  return response.json();
}

function privateHeaders(response: APIResponse) {
  expect(response.headers()).toMatchObject({
    "cache-control": "no-store",
    "x-content-type-options": "nosniff",
    "cross-origin-resource-policy": "same-origin",
  });
}

async function readAvatar(
  context: BrowserContext,
  userId: number,
): Promise<Buffer> {
  const response = await context.request.get(avatarPath);
  expect(response.status()).toBe(200);
  privateHeaders(response);
  expect(response.headers()).toMatchObject({
    "content-type": "image/png",
    "content-disposition": 'inline; filename="avatar.png"',
    "x-profile-user-id": String(userId),
  });
  const bytes = await response.body();
  expect(bytes.subarray(0, 8)).toEqual(
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
  );
  return bytes;
}

async function expectError(
  response: APIResponse,
  status: number,
  error: string,
  userId?: number,
) {
  expect(response.status()).toBe(status);
  privateHeaders(response);
  if (userId !== undefined)
    expect(response.headers()["x-profile-user-id"]).toBe(String(userId));
  expect(await response.json()).toEqual({ error });
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

async function openProfile(page: Page) {
  await page.getByRole("button", { name: "个人资料", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "个人资料", exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("选择头像", { exact: true })).toBeEnabled();
}

async function imageFile(
  page: Page,
  color: string,
  width: number,
  mimeType = "image/png",
  height = width,
): Promise<Upload> {
  // Synthetic raster fixtures are generated locally by the real browser. No
  // external URLs, image libraries, database injection or API mocks are used.
  const encoded = await page.evaluate(
    ({ color, width, height, mimeType }) => {
      const canvas = document.createElement("canvas");
      canvas.width = width;
      canvas.height = height;
      const drawing = canvas.getContext("2d");
      if (!drawing) throw new Error("Canvas context unavailable");
      drawing.fillStyle = color;
      drawing.fillRect(0, 0, width, height);
      return canvas.toDataURL(mimeType).split(",")[1];
    },
    { color, width, height, mimeType },
  );
  return {
    name: mimeType === "image/jpeg" ? "synthetic.jpg" : "synthetic.png",
    mimeType,
    buffer: Buffer.from(encoded, "base64"),
  };
}

function rotatedJPEG(jpeg: Buffer): Buffer {
  expect(jpeg.subarray(0, 2)).toEqual(Buffer.from([0xff, 0xd8]));
  // Minimal little-endian EXIF IFD with orientation 6 (90 degrees clockwise).
  // The browser decodes 3x2 as 2x3; server PNG normalization strips metadata.
  const exif = Buffer.alloc(32);
  exif.write("Exif\0\0", 0, "ascii");
  exif.write("II", 6, "ascii");
  exif.writeUInt16LE(42, 8);
  exif.writeUInt32LE(8, 10);
  exif.writeUInt16LE(1, 14);
  exif.writeUInt16LE(0x0112, 16);
  exif.writeUInt16LE(3, 18);
  exif.writeUInt32LE(1, 20);
  exif.writeUInt16LE(6, 24);
  const app1 = Buffer.from([0xff, 0xe1, 0, exif.length + 2]);
  return Buffer.concat([jpeg.subarray(0, 2), app1, exif, jpeg.subarray(2)]);
}

async function expectPixels(page: Page, rgb: number[], tolerance = 0) {
  const avatar = page.getByRole("img", { name: "当前头像", exact: true });
  await expect(avatar).toBeVisible();
  await expect
    .poll(async () => {
      const pixel = await avatar.evaluate((element) => {
        if (!(element instanceof HTMLCanvasElement))
          throw new Error("The authenticated avatar must render on a canvas");
        const drawing = element.getContext("2d");
        if (!drawing) throw new Error("Avatar canvas context unavailable");
        return Array.from(
          drawing.getImageData(
            Math.floor(element.width / 2),
            Math.floor(element.height / 2),
            1,
            1,
          ).data,
        );
      });
      return (
        pixel[3] === 255 &&
        rgb.every(
          (channel, index) => Math.abs(pixel[index] - channel) <= tolerance,
        )
      );
    })
    .toBe(true);
}

async function upload(page: Page, file: Upload, userId: number) {
  await page.getByLabel("选择头像", { exact: true }).setInputFiles(file);
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === avatarPath &&
        candidate.request().method() === "POST",
    ),
    page.getByRole("button", { name: /^(上传头像|替换头像)$/ }).click(),
  ]);
  expect(response.request().postDataJSON()).toEqual({
    userId,
    file: file.buffer.toString("base64"),
  });
  expect(response.request().headers()["x-csrf-token"]).toBeTruthy();
  expect(response.status()).toBe(200);
  expect(await response.json()).toEqual({ result: "success" });
  await expect(page.getByRole("status")).toContainText(successMessage);
  await expect(page.getByLabel("选择头像", { exact: true })).toHaveValue("");
}

async function uploadAuditCount(context: BrowserContext) {
  const audit = await readJSON(context, "/api/audit?pageSize=-1");
  return audit.List.filter(
    (row: { event: string }) => row.event === "profile_avatar_update",
  ).length;
}

test("real browser → API → PostgreSQL private self profile and avatar lifecycle", async ({
  page,
  context,
}, testInfo) => {
  test.setTimeout(180_000);
  // The profile harness must provide its own freshly migrated disposable DB.
  // Other lifecycle suites mutate their bootstrap account and cannot share it.
  const username = process.env.ADTR_E2E_USERNAME;
  const password = process.env.ADTR_E2E_PASSWORD;
  if (!username || !password)
    throw new Error(
      "Profile E2E requires a fresh disposable synthetic bootstrap account",
    );
  const changed = "Changed Profile Password 123";
  const viewerUsername = "e2e.profile.viewer";
  const viewerInitial = "Initial Profile Viewer 123";
  const viewerChanged = "Changed Profile Viewer 456";
  const viewerFields = {
    mobile: "13800000000",
    email: "profile.viewer@example.invalid",
    remark: "Synthetic profile browser fixture",
    address: "合成测试所在地",
    realName: "合成资料用户",
    department: "合成测试部门",
    post: "合成测试岗位",
  };
  await page.goto("/");
  const origin = new URL(page.url()).origin;
  const red = await imageFile(page, "#ff0000", 2);
  const marker = Buffer.from("synthetic-appended-content-must-not-be-stored");
  red.buffer = Buffer.concat([red.buffer, marker]);
  const green = await imageFile(page, "#00ff00", 3, "image/jpeg", 2);
  green.buffer = rotatedJPEG(green.buffer);
  const blue = await imageFile(page, "#0000ff", 4);
  const yellow = await imageFile(page, "#ffff00", 5);
  const oversizedDimensions = await imageFile(page, "#ffffff", 1025);
  let browserUploads = 0;
  page.on("request", (request) => {
    if (
      new URL(request.url()).pathname === avatarPath &&
      request.method() === "POST"
    )
      browserUploads += 1;
  });

  await test.step("private routes reject anonymous and forced-change sessions", async () => {
    for (const path of ["/api/profile/me", avatarPath])
      await expectError(
        await context.request.get(path),
        401,
        "unauthenticated",
      );
    await expectError(
      await context.request.post(avatarPath, {
        headers: { Origin: origin },
        data: { userId: 1, file: red.buffer.toString("base64") },
      }),
      401,
      "unauthenticated",
    );
    await login(page, username, password);
    await expect(
      page.getByRole("heading", { name: "修改密码", exact: true }),
    ).toBeVisible();
    await expectError(
      await context.request.get("/api/profile/me"),
      403,
      "password_change_required",
    );
    await changeInitialPassword(page, password, changed);
  });

  const admin = (await readJSON(context, "/api/auth/me")) as Session;
  const headers = { Origin: origin, "X-CSRF-Token": admin.csrfToken };
  let persistedGreen: Buffer;

  await test.step("self read, PNG upload and JPEG replacement persist sanitized PNG", async () => {
    const profileResponse = await context.request.get("/api/profile/me");
    expect(profileResponse.status()).toBe(200);
    privateHeaders(profileResponse);
    const profile = await profileResponse.json();
    expect(profile).toMatchObject({
      ID: admin.ID,
      username: username.toLowerCase(),
      role: "platform_admin",
      roleID: "platform_admin",
      avatar: "",
      disabled: false,
    });
    for (const secret of ["password", "passwordHash", "mfaSecret", "csrfToken"])
      expect(profile).not.toHaveProperty(secret);
    await expectError(
      await context.request.get(avatarPath),
      404,
      "avatar_not_found",
      admin.ID,
    );
    await openProfile(page);
    await expect(page.getByRole("img", { name: "当前头像" })).toHaveCount(0);
    await upload(page, red, admin.ID);
    await expectPixels(page, [255, 0, 0]);
    const persistedRed = await readAvatar(context, admin.ID);
    expect(persistedRed.includes(marker)).toBe(false);
    expect(persistedRed.equals(red.buffer)).toBe(false);
    expect((await readJSON(context, "/api/profile/me")).avatar).toBe(
      avatarPath,
    );
    await upload(page, green, admin.ID);
    await expectPixels(page, [0, 255, 0], 2);
    persistedGreen = await readAvatar(context, admin.ID);
    expect(persistedGreen.readUInt32BE(16)).toBe(3);
    expect(persistedGreen.readUInt32BE(20)).toBe(2);
    expect(persistedGreen.includes(Buffer.from("Exif\0\0"))).toBe(false);
    expect(persistedGreen.equals(persistedRed)).toBe(false);
    expect(persistedGreen.equals(green.buffer)).toBe(false);
    await page.reload();
    await openProfile(page);
    await expectPixels(page, [0, 255, 0], 2);
    expect(await readAvatar(context, admin.ID)).toEqual(persistedGreen);
    expect(browserUploads).toBe(2);
  });

  await test.step("invalid files, scope injection, missing CSRF and foreign origin cannot replace the avatar", async () => {
    const validBody = {
      userId: admin.ID,
      file: blue.buffer.toString("base64"),
    };
    for (const invalid of [
      {
        name: "synthetic.svg",
        mimeType: "image/svg+xml",
        buffer: Buffer.from("<svg/>"),
      },
      {
        name: "broken.png",
        mimeType: "image/png",
        buffer: Buffer.from("not a PNG"),
      },
      { ...oversizedDimensions, name: "too-wide.png" },
      {
        name: "too-large.png",
        mimeType: "image/png",
        buffer: Buffer.alloc(2 * 1024 * 1024 + 1),
      },
    ]) {
      await page.getByLabel("选择头像", { exact: true }).setInputFiles(invalid);
      await expect(page.getByRole("alert")).toBeVisible();
      await expect(page.getByLabel("选择头像", { exact: true })).toBeEnabled();
      await expect(
        page.getByRole("button", { name: /^(上传头像|替换头像)$/ }),
      ).toBeDisabled();
      expect(browserUploads).toBe(2);
    }
    for (const file of [
      "not-canonical-base64",
      "https://example.invalid/avatar.png",
      `data:image/png;base64,${validBody.file}`,
      Buffer.from("<svg/>").toString("base64"),
      oversizedDimensions.buffer.toString("base64"),
      Buffer.alloc(2 * 1024 * 1024 + 1).toString("base64"),
    ])
      await expectError(
        await context.request.post(avatarPath, {
          headers,
          data: { ...validBody, file },
        }),
        400,
        "invalid_avatar",
      );
    await expectError(
      await context.request.post(avatarPath, {
        headers,
        data: { ...validBody, tenantId: "other" },
      }),
      400,
      "invalid_input",
    );
    const rejectedHeaders: Record<string, string>[] = [
      { Origin: origin },
      { Origin: origin, "X-CSRF-Token": "incorrect-csrf" },
      { ...headers, Origin: "https://example.invalid" },
    ];
    for (const requestHeaders of rejectedHeaders)
      await expectError(
        await context.request.post(avatarPath, {
          headers: requestHeaders,
          data: validBody,
        }),
        403,
        "forbidden",
      );
    for (const path of [
      "/api/profile/me?userId=999",
      `${avatarPath}?userId=999`,
    ])
      await expectError(await context.request.get(path), 400, "invalid_input");
    expect(await readAvatar(context, admin.ID)).toEqual(persistedGreen);
    expect(await uploadAuditCount(context)).toBe(2);
  });

  let persistedBlue: Buffer;
  await test.step("lost committed response requires a read and never replays the upload", async () => {
    let finish!: (
      result: { status: number; body: unknown } | { error: unknown },
    ) => void;
    const committed = new Promise<
      { status: number; body: unknown } | { error: unknown }
    >((resolve) => {
      finish = resolve;
    });
    await page.route("**/api/profile/avatar", async (route) => {
      if (route.request().method() !== "POST") {
        await route.continue();
        return;
      }
      try {
        // The real server commits to PostgreSQL. Only delivery of that real
        // response is interrupted; no API response or success is fabricated.
        const actual = await route.fetch({ maxRetries: 0 });
        const result = { status: actual.status(), body: await actual.json() };
        await route.abort("connectionreset");
        finish(result);
      } catch (error) {
        await route.abort().catch(() => {});
        finish({ error });
      }
    });
    await page.getByLabel("选择头像", { exact: true }).setInputFiles(blue);
    await page.getByRole("button", { name: "替换头像", exact: true }).click();
    expect(await committed).toEqual({
      status: 200,
      body: { result: "success" },
    });
    await expect(page.getByRole("alert")).toContainText(recoveryMessage);
    await expect(
      page.getByRole("button", { name: /^(上传头像|替换头像)$/ }),
    ).toBeDisabled();
    await expect(page.getByLabel("选择头像", { exact: true })).toHaveValue("");
    expect(browserUploads).toBe(3);
    persistedBlue = await readAvatar(context, admin.ID);
    expect(persistedBlue.equals(persistedGreen)).toBe(false);
    expect(await uploadAuditCount(context)).toBe(3);
    const [recovered] = await Promise.all([
      page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === avatarPath &&
          response.request().method() === "GET",
      ),
      page
        .getByRole("button", { name: "重新读取当前头像", exact: true })
        .click(),
    ]);
    expect(recovered.status()).toBe(200);
    await expectPixels(page, [0, 0, 255]);
    // A read establishes the current pixels, not whether an interrupted write
    // has finished. Retain the warning; do not turn recovery into fake success.
    await expect(page.getByRole("alert")).toContainText(recoveryMessage);
    await expect(
      page.getByRole("status").filter({ hasText: successMessage }),
    ).toHaveCount(0);
    await page.unroute("**/api/profile/avatar");
    await page.reload();
    await openProfile(page);
    await expectPixels(page, [0, 0, 255]);
    expect(browserUploads).toBe(3);
    expect(await uploadAuditCount(context)).toBe(3);
    expect(await readAvatar(context, admin.ID)).toEqual(persistedBlue);
  });

  let viewerID: number;
  await test.step("create a genuine viewer fixture with no management grants", async () => {
    await page.getByRole("button", { name: "多因素认证", exact: true }).click();
    await page.getByLabel("当前密码", { exact: true }).fill(changed);
    await page.getByRole("button", { name: "开始设置", exact: true }).click();
    const secret = await page
      .getByLabel("设置密钥", { exact: true })
      .inputValue();
    const enrolledAt = Date.now();
    await page
      .getByLabel("认证器验证码", { exact: true })
      .fill(totp(secret, enrolledAt));
    await page.getByRole("button", { name: "验证并启用", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("多因素认证已启用");
    // Enrollment consumes its TOTP counter. Wait for a real unused counter;
    // never disable authentication or seed a privileged fixture in SQL.
    const nextCounterAt = (Math.floor(enrolledAt / 30_000) + 1) * 30_000 + 1100;
    while (Date.now() < nextCounterAt)
      await new Promise((resolve) =>
        setTimeout(resolve, nextCounterAt - Date.now()),
      );
    const session = (await readJSON(context, "/api/auth/me")) as Session;
    const created = await context.request.post("/api/access/users/create", {
      headers: { Origin: origin, "X-CSRF-Token": session.csrfToken },
      data: {
        username: viewerUsername,
        password: viewerInitial,
        roleID: "viewer",
        ...viewerFields,
        actorPassword: changed,
        totpCode: totp(secret, Date.now()),
      },
    });
    expect(created.status()).toBe(200);
    const result = await created.json();
    expect(result).toMatchObject({ result: "SUCCESS", sessionRevoked: false });
    viewerID = result.ID;
    expect(viewerID).toBeGreaterThan(0);
    expect(viewerID).not.toBe(admin.ID);
    await expectError(
      await context.request.post(avatarPath, {
        headers: { Origin: origin, "X-CSRF-Token": session.csrfToken },
        data: { userId: viewerID, file: blue.buffer.toString("base64") },
      }),
      403,
      "forbidden",
    );
    expect(await readAvatar(context, admin.ID)).toEqual(persistedBlue);
  });

  await test.step("logout discards private pixels and selection before switching users", async () => {
    await openProfile(page);
    await expectPixels(page, [0, 0, 255]);
    await page.getByLabel("选择头像", { exact: true }).setInputFiles(yellow);
    const staleCookies = await context.cookies();
    await page.getByRole("button", { name: "退出登录", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("img", { name: "当前头像" })).toHaveCount(0);
    await expect(page.getByLabel("选择头像", { exact: true })).toHaveCount(0);
    for (const path of ["/api/profile/me", avatarPath]) {
      await expectError(
        await context.request.get(path),
        401,
        "unauthenticated",
      );
      await expectError(
        await context.request.get(path, {
          headers: {
            Cookie: staleCookies
              .map((cookie) => `${cookie.name}=${cookie.value}`)
              .join("; "),
          },
        }),
        401,
        "unauthenticated",
      );
    }
    expect(
      await page.evaluate(() => localStorage.length + sessionStorage.length),
    ).toBe(0);
    await login(page, viewerUsername, viewerInitial);
    await changeInitialPassword(page, viewerInitial, viewerChanged);
    await openProfile(page);
    await expect(page.getByRole("img", { name: "当前头像" })).toHaveCount(0);
    await expect(page.getByLabel("选择头像", { exact: true })).toHaveValue("");
    await expectError(
      await context.request.get(avatarPath),
      404,
      "avatar_not_found",
      viewerID,
    );
  });

  await test.step("a viewer reads and updates only their own profile without MFA or grants", async () => {
    const viewer = (await readJSON(context, "/api/auth/me")) as Session;
    expect(viewer.ID).toBe(viewerID);
    expect(viewer.hasMfa).toBe(false);
    const profile = await readJSON(context, "/api/profile/me");
    expect(profile).toMatchObject({
      ID: viewerID,
      username: viewerUsername,
      role: "viewer",
      roleID: "viewer",
      priv: 3,
      hasMfa: false,
      avatar: "",
      ...viewerFields,
    });
    for (const value of Object.values(viewerFields))
      await expect(page.getByText(value, { exact: true })).toBeVisible();
    expect(await readJSON(context, "/api/access/menu")).toEqual({ menu: [] });
    for (const path of [
      "/api/access/users",
      "/api/access/roles",
      "/api/access/permissions",
    ])
      expect((await context.request.get(path)).status()).toBe(403);
    const viewerHeaders = { Origin: origin, "X-CSRF-Token": viewer.csrfToken };
    await expectError(
      await context.request.post(avatarPath, {
        headers: viewerHeaders,
        data: { userId: admin.ID, file: yellow.buffer.toString("base64") },
      }),
      403,
      "forbidden",
    );
    for (const path of [
      `/api/profile/me?userId=${admin.ID}`,
      `${avatarPath}?userId=${admin.ID}`,
    ])
      await expectError(await context.request.get(path), 400, "invalid_input");
    await upload(page, yellow, viewerID);
    await expectPixels(page, [255, 255, 0]);
    const saved = await readAvatar(context, viewerID);
    expect(saved.equals(persistedBlue)).toBe(false);
    await page.reload();
    await openProfile(page);
    await expectPixels(page, [255, 255, 0]);
    await page.screenshot({
      path: testInfo.outputPath("viewer-profile.png"),
      fullPage: true,
    });
    expect(await readAvatar(context, viewerID)).toEqual(saved);
    expect((await readJSON(context, "/api/profile/me")).avatar).toBe(
      avatarPath,
    );
    expect(await readJSON(context, "/api/access/menu")).toEqual({ menu: [] });
    expect(browserUploads).toBe(4);
    await page.getByRole("button", { name: "退出登录", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("img", { name: "当前头像" })).toHaveCount(0);
    await expectError(
      await context.request.get(avatarPath),
      401,
      "unauthenticated",
    );
    expect(
      await page.evaluate(() => localStorage.length + sessionStorage.length),
    ).toBe(0);
  });
});
