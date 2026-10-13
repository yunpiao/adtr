import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { resolve } from "node:path";
import { expect, type Page } from "@playwright/test";
import { navigateTo } from "./navigation";
import { directoryV2ResponseJSON } from "./directory-v2-response-observer";
export async function openGroupList(page: Page) {
  await navigateTo(page, "组资产");
  await page
    .getByRole("radio", { name: "选择数据源 synthetic.invalid", exact: true })
    .check();
  await page
    .getByRole("button", { name: "核对并打开连接详情", exact: true })
    .click();
  await expect(
    page.getByRole("table", { name: "组资产列表", exact: true }),
  ).toBeVisible();
}
export async function groupResponse(
  page: Page,
  action: () => Promise<unknown>,
) {
  const pending = page.waitForResponse(
    (r) =>
      new URL(r.url()).pathname === "/api/group-assets/v3" &&
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
export function expectedGroups() {
  return Array.from({ length: 23 }, (_, i) => {
    const n = i + 1,
      h = n.toString(16).padStart(2, "0"),
      raw = h.repeat(16),
      name = `group-${String(n).padStart(2, "0")}`;
    return {
      objectGUID: `${raw.slice(0, 8)}-${raw.slice(8, 12)}-${raw.slice(12, 16)}-${raw.slice(16, 20)}-${raw.slice(20)}`,
      distinguishedName: `CN=${name},OU=Groups,DC=synthetic,DC=invalid`,
      kind: "group",
      objectClass: ["group", "top"],
      samAccountName: n === 23 ? null : n === 20 ? "İΣ𐐀 literal.*" : name,
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
                `HOST/group-${String(n).padStart(2, "0")}.synthetic.invalid`,
                "HTTP/fixture",
              ],
    };
  });
}

// Only public repository/build metadata is recorded; never dump the environment.
export function groupEvidenceProvenance() {
  const root = execFileSync("git", ["rev-parse", "--show-toplevel"], {
    encoding: "utf8",
  }).trim();
  const git = (...args: string[]) =>
    execFileSync("git", args, { cwd: root, encoding: "utf8" }).trim();
  const sources = [
    "web/e2e/group-assets-v3.spec.ts",
    "web/e2e/group-assets-v3-readers.spec.ts",
    "web/e2e/group-assets-v3-fixture.ts",
    "web/e2e/group-assets-v3-helpers.ts",
    "web/e2e/directory-v2-response-observer.ts",
    "web/e2e/user-assets-v2-held-response.ts",
    "web/e2e/user-assets-v2-abort-isolation.ts",
    "web/e2e/native-stream-proof.ts",
    "web/e2e/native-tab-lifecycle.ts",
    "web/e2e/navigation.ts",
    "web/e2e/safe-runtime-diagnostics.ts",
    "scripts/e2e_runtime_diagnostics.py",
    "scripts/test_safe_navigation_diagnostics_contract.py",
    "web/src/e2e-navigation.test.ts",
    "web/src/SocNavigation.test.tsx",
    "web/playwright.config.ts",
    "web/src/GroupAssetsV3Workspace.tsx",
    "web/src/user-assets-soc.css",
    "web/src/style.css",
    "web/src/soc-shell.css",
    "cmd/adtr/main.go",
    "web/src/group-assets-v3-api.ts",
    "web/src/directory-v3-facts.ts",
    "web/src/directory-search-lowercase.ts",
    "web/src/directory-search-case-map.ts",
    "web/src/App.tsx",
    "web/src/SocNavigation.tsx",
    "internal/domains/group_assets_v3.go",
    "internal/auth/group_assets_v3_http.go",
    "internal/testldapfixture/group_assets_v3.go",
    "scripts/test_auth_e2e.py",
    "scripts/ldap_e2e_fixture.py",
    ".github/workflows/ci.yml",
    "web/package-lock.json",
  ];
  return {
    checkoutCommit: git("rev-parse", "HEAD"),
    checkoutTree: git("rev-parse", "HEAD^{tree}"),
    checkoutParents: git("cat-file", "-p", "HEAD")
      .split("\n\n", 1)[0]
      .split("\n")
      .filter((line) => /^parent [0-9a-f]{40}$/u.test(line))
      .map((line) => line.slice(7)),
    dirty: git("status", "--porcelain") !== "",
    eventHeadCommit: process.env.ADTR_EVENT_HEAD_SHA ?? null,
    eventBaseCommit: process.env.ADTR_EVENT_BASE_SHA ?? null,
    workflowCommit: process.env.GITHUB_SHA ?? null,
    runID: process.env.GITHUB_RUN_ID ?? null,
    runAttempt: process.env.GITHUB_RUN_ATTEMPT ?? null,
    sourceSHA256: Object.fromEntries(
      sources.sort().map((path) => [
        path,
        createHash("sha256")
          .update(readFileSync(resolve(root, path)))
          .digest("hex"),
      ]),
    ),
  };
}
