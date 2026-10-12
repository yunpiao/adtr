import { expect, type Page, type TestInfo } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { resolve } from "node:path";
import { navigateTo } from "./navigation";
import { directoryV2ResponseWitness } from "./directory-v2-response-observer";
import type { DirectoryV2Source } from "../src/directory-v2-api";

const workspaces = [
  {
    kind: "user",
    label: "用户资产",
    absent: "当前数据源没有可用的字典 3 观测；尚不能确认用户数量。",
    empty: "此观测在当前筛选下没有匹配用户。",
    count: null,
  },
  {
    kind: "computer",
    label: "计算机资产",
    absent: "暂无可读取的已完成观测，尚不能确定计算机数量。",
    empty: "本次观测未返回计算机对象。",
    count: "observedComputerCount",
  },
  {
    kind: "group",
    label: "组资产",
    absent: "暂无可读取的已完成观测，尚不能确定组数量。",
    empty: "本次观测未返回组对象。",
    count: "observedGroupCount",
  },
] as const;

export type EmptyAssetSelection = {
  domainId: string;
  revision: string;
  credentialRevision: string;
};
export type EmptyAssetObservation = {
  observationId: string;
  source: DirectoryV2Source;
};
const hash = (bytes: Buffer) =>
  createHash("sha256").update(bytes).digest("hex");

// Never insert a successful observation or intercept a response. The existing
// controls test owns provisioning, the real executor and its cancellation flow.
export async function captureEmptyAssetWorkspaces(
  page: Page,
  info: TestInfo,
  actorId: number,
  selection: EmptyAssetSelection,
  safe: (value: unknown) => void,
  observation?: EmptyAssetObservation,
) {
  const records: unknown[] = [];
  const stage = observation ? "observed-empty" : "no-observation";
  for (const workspace of workspaces) {
    await page.setViewportSize({ width: 1440, height: 900 });
    await navigateTo(page, workspace.label);
    if (
      workspace.kind === "user" &&
      new URL(page.url()).hash === "#user-assets-v2"
    )
      await page
        .getByRole("link", { name: "字典 3 · 主组 RID / SPN", exact: true })
        .click();
    await expect(page).toHaveURL(
      new RegExp(`#${workspace.kind}-assets-v3$`, "u"),
    );
    await page
      .getByRole("radio", { name: "选择数据源 synthetic.invalid", exact: true })
      .check();
    const path = `/api/${workspace.kind}-assets/v3`;
    const pending = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === path && r.request().method() === "GET",
    );
    await page
      .getByRole("button", { name: "核对并打开连接详情", exact: true })
      .click();
    const response = await pending;
    expect(response.status()).toBe(200);
    expect(response.headers()["cache-control"]).toBe("no-store");
    expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId));
    const witness = await directoryV2ResponseWitness(response);
    const query = Object.fromEntries(new URL(response.url()).searchParams);
    expect(query.domainId).toBe(selection.domainId);
    expect(query.expectedRevision).toBe(selection.revision);
    expect(query.expectedCredentialRevision).toBe(selection.credentialRevision);
    expect(query.search ?? "").toBe("");
    expect(query.observationId).toBeUndefined();
    expect(query.pageIdx).toBe("1");
    expect(query.pageSize).toBe("10");
    const expected = {
      dictionaryVersion: 3,
      selection,
      available: !!observation,
      ...(workspace.count ? { [workspace.count]: observation ? 0 : null } : {}),
      ...(observation ?? {}),
      list: [],
      page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 },
    };
    expect(witness.value).toEqual(expected);
    safe(witness.value);
    let pinned: unknown = null;
    if (observation) {
      const pendingPin = page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === path && r.request().method() === "GET",
      );
      await page
        .getByRole("button", {
          name:
            workspace.kind === "user"
              ? "搜索用户"
              : workspace.kind === "computer"
                ? "应用计算机筛选"
                : "应用组筛选",
          exact: true,
        })
        .click();
      const pinnedResponse = await pendingPin;
      const pinnedWitness = await directoryV2ResponseWitness(pinnedResponse);
      const pinnedQuery = Object.fromEntries(
        new URL(pinnedResponse.url()).searchParams,
      );
      expect(pinnedQuery).toEqual({
        ...query,
        observationId: observation.observationId,
      });
      expect(pinnedWitness.value).toEqual(expected);
      expect(pinnedWitness.status).toBe(200);
      expect(pinnedWitness.actor).toBe(String(actorId));
      expect(pinnedWitness.cache).toBe("no-store");
      pinned = {
        query: pinnedQuery,
        value: pinnedWitness.value,
        sha256: pinnedWitness.sha256,
        byteLength: pinnedWitness.byteLength,
      };
      safe(pinned);
    }
    const panel = page.getByRole("region", {
      name: workspace.label,
      exact: true,
    });
    await expect(
      panel.getByText(observation ? workspace.empty : workspace.absent, {
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      panel.getByText(observation ? workspace.absent : workspace.empty, {
        exact: true,
      }),
    ).toHaveCount(0);
    await expect(panel.getByRole("table")).toHaveCount(0);
    await expect(
      panel.getByRole("button", { name: /展开事实|查看详情/u }),
    ).toHaveCount(0);
    await expect(panel.locator(".asset-source-revisions")).toHaveText(
      `已选择域：synthetic.invalid · 配置版本 ${selection.revision} · 凭据版本 ${selection.credentialRevision}`,
    );
    if (observation) {
      await panel
        .getByText(`观测来源 · ${observation.observationId}`, { exact: true })
        .click();
      await expect(panel).toContainText("dc.synthetic.invalid");
      await expect(panel).toContainText("dc=synthetic,dc=invalid");
      await expect(panel).toContainText("共 0 页");
      if (workspace.kind !== "user")
        await expect(panel.locator(".asset-count")).toContainText(
          `仅该次观测中的${workspace.kind === "group" ? "组" : "计算机"}：0 · 匹配：0`,
        );
      for (const name of workspace.kind === "user"
        ? ["用户资产上一页", "用户资产下一页"]
        : ["上一页", "下一页"])
        await expect(
          panel.getByRole("button", { name, exact: true }),
        ).toBeDisabled();
    } else {
      await expect(
        panel.getByRole("button", { name: /上一页|下一页/u }),
      ).toHaveCount(0);
      await expect(
        panel.locator(".asset-count, .asset-observation-bar"),
      ).toHaveCount(0);
      await expect(panel.getByText(/观测来源 ·/u)).toHaveCount(0);
    }
    // Screenshots contain only the read-only asset page, never setup/proof inputs.
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    safe(await page.locator("body").innerText());
    const screenshots = [];
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 390, height: 844 },
    ]) {
      await page.setViewportSize(viewport);
      await expect(
        panel.getByText(observation ? workspace.empty : workspace.absent, {
          exact: true,
        }),
      ).toBeVisible();
      const geometry = await page.evaluate(() => ({
        width: innerWidth,
        scroll: document.documentElement.scrollWidth,
      }));
      expect(geometry.scroll).toBeLessThanOrEqual(geometry.width + 1);
      const filename = `directory-v3-empty-assets-${stage}-${workspace.kind}-${viewport.width}.png`;
      await page.screenshot({
        path: info.outputPath(filename),
        fullPage: true,
      });
      screenshots.push({
        filename,
        viewport,
        geometry,
        sha256: hash(readFileSync(info.outputPath(filename))),
      });
    }
    records.push({
      stage,
      kind: workspace.kind,
      path,
      query,
      actorId: String(actorId),
      value: witness.value,
      consumed: {
        byteLength: witness.byteLength,
        sha256: witness.sha256,
        state: witness.state,
        uncanceledEOF: witness.uncanceledEOF,
        eof: witness.eof,
        status: witness.status,
        actor: witness.actor,
        cache: witness.cache,
        readers: witness.readers,
        released: witness.released,
        signalAborted: witness.signalAborted,
        cancelCalls: witness.cancelCalls,
      },
      screenshots,
      pinned,
    });
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  return records;
}

