import { expect, type Page } from "@playwright/test";
import { performance } from "node:perf_hooks";
import { navigationGroupFor } from "../test-helpers/navigation";
import { appendSafeEvent } from "./safe-runtime-diagnostics";

// This test-side breakpoint is independently checked against SocNavigation.
// Wait for its rendered controls too: matchMedia can precede React's change.
const compactQuery = "(max-width: 760px)";
const started = performance.now();
let operation = 0;
const contexts = new WeakMap<Page, { id: number; begin: number }>();
export type NavigationSnapshot = {
  width: number;
  height: number;
  compact: boolean;
  panelVisible: boolean;
  panelHidden: boolean;
  panelInert: boolean;
  panelDialog: boolean;
  toggleVisible: boolean;
  toggleEnabled: boolean;
  toggleExpanded: boolean;
  targetVisible: boolean;
  targetEnabled: boolean;
  groupOpen: boolean;
  pageClosed: boolean;
  unavailable: boolean;
};
type Stage = "start" | "layout-ready" | "menu-open" | "target-ready" | "failed";
const unavailable = (pageClosed: boolean): NavigationSnapshot => ({
  width: 0,
  height: 0,
  compact: false,
  panelVisible: false,
  panelHidden: false,
  panelInert: false,
  panelDialog: false,
  toggleVisible: false,
  toggleEnabled: false,
  toggleExpanded: false,
  targetVisible: false,
  targetEnabled: false,
  groupOpen: false,
  pageClosed,
  unavailable: true,
});

// Return only booleans and bounded geometry, never DOM text, identifiers,
// URL/query/hash, response bodies or exception messages.
export async function navigationSnapshot(
  page: Page,
  name: string,
): Promise<NavigationSnapshot> {
  if (page.isClosed()) return unavailable(true);
  const groupId = navigationGroupFor(name)?.id ?? null;
  try {
    return await page.evaluate(
      ({ targetName, groupId, query }) => {
        const visible = (element: Element | null): boolean => {
          if (!(element instanceof HTMLElement)) return false;
          const rect = element.getBoundingClientRect();
          if (rect.width <= 0 || rect.height <= 0) return false;
          for (
            let node: HTMLElement | null = element;
            node;
            node = node.parentElement
          ) {
            const style = getComputedStyle(node);
            if (
              node.hidden ||
              style.display === "none" ||
              style.visibility === "hidden" ||
              style.visibility === "collapse"
            )
              return false;
            if (
              node instanceof HTMLDetailsElement &&
              !node.open &&
              element !== node.querySelector("summary")
            )
              return false;
          }
          return true;
        };
        const enabled = (element: Element | null): boolean =>
          element instanceof HTMLButtonElement &&
          !element.disabled &&
          !element.closest("[inert]");
        const panel = document.getElementById("soc-navigation-panel");
        const toggle = document.querySelector(
          'button[aria-controls="soc-navigation-panel"][aria-label="打开导航"]',
        );
        const target =
          Array.from(panel?.querySelectorAll("button") ?? []).find(
            (button) =>
              (button.getAttribute("aria-label") ?? button.textContent?.trim()) ===
              targetName,
          ) ?? null;
        const details = groupId
          ? panel?.querySelector<HTMLDetailsElement>(
              `details[data-navigation-group="${groupId}"]`,
            )
          : null;
        return {
          width: Math.min(16384, Math.max(0, Math.floor(window.innerWidth))),
          height: Math.min(16384, Math.max(0, Math.floor(window.innerHeight))),
          compact: window.matchMedia(query).matches,
          panelVisible: visible(panel),
          panelHidden: panel?.hasAttribute("hidden") ?? true,
          panelInert: panel?.hasAttribute("inert") ?? true,
          panelDialog:
            panel?.getAttribute("role") === "dialog" &&
            panel?.getAttribute("aria-modal") === "true",
          toggleVisible: visible(toggle),
          toggleEnabled: enabled(toggle),
          toggleExpanded: toggle?.getAttribute("aria-expanded") === "true",
          targetVisible: visible(target),
          targetEnabled: enabled(target),
          groupOpen: groupId === null || details?.open === true,
          pageClosed: false,
          unavailable: false,
        };
      },
      { targetName: name, groupId, query: compactQuery },
    );
  } catch {
    return unavailable(page.isClosed());
  }
}

export function navigationLayout(snapshot: NavigationSnapshot) {
  if (snapshot.unavailable || snapshot.pageClosed) return "pending";
  if (snapshot.compact) {
    if (
      snapshot.panelVisible &&
      !snapshot.panelHidden &&
      !snapshot.panelInert &&
      snapshot.panelDialog &&
      snapshot.toggleExpanded &&
      !snapshot.toggleVisible
    )
      return "compact-open";
    if (
      !snapshot.panelVisible &&
      snapshot.panelHidden &&
      snapshot.panelInert &&
      !snapshot.panelDialog &&
      snapshot.toggleVisible &&
      snapshot.toggleEnabled &&
      !snapshot.toggleExpanded
    )
      return "compact-closed";
    return "pending";
  }
  return snapshot.panelVisible &&
    !snapshot.panelHidden &&
    !snapshot.panelInert &&
    !snapshot.panelDialog &&
    !snapshot.toggleVisible &&
    !snapshot.toggleExpanded
    ? "desktop-ready"
    : "pending";
}

