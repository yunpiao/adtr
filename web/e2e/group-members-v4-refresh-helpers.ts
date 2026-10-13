import {
  expect,
  type Page,
  type TestInfo,
  type Request,
} from "@playwright/test";
import { createHash } from "node:crypto";
import {
  emptyMembershipSources,
  emptyMembershipProvenance,
} from "./group-members-v4-empty-helpers";
import { openMembers } from "./group-members-v4-helpers";
import { groupMembersV4ResponseJSON } from "./group-members-v4-response-observer";
import { navigateTo } from "./navigation";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
export const refreshMembershipSources = [
  ...emptyMembershipSources,
  "internal/testldapfixture/main.go",
  "internal/testldapfixture/server.go",
  "internal/testldapfixture/directory.go",
  "internal/testldapfixture/directory_v2.go",
  "internal/testldapfixture/empty_transition.go",
  "internal/testldapfixture/empty_transition_linux.go",
  "internal/testldapfixture/empty_transition_other.go",
  "internal/testldapfixture/empty_transition_test.go",
  "internal/testldapfixture/empty_transition_config_test.go",
  "web/src/asset-empty-transition-marker.test.ts",
  "internal/testldapfixture/directory_v4_refresh_test.go",
  "internal/directoryassets/objects.go",
  "internal/directoryassets/objects_v2.go",
  "internal/directoryassets/objects_v3.go",
  "web/e2e/asset-empty-transition-marker.ts",
  "web/e2e/group-members-v4-refresh.spec.ts",
  "web/e2e/group-members-v4-refresh-helpers.ts",
  "scripts/validate_group_members_v4_refresh_evidence.py",
  "scripts/test_group_members_v4_refresh_evidence_contract.py",
  "scripts/test_group_members_v4_refresh_runner_contract.py",
  "scripts/test_group_members_v4_refresh_fixture_contract.py",
  "web/src/GroupMembersV4Workspace.test.tsx",
];
export async function refreshMembershipProvenance() {
  const original = await emptyMembershipProvenance();
  const root = execFileSync("git", ["rev-parse", "--show-toplevel"], {
    encoding: "utf8",
  }).trim();
  return {
    ...original,
    sourceDigests: Object.fromEntries(
      [...new Set(refreshMembershipSources)].map((path) => [
        path,
        createHash("sha256")
          .update(readFileSync(`${root}/${path}`))
          .digest("hex"),
      ]),
    ),
  };
}
const display = (raw: string) =>
  raw
    .replaceAll("\\", "\\\\")
    .replace(
      /[\p{Cc}\p{Cf}\u2028\u2029]/gu,
      (c) => `\\u${c.codePointAt(0)!.toString(16).padStart(4, "0")}`,
    );
