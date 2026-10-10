import { navigateTo } from "./navigation";
import {
  test,
  expect,
  type BrowserContext,
  type Page,
  type Request,
  type Route,
} from "@playwright/test";
import { createHmac } from "node:crypto";
import {
  requireSameNativeWindow,
  useNativeTabLifecycle,
} from "./native-tab-lifecycle";

// Runs only against the session-invalidation harness's freshly migrated,
// disposable API/PostgreSQL fixture. No AD connection or B2 source barrier is
// exercised here. Network interception delays genuine responses, never bodies.
const profilePath = "/api/profile/me";
type Session = { ID: number; username: string; csrfToken: string };

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

async function session(context: BrowserContext): Promise<Session> {
  const response = await context.request.get("/api/auth/me");
  expect(response.status()).toBe(200);
  return response.json();
}

async function login(page: Page, username: string, password: string) {
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function changeInitialPassword(page: Page, old: string, changed: string) {
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await page.getByLabel("当前密码", { exact: true }).fill(old);
  await page.getByLabel("新密码", { exact: true }).fill(changed);
  await page.getByLabel("确认新密码", { exact: true }).fill(changed);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
}

async function openProfile(page: Page, username: string) {
  await navigateTo(page, "个人资料");
  await expect(page.getByLabel("选择头像", { exact: true })).toBeEnabled();
  await expect(
    page.locator(".profile-workspace dd").filter({ hasText: username }),
  ).toHaveText(username);
}

async function signedIn(page: Page, username: string) {
  await expect(page.locator(".signed-in strong")).toHaveText(username);
  await expect(
    page.getByRole("status").filter({ hasText: "正在核验当前会话" }),
  ).toHaveCount(0);
}

async function logout(page: Page) {
  await navigateTo(page, "退出登录");
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
}

// Fetch the response from the real authenticated server before releasing the
// delivery gate. The browser may abort the old request on invalidation; both a
// cancelled delivery and a delivered-but-discarded response are safe outcomes.
async function delayProfile(page: Page) {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let ready!: (result: {
    status: number;
    body: { ID: number; username: string };
    cache: string;
  }) => void;
  let fail!: (error: unknown) => void;
  const fetched = new Promise<{
    status: number;
    body: { ID: number; username: string };
    cache: string;
  }>((resolve, reject) => {
    ready = resolve;
    fail = reject;
  });
  let finish!: (error: unknown) => void;
  const finished = new Promise<unknown>((resolve) => {
    finish = resolve;
  });
  let terminal: Promise<void> | undefined;
  let claimed = false;
  const handler = async (route: Route) => {
    if (claimed || route.request().method() !== "GET") {
      await route.continue();
      return;
    }
    claimed = true;
    const request = route.request();
    terminal = new Promise<void>((resolve) => {
      const ended = (candidate: Request) => {
        if (candidate !== request) return;
        page.off("requestfinished", ended);
        page.off("requestfailed", ended);
        resolve();
      };
      page.on("requestfinished", ended);
      page.on("requestfailed", ended);
    });
    try {
      const actual = await route.fetch({ maxRetries: 0, timeout: 15_000 });
      ready({
        status: actual.status(),
        body: await actual.json(),
        cache: actual.headers()["cache-control"],
      });
      await gate;
      try {
        await route.fulfill({ response: actual });
      } catch (error) {
        // Do not disguise a transport error as successful delivery. Confirm
        // the browser's own cancellation before accepting an aborted route.
        if (
          !/aborted|cancelled|canceled/i.test(
            route.request().failure()?.errorText ?? "",
          )
        )
          throw error;
      }
      finish(undefined);
    } catch (error) {
      fail(error);
      await route.abort().catch(() => {});
      finish(error);
    }
  };
  await page.route(`**${profilePath}`, handler);
  return {
    fetched,
    async release() {
      release();
      const error = await finished;
      await page.unroute(`**${profilePath}`, handler);
      if (error) throw error;
      // Wait for the browser's terminal request event, not merely Playwright's
      // fulfillment command, before asserting that stale data stays absent.
      await terminal;
      await page.evaluate(
        () => new Promise<void>((resolve) => setTimeout(resolve, 0)),
      );
    },
    unblock: release,
  };
}

function watchNavigation(page: Page) {
  const url = page.url();
  let navigations = 0;
  const listener = (frame: import("@playwright/test").Frame) => {
    if (frame === page.mainFrame()) navigations++;
  };
  page.on("framenavigated", listener);
  return () => {
    expect(page.url()).toBe(url);
    expect(navigations, "old tab must not navigate to clear its session").toBe(
      0,
    );
    page.off("framenavigated", listener);
  };
}

function watchPosts(page: Page) {
  let posts = 0;
  const listener = (request: Request) => {
    if (request.method() === "POST") posts++;
  };
  page.on("request", listener);
  return () => {
    expect(posts, "old tab must never automatically replay a mutation").toBe(0);
    page.off("request", listener);
  };
}

type LifecycleEvent = { type: string; visible: boolean; trusted: boolean };
declare global {
  interface Window {
    sessionInvalidationEvents: LifecycleEvent[];
  }
}

async function fallbackPage(context: BrowserContext) {
  const page = await context.newPage();
  await page.addInitScript(() => {
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
    window.sessionInvalidationEvents = [];
    window.addEventListener("focus", (event) => {
      if (event.target === window)
        window.sessionInvalidationEvents.push({
          type: event.type,
          visible: document.visibilityState === "visible",
          trusted: event.isTrusted,
        });
    });
    document.addEventListener("visibilitychange", (event) => {
      window.sessionInvalidationEvents.push({
        type: event.type,
        visible: document.visibilityState === "visible",
        trusted: event.isTrusted,
      });
    });
  });
  await useNativeTabLifecycle(page);
  await page.goto("/");
  return page;
}

async function refocusWithRealSessionRead(page: Page) {
  // Visibility and focus loss are separate browser lifecycle updates. Do not
  // clear the witness or switch back while the previous deactivation is still
  // pending: an already-focused document need not emit another focus event.
  // Observe both native states without dispatching events or overriding them.
  await expect
    .poll(() =>
      page.evaluate(() => ({
        visibility: document.visibilityState,
        focused: document.hasFocus(),
        events: window.sessionInvalidationEvents,
      })),
    )
    .toMatchObject({ visibility: "hidden", focused: false });
  await page.evaluate(() => {
    window.sessionInvalidationEvents = [];
  });
  const read = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === "/api/auth/me" &&
      response.request().method() === "GET" &&
      response.status() === 200,
  );
  await page.bringToFront();
  await read;
  // Keep the event witnesses in assertion output. A boolean hid whether the
  // browser missed focus, visibility, trust, or their foreground state in CI.
  await expect
    .poll(() =>
      page.evaluate(() => ({
        visibility: document.visibilityState,
        focused: document.hasFocus(),
        events: window.sessionInvalidationEvents,
      })),
    )
    .toEqual({
      visibility: "visible",
      focused: true,
      events: expect.arrayContaining([
        // Chromium may deliver native focus before its visibility update.
        // Both events must be trusted; settled focus/visibility are above.
        expect.objectContaining({ type: "focus", trusted: true }),
        { type: "visibilitychange", visible: true, trusted: true },
      ]),
    });
  await expect(
    page.getByRole("status").filter({ hasText: "正在核验当前会话" }),
  ).toHaveCount(0);
}

