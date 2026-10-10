import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { navigationGroupFor } from "../test-helpers/navigation";

// Standalone workspace tests have no shell. App tests must open the production
// disclosure through its native summary click before using the existing button.
export async function revealNavigation(name: string) {
  const navigation = document.querySelector('nav[aria-label="账户设置"]');
  if (!navigation) return;
  const toggle = screen.queryByRole("button", {
    name: "打开导航",
  });
  if (toggle) await userEvent.click(toggle);
  const group = navigationGroupFor(name);
  if (!group) return;
  const details = navigation.querySelector<HTMLDetailsElement>(
    `details[data-navigation-group="${group.id}"]`,
  );
  if (!details) throw new Error(`Missing navigation group: ${group.label}`);
  const summary = details.querySelector("summary");
  if (!summary)
    throw new Error(`Missing navigation disclosure: ${group.label}`);
  if (!details.open) await userEvent.click(summary);
  await waitFor(() => screen.getByRole("button", { name }));
}
