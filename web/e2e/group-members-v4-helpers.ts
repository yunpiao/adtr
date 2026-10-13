import { expect, type Page, type TestInfo } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { navigateTo } from "./navigation";
import { groupMembersV4ResponseJSON } from "./group-members-v4-response-observer";

export const membershipSources = [
  "web/e2e/group-members-v4-response-observer.ts",
  "web/src/GroupMembersV4Observer.test.ts",
  "web/e2e/group-members-v4.spec.ts",
  "web/e2e/group-members-v4-helpers.ts",
  "scripts/validate_group_members_v4_evidence.py",
  "scripts/test_group_members_v4_evidence_contract.py",
  "web/e2e/navigation.ts",
  "web/test-helpers/navigation.ts",
  "web/playwright.config.ts",
  "web/src/App.tsx",
  "web/src/SocNavigation.tsx",
  "web/src/GroupMembersV4Workspace.tsx",
  "web/src/group-members-v4-api.ts",
  "web/src/group-members-v4-read.ts",
  "web/src/directory-v4-facts.ts",
  "web/src/directory-v2-json.ts",
  "web/e2e/directory-v2-response-observer.ts",
  "web/src/directory-v3-facts.ts",
  "web/src/directory-v2-api.ts",
  "web/src/SavedSourcePicker.tsx",
  "web/src/domain-selection-api.ts",
  "web/src/style.css",
  "web/src/soc-shell.css",
  "web/package-lock.json",
  "internal/auth/access_types.go",
  "internal/auth/access_http.go",
  "internal/auth/resource_types.go",
  "internal/auth/group_members_v4_http.go",
  "internal/domains/group_members_v4.go",
];
export async function membershipProvenance() {
  const git = (...args: string[]) =>
    execFileSync("git", args, { encoding: "utf8" }).trim();
  const root = git("rev-parse", "--show-toplevel");
  return {
    checkoutCommit: git("rev-parse", "HEAD"),
    checkoutTree: git("rev-parse", "HEAD^{tree}"),
    runId: process.env.GITHUB_RUN_ID ?? "local",
    runAttempt: process.env.GITHUB_RUN_ATTEMPT ?? "1",
    sourceDigests: Object.fromEntries(
      membershipSources.map((path) => [
        path,
        createHash("sha256")
          .update(readFileSync(`${root}/${path}`))
          .digest("hex"),
      ]),
    ),
  };
}
async function captureResponse(
  page: Page,
  path: string,
  actorId: number,
  action: () => Promise<unknown>,
) {
  const pending = page.waitForResponse(
    (r) => new URL(r.url()).pathname === path && r.request().method() === "GET",
  );
  await action();
  const response = await pending;
  expect(response.status()).toBe(200);
  expect(response.headers()["cache-control"]).toBe("no-store");
  expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId));
  return {
    query: Object.fromEntries(new URL(response.url()).searchParams),
    value: await groupMembersV4ResponseJSON(response),
  };
}
export async function openMembers(page: Page, actorId: number) {
  await navigateTo(page, "组原始成员 · 字典 4");
  await expect(
    page.getByRole("heading", { name: "组成员原始观测 · 字典 4", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("radio", { name: "选择数据源 synthetic.invalid", exact: true })
    .check();
  return captureResponse(page, "/api/directory/v4/observation", actorId, () =>
    page
      .getByRole("button", { name: "核对并打开连接详情", exact: true })
      .click(),
  );
}
// Independent expected display, not the production display helper.
function display(raw: string) {
  return raw
    .replaceAll("\\", "\\\\")
    .replace(/[\p{Cc}\p{Cf}\u2028\u2029]/gu, (c) => {
      const point = c.codePointAt(0)!;
      return point <= 0xffff
        ? `\\u${point.toString(16).padStart(4, "0")}`
        : `\\u{${point.toString(16)}}`;
    });
}
export async function captureMembers(
  page: Page,
  actorId: number,
  domainId: string,
  connection: { revision: string; credentialRevision: string },
  observationId: string,
  source: unknown,
  expected: any[],
  info: TestInfo,
  reader = false,
) {
  const writes: string[] = [];
  const listener = (request: import("@playwright/test").Request) => {
    if (
      new URL(request.url()).pathname.startsWith("/api/") &&
      !["GET", "HEAD"].includes(request.method()) &&
      !(
        request.method() === "POST" &&
        new URL(request.url()).pathname === "/api/access/check"
      )
    )
      writes.push(request.method() + " " + new URL(request.url()).pathname);
  };
  page.on("request", listener);
  try {
    await navigateTo(page, "账户概览");
    const discovery = await openMembers(page, actorId);
    expect(discovery.query).toMatchObject({ domainId, kind: "group" });
    expect(discovery.value.list).toEqual(
      expected.filter((o) => o.kind === "group"),
    );
    expect(discovery.value.observationId).toBe(observationId);
    expect(discovery.value.source).toEqual(source);
    const reread = await captureResponse(
      page,
      "/api/directory/v4/observation",
      actorId,
      () =>
        page.getByRole("button", { name: "重读原观测", exact: true }).click(),
    );
    expect(reread.query.observationId).toBe(observationId);
    expect(reread.value).toEqual(discovery.value);
    const cases = [];
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 390, height: 844 },
    ]) {
      await page.setViewportSize(viewport);
      for (const index of [12, 13, 14]) {
        const object = expected[index];
        const reply = await captureResponse(
          page,
          "/api/group-assets/v4/members",
          actorId,
          () =>
            page
              .getByRole("list", { name: "本次观测的组", exact: true })
              .getByRole("listitem")
              .filter({ hasText: object.objectGUID })
              .getByRole("button", { name: "查看原始成员", exact: true })
              .click(),
        );
        expect(reply.query).toEqual({
          domainId,
          expectedRevision: connection.revision,
          expectedCredentialRevision: connection.credentialRevision,
          observationId,
          objectGUID: object.objectGUID,
        });
        expect(reply.value.object).toEqual(object);
        expect(reply.value.source).toEqual(source);
        expect(reply.value.observationId).toBe(observationId);
        const detail = page.getByRole("region", {
          name: "组原始成员详情",
          exact: true,
        });
        await expect(detail).toBeVisible();
        await expect(detail).toContainText(object.objectGUID);
        await expect(detail).toContainText(observationId);
        let rendered: string[] = [];
        if (object.rawMember === null)
          await expect(detail).toContainText(
            "member 未返回，成员情况未知；无法区分属性缺失与权限省略。",
          );
        else if (object.rawMember.length === 0)
          await expect(detail).toContainText(
            "member 已返回空列表，不能据此断定组为空。",
          );
        else {
          rendered = await detail.getByRole("listitem").allTextContents();
          expect(rendered).toEqual(object.rawMember.map(display));
          await expect(detail.locator("a, script")).toHaveCount(0);
        }
        await detail.scrollIntoViewIfNeeded();
        const geometry = await page.evaluate(() => ({
          viewport: window.innerWidth,
          scrollWidth: document.documentElement.scrollWidth,
          overflowing: Array.from(document.querySelectorAll("*"))
            .filter((element) => {
              const rect = element.getBoundingClientRect();
              return (
                rect.right > window.innerWidth ||
                element.scrollWidth > element.clientWidth
              );
            })
            .slice(0, 30)
            .map((element) => {
              const rect = element.getBoundingClientRect();
              // Geometry only: never include observed text, values or attributes.
              return {
                tag: element.tagName,
                width: rect.width,
                right: rect.right,
                clientWidth: element.clientWidth,
                scrollWidth: element.scrollWidth,
              };
            }),
        }));
        expect(
          geometry.scrollWidth <= geometry.viewport,
          `group-members-v4 viewport geometry: ${JSON.stringify(geometry)}`,
        ).toBe(true);
        const filename = `group-members-v4-${reader ? "reader-" : ""}${viewport.width}-${index}.png`;
        const bytes = await page.screenshot({
          path: info.outputPath(filename),
          fullPage: false,
        });
        expect(bytes.readUInt32BE(16)).toBe(viewport.width);
        expect(bytes.readUInt32BE(20)).toBe(viewport.height);
        cases.push({
          viewport,
          reply,
          rendered,
          screenshot: {
            filename,
            sha256: createHash("sha256").update(bytes).digest("hex"),
          },
        });
        await page
          .getByRole("button", { name: "关闭成员详情", exact: true })
          .click();
        await expect(detail).toHaveCount(0);
      }
    }
    expect(writes).toEqual([]);
    return {
      actorId,
      authorization: reader
        ? "custom_readonly_role"
        : "platform_admin_after_v4_use_revoke",
      discovery,
      reread,
      cases,
      writes,
    };
  } finally {
    page.off("request", listener);
  }
}
