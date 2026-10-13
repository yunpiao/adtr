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
export const paginationMembershipSources = [
  "internal/testldapfixture/main.go",
  "internal/testldapfixture/server.go",
  "internal/testldapfixture/directory.go",
  "internal/testldapfixture/directory_v4_pagination.go",
  "internal/testldapfixture/directory_v4_pagination_test.go",
  "scripts/test_group_members_v4_pagination_fixture_contract.py",
  "scripts/test_group_members_v4_pagination_evidence_contract.py",
  "scripts/test_group_members_v4_pagination_runner_contract.py",
  "scripts/validate_group_members_v4_pagination_evidence.py",
  "web/e2e/group-members-v4-pagination-helpers.ts",
  "web/e2e/group-members-v4-pagination.spec.ts",
  "web/src/GroupMembersV4Workspace.test.tsx",

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
export async function paginationMembershipProvenance() {
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
      paginationMembershipSources.map((path) => [
        path,
        createHash("sha256")
          .update(readFileSync(`${root}/${path}`))
          .digest("hex"),
      ]),
    ),
  };
}
// Expected fixture facts are independent of both the Go generator and stored bytes.
export function paginationExpectedObjects() {
  return Array.from({ length: 30 }, (_, i) => {
    const n = i + 1;
    const kind = n <= 26 ? "group" : n <= 28 ? "user" : "computer";
    const index = n <= 26 ? n : n <= 28 ? n - 26 : n - 28;
    const name = `pagination-${kind}-${String(index).padStart(2, "0")}`;
    const unit =
      kind === "group" ? "Groups" : kind === "user" ? "Users" : "Computers";
    const hex = n.toString(16).padStart(2, "0");
    const rawMember =
      n === 1 || n > 26
        ? null
        : n === 2
          ? []
          : n === 26
            ? [
                "CN=page-two-only,OU=Users,DC=synthetic,DC=invalid",
                "CN=raw\u0000\n😀\u202e<script>,DC=synthetic,DC=invalid",
                "\\u0000",
                "CN=page-two-last,OU=Users,DC=synthetic,DC=invalid",
              ]
            : [
                `CN=page-member-${String(n).padStart(2, "0")},OU=Users,DC=synthetic,DC=invalid`,
              ];
    return {
      objectGUID: `${hex.repeat(4)}-${hex.repeat(2)}-${hex.repeat(2)}-${hex.repeat(2)}-${hex.repeat(6)}`,
      distinguishedName: `CN=${name},OU=${unit},DC=synthetic,DC=invalid`,
      kind,
      objectClass:
        kind === "group"
          ? ["group", "top"]
          : kind === "user"
            ? ["organizationalperson", "person", "top", "user"]
            : ["computer", "organizationalperson", "person", "top", "user"],
      samAccountName: name + (kind === "computer" ? "$" : ""),
      userAccountControl: null,
      objectSid: null,
      mail: null,
      description: null,
      whenCreated: null,
      primaryGroupID: null,
      servicePrincipalName: null,
      rawMember,
      returnedMemberCount: rawMember === null ? null : rawMember.length,
      memberReadState: rawMember === null ? "not_returned" : "returned",
    };
  });
}
const display = (raw: string) =>
  raw.replaceAll("\\", "\\\\").replace(/[\p{Cc}\p{Cf}\u2028\u2029]/gu, (c) => {
    const point = c.codePointAt(0)!;
    return point <= 0xffff
      ? `\\u${point.toString(16).padStart(4, "0")}`
      : `\\u{${point.toString(16)}}`;
  });
