import {
  expect,
  type BrowserContext,
  type Page,
  type Route,
} from "@playwright/test";
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

// These synthetic pages own only the /me route installed below. Keep patterns
// installed until every tracked continuation settles; only then remove them.
export async function drainSourceVerificationRoutes(
  page: Pick<Page, "unrouteAll">,
  release: () => void,
  active: Set<Promise<void>>,
) {
  release();
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    await Promise.race([
      (async () => {
        while (active.size) await Promise.allSettled([...active]);
        await page.unrouteAll({ behavior: "wait" });
      })(),
      new Promise<never>((_, reject) => {
        timer = setTimeout(
          () => reject(new Error("Source verification route drain timed out")),
          10_000,
        );
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

export function sourceVerificationFailure(error: unknown) {
  const message = error instanceof Error ? error.message : "";
  if (/Route is already handled/i.test(message)) return "already_handled";
  if (
    /Target.*closed|page.*closed|context.*closed|browser.*closed/i.test(message)
  )
    return "target_closed";
  if (/Invalid.*[Ii]nterception/i.test(message)) return "invalid_interception";
  if (/timed out|Timeout/i.test(message)) return "timeout";
  if (/expect\(|toHaveAttribute|toMatchObject|toEqual|toBe\(/.test(message))
    return "assertion";
  return "other";
}

export class SourceFocusVerificationError extends Error {}

export async function sourceFallbackRefocus(page: Page, actor: number) {
  let phase = "wait_native_hidden";
  const continuationFailures: string[] = [];
  const failures: { phase: string; category: string }[] = [];
  try {
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
    const active = new Set<Promise<void>>();
    const hold = (route: Route) => {
      const work = (async () => {
        await gate;
        try {
          await route.continue();
        } catch (error) {
          if (
            !/aborted|cancelled|canceled/i.test(
              route.request().failure()?.errorText ?? "",
            )
          ) {
            const category = sourceVerificationFailure(error);
            continuationFailures.push(category);
            throw new Error(
              `Session verification continuation failed: ${category}`,
            );
          }
        }
      })();
      active.add(work);
      void work.then(
        () => active.delete(work),
        () => active.delete(work),
      );
      return work;
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
      phase = "trusted_refocus";
      await page.bringToFront();
      await verificationRequested;
      phase = "checking_hidden_inert";
      await expect(page.locator("main.soc-main")).toHaveAttribute("hidden", "");
      await expect(page.locator("main.soc-main")).toHaveAttribute("inert", "");
    } catch (error) {
      failures.push({ phase, category: sourceVerificationFailure(error) });
    }
    phase = "drain_me_routes";
    try {
      await drainSourceVerificationRoutes(page, release, active);
    } catch (error) {
      failures.push({ phase, category: sourceVerificationFailure(error) });
    }
    if (failures.length) throw new Error("Source verification barrier failed");
    phase = "read_actual_me";
    expect(continuationFailures).toEqual([]);
    const response = await verified;
    const actualActor = (await response.json()).ID as number;
    expect(actualActor).toBe(actor);
    phase = "trusted_lifecycle_settled";
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
        (window as unknown as { __sourceLifecycle: unknown[] })
          .__sourceLifecycle,
    );
    return {
      actorID: actualActor,
      verifiedAtNode: performance.now(),
      hiddenAndInertDuringVerification: true,
      continuationFailures,
      lifecycle,
    };
  } catch (error) {
    const state = page.isClosed()
      ? { closed: true }
      : await page
          .evaluate(() => {
            const main = document.querySelector<HTMLElement>("main.soc-main");
            return {
              closed: false,
              visible: document.visibilityState,
              focused: document.hasFocus(),
              mainExists: !!main,
              mainHidden: main?.hidden ?? null,
              mainInert: main?.inert ?? null,
            };
          })
          .catch(() => ({ stateReadable: false }));
    if (!failures.length)
      failures.push({ phase, category: sourceVerificationFailure(error) });
    throw new SourceFocusVerificationError(
      `Source focus verification failed: ${JSON.stringify({ failures, continuationFailures, state })}`,
    );
  }
}
