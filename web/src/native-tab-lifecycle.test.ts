// @vitest-environment node
import type { Page } from "@playwright/test";
import { beforeEach, expect, test, vi } from "vitest";

const installed = vi.hoisted(() => ({ version: "1.56.1" }));
vi.mock("node:module", () => ({
  createRequire: () => (specifier: string) => {
    if (specifier !== "playwright-core/package.json")
      throw new Error(`Unexpected package lookup: ${specifier}`);
    return { version: installed.version };
  },
}));

beforeEach(() => {
  installed.version = "1.56.1";
  vi.resetModules();
});

function fixture(browserName = "chromium") {
  const session = { send: vi.fn().mockResolvedValue({}) };
  const toImpl = vi.fn().mockReturnValue({
    delegate: { _mainFrameSession: { _client: session } },
  });
  const newCDPSession = vi.fn(() => {
    throw new Error("A fresh session cannot disable its owner's emulation");
  });
  const page = {
    _connection: { toImpl },
    context: () => ({
      browser: () => ({ browserType: () => ({ name: () => browserName }) }),
      newCDPSession,
    }),
  } as unknown as Page;
  return { page, session, toImpl, newCDPSession };
}

test("disables emulation on the owning session without attaching a fresh session", async () => {
  const { useNativeTabLifecycle } = await import("../e2e/native-tab-lifecycle");
  const { page, session, toImpl, newCDPSession } = fixture();
  await useNativeTabLifecycle(page);
  expect(toImpl).toHaveBeenCalledExactlyOnceWith(page);
  expect(session.send).toHaveBeenCalledExactlyOnceWith(
    "Emulation.setFocusEmulationEnabled",
    { enabled: false },
  );
  expect(session.send.mock.contexts).toEqual([session]);
  expect(newCDPSession).not.toHaveBeenCalled();
});

test("fails closed when the pinned Playwright version changes", async () => {
  installed.version = "1.57.0";
  const { useNativeTabLifecycle } = await import("../e2e/native-tab-lifecycle");
  const { page, session, toImpl } = fixture();
  await expect(useNativeTabLifecycle(page)).rejects.toThrow(
    "pinned Playwright 1.56.1 Chromium",
  );
  expect(toImpl).not.toHaveBeenCalled();
  expect(session.send).not.toHaveBeenCalled();
});

test("fails closed for a non-Chromium browser", async () => {
  const { useNativeTabLifecycle } = await import("../e2e/native-tab-lifecycle");
  const { page, session, toImpl } = fixture("firefox");
  await expect(useNativeTabLifecycle(page)).rejects.toThrow(
    "pinned Playwright 1.56.1 Chromium",
  );
  expect(toImpl).not.toHaveBeenCalled();
  expect(session.send).not.toHaveBeenCalled();
});

test("fails closed when the in-process owner mapping is unavailable", async () => {
  const { useNativeTabLifecycle } = await import("../e2e/native-tab-lifecycle");
  const { page, session, newCDPSession } = fixture();
  Object.assign(page, { _connection: {} });
  await expect(useNativeTabLifecycle(page)).rejects.toThrow(
    "owning Chromium session",
  );
  expect(session.send).not.toHaveBeenCalled();
  expect(newCDPSession).not.toHaveBeenCalled();
});

test.each([
  undefined,
  {},
  { delegate: { _mainFrameSession: { _client: {} } } },
])(
  "fails closed for an incompatible owner-session shape: %j",
  async (implementation) => {
    const { useNativeTabLifecycle } = await import(
      "../e2e/native-tab-lifecycle"
    );
    const { page, session, toImpl, newCDPSession } = fixture();
    toImpl.mockReturnValue(implementation);
    await expect(useNativeTabLifecycle(page)).rejects.toThrow(
      "owning Chromium session",
    );
    expect(session.send).not.toHaveBeenCalled();
    expect(newCDPSession).not.toHaveBeenCalled();
  },
);

