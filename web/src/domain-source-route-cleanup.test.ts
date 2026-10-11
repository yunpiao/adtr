// @vitest-environment node
// Real pinned client methods, controlled continuation promises, no browser.
// This verifies route cleanup ordering only, never product acceptance.
import { expect, test, vi } from "vitest";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import {
  drainSourceVerificationRoutes,
  sourceVerificationFailure,
} from "../e2e/domain-source-fallback";
const require = createRequire(import.meta.url);
const root = dirname(require.resolve("playwright-core/package.json"));
const { Page } = require(join(root, "lib/client/page.js"));
const { RouteHandler } = require(join(root, "lib/client/network.js"));

function deferred<T = void>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

async function ordering(drain: boolean) {
  expect(require(join(root, "package.json")).version).toBe("1.56.1");
  const gate = deferred();
  const active = new Set<Promise<void>>();
  const controls = [deferred(), deferred()];
  const events: string[] = [];
  const callback = (route: { continue: () => Promise<void> }) => {
    const work = (async () => {
      await gate.promise;
      await route.continue();
    })();
    active.add(work);
    void work.then(
      () => active.delete(work),
      () => active.delete(work),
    );
    return work;
  };
  const platform = {
    zones: {
      current: () => ({ pop: () => ({ run: (fn: () => unknown) => fn() }) }),
    },
  };
  const handler = new RouteHandler(
    platform,
    undefined,
    "**/api/auth/me",
    callback,
  );
  const page = Object.create(Page.prototype);
  page._routes = [handler];
  page._closeWasCalled = false;
  page._browserContext = {
    _closingStatus: "none",
    _onRoute: async () => {
      throw new Error("Unexpected fallback");
    },
  };
  page._updateInterceptionPatterns = async () => {
    events.push("disable_interception");
  };
  page._disposeHarRouters = () => {};
  const routes = controls.map((control, index) => {
    const handled = deferred<boolean>();
    return {
      _didThrow: false,
      request: () => ({ url: () => "http://example.invalid/api/auth/me" }),
      _startHandling: () => handled.promise,
      continue: async () => {
        events.push(`continue_${index}_started`);
        await control.promise;
        events.push(`continue_${index}_finished`);
        handled.resolve(true);
      },
    };
  });
  const running = routes.map((route) => page._onRoute(route));
  const cleanup = drain
    ? drainSourceVerificationRoutes(page, gate.resolve, active)
    : (async () => {
        gate.resolve();
        await page.unrouteAll({ behavior: "wait" });
      })();
  await tick();
  controls[0].resolve();
  await tick();
  const disabledBeforeSecondFinished = events.includes("disable_interception");
  controls[1].resolve();
  await Promise.all([...running, cleanup]);
  return { disabledBeforeSecondFinished, events };
}

test("control: wait alone can disable interception while another handler remains pending", async () => {
  expect((await ordering(false)).disabledBeforeSecondFinished).toBe(true);
});
test("production helper drains both continuations before removing the owned routes", async () => {
  const result = await ordering(true);
  expect(result.disabledBeforeSecondFinished).toBe(false);
  expect(result.events.indexOf("disable_interception")).toBeGreaterThan(
    result.events.indexOf("continue_1_finished"),
  );
});
test("cleanup errors remain failures and release runs before the wait request", async () => {
  const error = new Error("controlled cleanup failure");
  const events: string[] = [];
  const unrouteAll = vi.fn(async (options: unknown) => {
    events.push("unroute");
    expect(options).toEqual({ behavior: "wait" });
    throw error;
  });
  await expect(
    drainSourceVerificationRoutes(
      { unrouteAll },
      () => {
        events.push("release");
      },
      new Set(),
    ),
  ).rejects.toBe(error);
  expect(events).toEqual(["release", "unroute"]);
});
test("diagnostics expose fixed classes, never raw response or request details", () => {
  expect(
    sourceVerificationFailure(
      new Error("Route is already handled! private-canary"),
    ),
  ).toBe("already_handled");
  expect(
    sourceVerificationFailure(
      new Error(
        "Target page, context or browser has been closed private-canary",
      ),
    ),
  ).toBe("target_closed");
  expect(sourceVerificationFailure(new Error("Timeout private-canary"))).toBe(
    "timeout",
  );
  expect(
    sourceVerificationFailure(new Error("unexpected private-canary")),
  ).toBe("other");
});
