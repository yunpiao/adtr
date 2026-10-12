import { publishEmptyFixtureMarker } from "./asset-empty-transition-marker";
import { expect, type Page, type TestInfo } from "@playwright/test";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFile, writeFile } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { directoryV2ResponseWitness } from "./directory-v2-response-observer";

const hash = (bytes: Buffer) =>
  createHash("sha256").update(bytes).digest("hex");
const sourceFiles = [
  "internal/testldapfixture/empty_transition_linux.go",
  "internal/testldapfixture/empty_transition_other.go",
  "internal/domains/group_assets_v3.go",
  "internal/domains/computer_assets_v3.go",
  "internal/domains/directory_v3_observation_codec.go",
  "internal/domains/directory_v2_observation_codec.go",

  "web/e2e/asset-empty-refresh.ts",
  "web/e2e/asset-empty-transition-marker.ts",
  "web/e2e/group-assets-v3.spec.ts",
  "web/e2e/computer-assets-v3.spec.ts",
  "web/e2e/group-assets-v3-fixture.ts",
  "web/e2e/computer-assets-v3-fixture.ts",
  "web/e2e/directory-v2-response-observer.ts",
  "web/src/GroupAssetsV3Workspace.tsx",
  "web/src/ComputerAssetsV3Workspace.tsx",
  "web/src/group-assets-v3-api.ts",
  "web/src/computer-assets-v3-api.ts",
  "internal/testldapfixture/main.go",
  "internal/testldapfixture/server.go",
  "internal/testldapfixture/directory.go",
  "internal/testldapfixture/empty_transition.go",
  "scripts/ldap_e2e_fixture.py",
  "scripts/test_auth_e2e.py",
  "scripts/validate_asset_empty_refresh.py",
  "scripts/validate_empty_asset_evidence.py",
  "web/package-lock.json",
  ".github/workflows/ci.yml",
].sort();

type Fixture = {
  actorId: number;
  domainId: string;
  taskUUID: string;
  beforeConnection: { revision: string; credentialRevision: string };
  sync: {
    domainId: string;
    expectedRevision: string;
    expectedCredentialGeneration: string;
    idempotencyKey: string;
  };
  read: (path: string) => Promise<any>;
  post: (path: string, data: Record<string, unknown>) => Promise<any>;
  evidence: (taskUUID?: string) => Promise<any>;
  safe: (value: unknown) => void;
};