export async function capturePaginationMembers(
  page: Page,
  actorId: number,
  domainId: string,
  connection: { revision: string; credentialRevision: string },
  observationId: string,
  source: unknown,
  info: TestInfo,
) {
  const writes: string[] = [];
  const listener = (request: Request) => {
    const path = new URL(request.url()).pathname;
    if (
      path.startsWith("/api/") &&
      !["GET", "HEAD"].includes(request.method()) &&
      !(path === "/api/access/check" && request.method() === "POST")
    )
      writes.push(request.method() + " " + path);
  };
  page.on("request", listener);
  const expected = paginationExpectedObjects().slice(0, 26);
  const cases = [];
  try {
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 390, height: 844 },
    ]) {
      await page.setViewportSize(viewport);
      await navigateTo(page, "账户概览");
      const workspace = page.getByRole("region", {
        name: "组成员原始观测 · 字典 4",
        exact: true,
      });
      const steps = [];
      const capture = async (path: string, action: () => Promise<unknown>) => {
        const pending = page.waitForResponse(
          (r) =>
            new URL(r.url()).pathname === path &&
            r.request().method() === "GET",
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
      };
      const shot = async (name: string) => {
        expect(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= window.innerWidth,
          ),
        ).toBe(true);
        const filename = `group-members-v4-pagination-reader-${viewport.width}-${name}.png`;
        const bytes = await page.screenshot({
          path: info.outputPath(filename),
          fullPage: false,
        });
        expect(bytes.readUInt32BE(16)).toBe(viewport.width);
        expect(bytes.readUInt32BE(20)).toBe(viewport.height);
        return {
          filename,
          sha256: createHash("sha256").update(bytes).digest("hex"),
        };
      };
      const listStep = async (
        name: string,
        reply: { query: Record<string, string>; value: any },
        pageIdx: number,
        pageSize: number,
      ) => {
        const want = expected.slice(
          (pageIdx - 1) * pageSize,
          pageIdx * pageSize,
        );
        expect(reply.query).toEqual({
          domainId,
          kind: "group",
          pageIdx: String(pageIdx),
          pageSize: String(pageSize),
          ...(name === "discovery" ? {} : { observationId }),
        });
        expect(reply.value).toEqual({
          dictionaryVersion: 4,
          available: true,
          observationId,
          source,
          list: want,
          page: {
            pageIdx,
            pageSize,
            total: 26,
            totalPage: Math.ceil(26 / pageSize),
          },
        });
        const items = workspace
          .getByRole("list", { name: "本次观测的组", exact: true })
          .getByRole("listitem");
        await expect(items).toHaveCount(want.length);
        await expect(items.locator("span:nth-of-type(2)")).toHaveText(
          want.map((o) => o.objectGUID),
        );
        const pager = workspace.getByText(
          `第 ${pageIdx} 页 · 共 ${Math.ceil(26 / pageSize)} 页（仅本次观测中的组）`,
          { exact: true },
        );
        await expect(pager).toBeVisible();
        const previous = workspace.getByRole("button", {
          name: "上一页",
          exact: true,
        });
        const next = workspace.getByRole("button", {
          name: "下一页",
          exact: true,
        });
        if (pageIdx === 1) await expect(previous).toBeDisabled();
        else await expect(previous).toBeEnabled();
        if (pageIdx === Math.ceil(26 / pageSize))
          await expect(next).toBeDisabled();
        else await expect(next).toBeEnabled();
        await expect(
          workspace.getByLabel("每页组数", { exact: true }),
        ).toHaveValue(String(pageSize));
        await expect(
          workspace.getByRole("region", {
            name: "组原始成员详情",
            exact: true,
          }),
        ).toHaveCount(0);
        const rendered = {
          guids: await items.locator("span:nth-of-type(2)").allTextContents(),
          pagerText: await pager.innerText(),
          previousDisabled: await previous.isDisabled(),
          nextDisabled: await next.isDisabled(),
          pageSize: await workspace
            .getByLabel("每页组数", { exact: true })
            .inputValue(),
          sourceValues: await workspace.locator("dl dd").allTextContents(),
          detailCount: await workspace
            .getByRole("region", { name: "组原始成员详情", exact: true })
            .count(),
        };
        expect(rendered.sourceValues).toEqual([
          observationId,
          ...[
            "dc_host_name",
            "domain",
            "naming_context",
            "started_at",
            "completed_at",
          ].map((k) => (source as Record<string, unknown>)[k]),
        ]);
        await pager.scrollIntoViewIfNeeded();
        steps.push({ name, ...reply, rendered, screenshot: await shot(name) });
      };
      await listStep("discovery", await openMembers(page, actorId), 1, 25);
      await listStep(
        "next",
        await capture("/api/directory/v4/observation", () =>
          workspace
            .getByRole("button", { name: "下一页", exact: true })
            .click(),
        ),
        2,
        25,
      );
      const detail = await capture("/api/group-assets/v4/members", () =>
        workspace
          .getByRole("button", { name: "查看原始成员", exact: true })
          .click(),
      );
      expect(detail.query).toEqual({
        domainId,
        expectedRevision: connection.revision,
        expectedCredentialRevision: connection.credentialRevision,
        observationId,
        objectGUID: expected[25].objectGUID,
      });
      expect(detail.value).toEqual({
        dictionaryVersion: 4,
        selection: {
          domainId,
          revision: connection.revision,
          credentialRevision: connection.credentialRevision,
        },
        observationId,
        source,
        object: expected[25],
      });
      const region = workspace.getByRole("region", {
        name: "组原始成员详情",
        exact: true,
      });
      await expect(region).toBeVisible();
      const memberItems = region
        .getByRole("list", {
          name: "原始 member 值（可逆展示转义）",
          exact: true,
        })
        .getByRole("listitem");
      await expect(memberItems).toHaveText(
        expected[25].rawMember!.map(display),
      );
      const identity = await region.locator("p").first().innerText();
      const match = /^对象 GUID：(\S+) · 观测编号：(\S+)$/u.exec(identity);
      expect(match).not.toBeNull();
      const countText = await region
        .getByText("本次返回 member 值数：4，不代表完整成员数。", {
          exact: true,
        })
        .innerText();
      await region.scrollIntoViewIfNeeded();
      steps.push({
        name: "detail",
        ...detail,
        rendered: {
          rawMember: await memberItems.allTextContents(),
          guid: match![1],
          observationId: match![2],
          returnedMemberCountText: countText,
        },
        screenshot: await shot("detail"),
      });
      await listStep(
        "previous",
        await capture("/api/directory/v4/observation", () =>
          workspace
            .getByRole("button", { name: "上一页", exact: true })
            .click(),
        ),
        1,
        25,
      );
      await listStep(
        "size50",
        await capture("/api/directory/v4/observation", () =>
          workspace.getByLabel("每页组数", { exact: true }).selectOption("50"),
        ),
        1,
        50,
      );
      await listStep(
        "size25",
        await capture("/api/directory/v4/observation", () =>
          workspace.getByLabel("每页组数", { exact: true }).selectOption("25"),
        ),
        1,
        25,
      );
      await listStep(
        "reread",
        await capture("/api/directory/v4/observation", () =>
          workspace
            .getByRole("button", { name: "重读原观测", exact: true })
            .click(),
        ),
        1,
        25,
      );
      cases.push({ viewport, steps });
    }
    expect(writes).toEqual([]);
    return { actorId, authorization: "custom_readonly_role", cases, writes };
  } finally {
    page.off("request", listener);
  }
}
