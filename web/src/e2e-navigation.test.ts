import type { Page } from "@playwright/test";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const control = vi.hoisted(() => ({
  advance: () => {},
  appendSafeEvent: vi.fn(),
}));

// Substitute only the Playwright transport/assertion boundary. Browser evaluate
// callbacks run against actual DOM elements; no readiness algorithm is copied.
vi.mock("@playwright/test", async () => {
  const { expect: check } = await import("vitest");
  async function eventually(assertion: () => Promise<void> | void) {
    let failure: unknown;
    for (let attempt = 0; attempt < 8; attempt++) {
      try {
        await assertion();
        return;
      } catch (error) {
        failure = error;
        control.advance();
      }
    }
    throw failure;
  }
  const assertions = (actual: unknown, negate = false): unknown => {
    const verify = (assertion: (value: unknown) => void) =>
      eventually(async () => {
        const value = typeof actual === "function" ? await actual() : actual;
        assertion(value);
      });
    const checked = (value: unknown) =>
      negate ? check(value).not : check(value);
    return {
      get not() {
        return assertions(actual, !negate);
      },
      toBe: (expected: unknown) =>
        verify((value) => checked(value).toBe(expected)),
      toEqual: (expected: unknown) =>
        verify((value) => checked(value).toEqual(expected)),
      toBeTruthy: () => verify((value) => checked(value).toBeTruthy()),
      toBeVisible: () =>
        verify((value) => checked((value as TestLocator).visible()).toBe(true)),
      toBeEnabled: () =>
        verify((value) =>
          checked((value as TestLocator).element().hasAttribute("disabled")).toBe(
            false,
          ),
        ),
      toHaveAttribute: (name: string, expected?: string) =>
        verify((value) => {
          const element = (value as TestLocator).element();
          if (expected === undefined)
            checked(element.hasAttribute(name)).toBe(true);
          else checked(element.getAttribute(name)).toBe(expected);
        }),
      toContainText: (expected: string) =>
        verify((value) =>
          checked((value as TestLocator).element().textContent).toContain(
            expected,
          ),
        ),
    };
  };
  return {
    expect: Object.assign(assertions, {
      poll: (read: () => Promise<unknown>) => assertions(read),
    }),
  };
});
vi.mock("../e2e/safe-runtime-diagnostics", () => ({
  appendSafeEvent: control.appendSafeEvent,
}));

import { navigateTo, revealNavigation } from "../e2e/navigation";

function visible(element: Element | null): boolean {
  if (!element || element.closest("[hidden]")) return false;
  const details = element.closest("details");
  return !details || details.open || element.tagName === "SUMMARY";
}

class TestLocator {
  constructor(
    private readonly find: () => Element | null,
    private readonly onClick: (element: Element) => Promise<void>,
  ) {}
  element() {
    const element = this.find();
    if (!element) throw new Error("fixture locator did not resolve");
    return element;
  }
  visible() {
    return visible(this.find());
  }
  async isVisible() {
    return this.visible();
  }
  async isEnabled() {
    return !this.element().hasAttribute("disabled");
  }
  async getAttribute(name: string) {
    return this.element().getAttribute(name);
  }
  async evaluate<T>(read: (element: Element) => T) {
    return read(this.element());
  }
  locator(selector: string) {
    return new TestLocator(
      () => this.element().querySelector(selector),
      this.onClick,
    );
  }
  async click(options?: unknown) {
    expect(options).toBeUndefined();
    expect(this.visible()).toBe(true);
    expect(this.element().closest("[inert]")).toBeNull();
    expect(await this.isEnabled()).toBe(true);
    await this.onClick(this.element());
  }
}