test("preserves a protocol failure instead of falling back or skipping", async () => {
  const { useNativeTabLifecycle } = await import("../e2e/native-tab-lifecycle");
  const { page, session, newCDPSession } = fixture();
  session.send.mockRejectedValue(new Error("Synthetic protocol failure"));
  await expect(useNativeTabLifecycle(page)).rejects.toThrow(
    "Synthetic protocol failure",
  );
  expect(newCDPSession).not.toHaveBeenCalled();
});

function windowFixture(response: unknown) {
  const cdp = {
    send: vi.fn().mockResolvedValue(response),
    detach: vi.fn().mockResolvedValue(undefined),
  };
  const newCDPSession = vi.fn().mockResolvedValue(cdp);
  const page = {
    context: () => ({ newCDPSession }),
  } as unknown as Page;
  return { page, cdp, newCDPSession };
}

test("accepts matching native window IDs and detaches both read sessions", async () => {
  const { requireSameNativeWindow } = await import(
    "../e2e/native-tab-lifecycle"
  );
  const tabs = [
    windowFixture({ windowId: 17 }),
    windowFixture({ windowId: 17 }),
  ];
  await requireSameNativeWindow(tabs.map(({ page }) => page));
  for (const { page, cdp, newCDPSession } of tabs) {
    expect(newCDPSession).toHaveBeenCalledExactlyOnceWith(page);
    expect(cdp.send).toHaveBeenCalledExactlyOnceWith(
      "Browser.getWindowForTarget",
    );
    expect(cdp.detach).toHaveBeenCalledOnce();
  }
});

test("rejects tabs in different native windows and detaches both read sessions", async () => {
  const { requireSameNativeWindow } = await import(
    "../e2e/native-tab-lifecycle"
  );
  const tabs = [
    windowFixture({ windowId: 17 }),
    windowFixture({ windowId: 18 }),
  ];
  await expect(
    requireSameNativeWindow(tabs.map(({ page }) => page)),
  ).rejects.toThrow("must be tabs in one native window");
  for (const { cdp } of tabs) expect(cdp.detach).toHaveBeenCalledOnce();
});

test.each([
  { label: "missing", response: {} },
  { label: "undefined", response: { windowId: undefined } },
  { label: "null", response: { windowId: null } },
  { label: "string", response: { windowId: "17" } },
  { label: "zero", response: { windowId: 0 } },
  { label: "negative", response: { windowId: -1 } },
  { label: "fractional", response: { windowId: 1.5 } },
  { label: "NaN", response: { windowId: NaN } },
  { label: "infinite", response: { windowId: Infinity } },
  {
    label: "unsafe integer",
    response: { windowId: Number.MAX_SAFE_INTEGER + 1 },
  },
])(
  "rejects a $label native window ID and detaches the read session",
  async ({ response }) => {
    const { requireSameNativeWindow } = await import(
      "../e2e/native-tab-lifecycle"
    );
    const first = windowFixture(response);
    const second = windowFixture(response);
    await expect(
      requireSameNativeWindow([first.page, second.page]),
    ).rejects.toThrow("invalid native window ID");
    expect(first.cdp.detach).toHaveBeenCalledOnce();
    expect(second.newCDPSession).not.toHaveBeenCalled();
  },
);

test("detaches the read session when its native-window protocol request fails", async () => {
  const { requireSameNativeWindow } = await import(
    "../e2e/native-tab-lifecycle"
  );
  const first = windowFixture({ windowId: 17 });
  const second = windowFixture({ windowId: 17 });
  second.cdp.send.mockRejectedValue(
    new Error("Native-window protocol failure"),
  );
  await expect(
    requireSameNativeWindow([first.page, second.page]),
  ).rejects.toThrow("Native-window protocol failure");
  expect(first.cdp.detach).toHaveBeenCalledOnce();
  expect(second.cdp.detach).toHaveBeenCalledOnce();
});