export function writeEmptyAssetEvidence(
  info: TestInfo,
  data: Record<string, unknown>,
  safe: (value: unknown) => void,
) {
  const root = execFileSync("git", ["rev-parse", "--show-toplevel"], {
    encoding: "utf8",
  }).trim();
  const git = (...args: string[]) =>
    execFileSync("git", args, { cwd: root, encoding: "utf8" }).trim();
  const sources = [
    "web/e2e/directory-v3-controls.spec.ts",
    "web/e2e/directory-v3-empty-assets.ts",
    "web/e2e/directory-v2-response-observer.ts",
    "web/e2e/directory-v3-ledger-observer.ts",
    "web/e2e/navigation.ts",
    "web/src/UserAssetsV3Workspace.tsx",
    "web/src/ComputerAssetsV3Workspace.tsx",
    "web/src/GroupAssetsV3Workspace.tsx",
    "web/src/user-assets-v3-api.ts",
    "web/src/computer-assets-v3-api.ts",
    "web/src/group-assets-v3-api.ts",
    "scripts/validate_empty_asset_evidence.py",
    "scripts/test_auth_e2e.py",
    "scripts/ldap_e2e_fixture.py",
    ".github/workflows/ci.yml",
    "web/package-lock.json",
  ];
  const evidence = {
    format: 1,
    ...data,
    provenance: {
      checkoutCommit: git("rev-parse", "HEAD"),
      checkoutTree: git("rev-parse", "HEAD^{tree}"),
      checkoutParents: git("show", "-s", "--format=%P", "HEAD").split(" "),
      dirty: git("status", "--porcelain") !== "",
      eventHeadCommit: process.env.ADTR_EVENT_HEAD_SHA ?? null,
      eventBaseCommit: process.env.ADTR_EVENT_BASE_SHA ?? null,
      workflowCommit: process.env.GITHUB_SHA ?? null,
      runID: process.env.GITHUB_RUN_ID ?? null,
      runAttempt: process.env.GITHUB_RUN_ATTEMPT ?? null,
      sourceSHA256: Object.fromEntries(
        sources
          .sort()
          .map((path) => [path, hash(readFileSync(resolve(root, path)))]),
      ),
    },
  };
  safe(evidence);
  const output = info.outputPath("directory-v3-empty-assets-evidence.json");
  writeFileSync(output, JSON.stringify(evidence, null, 2));
  execFileSync(
    "python3",
    [
      resolve(root, "scripts/validate_empty_asset_evidence.py"),
      output,
      "--source-root",
      root,
    ],
    { cwd: root, stdio: "pipe", timeout: 10_000, maxBuffer: 16_384 },
  );
}
