import {
  expect,
  type Page,
  type TestInfo,
  type Request,
} from "@playwright/test";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { navigateTo } from "./navigation";
import { openMembers } from "./group-members-v4-helpers";
import { groupMembersV4ResponseJSON } from "./group-members-v4-response-observer";
export const emptyMembershipSources = [
  ".github/workflows/ci.yml",
  "internal/auth/access_http.go",
  "internal/auth/access_types.go",
  "internal/auth/group_members_v4_http.go",
  "internal/auth/resource_types.go",
  "internal/directoryassets/objects_v4.go",
  "internal/domainconfig/config.go",
  "internal/domains/directory_observation_v4_schema.go",
  "internal/domains/directory_v4_observation_codec.go",
  "internal/domains/group_members_v4.go",
  "internal/ldapconnection/directory_protocol_v4.go",
  "internal/ldapconnection/directory_v4.go",
  "internal/store/directory_v4_migration.go",
  "internal/testldapfixture/directory_v4.go",
  "scripts/ldap_e2e_fixture.py",
  "scripts/test_auth_e2e.py",
  "scripts/test_group_members_v4_empty_evidence_contract.py",
  "scripts/test_group_members_v4_empty_runner_contract.py",
  "scripts/test_group_members_v4_evidence_contract.py",
  "scripts/validate_directory_v4_evidence.py",
  "scripts/validate_group_members_v4_empty_evidence.py",
  "scripts/validate_group_members_v4_evidence.py",
  "web/e2e/directory-v2-response-observer.ts",
  "web/e2e/directory-v4-backend.spec.ts",
  "web/e2e/group-members-v4-empty-helpers.ts",
  "web/e2e/group-members-v4-empty.spec.ts",
  "web/e2e/group-members-v4-helpers.ts",
  "web/e2e/group-members-v4-response-observer.ts",
  "web/e2e/group-members-v4.spec.ts",
  "web/e2e/navigation.ts",
  "web/package-lock.json",
  "web/playwright.config.ts",
  "web/src/App.tsx",
  "web/src/GroupMembersV4Observer.test.ts",
  "web/src/GroupMembersV4Workspace.tsx",
  "web/src/SavedSourcePicker.tsx",
  "web/src/SocNavigation.tsx",
  "web/src/directory-v2-api.ts",
  "web/src/directory-v2-json.ts",
  "web/src/directory-v3-facts.ts",
  "web/src/directory-v4-facts.ts",
  "web/src/domain-selection-api.ts",
  "web/src/group-members-v4-api.ts",
  "web/src/group-members-v4-read.ts",
  "web/src/soc-shell.css",
  "web/src/style.css",
  "web/test-helpers/navigation.ts",
];
export async function emptyMembershipProvenance() {
  const git = (...args: string[]) =>
    execFileSync("git", args, { encoding: "utf8" }).trim();
  const root = git("rev-parse", "--show-toplevel");
  const checkoutCommit = git("rev-parse", "HEAD");
  return {
    checkoutCommit,
    checkoutTree: git("rev-parse", "HEAD^{tree}"),
    eventHeadCommit: process.env.ADTR_EVENT_HEAD_SHA ?? checkoutCommit,
    eventBaseCommit:
      process.env.ADTR_EVENT_BASE_SHA ?? git("rev-parse", "HEAD^"),
    workflowCommit: process.env.GITHUB_SHA ?? checkoutCommit,
    runId: process.env.GITHUB_RUN_ID ?? "local",
    runAttempt: process.env.GITHUB_RUN_ATTEMPT ?? "1",
    eventName: process.env.GITHUB_EVENT_NAME ?? "local",
    sourceDigests: Object.fromEntries(
      emptyMembershipSources.map((path) => [
        path,
        createHash("sha256")
          .update(readFileSync(`${root}/${path}`))
          .digest("hex"),
      ]),
    ),
  };
}
export async function captureEmptyMembers(
  page: Page,
  actorId: number,
  domainId: string,
  observationId: string,
  source: unknown,
  info: TestInfo,
) {
  const writes: string[] = [],
    detailRequests: string[] = [];
  const listener = (request: Request) => {
    const path = new URL(request.url()).pathname;
    if (path === "/api/group-assets/v4/members")
      detailRequests.push(request.method() + " " + path);
    if (
      path.startsWith("/api/") &&
      !["GET", "HEAD"].includes(request.method()) &&
      !(request.method() === "POST" && path === "/api/access/check")
    )
      writes.push(request.method() + " " + path);
  };
  page.on("request", listener);
  try {
    await navigateTo(page, "账户概览");
    const discovery = await openMembers(page, actorId);
    expect(discovery.query).toEqual({
      domainId,
      kind: "group",
      pageIdx: "1",
      pageSize: "25",
    });
    expect(discovery.value).toEqual({
      dictionaryVersion: 4,
      available: true,
      observationId,
      source,
      list: [],
      page: { pageIdx: 1, pageSize: 25, total: 0, totalPage: 0 },
    });
    const pending = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/directory/v4/observation" &&
        r.request().method() === "GET",
    );
    await page.getByRole("button", { name: "重读原观测", exact: true }).click();
    const response = await pending;
    expect(response.status()).toBe(200);
    expect(response.headers()["cache-control"]).toBe("no-store");
    expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId));
    const reread = {
      query: Object.fromEntries(new URL(response.url()).searchParams),
      value: await groupMembersV4ResponseJSON(response),
    };
    expect(reread.query).toEqual({ ...discovery.query, observationId });
    expect(reread.value).toEqual(discovery.value);
    const cases = [];
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 390, height: 844 },
    ]) {
      await page.setViewportSize(viewport);
      const workspace = page.getByRole("region", {
        name: "组成员原始观测 · 字典 4",
        exact: true,
      });
      const status = workspace.getByRole("status");
      await expect(status).toHaveText("本页观测未返回组对象；成员情况未知。");
      await expect(workspace).toContainText(observationId);
      await expect(
        workspace.getByRole("button", { name: "上一页", exact: true }),
      ).toBeDisabled();
      await expect(
        workspace.getByRole("button", { name: "下一页", exact: true }),
      ).toBeDisabled();
      await expect(
        workspace.getByLabel("每页组数", { exact: true }),
      ).toHaveValue("25");
      const pager = workspace.getByText(
        "第 1 页 · 共 0 页（仅本次观测中的组）",
        { exact: true },
      );
      await expect(pager).toBeVisible();
      const groups = workspace
        .getByRole("list", { name: "本次观测的组", exact: true })
        .getByRole("listitem");
      const detail = workspace.getByRole("region", {
        name: "组原始成员详情",
        exact: true,
      });
      await expect(groups).toHaveCount(0);
      await expect(detail).toHaveCount(0);
      await expect(
        workspace.getByRole("button", { name: "查看原始成员", exact: true }),
      ).toHaveCount(0);
      const rendered = {
        sourceValues: await workspace.locator("dl dd").allTextContents(),
        unknownText: await status.innerText(),
        pagerText: await pager.innerText(),
        previousDisabled: await workspace
          .getByRole("button", { name: "上一页", exact: true })
          .isDisabled(),
        nextDisabled: await workspace
          .getByRole("button", { name: "下一页", exact: true })
          .isDisabled(),
        groupCount: await groups.count(),
        detailCount: await detail.count(),
      };
      expect(rendered.sourceValues).toEqual([
        observationId,
        ...[
          "dc_host_name",
          "domain",
          "naming_context",
          "started_at",
          "completed_at",
        ].map((key) => (source as Record<string, unknown>)[key]),
      ]);
      await status.scrollIntoViewIfNeeded();
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      ).toBe(true);
      const filename = `group-members-v4-empty-reader-${viewport.width}.png`;
      const bytes = await page.screenshot({
        path: info.outputPath(filename),
        fullPage: false,
      });
      expect(bytes.readUInt32BE(16)).toBe(viewport.width);
      expect(bytes.readUInt32BE(20)).toBe(viewport.height);
      cases.push({
        viewport,
        rendered,
        screenshot: {
          filename,
          sha256: createHash("sha256").update(bytes).digest("hex"),
        },
      });
    }
    expect(writes).toEqual([]);
    expect(detailRequests).toEqual([]);
    return {
      actorId,
      authorization: "custom_readonly_role",
      discovery,
      reread,
      cases,
      writes,
      detailRequests,
    };
  } finally {
    page.off("request", listener);
  }
}