export async function captureAssetEmptyRefresh(
  page: Page,
  info: TestInfo,
  kind: "group" | "computer",
  f: Fixture,
  oldTask: string,
  expectedRows: unknown[],
) {
  const label = kind === "group" ? "组" : "计算机";
  const countKey =
    kind === "group" ? "observedGroupCount" : "observedComputerCount";
  const selection = {
    domainId: f.domainId,
    revision: f.beforeConnection.revision,
    credentialRevision: f.beforeConnection.credentialRevision,
  };
  const base = {
    domainId: selection.domainId,
    expectedRevision: selection.revision,
    expectedCredentialRevision: selection.credentialRevision,
    pageIdx: "1",
    pageSize: "10",
  };
  const path = `/api/${kind}-assets/v3`;
  const api = async (extra: Record<string, string>) => {
    const query = { ...base, ...extra };
    const value = await f.read(`${path}?${new URLSearchParams(query)}`);
    f.safe(value);
    return { query, value };
  };
  const browser = async (action: () => Promise<unknown>) => {
    const pending = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === path && r.request().method() === "GET",
    );
    await action();
    const response = await pending;
    const witness = await directoryV2ResponseWitness(response);
    expect(witness.status).toBe(200);
    expect(witness.actor).toBe(String(f.actorId));
    expect(witness.cache).toBe("no-store");
    const { value, identity: _identity, ...consumed } = witness;
    const record = {
      query: Object.fromEntries(new URL(response.url()).searchParams),
      value: value as any,
      consumed,
    };
    f.safe(record);
    return record;
  };
  const panel = page.getByRole("region", { name: `${label}资产`, exact: true });
  await expect(
    page.getByLabel(`${label}资产关键词`, { exact: true }),
  ).toHaveValue("");
  const oldBefore = await api({ observationId: oldTask, pageSize: "50" });
  expect(oldBefore.value).toMatchObject({
    dictionaryVersion: 3,
    available: true,
    selection,
    observationId: oldTask,
    [countKey]: 23,
    page: { pageIdx: 1, pageSize: 50, total: 23, totalPage: 1 },
  });
  expect(oldBefore.value.list).toEqual(expectedRows);
  const settledEvidence = async (task: string) => {
    let proof: any;
    await expect
      .poll(
        async () => {
          proof = await f.evidence(task);
          return proof?.useState;
        },
        { timeout: 30000 },
      )
      .toBe("quiesced");
    return proof;
  };
  const oldStored = await settledEvidence(oldTask);
  const firstStored = await settledEvidence(f.taskUUID);
  const sourceIdentity = (s: any) => ({
    server_name: s.server_name,
    dc_host_name: s.dc_host_name,
    domain: s.domain,
    naming_context: s.naming_context,
  });
  const validateStored = (proof: any, task: string, count: number) => {
    expect(proof).toMatchObject({
      taskUUID: task,
      actorId: String(f.actorId),
      selection,
      dictionaryVersion: 3,
      count,
      taskState: "succeeded",
      taskName: "domain.directory_read.v3",
      payloadVersion: 1,
      maxAttempts: 1,
      attempt: 1,
      purpose: "domain.directory_read.v3",
      useState: "quiesced",
      reason: "executor_returned",
      dependencies: 0,
    });
    const bytes = Buffer.from(proof.bodyBase64, "base64");
    expect(hash(bytes)).toBe(proof.sha256);
    const body = JSON.parse(bytes.toString("utf8"));
    expect(body.dictionaryVersion).toBe(3);
    expect(body.objects).toHaveLength(count);
    return body;
  };
  const oldBody = validateStored(oldStored, oldTask, 30);
  validateStored(firstStored, f.taskUUID, 30);
  expect(oldBody.source).toEqual(oldBefore.value.source);
  await publishEmptyFixtureMarker();
  const request = {
    ...f.sync,
    idempotencyKey: `${kind}-third-empty-real-wire`,
  };
  const submitted = await f.post("/api/directory/v3/sync", request);
  const emptyTask = submitted.task.taskUUID;
  expect([oldTask, f.taskUUID]).not.toContain(emptyTask);
  expect(submitted.replayed).toBe(false);
  await expect
    .poll(
      async () =>
        (await f.read(`/api/directory/v3/task?taskUUID=${emptyTask}`)).task
          .state,
      { timeout: 30000 },
    )
    .toBe("succeeded");
  const emptyStored = await settledEvidence(emptyTask);
  const emptyBody = validateStored(emptyStored, emptyTask, 0);
  expect(emptyBody.objects).toEqual([]);
  expect(emptyBody.source.pages).toBe(1);
  expect(sourceIdentity(emptyBody.source)).toEqual(
    sourceIdentity(oldBody.source),
  );
  expect(emptyStored.sourcePins).toEqual(oldStored.sourcePins);
  expect(emptyStored.sourcePins).toEqual(firstStored.sourcePins);
  const receipt = await f.read(
    `/api/directory/v3/receipt?domainId=${f.domainId}&idempotencyKey=${request.idempotencyKey}`,
  );
  expect(receipt.task.taskUUID).toBe(emptyTask);
  const oldPinned = await browser(() =>
    page.getByRole("button", { name: "下一页", exact: true }).click(),
  );
  expect(oldPinned.query.observationId).toBe(oldTask);
  expect(oldPinned.value).toMatchObject({
    available: true,
    selection,
    observationId: oldTask,
    [countKey]: 23,
    page: { pageIdx: 2, pageSize: 10, total: 23, totalPage: 3 },
  });
  expect(oldPinned.value.list).toEqual(expectedRows.slice(10, 20));
  expect(oldPinned.value.source).toEqual(oldBody.source);
  const screenshots: unknown[] = [];
  const capture = async (
    stage: "old-pinned" | "refreshed-empty",
    task: string,
  ) => {
    for (const [width, height] of [
      [1440, 900],
      [390, 844],
    ]) {
      await page.setViewportSize({ width, height });
      await expect(
        panel.getByText(`观测来源 · ${task}`, { exact: true }),
      ).toBeVisible();
      if (stage === "old-pinned") {
        await expect(
          panel.getByRole("table", { name: `${label}资产列表`, exact: true }),
        ).toBeVisible();
        await expect(panel.getByRole("row")).toHaveCount(11);
      } else {
        await expect(
          panel.getByText(`本次观测未返回${label}对象。`, { exact: true }),
        ).toBeVisible();
        await expect(panel.getByRole("table")).toHaveCount(0);
        await expect(
          panel.getByRole("button", { name: "展开事实", exact: true }),
        ).toHaveCount(0);
      }
      const geometry = await page.evaluate(() => ({
        width: innerWidth,
        scroll: document.documentElement.scrollWidth,
      }));
      expect(geometry.scroll).toBeLessThanOrEqual(width + 1);
      const filename = `${kind}-assets-v3-empty-refresh-${stage}-${width}.png`;
      await page.screenshot({
        path: info.outputPath(filename),
        fullPage: true,
      });
      screenshots.push({
        stage,
        observationId: task,
        filename,
        viewport: { width, height },
        geometry,
        sha256: hash(await readFile(info.outputPath(filename))),
      });
    }
  };
  await capture("old-pinned", oldTask);
  await page.setViewportSize({ width: 1440, height: 960 });
  await page
    .getByLabel(`${label}资产关键词`, { exact: true })
    .fill(`${kind}-22`);
  const oldSearch = await browser(() =>
    page.getByRole("button", { name: `应用${label}筛选`, exact: true }).click(),
  );
  expect(oldSearch.query.observationId).toBe(oldTask);
  expect(oldSearch.query.search).toBe(`${kind}-22`);
  expect(oldSearch.value).toEqual({
    ...oldBefore.value,
    list: [expectedRows[21]],
    page: { pageIdx: 1, pageSize: 10, total: 1, totalPage: 1 },
  });
  const oldCleared = await browser(() =>
    page.getByRole("button", { name: `清除${label}筛选`, exact: true }).click(),
  );
  expect(oldCleared.query.observationId).toBe(oldTask);
  expect(oldCleared.query.search ?? "").toBe("");
  expect(oldCleared.value).toEqual({
    ...oldBefore.value,
    list: expectedRows.slice(0, 10),
    page: { pageIdx: 1, pageSize: 10, total: 23, totalPage: 3 },
  });

  const latest = await api({});
  const emptyExpected = {
    dictionaryVersion: 3,
    selection,
    available: true,
    observationId: emptyTask,
    source: emptyBody.source,
    [countKey]: 0,
    list: [],
    page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 },
  };
  expect(latest.value).toEqual(emptyExpected);
  const refreshed = await browser(() =>
    page
      .getByRole("button", { name: `刷新${label}资产观测`, exact: true })
      .click(),
  );
  expect(refreshed.query.observationId).toBeUndefined();
  expect(refreshed.value).toEqual(emptyExpected);
  const emptyPinned = await browser(() =>
    page.getByRole("button", { name: `应用${label}筛选`, exact: true }).click(),
  );
  expect(emptyPinned.query.observationId).toBe(emptyTask);
  expect(emptyPinned.value).toEqual(emptyExpected);
  await capture("refreshed-empty", emptyTask);
  await page.setViewportSize({ width: 1440, height: 960 });
  const historyFirst = await api({ observationId: f.taskUUID, pageSize: "50" });
  const historySecond = await api({ observationId: oldTask, pageSize: "50" });
  expect(historyFirst.value.list).toEqual(expectedRows);
  expect(historyFirst.value.observationId).toBe(f.taskUUID);
  expect(historySecond).toEqual(oldBefore);
  expect(await f.evidence(f.taskUUID)).toEqual(firstStored);
  expect(await f.evidence(oldTask)).toEqual(oldStored);
  const root = execFileSync("git", ["rev-parse", "--show-toplevel"], {
    encoding: "utf8",
  }).trim();
  const git = (...args: string[]) =>
    execFileSync("git", args, { cwd: root, encoding: "utf8" }).trim();
  const provenance = {
    checkoutCommit: git("rev-parse", "HEAD"),
    checkoutTree: git("rev-parse", "HEAD^{tree}"),
    checkoutParents: git("cat-file", "-p", "HEAD")
      .split("\n\n", 1)[0]
      .split("\n")
      .filter((x) => /^parent [0-9a-f]{40}$/u.test(x))
      .map((x) => x.slice(7)),
    dirty: git("status", "--porcelain") !== "",
    eventHeadCommit: process.env.ADTR_EVENT_HEAD_SHA ?? null,
    eventBaseCommit: process.env.ADTR_EVENT_BASE_SHA ?? null,
    workflowCommit: process.env.GITHUB_SHA ?? null,
    runID: process.env.GITHUB_RUN_ID ?? null,
    runAttempt: process.env.GITHUB_RUN_ATTEMPT ?? null,
    sourceSHA256: Object.fromEntries(
      sourceFiles.map((p) => [p, hash(readFileSync(resolve(root, p)))]),
    ),
  };
  const evidence = {
    format: 1,
    kind,
    actorId: String(f.actorId),
    selection,
    request,
    firstStored,
    oldStored,
    emptyStored,
    oldBefore,
    oldPinned,
    oldSearch,
    oldCleared,
    latest,
    refreshed,
    emptyPinned,
    historyFirst,
    historySecond,
    screenshots,
    provenance,
  };
  f.safe(evidence);
  const encoded = JSON.stringify(evidence, null, 2);
  expect(Buffer.byteLength(encoded)).toBeLessThanOrEqual(512 * 1024);
  await writeFile(
    info.outputPath(`${kind}-assets-v3-empty-refresh-evidence.json`),
    encoded,
  );
}