test("real browser → API → PostgreSQL cross-tab session invalidation and focus fallback", async ({
  page: actor,
  context,
}, testInfo) => {
  test.setTimeout(360_000);
  const username = process.env.ADTR_E2E_USERNAME;
  const initial = process.env.ADTR_E2E_PASSWORD;
  if (!username || !initial)
    throw new Error(
      "Session invalidation E2E requires a fresh disposable synthetic bootstrap account",
    );
  const changed = "Changed Session Admin 123";
  const viewerUsername = "e2e.session.viewer";
  const viewerInitial = "Initial Session Viewer 123";
  const viewerChanged = "Changed Session Viewer 456";
  const proofDraft = "Unsubmitted Synthetic Proof 789";
  let viewerID = 0;

  await test.step("bootstrap MFA and create the viewer through the real authorized API", async () => {
    await actor.goto("/");
    await login(actor, username, initial);
    await changeInitialPassword(actor, initial, changed);
    await navigateTo(actor, "多因素认证");
    await actor.getByLabel("当前密码", { exact: true }).fill(changed);
    await actor.getByRole("button", { name: "开始设置", exact: true }).click();
    const secret = await actor
      .getByLabel("设置密钥", { exact: true })
      .inputValue();
    const enrolledAt = Date.now();
    await actor
      .getByLabel("认证器验证码", { exact: true })
      .fill(totp(secret, enrolledAt));
    await actor
      .getByRole("button", { name: "验证并启用", exact: true })
      .click();
    await expect(actor.getByRole("status")).toContainText("多因素认证已启用");
    const nextCounterAt = (Math.floor(enrolledAt / 30_000) + 1) * 30_000 + 1100;
    while (Date.now() < nextCounterAt)
      await new Promise((resolve) =>
        setTimeout(resolve, nextCounterAt - Date.now()),
      );
    const admin = await session(context);
    const created = await context.request.post("/api/access/users/create", {
      headers: {
        Origin: new URL(actor.url()).origin,
        "X-CSRF-Token": admin.csrfToken,
      },
      data: {
        username: viewerUsername,
        password: viewerInitial,
        roleID: "viewer",
        realName: "合成跨标签用户",
        remark: "Synthetic session invalidation fixture",
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
  });

  await test.step("notifications discard actor A's private UI and proof while a genuine A response is delayed", async () => {
    const admin = await session(context);
    const profileTab = await context.newPage();
    const proofTab = await context.newPage();
    const checkProfilePosts = watchPosts(profileTab);
    const checkProofPosts = watchPosts(proofTab);
    await profileTab.goto("/");
    await openProfile(profileTab, admin.username);
    await proofTab.goto("/");
    await navigateTo(proofTab, "重置用户密码");
    await proofTab
      .getByLabel("目标用户名", { exact: true })
      .fill("synthetic.unsubmitted.target");
    await proofTab
      .getByLabel("管理员当前密码", { exact: true })
      .fill(proofDraft);
    await proofTab
      .getByLabel("管理员认证器验证码", { exact: true })
      .fill("123456");
    const checkProfileNavigation = watchNavigation(profileTab);
    const checkProofNavigation = watchNavigation(proofTab);
    const delayed = await delayProfile(profileTab);
    try {
      await profileTab
        .getByRole("button", { name: "刷新个人资料", exact: true })
        .click();
      expect(await delayed.fetched).toMatchObject({
        status: 200,
        cache: "no-store",
        body: { ID: admin.ID, username: admin.username },
      });
      await signedIn(profileTab, admin.username);
      await expect(
        proofTab.getByLabel("管理员当前密码", { exact: true }),
      ).toHaveValue(proofDraft);
      await logout(actor);
      // Do not focus, click, reload or navigate either old tab. Only the real
      // same-origin notification can clear these views while delivery is held.
      for (const old of [profileTab, proofTab]) {
        await expect(
          old.getByRole("heading", { name: "登录账户", exact: true }),
        ).toBeVisible();
        await expect(old.locator(".signed-in")).toHaveCount(0);
        await expect(old.locator('input[type="password"]')).toHaveValue("");
      }
      await expect(
        proofTab.getByLabel("管理员当前密码", { exact: true }),
      ).toHaveCount(0);
      await expect(
        proofTab.getByLabel("管理员认证器验证码", { exact: true }),
      ).toHaveCount(0);
      await expect(
        profileTab.getByRole("heading", { name: "个人资料", exact: true }),
      ).toHaveCount(0);
      expect((await context.request.get("/api/auth/me")).status()).toBe(401);
      await login(actor, viewerUsername, viewerInitial);
      await changeInitialPassword(actor, viewerInitial, viewerChanged);
      expect((await session(context)).ID).toBe(viewerID);
      for (const old of [profileTab, proofTab])
        await signedIn(old, viewerUsername);
      await delayed.release();
      for (const old of [profileTab, proofTab]) {
        await signedIn(old, viewerUsername);
        await expect(
          old.getByText(admin.username, { exact: true }),
        ).toHaveCount(0);
        await expect(old.locator('input[type="password"]')).toHaveCount(0);
        await expect(
          old.getByRole("heading", { name: "个人资料", exact: true }),
        ).toHaveCount(0);
      }
      checkProfileNavigation();
      checkProofNavigation();
      checkProfilePosts();
      checkProofPosts();
    } finally {
      delayed.unblock();
      await profileTab.close();
      await proofTab.close();
    }
  });

  await test.step("real focus and visibility preserve unchanged drafts and discard a rotated session with both transports disabled", async () => {
    // No second privileged fixture or MFA proof is necessary: re-login as the
    // viewer rotates the same actor's CSRF/session, which must also invalidate.
    await actor.close();
    const profileTab = await fallbackPage(context);
    const checkProfilePosts = watchPosts(profileTab);
    await profileTab.bringToFront();
    await openProfile(profileTab, viewerUsername);
    const proofTab = await fallbackPage(context);
    const checkProofPosts = watchPosts(proofTab);
    await proofTab.bringToFront();
    await navigateTo(proofTab, "修改密码");
    await proofTab.getByLabel("当前密码", { exact: true }).fill(proofDraft);
    await proofTab
      .getByLabel("新密码", { exact: true })
      .fill("Unsubmitted New Password 123");
    const switchingTab = await fallbackPage(context);
    await requireSameNativeWindow([profileTab, proofTab, switchingTab]);
    await switchingTab.bringToFront();
    await expect
      .poll(() => proofTab.evaluate(() => document.visibilityState))
      .toBe("hidden");
    const before = await session(context);
    await refocusWithRealSessionRead(proofTab);
    await expect(proofTab.getByLabel("当前密码", { exact: true })).toHaveValue(
      proofDraft,
    );
    await expect(proofTab.getByLabel("新密码", { exact: true })).toHaveValue(
      "Unsubmitted New Password 123",
    );
    expect((await session(context)).csrfToken).toBe(before.csrfToken);

    await profileTab.bringToFront();
    const delayed = await delayProfile(profileTab);
    const checkProfileNavigation = watchNavigation(profileTab);
    const checkProofNavigation = watchNavigation(proofTab);
    try {
      await profileTab
        .getByRole("button", { name: "刷新个人资料", exact: true })
        .click();
      expect(await delayed.fetched).toMatchObject({
        status: 200,
        cache: "no-store",
        body: { ID: viewerID, username: viewerUsername },
      });
      await switchingTab.bringToFront();
      await expect
        .poll(() => profileTab.evaluate(() => document.visibilityState))
        .toBe("hidden");
      await logout(switchingTab);
      await login(switchingTab, viewerUsername, viewerChanged);
      await signedIn(switchingTab, viewerUsername);
      const after = await session(context);
      expect(after.ID).toBe(before.ID);
      expect(after.csrfToken).not.toBe(before.csrfToken);
      // Both transports really are unavailable and the background tab retains
      // its draft until a browser lifecycle event starts the fallback read.
      await expect(
        proofTab.getByLabel("当前密码", { exact: true }),
      ).toHaveValue(proofDraft);
      await refocusWithRealSessionRead(proofTab);
      await signedIn(proofTab, viewerUsername);
      await expect(
        proofTab.getByRole("heading", { name: "修改密码", exact: true }),
      ).toHaveCount(0);
      await expect(proofTab.locator('input[type="password"]')).toHaveCount(0);
      await refocusWithRealSessionRead(profileTab);
      await signedIn(profileTab, viewerUsername);
      await expect(
        profileTab.getByRole("heading", { name: "个人资料", exact: true }),
      ).toHaveCount(0);
      await delayed.release();
      await expect(
        profileTab.getByRole("heading", { name: "个人资料", exact: true }),
      ).toHaveCount(0);
      await expect(
        profileTab.getByLabel("选择头像", { exact: true }),
      ).toHaveCount(0);
      checkProfileNavigation();
      checkProofNavigation();
      await proofTab.bringToFront();
      await navigateTo(proofTab, "修改密码");
      await expect(
        proofTab.getByLabel("当前密码", { exact: true }),
      ).toHaveValue("");
      await expect(proofTab.getByLabel("新密码", { exact: true })).toHaveValue(
        "",
      );
      checkProfilePosts();
      checkProofPosts();
    } finally {
      delayed.unblock();
      await profileTab.close();
      await proofTab.close();
      await switchingTab.close();
    }
  });
  await testInfo.attach("session-invalidation-scope", {
    body: JSON.stringify(
      {
        evidence: "real browser, authenticated API and disposable PostgreSQL",
        scenarios: [
          "actor A to viewer B with delayed A profile",
          "real focus/visibility without messaging/storage",
          "same-session draft preservation",
          "same-actor session rotation clears drafts",
        ],
        exclusions: [
          "Active Directory",
          "B2-specific source replacement barrier",
        ],
      },
      null,
      2,
    ),
    contentType: "application/json",
  });
});