export async function captureRefreshMembers(
  page: Page,
  actorId: number,
  domainId: string,
  old: any,
  info: TestInfo,
  advance: () => Promise<any>,
) {
  const workspace = page.getByRole("region", {
    name: "组成员原始观测 · 字典 4",
    exact: true,
  });
  const detail = workspace.getByRole("region", {
    name: "组原始成员详情",
    exact: true,
  });
  const writes: string[] = [];
  const detailRequests: { query: Record<string, string> }[] = [];
  const listener = (request: Request) => {
    const path = new URL(request.url()).pathname;
    if (path === "/api/group-assets/v4/members")
      detailRequests.push({
        query: Object.fromEntries(new URL(request.url()).searchParams),
      });
    if (
      path.startsWith("/api/") &&
      !["GET", "HEAD"].includes(request.method()) &&
      !(path === "/api/access/check" && request.method() === "POST")
    )
      writes.push(`${request.method()} ${path}`);
  };
  page.on("request", listener);
  try {
    const capture = async (path: string, action: () => Promise<unknown>) => {
      const pending = page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === path && r.request().method() === "GET",
      );
      await action();
      const r = await pending;
      expect(r.status()).toBe(200);
      expect(r.headers()["cache-control"]).toBe("no-store");
      expect(r.headers()["x-adtr-user-id"]).toBe(String(actorId));
      return {
        query: Object.fromEntries(new URL(r.url()).searchParams),
        value: await groupMembersV4ResponseJSON(r),
      };
    };
    const listPath = "/api/directory/v4/observation",
      detailPath = "/api/group-assets/v4/members";
    const group = old.objects[14];
    const groups = old.objects.filter((o: any) => o.kind === "group");
    const rendered = async (id: string, count: number, hasDetail: boolean) => {
      await expect(workspace.locator("dl dd").first()).toHaveText(id);
      await expect(
        workspace
          .getByRole("list", { name: "本次观测的组", exact: true })
          .getByRole("listitem"),
      ).toHaveCount(count);
      await expect(detail).toHaveCount(hasDetail ? 1 : 0);
      if (hasDetail) {
        await expect(detail).toContainText(id);
        await expect(detail).toContainText(group.objectGUID);
        await expect(detail.getByRole("listitem")).toHaveText(
          group.rawMember.map(display),
        );
      }
      return {
        observationId: await workspace.locator("dl dd").first().innerText(),
        sourceValues: await workspace.locator("dl dd").allTextContents(),
        pagerText: await workspace
          .getByText(`第 1 页 · 共 ${count ? 1 : 0} 页（仅本次观测中的组）`, {
            exact: true,
          })
          .innerText(),
        previousDisabled: await workspace
          .getByRole("button", { name: "上一页", exact: true })
          .isDisabled(),
        nextDisabled: await workspace
          .getByRole("button", { name: "下一页", exact: true })
          .isDisabled(),
        groupCount: await workspace
          .getByRole("list", { name: "本次观测的组", exact: true })
          .getByRole("listitem")
          .count(),
        detailCount: await detail.count(),
        rawMember: hasDetail
          ? await detail.getByRole("listitem").allTextContents()
          : [],
      };
    };
    const openDetail = () =>
      capture(detailPath, () =>
        workspace
          .getByRole("listitem")
          .filter({ has: page.getByText(group.objectGUID, { exact: true }) })
          .getByRole("button", { name: "查看原始成员", exact: true })
          .click(),
      );
    await page.setViewportSize({ width: 1440, height: 900 });
    const initialDiscovery = await openMembers(page, actorId);
    expect(initialDiscovery.query).toEqual({
      domainId,
      kind: "group",
      pageIdx: "1",
      pageSize: "25",
    });
    expect(initialDiscovery.value).toEqual({
      dictionaryVersion: 4,
      available: true,
      observationId: old.taskUUID,
      source: old.source,
      list: groups,
      page: { pageIdx: 1, pageSize: 25, total: 12, totalPage: 1 },
    });
    const initialDetail = await openDetail();
    expect(initialDetail.value.object).toEqual(group);
    const initialRendered = await rendered(old.taskUUID, 12, true);
    await detail.scrollIntoViewIfNeeded();
    const initialFilename = "group-members-v4-refresh-reader-1440-initial.png";
    const initialBytes = await page.screenshot({
      path: info.outputPath(initialFilename),
      fullPage: false,
    });
    expect(initialBytes.readUInt32BE(16)).toBe(1440);
    expect(initialBytes.readUInt32BE(20)).toBe(900);
    const initialScreenshot = {
      filename: initialFilename,
      sha256: createHash("sha256").update(initialBytes).digest("hex"),
    };
    const transition = await advance();
    const empty = transition.emptyBackend;
    const retained = await rendered(old.taskUUID, 12, true);
    const steps: any[] = [];
    const pinned = await capture(listPath, () =>
      page.getByRole("button", { name: "重读原观测", exact: true }).click(),
    );
    expect(pinned.query).toEqual({
      ...initialDiscovery.query,
      observationId: old.taskUUID,
    });
    expect(pinned.value).toEqual(initialDiscovery.value);
    steps.push({
      name: "pinned-reread",
      ...pinned,
      rendered: await rendered(old.taskUUID, 12, false),
    });
    const reopened = await openDetail();
    expect(reopened).toEqual(initialDetail);
    steps.push({
      name: "old-detail-reopen",
      ...reopened,
      rendered: await rendered(old.taskUUID, 12, true),
    });
    const clearStates: any[] = [];
    const rediscover = async () => {
      await page
        .getByRole("button", { name: "重新选择数据源", exact: true })
        .click();
      await expect(detail).toHaveCount(0);
      await expect(workspace.locator("dl dd")).toHaveCount(0);
      await expect(
        workspace.getByRole("list", { name: "本次观测的组", exact: true }),
      ).toHaveCount(0);
      clearStates.push({
        detailCount: await detail.count(),
        sourceCount: await workspace.locator("dl dd").count(),
        listCount: await workspace
          .getByRole("list", { name: "本次观测的组", exact: true })
          .count(),
      });
      await page
        .getByRole("radio", {
          name: "选择数据源 synthetic.invalid",
          exact: true,
        })
        .check();
      return capture(listPath, () =>
        page
          .getByRole("button", { name: "核对并打开连接详情", exact: true })
          .click(),
      );
    };
    const emptyStep = async (name: string, reply: any, pinned = false) => {
      expect(reply.query).toEqual({
        ...initialDiscovery.query,
        ...(pinned ? { observationId: empty.taskUUID } : {}),
      });
      expect(reply.value).toEqual({
        dictionaryVersion: 4,
        available: true,
        observationId: empty.taskUUID,
        source: empty.source,
        list: [],
        page: { pageIdx: 1, pageSize: 25, total: 0, totalPage: 0 },
      });
      await expect(workspace.getByRole("status")).toHaveText(
        "本页观测未返回组对象；成员情况未知。",
      );
      steps.push({
        name,
        ...reply,
        rendered: await rendered(empty.taskUUID, 0, false),
      });
    };
    await emptyStep("rediscover-empty", await rediscover());
    await emptyStep(
      "empty-reread",
      await capture(listPath, () =>
        page.getByRole("button", { name: "重读原观测", exact: true }).click(),
      ),
      true,
    );
    await emptyStep("rediscover-repeat", await rediscover());
    await navigateTo(page, "账户概览");
    await expect(workspace).toHaveCount(0);
    await emptyStep("return-empty", await openMembers(page, actorId));
    const cases = [];
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 390, height: 844 },
    ]) {
      await page.setViewportSize(viewport);
      const state = await rendered(empty.taskUUID, 0, false);
      const previous = workspace.getByRole("button", {
          name: "上一页",
          exact: true,
        }),
        next = workspace.getByRole("button", { name: "下一页", exact: true });
      await expect(previous).toBeDisabled();
      await expect(next).toBeDisabled();
      const status = workspace.getByRole("status");
      await expect(status).toHaveText("本页观测未返回组对象；成员情况未知。");
      await status.scrollIntoViewIfNeeded();
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      ).toBe(true);
      const filename = `group-members-v4-refresh-reader-${viewport.width}.png`,
        bytes = await page.screenshot({
          path: info.outputPath(filename),
          fullPage: false,
        });
      expect(bytes.readUInt32BE(16)).toBe(viewport.width);
      expect(bytes.readUInt32BE(20)).toBe(viewport.height);
      cases.push({
        viewport,
        rendered: {
          ...state,
          unknownText: await status.innerText(),
          previousDisabled: await previous.isDisabled(),
          nextDisabled: await next.isDisabled(),
        },
        screenshot: {
          filename,
          sha256: createHash("sha256").update(bytes).digest("hex"),
        },
      });
    }
    expect(writes).toEqual([]);
    expect(detailRequests).toEqual([
      { query: initialDetail.query },
      { query: initialDetail.query },
    ]);
    return {
      ui: {
        actorId,
        authorization: "custom_readonly_role",
        initialDiscovery,
        initialDetail,
        initialRendered,
        initialScreenshot,
        retained,
        steps,
        clearStates,
        cases,
        writes,
        detailRequests,
      },
      transition,
    };
  } finally {
    page.off("request", listener);
  }
}
