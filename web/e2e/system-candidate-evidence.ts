import { expect, type Page, type TestInfo } from "@playwright/test";
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import type {
  CandidateKind,
  CandidateObservation,
} from "./system-candidate-observations";

export type SystemCandidateVisual = {
  file: string;
  state: CandidateKind;
  sha256: string;
  viewport: { width: number; height: number };
  documentWidth: number;
  responseSHA256: string;
};

async function assertSystemCandidate(
  page: Page,
  observed: CandidateObservation,
) {
  if (observed.kind === "overview-disk") {
    const card = page.getByRole("region", {
      name: "API 根文件系统观测",
      exact: true,
    });
    await expect(card).toBeVisible();
    const disk = observed.disk;
    const known =
      !!disk &&
      !observed.current.stale &&
      observed.current.availability === "available" &&
      disk.availability === "available" &&
      disk.percent !== null;
    await expect(card.locator("strong")).toHaveText(
      known ? `${disk!.percent!.toFixed(2)}%` : "当前使用率未知",
    );
    await expect(card.locator("[data-state]")).toHaveAttribute(
      "data-state",
      known ? "observed" : "unknown",
    );
    if (disk) {
      await expect(card).toContainText(disk.source);
      await expect(card).toContainText(disk.scope);
      await expect(card).toContainText(new Date(disk.observedAt).toISOString());
      if (!known && disk.percent !== null)
        await expect(card).toContainText(
          `已有观测：${disk.percent.toFixed(2)}%`,
        );
    }
  } else {
    const table = page.getByRole("table", { name: "实际文件系统存储" });
    await expect(table).toBeVisible();
    await expect(table.locator("thead th").first()).toHaveText("本页序号");
    await expect(table.locator("thead th").first()).toHaveAttribute(
      "scope",
      "col",
    );
    await expect(table.locator("tbody tr")).toHaveCount(observed.rows!.length);
    for (const [index, item] of observed.rows!.entries()) {
      const row = table.locator("tbody tr").nth(index);
      await expect(row.getByRole("rowheader")).toHaveText(item.id);
      await expect(row.getByRole("cell").first()).toHaveText(
        String(item.ordinal),
      );
      await expect(
        row.getByRole("button", { name: `调整阈值 ${item.id}`, exact: true }),
      ).toBeVisible();
      await expect(row).toContainText(`${item.alarmPercent}%`);
      await expect(row).toContainText(`版本 ${item.revision}`);
    }
  }
}

// This adds four success-only images. The original twelve captures and all
// runtime/write/permission checks remain untouched in system.spec.ts.
export async function captureSystemCandidateViews(
  page: Page,
  testInfo: TestInfo,
  observed: CandidateObservation,
  visuals: SystemCandidateVisual[],
) {
  for (const [device, viewport] of [
    ["desktop", { width: 1440, height: 900 }],
    ["mobile", { width: 390, height: 844 }],
  ] as const) {
    await page.setViewportSize(viewport);
    await assertSystemCandidate(page, observed);
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    for (const label of ["未使用的认证器验证码", "认证器验证码", "设置密钥"])
      await expect(page.getByLabel(label, { exact: true })).toHaveCount(0);
    await page.evaluate(() => window.scrollTo(0, 0));
    const documentWidth = await page.evaluate(
      () => document.documentElement.scrollWidth,
    );
    expect(documentWidth).toBeLessThanOrEqual(viewport.width);
    const file = `system-soc-candidate-${device}-${observed.kind}.png`;
    const bytes = await page.screenshot({
      path: testInfo.outputPath(file),
      fullPage: true,
    });
    // Changed candidate fields must fail, not be silently rebound to old bytes.
    await assertSystemCandidate(page, observed);
    visuals.push({
      file,
      state: observed.kind,
      sha256: createHash("sha256").update(bytes).digest("hex"),
      viewport,
      documentWidth,
      responseSHA256: observed.responseSHA256,
    });
  }
  await page.setViewportSize({ width: 1440, height: 900 });
}

type BaseManifest = {
  checkoutCommit: string;
  checkoutTree: string;
  checkoutParents: string[];
  eventHeadCommit: string;
  eventBaseCommit: string;
  dirty: boolean;
  workflowCommit: string | null;
  workflowRun: string | null;
  workflowAttempt: string | null;
  runtime: unknown;
  sourceSHA256: Record<string, string>;
  artifactsSHA256: unknown;
  frontendAssetsSHA256: Record<string, string>;
};

export async function writeSystemCandidateEvidence(
  testInfo: TestInfo,
  original: BaseManifest,
  observations: CandidateObservation[],
  screenshots: SystemCandidateVisual[],
  hashFile: (file: string) => string,
) {
  expect(observations.map((item) => item.kind)).toEqual([
    "overview-disk",
    "storage-ordinals",
  ]);
  expect(observations.every((item) => item.nativeFinished)).toBe(true);
  expect(screenshots).toHaveLength(4);
  for (const observation of observations) {
    const images = screenshots.filter(
      (image) => image.state === observation.kind,
    );
    expect(images).toHaveLength(2);
    expect(
      images.every(
        (image) => image.responseSHA256 === observation.responseSHA256,
      ),
    ).toBe(true);
  }
  const extraSources = [
    "web/e2e/system-candidate-evidence.ts",
    "web/e2e/system-candidate-observations.ts",
    "web/src/SystemDiskOverview.tsx",
    "web/src/SystemDiskOverview.test.tsx",
    "web/src/SystemStorageRowNumbers.test.tsx",
    "web/src/system-candidate-observations.test.ts",
    "scripts/system_candidate_native_source_contract.py",
    "scripts/test_system_candidate_native_contract.py",
    "docs/system-native-candidate-evidence.md",
    "docs/system-disk-overview-contract.md",
    "docs/system-storage-row-numbers-contract.md",
  ];
  const manifest = {
    formatVersion: 1,
    suite: "system-candidates",
    recordedAt: new Date().toISOString(),
    chain: "real browser response → existing API → Linux sampler / PostgreSQL",
    checkoutCommit: original.checkoutCommit,
    checkoutTree: original.checkoutTree,
    checkoutParents: original.checkoutParents,
    eventHeadCommit: original.eventHeadCommit,
    eventBaseCommit: original.eventBaseCommit,
    dirty: original.dirty,
    workflowCommit: original.workflowCommit,
    workflowRun: original.workflowRun,
    workflowAttempt: original.workflowAttempt,
    runtime: original.runtime,
    artifactsSHA256: original.artifactsSHA256,
    frontendAssetsSHA256: original.frontendAssetsSHA256,
    sourceSHA256: {
      ...original.sourceSHA256,
      ...Object.fromEntries(extraSources.map((file) => [file, hashFile(file)])),
    },
    observations,
    screenshots,
    coverage:
      "Existing initial overview and first storage page only. No native second page or injected stale/clock-skew observation is claimed.",
  };
  const path = testInfo.outputPath("system-soc-candidates-evidence.json");
  writeFileSync(path, JSON.stringify(manifest, null, 2) + "\n");
  await testInfo.attach("system-soc-candidates-evidence", {
    path,
    contentType: "application/json",
  });
}
