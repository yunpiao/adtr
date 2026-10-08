import type { Page } from "@playwright/test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const playwrightVersion = (
  require("playwright-core/package.json") as { version: string }
).version;

type OwningSession = {
  send(
    method: "Emulation.setFocusEmulationEnabled",
    parameters: { enabled: false },
  ): Promise<unknown>;
};

// This narrowly scoped adapter is tied to the repository's exact Playwright
// version. Its in-process connection exposes the server Page and the original
// Chromium session used by FrameSession._initialize(). Fail closed if that
// plumbing changes; a separately attached CDP session cannot reset its owner.
export async function useNativeTabLifecycle(page: Page) {
  if (
    playwrightVersion !== "1.56.1" ||
    page.context().browser()?.browserType().name() !== "chromium"
  )
    throw new Error(
      "Native tab lifecycle requires pinned Playwright 1.56.1 Chromium",
    );

  const connection = (
    page as unknown as {
      _connection?: { toImpl?: (value: Page) => unknown };
    }
  )._connection;
  const implementation = connection?.toImpl?.(page) as
    | { delegate?: { _mainFrameSession?: { _client?: OwningSession } } }
    | undefined;
  const session = implementation?.delegate?._mainFrameSession?._client;
  if (!session || typeof session.send !== "function")
    throw new Error(
      "Cannot access Playwright's owning Chromium session for native lifecycle",
    );

  // Chromium 141 EmulationHandler owns a visible capturer per CDP session.
  // Sending false on a new session is a no-op and leaves Playwright's capturer
  // active. Reset the SAME session that enabled it, before app navigation.
  // No DOM visibility/focus property or event is overridden or dispatched.
  // https://github.com/chromium/chromium/blob/141.0.7390.37/content/browser/devtools/protocol/emulation_handler.cc#L974
  await session.send("Emulation.setFocusEmulationEnabled", { enabled: false });
}

export async function requireSameNativeWindow(pages: Page[]) {
  const windowIDs: number[] = [];
  for (const page of pages) {
    const cdp = await page.context().newCDPSession(page);
    try {
      const { windowId } = await cdp.send("Browser.getWindowForTarget");
      if (!Number.isSafeInteger(windowId) || windowId <= 0)
        throw new Error("Chromium returned an invalid native window ID");
      windowIDs.push(windowId);
    } finally {
      await cdp.detach();
    }
  }
  if (windowIDs.length < 2 || new Set(windowIDs).size !== 1)
    throw new Error(
      `Lifecycle pages must be tabs in one native window: ${windowIDs}`,
    );
}