function fixture({ compact = false, open = false } = {}) {
  document.body.innerHTML = `
    <button class="soc-mobile-toggle" aria-label="打开导航"
      aria-controls="soc-navigation-panel" aria-expanded="false"></button>
    <aside id="soc-navigation-panel" class="soc-sidebar" aria-label="工作区导航">
      <nav aria-label="账户设置">
        <button data-target="tasks">后台任务</button>
        <details data-navigation-group="identity">
          <summary>账户与身份</summary><button data-target="account">账户概览</button>
        </details>
      </nav>
      <span>CANARY-private-DN-session-secret</span>
    </aside>`;
  const panel = document.querySelector<HTMLElement>("aside")!;
  const toggle = document.querySelector<HTMLButtonElement>(".soc-mobile-toggle")!;
  const details = document.querySelector<HTMLDetailsElement>("details")!;
  const state = {
    compact,
    toggleClicks: 0,
    summaryClicks: 0,
    targetClicks: [] as string[],
    evaluations: 0,
    closed: false,
    evaluateError: undefined as Error | undefined,
    onToggle: () => true,
    onSummary: () => true,
    targetError: undefined as Error | undefined,
  };
  vi.stubGlobal("matchMedia", () => ({ matches: state.compact }));
  function renderLayout(mobile = state.compact, expanded = false) {
    toggle.hidden = !mobile || expanded;
    toggle.setAttribute("aria-expanded", String(expanded));
    panel.hidden = mobile && !expanded;
    panel.toggleAttribute("inert", mobile && !expanded);
    if (mobile && expanded) {
      panel.setAttribute("role", "dialog");
      panel.setAttribute("aria-modal", "true");
    } else {
      panel.removeAttribute("role");
      panel.removeAttribute("aria-modal");
    }
  }
  renderLayout(compact, open);
  for (const element of document.querySelectorAll<HTMLElement>("*")) {
    element.getBoundingClientRect = () =>
      ({
        width: visible(element) ? 100 : 0,
        height: visible(element) ? 30 : 0,
      }) as DOMRect;
    element.getClientRects = () =>
      (visible(element)
        ? [element.getBoundingClientRect()]
        : []) as unknown as DOMRectList;
  }
  async function click(element: Element) {
    if (element === toggle) {
      state.toggleClicks++;
      if (state.onToggle()) renderLayout(state.compact, true);
    } else if (element.tagName === "SUMMARY") {
      state.summaryClicks++;
      if (state.onSummary()) details.open = true;
    } else {
      state.targetClicks.push(element.getAttribute("data-target")!);
      if (state.targetError) throw state.targetError;
      renderLayout(state.compact, false);
    }
  }
  const page = {
    isClosed: () => state.closed,
    viewportSize: () => ({ width: state.compact ? 390 : 1440, height: 900 }),
    url: () => "https://example.test/?token=CANARY-private-DN-session-secret",
    evaluate: async <T, A>(read: (argument: A) => T, argument: A) => {
      state.evaluations++;
      if (state.evaluateError) throw state.evaluateError;
      return read(argument);
    },
    locator: (selector: string) =>
      new TestLocator(() => document.querySelector(selector), click),
    getByRole: (_role: string, { name }: { name: string }) =>
      new TestLocator(
        () =>
          [...document.querySelectorAll("button")].find(
            (element) =>
              (element.getAttribute("aria-label") ?? element.textContent) === name,
          ) ?? null,
        click,
      ),
  } as unknown as Page;
  return { page, state, panel, toggle, details, renderLayout };
}