const snapshotBooleans = [
  "compact",
  "panelVisible",
  "panelHidden",
  "panelInert",
  "panelDialog",
  "toggleVisible",
  "toggleEnabled",
  "toggleExpanded",
  "targetVisible",
  "targetEnabled",
  "groupOpen",
  "pageClosed",
  "unavailable",
] as const;
function validSnapshot(snapshot: NavigationSnapshot): boolean {
  if (
    !snapshot ||
    typeof snapshot !== "object" ||
    Object.keys(snapshot).sort().join(",") !==
      ["width", "height", ...snapshotBooleans].sort().join(",") ||
    snapshotBooleans.some((key) => typeof snapshot[key] !== "boolean") ||
    !Number.isInteger(snapshot.width) ||
    !Number.isInteger(snapshot.height)
  )
    return false;
  if (snapshot.unavailable)
    return (
      snapshot.width === 0 &&
      snapshot.height === 0 &&
      snapshotBooleans
        .filter((key) => key !== "unavailable" && key !== "pageClosed")
        .every((key) => !snapshot[key])
    );
  return (
    !snapshot.pageClosed &&
    snapshot.width >= 1 &&
    snapshot.width <= 16384 &&
    snapshot.height >= 1 &&
    snapshot.height <= 16384
  );
}

function record(
  stage: Stage,
  id: number,
  begin: number,
  snapshot: NavigationSnapshot,
) {
  if (!validSnapshot(snapshot)) return;
  try {
    appendSafeEvent({
      origin: "navigation",
      stage,
      operation: id,
      at_ms: Math.min(
        86_400_000,
        Math.max(0, Math.floor(performance.now() - started)),
      ),
      elapsed_ms: Math.min(
        86_400_000,
        Math.max(0, Math.floor(performance.now() - begin)),
      ),
      snapshot,
    });
  } catch {
    // Diagnostics never replace a failure, retry an action or emit raw errors.
  }
}

// Exercise only native controls. No force click, DOM mutation, hash shortcut,
// custom timeout or retry of the requested target action.
export async function revealNavigation(page: Page, name: string) {
  operation = Math.min(1_000_000, operation + 1);
  const id = operation;
  const begin = performance.now();
  contexts.set(page, { id, begin });
  let snapshot = await navigationSnapshot(page, name);
  record("start", id, begin, snapshot);
  try {
    await expect
      .poll(
        async () => {
          snapshot = await navigationSnapshot(page, name);
          return navigationLayout(snapshot);
        },
        {
          message:
            "Responsive navigation controls must match the current viewport",
        },
      )
      .not.toBe("pending");
    record("layout-ready", id, begin, snapshot);
    if (navigationLayout(snapshot) === "compact-closed") {
      await page.getByRole("button", { name: "打开导航", exact: true }).click();
      await expect
        .poll(
          async () => {
            snapshot = await navigationSnapshot(page, name);
            return navigationLayout(snapshot);
          },
          {
            message:
              "Native mobile navigation must open before selecting a target",
          },
        )
        .toBe("compact-open");
    }
    record("menu-open", id, begin, snapshot);
    const panel = page.locator("#soc-navigation-panel");
    await expect(panel).toBeVisible();
    await expect(panel).not.toHaveAttribute("inert", "");
    const group = navigationGroupFor(name);
    if (group) {
      const details = page.locator(`details[data-navigation-group="${group.id}"]`);
      const summary = details.locator("summary");
      await expect(summary).toContainText(group.label);
      await expect(summary).toBeVisible();
      if (
        !(
          await details.evaluate((element) => (element as HTMLDetailsElement).open)
        )
      )
        await summary.click();
      await expect(details).toHaveAttribute("open", "");
    }
    const target = page.getByRole("button", { name, exact: true });
    await expect(target).toBeVisible();
    await expect(target).toBeEnabled();
    snapshot = await navigationSnapshot(page, name);
    await expect
      .poll(
        async () => {
          snapshot = await navigationSnapshot(page, name);
          return (
            !snapshot.unavailable &&
            !snapshot.pageClosed &&
            snapshot.panelVisible &&
            !snapshot.panelInert &&
            snapshot.groupOpen &&
            snapshot.targetVisible &&
            snapshot.targetEnabled
          );
        },
        { message: "Navigation target must remain visible and enabled" },
      )
      .toBe(true);
    record("target-ready", id, begin, snapshot);
  } catch (error) {
    record("failed", id, begin, await navigationSnapshot(page, name));
    throw error;
  }
}

export async function navigateTo(page: Page, name: string) {
  await revealNavigation(page, name);
  // Exactly one requested action: never retry it after an uncertain outcome.
  try {
    await page.getByRole("button", { name, exact: true }).click();
  } catch (error) {
    const context = contexts.get(page);
    if (context)
      record(
        "failed",
        context.id,
        context.begin,
        await navigationSnapshot(page, name),
      );
    throw error;
  }
}
