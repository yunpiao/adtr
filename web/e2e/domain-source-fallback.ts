import { expect, type BrowserContext, type Page } from "@playwright/test";
import { useNativeTabLifecycle } from "./native-tab-lifecycle";

export async function sourceFallbackPage(
  context: BrowserContext,
  opener?: Page,
) {
  // A real popup copies the opener's metadata-only sessionStorage recovery
  // intent. Do not manufacture intent or identity in browser storage.
  let page: Page;
  if (opener) {
    const opened = context.waitForEvent("page");
    await opener.evaluate(() => {
      window.open("about:blank", "_blank");
    });
    page = await opened;
  } else page = await context.newPage();
  await useNativeTabLifecycle(page);
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
    const events: { type: string; trusted: boolean; visible: boolean }[] = [];
    const observe = (event: Event) =>
      events.push({
        type: event.type,
        trusted: event.isTrusted,
        visible: document.visibilityState === "visible",
      });
    window.addEventListener("focus", (event) => {
      if (event.target === window) observe(event);
    });
    document.addEventListener("visibilitychange", observe);
    Object.defineProperty(window, "__sourceLifecycle", { value: events });
  });
  await page.goto("/");
  return page;
}

export async function sourceFallbackRefocus(page: Page, actor: number) {
  await expect
    .poll(() =>
      page.evaluate(() => ({
        visible: document.visibilityState,
        focused: document.hasFocus(),
      })),
    )
    .toEqual({ visible: "hidden", focused: false });
  await page.evaluate(() => {
    (
      window as unknown as { __sourceLifecycle: unknown[] }
    ).__sourceLifecycle.length = 0;
  });
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const path = "**/api/auth/me";
  const hold = async (route: import("@playwright/test").Route) => {
    await gate;
    try {
      await route.continue();
    } catch {
      if (
        !/aborted|cancelled|canceled/i.test(
          route.request().failure()?.errorText ?? "",
        )
      )
        throw new Error("Session verification request continuation failed");
    }
  };
  await page.route(path, hold);
  const verificationRequested = page.waitForRequest(
    (request) => new URL(request.url()).pathname === "/api/auth/me",
  );
  const verified = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === "/api/auth/me" &&
      response.request().method() === "GET" &&
      response.status() === 200,
  );
  void verificationRequested.catch(() => {});
  void verified.catch(() => {});
  try {
    await page.bringToFront();
    await verificationRequested;
    await expect(page.locator("main.soc-main")).toHaveAttribute("hidden", "");
    await expect(page.locator("main.soc-main")).toHaveAttribute("inert", "");
  } finally {
    release();
    await page.unroute(path, hold);
  }
  const response = await verified;
  const actualActor = (await response.json()).ID as number;
  expect(actualActor).toBe(actor);
  await expect
    .poll(() =>
      page.evaluate(() => ({
        visible: document.visibilityState,
        focused: document.hasFocus(),
        events: (window as unknown as { __sourceLifecycle: unknown[] })
          .__sourceLifecycle,
      })),
    )
    .toMatchObject({
      visible: "visible",
      focused: true,
      events: expect.arrayContaining([
        expect.objectContaining({ type: "focus", trusted: true }),
        { type: "visibilitychange", visible: true, trusted: true },
      ]),
    });
  await expect(
    page.getByRole("status").filter({ hasText: "正在核验当前会话" }),
  ).toHaveCount(0);
  const lifecycle = await page.evaluate(
    () =>
      (window as unknown as { __sourceLifecycle: unknown[] }).__sourceLifecycle,
  );
  return {
    actorID: actualActor,
    verifiedAtNode: performance.now(),
    hiddenAndInertDuringVerification: true,
    lifecycle,
  };
}