beforeEach(() => {
  control.advance = () => {};
  control.appendSafeEvent.mockReset();
});
afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("E2E navigation readiness", () => {
  it("waits for delayed compact rendering before clicking the toggle once", async () => {
    const { page, state, renderLayout } = fixture();
    state.compact = true;
    let turns = 0;
    control.advance = () => {
      expect(state.toggleClicks).toBe(0);
      expect(state.targetClicks).toEqual([]);
      if (++turns === 3) renderLayout(true, false);
    };
    await navigateTo(page, "后台任务");
    expect(turns).toBeGreaterThanOrEqual(3);
    expect(state.toggleClicks).toBe(1);
    expect(state.targetClicks).toEqual(["tasks"]);
  });

  it("navigates on desktop without opening mobile navigation", async () => {
    const { page, state } = fixture();
    await navigateTo(page, "账户概览");
    expect(state.toggleClicks).toBe(0);
    expect(state.summaryClicks).toBe(1);
    expect(state.targetClicks).toEqual(["account"]);
  });

  it("does not reopen an already-open compact drawer or an expanded group", async () => {
    const { page, state, details } = fixture({ compact: true, open: true });
    details.open = true;
    await revealNavigation(page, "账户概览");
    await revealNavigation(page, "账户概览");
    expect(state.toggleClicks).toBe(0);
    expect(state.summaryClicks).toBe(0);
    expect(state.targetClicks).toEqual([]);
  });

  it("waits for the drawer to become non-inert without clicking it again", async () => {
    const { page, state, renderLayout, panel } = fixture({ compact: true });
    state.onToggle = () => {
      renderLayout(true, true);
      panel.setAttribute("inert", "");
      return false;
    };
    let turns = 0;
    control.advance = () => {
      expect(state.toggleClicks).toBe(1);
      expect(state.targetClicks).toEqual([]);
      if (++turns === 2) panel.removeAttribute("inert");
    };
    await navigateTo(page, "后台任务");
    expect(turns).toBeGreaterThanOrEqual(2);
    expect(state.toggleClicks).toBe(1);
    expect(state.targetClicks).toEqual(["tasks"]);
  });

  it("waits for delayed disclosure rendering without clicking its summary twice", async () => {
    const { page, state, details } = fixture();
    state.onSummary = () => false;
    let turns = 0;
    control.advance = () => {
      expect(state.summaryClicks).toBe(1);
      expect(state.targetClicks).toEqual([]);
      if (++turns === 2) details.open = true;
    };
    await navigateTo(page, "账户概览");
    expect(turns).toBeGreaterThanOrEqual(2);
    expect(state.summaryClicks).toBe(1);
    expect(state.targetClicks).toEqual(["account"]);
  });

  it("waits for target enablement without repeating the business click", async () => {
    const { page, state } = fixture();
    const target = document.querySelector<HTMLButtonElement>(
      '[data-target="tasks"]',
    )!;
    target.disabled = true;
    let turns = 0;
    control.advance = () => {
      expect(state.targetClicks).toEqual([]);
      if (++turns === 2) target.disabled = false;
    };
    await navigateTo(page, "后台任务");
    expect(turns).toBeGreaterThanOrEqual(2);
    expect(state.targetClicks).toEqual(["tasks"]);
  });

  it("fails a persistently inert panel without trying any navigation action", async () => {
    const { page, state, panel } = fixture();
    panel.setAttribute("inert", "");
    await expect(navigateTo(page, "账户概览")).rejects.toThrow();
    expect(state.toggleClicks).toBe(0);
    expect(state.summaryClicks).toBe(0);
    expect(state.targetClicks).toEqual([]);
    expect(control.appendSafeEvent.mock.calls.at(-1)?.[0]).toMatchObject({
      stage: "failed",
      snapshot: { panelInert: true, unavailable: false },
    });
  });

  it("fails a closed page without clicking or masking the original failure", async () => {
    const { page, state } = fixture();
    const failure = new Error("synthetic page closed CANARY-private-error");
    state.closed = true;
    state.evaluateError = failure;
    control.appendSafeEvent.mockImplementation(() => {
      throw new Error("synthetic diagnostics unavailable");
    });
    await expect(navigateTo(page, "后台任务")).rejects.not.toThrow(
      "synthetic diagnostics unavailable",
    );
    expect(state.evaluations).toBe(0);
    expect(state.toggleClicks).toBe(0);
    expect(state.summaryClicks).toBe(0);
    expect(state.targetClicks).toEqual([]);
  });

  it("preserves a target click failure without replay when diagnostics also fail", async () => {
    const { page, state } = fixture();
    const failure = new Error("synthetic transport CANARY-private-error");
    state.targetError = failure;
    control.appendSafeEvent.mockImplementation(() => {
      throw new Error("synthetic diagnostics unavailable");
    });
    await expect(navigateTo(page, "后台任务")).rejects.toBe(failure);
    expect(state.targetClicks).toEqual(["tasks"]);
  });

  it("records safe state without DOM text, URLs, target labels or error contents", async () => {
    const { page, state } = fixture();
    await navigateTo(page, "后台任务");
    state.evaluateError = new Error("CANARY-private-error");
    await expect(navigateTo(page, "CANARY-private-target")).rejects.toThrow();
    expect(control.appendSafeEvent).toHaveBeenCalled();
    const records = JSON.stringify(control.appendSafeEvent.mock.calls);
    expect(records).not.toContain("CANARY");
    expect(records).not.toContain("https://");
    expect(records).not.toContain("token=");
    expect(records).not.toContain("后台任务");
    expect(control.appendSafeEvent.mock.calls.at(-1)?.[0]).toMatchObject({
      origin: "navigation",
      stage: "failed",
      snapshot: { unavailable: true },
    });
  });

  it("opens only once for repeated reveal and reopens after navigation hides it", async () => {
    const { page, state } = fixture({ compact: true });
    await revealNavigation(page, "后台任务");
    await revealNavigation(page, "后台任务");
    expect(state.toggleClicks).toBe(1);
    await navigateTo(page, "后台任务");
    expect(state.toggleClicks).toBe(1);
    await navigateTo(page, "账户概览");
    expect(state.toggleClicks).toBe(2);
    expect(state.targetClicks).toEqual(["tasks", "account"]);
  });
});
