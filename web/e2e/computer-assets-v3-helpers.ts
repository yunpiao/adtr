import { expect, type Page } from "@playwright/test";
import { navigateTo } from "./navigation";
import { directoryV2ResponseJSON } from "./directory-v2-response-observer";
export async function openComputerList(page: Page) {
  await navigateTo(page, "计算机资产");
  await page
    .getByRole("radio", { name: "选择数据源 synthetic.invalid", exact: true })
    .check();
  await page
    .getByRole("button", { name: "核对并打开连接详情", exact: true })
    .click();
  await expect(
    page.getByRole("table", { name: "计算机资产列表", exact: true }),
  ).toBeVisible();
}
export async function computerResponse(
  page: Page,
  action: () => Promise<unknown>,
) {
  const pending = page.waitForResponse(
    (r) =>
      new URL(r.url()).pathname === "/api/computer-assets/v3" &&
      r.request().method() === "GET",
  );
  await action();
  const response = await pending;
  expect(response.status()).toBe(200);
  expect(response.headers()["cache-control"]).toBe("no-store");
  return {
    url: new URL(response.url()),
    value: await directoryV2ResponseJSON(response),
  };
}
export function expectedComputers() {
  return Array.from({ length: 23 }, (_, i) => {
    const n = i + 1,
      h = n.toString(16).padStart(2, "0"),
      raw = h.repeat(16),
      name = `computer-${String(n).padStart(2, "0")}`;
    return {
      objectGUID: `${raw.slice(0, 8)}-${raw.slice(8, 12)}-${raw.slice(12, 16)}-${raw.slice(16, 20)}-${raw.slice(20)}`,
      distinguishedName: `CN=${name},OU=Computers,DC=synthetic,DC=invalid`,
      kind: "computer",
      objectClass: [
        "computer",
        "organizationalPerson",
        "person",
        "top",
        "user",
      ],
      samAccountName: n === 23 ? null : name + "$",
      userAccountControl: n === 1 ? 0 : 4096,
      objectSid: n === 23 ? null : `S-1-5-${n}`,
      mail: n === 23 ? null : "mail-only@example.test",
      description: n === 23 ? null : ["observed\u0000\n<script>😀"],
      whenCreated:
        n === 23
          ? null
          : n === 1
            ? "0001-01-01T00:00:00Z"
            : "2026-01-01T00:00:00Z",
      primaryGroupID: n === 23 ? null : n === 1 ? 0 : 515,
      servicePrincipalName:
        n === 23
          ? null
          : n === 1
            ? ["HOST/Z", "host/a\u0000\n😀<script>", "HOST/a"]
            : [
                `HOST/computer-${String(n).padStart(2, "0")}.synthetic.invalid`,
                "HTTP/fixture",
              ],
    };
  });
}
