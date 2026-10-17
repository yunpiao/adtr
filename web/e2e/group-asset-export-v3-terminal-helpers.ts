import { runningGuard } from "./owned-running-control";
import { expect, type Page, type BrowserContext, type TestInfo } from "@playwright/test";
import { readFile, writeFile, readdir, readlink } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { setupGroupFixture, readJSON, proof } from "./group-assets-v3-fixture";
import { observeDirectoryV2Responses } from "./directory-v2-response-observer";
import { assetAction, downloadHeaderNames, evidenceProvenance, exportPrefix, hash, openAssets, responseValue, sensitiveBudget, storedExport, switchToExportOwner } from "./group-asset-export-v3-helpers";
import { resolve } from "node:path";
import { activityAlias, assertActivityScope } from "./owned-worker-activity-scope";
const run = promisify(execFile);
export const safeColumns = ["dn", "guid"];
export const allColumns = ["guid", "sAMAccountName", "sid", "dn", "whenCreated"];
export const panelFor = (page: Page) => page.getByRole("region", { name: "组资产导出", exact: true });
export async function terminalSetup(page: Page, context: BrowserContext) {
  expect(process.env.ADTR_E2E_GROUP_ASSET_EXPORT_V3).toBe("true");
  await page.setViewportSize({ width: 1440, height: 900 });
  await observeDirectoryV2Responses(context);
  const fixture = await setupGroupFixture(page, context, {});
  const source = await fixture.evidence();
  expect(source.taskUUID).toBe(fixture.taskUUID);
  expect(source.sha256).toBe(hash(Buffer.from(source.bodyBase64, "base64")));
  await switchToExportOwner(page, fixture.bootstrap);
  const actorId = (await readJSON(context, "/api/auth/me")).ID as number;
  expect(actorId).not.toBe(fixture.actorId);
  const before = await sensitiveBudget(actorId, fixture.actorId);
  expect(before).toEqual({ owner: 7, producer: 9, observations: 1, directoryUses: 1, exportTasks: 0 });
  await openAssets(page, actorId);
  await page.locator("summary").filter({ hasText: /^创建时间排序$/u }).click();
  await page.getByLabel("创建时间排序", { exact: true }).selectOption("desc");
  await page.getByLabel("组资产关键词", { exact: true }).fill("group-");
  const list = await assetAction(page, actorId, () => page.getByRole("button", { name: "应用组筛选", exact: true }).click());
  expect(list.observationId).toBe(source.taskUUID);
  expect(list.page).toMatchObject({ total: 23, pageSize: 10 });
  expect(list.list).toHaveLength(10);
  await page.getByRole("button", { name: "导出匹配组 XLSX", exact: true }).click();
  return { fixture, source, actorId, before, list };
}
export async function chooseColumns(page: Page, columns: string[]) {
  const panel = panelFor(page);
  for (const column of allColumns) await panel.getByLabel(`导出列：${column}`, { exact: true }).uncheck();
  for (const column of columns) await panel.getByLabel(`导出列：${column}`, { exact: true }).check();
  await expect(panel.getByLabel("导出列顺序", { exact: true })).toHaveText(columns.join(" → "));
}
export async function createExport(page: Page, setup: Awaited<ReturnType<typeof terminalSetup>>) {
  await proof(page, setup.fixture.bootstrap.password, setup.fixture.bootstrap.auth);
  const [response] = await Promise.all([
    page.waitForResponse((r) => new URL(r.url()).pathname === exportPrefix && r.request().method() === "POST"),
    panelFor(page).getByRole("button", { name: "创建组导出", exact: true }).click(),
  ]);
  const create = await responseValue(response, setup.actorId);
  // Never persist the MFA/password-bearing request; retain only its public key.
  const idempotencyKey: string = response.request().postDataJSON().idempotencyKey;
  expect(idempotencyKey).toMatch(/^[A-Za-z0-9_.-]{1,128}$/u);
  expect(create.replayed).toBe(false);
  setup.fixture.safe(create);
  return { create, idempotencyKey };
}
export async function receiptAndDetail(page: Page, actor: number, taskID: string) {
  const [rr, dr] = await Promise.all([
    page.waitForResponse((r) => new URL(r.url()).pathname === `${exportPrefix}/receipt`),
    page.waitForResponse((r) => new URL(r.url()).pathname === `${exportPrefix}/detail` && new URL(r.url()).searchParams.get("taskID") === taskID),
    panelFor(page).getByRole("button", { name: "核对原导出回执", exact: true }).click(),
  ]);
  const receipt = await responseValue(rr, actor), detail = await responseValue(dr, actor);
  expect(receipt.taskID).toBe(taskID);
  expect(detail.taskID).toBe(taskID);
  return { receipt, detail };
}
async function databaseJSON(sql: string) {
  const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container)) throw new Error("Owned isolated database required");
  let stdout: string;
  try {
    ({ stdout } = await run("docker", ["exec", "-e", "PGOPTIONS=-c statement_timeout=2000 -c default_transaction_read_only=on", container, "psql", "-U", "postgres", "-d", "adtr_e2e", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", sql], { encoding: "utf8", timeout: 5000, maxBuffer: 2 << 20 }));
  } catch { throw new Error("Read-only isolated terminal export evidence query failed"); }
  if (!stdout.trim()) throw new Error("Missing terminal export evidence");
  return JSON.parse(stdout);
}
export async function terminalExport(taskID: string) {
  if (!/^[A-Za-z0-9_.-]{1,128}$/u.test(taskID)) throw new Error("Invalid task identity");
  return databaseJSON(`SELECT jsonb_build_object('taskID',t.task_id,'actorId',t.actor_id::text,'kind',t.kind,'state',t.state,'error',t.error_code,'attempt',t.attempt,'maxAttempts',t.max_attempts,'progress',t.progress,'resultVersion',t.result_version,'result',t.result,'idempotencyKey',t.idempotency_key,'leaseOwner',t.lease_owner,'leaseUntil',t.lease_until,'snapshotCount',(SELECT count(*) FROM adtr.group_asset_export_snapshots s WHERE s.task_id=t.task_id),'manifestCount',(SELECT count(*) FROM adtr.group_asset_export_manifests m WHERE m.task_id=t.task_id),'chunkCount',(SELECT count(*) FROM adtr.group_asset_export_chunks c WHERE c.task_id=t.task_id),'events',(SELECT jsonb_agg(jsonb_build_object('action',e.action,'state',e.state,'attempt',e.attempt,'resultVersion',e.result_version) ORDER BY e.id) FROM adtr.task_events e WHERE e.task_id=t.task_id)) FROM adtr.tasks t WHERE t.tenant_id='default' AND t.task_id='${taskID}'`);
}
export async function screenshot(page: Page, info: TestInfo, filename: string) {
  await expect(panelFor(page).locator('input[type="password"]')).toHaveCount(0);
  await page.screenshot({ path: info.outputPath(filename), fullPage: false });
  await info.attach(filename, { path: info.outputPath(filename), contentType: "image/png" });
  return { filename, sha256: hash(await readFile(info.outputPath(filename))) };
}
export async function recoveryDownload(page: Page, info: TestInfo, setup: Awaited<ReturnType<typeof terminalSetup>>, operation: Awaited<ReturnType<typeof createExport>>) {
  const { create, idempotencyKey } = operation, panel = panelFor(page);
  await expect(panel.getByRole("button", { name: "下载组 XLSX", exact: true })).toBeVisible({ timeout: 30_000 });
  const { receipt } = await receiptAndDetail(page, setup.actorId, create.taskID);
  const [download, response, dr] = await Promise.all([
    page.waitForEvent("download"),
    page.waitForResponse((r) => new URL(r.url()).pathname === `${exportPrefix}/download`),
    page.waitForResponse((r) => new URL(r.url()).pathname === `${exportPrefix}/detail`),
    panel.getByRole("button", { name: "下载组 XLSX", exact: true }).click(),
  ]);
  const detail = await responseValue(dr, setup.actorId);
  expect(response.status()).toBe(200);
  const filename = `group-assets-v3-${create.taskID}.xlsx`;
  expect(download.suggestedFilename()).toBe(filename);
  await download.saveAs(info.outputPath(filename));
  const bytes = await readFile(info.outputPath(filename));
  expect(detail.artifact).toEqual({ filename, rowCount: 23, byteCount: bytes.length, sha256: hash(bytes) });
  await info.attach(filename, { path: info.outputPath(filename), contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" });
  const readyStatus = panel.getByRole("status").filter({ hasText: "完整文件已就绪" });
  await readyStatus.scrollIntoViewIfNeeded();
  await expect(readyStatus).toBeInViewport({ ratio: 1 });
  await expect(panel.getByText(`任务 ID：${create.taskID}`, { exact: true })).toBeInViewport({ ratio: 1 });
  await expect(panel.getByRole("button", { name: "下载组 XLSX", exact: true })).toBeInViewport({ ratio: 1 });
  return { name: "recovery", idempotencyKey, request: create.request, create, receipt, detail,
    download: { filename, headers: Object.fromEntries(downloadHeaderNames.map((key) => [key, response.headers()[key]])) },
    database: await storedExport(create.taskID), pageSize: setup.list.page.pageSize, firstPageCount: setup.list.list.length,
    screenshot: await screenshot(page, info, "recovery-ready.png") };
}
export async function writeTerminalEvidence(info: TestInfo, setup: Awaited<ReturnType<typeof terminalSetup>>, suite: string, cases: unknown, worker: unknown = null) {
  const after = await sensitiveBudget(setup.actorId, setup.fixture.actorId);
  expect(after).toEqual({ ...setup.before, owner: 9, exportTasks: suite === "failureRecovery" ? 2 : 1 });
  const evidence = { format: 1, suite, provenance: evidenceProvenance(), source: setup.source, actorId: setup.actorId, sensitiveBudget: { before: setup.before, after }, worker, cases };
  setup.fixture.safe(evidence); setup.fixture.safe(setup.fixture.consoles);
  const filename = suite === "failureRecovery" ? "group-asset-export-v3-failure-evidence.json" : "group-asset-export-v3-cancel-evidence.json";
  await writeFile(info.outputPath(filename), JSON.stringify(evidence, null, 2));
  await info.attach(filename, { path: info.outputPath(filename), contentType: "application/json" });
}
export async function unavailableDownload(context: BrowserContext, taskID: string) {
  const response = await context.request.get(`${exportPrefix}/download?${new URLSearchParams({ taskID })}`);
  expect(response.status()).toBe(409);
  expect(response.headers()["cache-control"]).toBe("no-store");
  const body = await response.json();
  expect(body).toEqual({ error: "export_not_ready" });
  return { status: response.status(), error: body.error, cacheControl: response.headers()["cache-control"] };
}
// Linux-only, owned isolated CI Worker control. Identity is checked before every
// signal; neither arbitrary PIDs nor credentials are accepted or logged.
function workerConfig() {
  const pid = Number(process.env.ADTR_E2E_WORKER_PID), parentPID = Number(process.env.ADTR_E2E_HARNESS_PID);
  const starttime = process.env.ADTR_E2E_EXPORT_WORKER_STARTTIME ?? "";
  const nonce = process.env.ADTR_E2E_EXPORT_WORKER_NONCE ?? "";
  const binary = process.env.ADTR_E2E_EXPORT_WORKER_BINARY ?? "";
  const applicationName = process.env.ADTR_E2E_EXPORT_WORKER_APPLICATION_NAME ?? "";
  const readyURL = process.env.ADTR_E2E_EXPORT_WORKER_READY_URL ?? "";
  if (![pid, parentPID].every((n) => Number.isSafeInteger(n) && n > 1) || !/^[0-9]+$/u.test(starttime) || !/^[0-9a-f]{32}$/u.test(nonce) || binary !== resolve("../bin/adtr") || applicationName !== `adtr-user-export-${nonce}` || !/^http:\/\/127\.0\.0\.1:[0-9]+\/readyz$/u.test(readyURL)) throw new Error("Dedicated owned Worker identity required");
  return { pid, parentPID, starttime, nonce, binary, applicationName, readyURL };
}
async function checkedWorker() {
  const config = workerConfig(), root = `/proc/${config.pid}`;
  try {
    const stat = await readFile(`${root}/stat`, "utf8"), tail = stat.slice(stat.lastIndexOf(")") + 2).trim().split(/\s+/u);
    const cmd = (await readFile(`${root}/cmdline`, "utf8")).split("\0");
    const env = (await readFile(`${root}/environ`, "utf8")).split("\0");
    // The Worker opens/closes a connection for each transaction, so a genuine
    // idle Worker normally has zero PG sessions. Bind that zero to this exact
    // database/application before accepting it; never persist or print a DSN.
    const databaseEntries = env.filter((entry) => entry.startsWith("ADTR_DATABASE_URL="));
    if (databaseEntries.length !== 1) throw new Error("database identity");
    const database = new URL(databaseEntries[0].slice("ADTR_DATABASE_URL=".length));
    const baseline = new URL(process.env.ADTR_DATABASE_URL ?? "");
    if (baseline.searchParams.has("application_name") || baseline.pathname !== "/adtr_e2e" || !["postgres:", "postgresql:"].includes(baseline.protocol) || database.searchParams.getAll("application_name").length !== 1 || database.searchParams.get("application_name") !== config.applicationName) throw new Error("database identity");
    database.searchParams.delete("application_name");
    if (database.toString() !== baseline.toString()) throw new Error("database identity");
    if (tail[1] !== String(config.parentPID) || tail[19] !== config.starttime || JSON.stringify(cmd) !== JSON.stringify(["./bin/adtr", "-mode", "worker", ""]) || await readlink(`${root}/exe`) !== config.binary || await readlink(`${root}/cwd`) !== resolve("..") || !env.includes(`ADTR_E2E_EXPORT_WORKER_NONCE=${config.nonce}`)) throw new Error("identity");
    const threads = await readdir(`${root}/task`);
    if (threads.length === 0) throw new Error("missing threads");
    const states = await Promise.all(threads.map(async (id) => {
      if (!/^[0-9]+$/u.test(id)) throw new Error("thread");
      return (await readFile(`${root}/task/${id}/status`, "utf8")).match(/^State:\s+(\S)/mu)?.[1] ?? "";
    }));
    return { ...config, states, databaseIdentityVerified: true };
  } catch { throw new Error("Owned Worker identity could not be verified"); }
}
async function signalWorker(signal: "SIGSTOP" | "SIGCONT") {
  if (signal === "SIGCONT" && (process.env.ADTR_E2E_RUNNING_CANCEL_GUARD_URL !== undefined || process.env.ADTR_E2E_RUNNING_CANCEL_GUARD_TOKEN !== undefined)) {
    await runningGuard("resume");
    return;
  }
  const worker = await checkedWorker();
  // Recheck process birth/parent immediately before signalling, after thread reads.
  const stat = await readFile(`/proc/${worker.pid}/stat`, "utf8");
  const tail = stat.slice(stat.lastIndexOf(")") + 2).trim().split(/\s+/u);
  if (tail[1] !== String(worker.parentPID) || tail[19] !== worker.starttime) throw new Error("Owned Worker changed before signal");
  process.kill(worker.pid, signal);
}
async function queueActivity(scope?: { workerID: string; alias: string }) {
  if (scope && (scope.workerID.length < 1 || scope.workerID.length > 128 || /[^A-Za-z0-9_.:-]/u.test(scope.workerID) || scope.alias.length !== 64 || !scope.alias.startsWith("ci-activity-") || /[^a-z2-7]/u.test(scope.alias.slice(12)) || scope.workerID === scope.alias)) throw new Error("Invalid owned activity scope");
  const value = await databaseJSON(scope
    ? `SELECT jsonb_build_object('scope',jsonb_build_object('original',count(*) FILTER (WHERE worker_id='${scope.workerID}'),'alias',count(*) FILTER (WHERE worker_id='${scope.alias}'),'other',count(*) FILTER (WHERE worker_id NOT IN ('${scope.workerID}','${scope.alias}'))),'count',count(*) FILTER (WHERE worker_id='${scope.workerID}'),'workerID',min(worker_id) FILTER (WHERE worker_id='${scope.workerID}'),'success',(min(extract(epoch FROM last_success_at)) FILTER (WHERE worker_id='${scope.workerID}'))::text) FROM adtr.system_worker_activity WHERE tenant_id='default' AND cycle='queue'`
    : "SELECT jsonb_build_object('count',count(*),'workerID',min(worker_id),'success',min(extract(epoch FROM last_success_at))::text) FROM adtr.system_worker_activity WHERE tenant_id='default' AND cycle='queue'");
  if (scope) assertActivityScope(value.scope);
  expect(value.count).toBe(1);
  expect(value.workerID).toMatch(/^[A-Za-z0-9_.:-]{1,128}$/u);
  expect(value.success).toMatch(/^[0-9]+\.[0-9]+$/u);
  return value;
}
let ownedStopped = false;
export async function pauseOwnedExportWorker() {
  // Any missed in-flight transaction resumes before another bounded attempt.
  const deadline = Date.now() + 10_000;
  const samples = { attempts: 0, zeroConnections: 0, activeTransactions: 0, lastConnections: 0, lastActiveTransactions: 0 };
  try {
    while (Date.now() < deadline) {
      await signalWorker("SIGSTOP"); ownedStopped = true;
      await expect.poll(async () => (await checkedWorker()).states.every((s) => s === "T"), { timeout: 2000 }).toBe(true);
      const w = await checkedWorker();
      const pg = await databaseJSON(`SELECT jsonb_build_object('connections',count(*),'activeTransactions',count(*) FILTER (WHERE xact_start IS NOT NULL)) FROM pg_stat_activity WHERE datname=current_database() AND application_name='${w.applicationName}'`);
      if (!Number.isSafeInteger(pg.connections) || pg.connections < 0 || !Number.isSafeInteger(pg.activeTransactions) || pg.activeTransactions < 0 || pg.activeTransactions > pg.connections) throw new Error("Invalid Worker quiescence counts");
      samples.attempts++; samples.zeroConnections += Number(pg.connections === 0);
      samples.activeTransactions += Number(pg.activeTransactions > 0);
      samples.lastConnections = pg.connections; samples.lastActiveTransactions = pg.activeTransactions;
      if (w.databaseIdentityVerified && pg.activeTransactions === 0) {
        // Capture only AFTER stop confirmation, so a pre-stop late commit cannot
        // be mistaken for the post-resume real queue cycle.
        const queue = await queueActivity();
        return { pid: w.pid, parentPID: w.parentPID, starttime: w.starttime, binarySHA256: hash(await readFile(w.binary)), nonceSHA256: hash(w.nonce), applicationNameSHA256: hash(w.applicationName), allThreadsStopped: true, databaseIdentityVerified: w.databaseIdentityVerified, threadCount: w.states.length, connections: pg.connections, activeTransactions: 0, workerID: queue.workerID, queueSuccessBefore: queue.success };
      }
      await signalWorker("SIGCONT"); ownedStopped = false;
      await new Promise((done) => setTimeout(done, 100));
    }
    // Only bounded integer counters are diagnostic; no process environment,
    // application identifier, SQL output or credentials are included.
    throw new Error(`Owned Worker did not reach a transaction-free stop: ${JSON.stringify(samples)}`);
  } catch (error) { await restoreOwnedExportWorker(); throw error; }
}
export async function restoreOwnedExportWorker() {
  if (ownedStopped) { await signalWorker("SIGCONT"); ownedStopped = false; }
}
export async function resumeOwnedExportWorker(context: BrowserContext, pause: Awaited<ReturnType<typeof pauseOwnedExportWorker>>, activityTaskID?: string) {
  await restoreOwnedExportWorker();
  await expect.poll(async () => (await checkedWorker()).states.every((s) => s !== "T" && s !== "t"), { timeout: 5000 }).toBe(true);
  const w = await checkedWorker();
  const scope = activityTaskID === undefined ? undefined : { workerID: pause.workerID, alias: activityAlias(w.nonce, activityTaskID) };
  await expect.poll(async () => (await context.request.get(w.readyURL)).status(), { timeout: 10_000 }).toBe(200);
  await expect.poll(async () => {
    const queue = await queueActivity(scope);
    expect(queue.workerID).toBe(pause.workerID);
    return Number(queue.success) > Number(pause.queueSuccessBefore);
  }, { timeout: 15_000 }).toBe(true);
  const first = await queueActivity(scope);
  expect(first.workerID).toBe(pause.workerID);
  // One completion could be a pre-STOP poll whose activity write was pending.
  // A second strictly newer recorded success proves a fresh post-resume cycle.
  await expect.poll(async () => {
    const queue = await queueActivity(scope);
    expect(queue.workerID).toBe(pause.workerID);
    return Number(queue.success) > Number(first.success);
  }, { timeout: 20_000 }).toBe(true);
  const queue = await queueActivity(scope);
  expect(queue.workerID).toBe(pause.workerID);
  return { sameIdentity: w.pid === pause.pid && w.starttime === pause.starttime && hash(w.nonce) === pause.nonceSHA256, allThreadsRunning: true, readyStatus: 200, workerID: queue.workerID, queueSuccessAfterFirst: first.success, queueSuccessAfter: queue.success };
}
