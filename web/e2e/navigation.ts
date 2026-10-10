import { expect, type Page } from "@playwright/test";
import { navigationGroupFor } from "../test-helpers/navigation";

// Exercise the same disclosure and mobile controls as a person. Never force a
// hidden button click, alter details.open, or substitute a hash/API navigation.
export async function revealNavigation(page: Page, name: string) {
  const toggle = page.getByRole("button", { name: "打开导航", exact: true });
  if (await toggle.isVisible()) await toggle.click();
  const group = navigationGroupFor(name);
  if (!group) return;
  const details = page.locator(`details[data-navigation-group="${group.id}"]`);
  const summary = details.locator("summary");
  await expect(summary).toContainText(group.label);
  if (
    !(await details.evaluate((element) => (element as HTMLDetailsElement).open))
  )
    await summary.click();
  await expect(details).toHaveAttribute("open", "");
}

export async function navigateTo(page: Page, name: string) {
  await revealNavigation(page, name);
  await page.getByRole("button", { name, exact: true }).click();
}
