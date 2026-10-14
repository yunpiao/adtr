import { expect, type Page, type TestInfo } from "@playwright/test";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { writeFile } from "node:fs/promises";
import { evidenceProvenance, hash, sensitiveBudget } from "./user-asset-export-v3-helpers";
import { panelFor, terminalSetup } from "./user-asset-export-v3-terminal-helpers";

// Only this isolated fixture's loopback controller receives the private token.
// Native fetch avoids Playwright request diagnostics recording its header.
export async function runningGuard(path: "checkpoint" | "resume", body: Record<string, string> = {}) {
  const base = process.env.ADTR_E2E_RUNNING_CANCEL_GUARD_URL ?? "";
  const token = process.env.ADTR_E2E_RUNNING_CANCEL_GUARD_TOKEN ?? "";
  if (!/^http:\/\/127\.0\.0\.1:[0-9]+$/u.test(base) || !/^[0-9a-f]{64}$/u.test(token)) throw new Error("Owned running-cancel guard required");
  try {
    const response = await fetch(`${base}/${path}`, {
      method: "POST", redirect: "error", signal: AbortSignal.timeout(path === "checkpoint" ? 12_000 : 3000),
      headers: { "Content-Type": "application/json", "X-ADTR-Guard-Token": token }, body: JSON.stringify(body),
    });
    if (!response.ok) throw new Error("guard rejected");
    return await response.json();
  } catch { throw new Error(`Owned running-cancel guard ${path} failed`); }
}

type CapturedStatus = {
  bytes: Buffer;
  metadata: { filename: string; sha256: string; inputsExcluded: true; taskID: string; state: string };
};
export async function captureRunningStatus(page: Page, taskID: string, state: "running" | "cancel_requested" | "cancelled", filename: string): Promise<CapturedStatus> {
  const panel = panelFor(page);
  const labels = { running: "执行中", cancel_requested: "已请求取消，待确认", cancelled: "已取消" };
  const status = panel.getByRole("status").filter({ hasText: new RegExp(`^${labels[state]} ·`) });
  const identity = panel.getByText(`任务 ID：${taskID}`, { exact: true });
  const progress = panel.getByLabel("用户导出持久化进度", { exact: true }).locator("..");
  await status.scrollIntoViewIfNeeded({ timeout: 1500 });
  await progress.scrollIntoViewIfNeeded({ timeout: 1500 });
  const boxes = await Promise.all([identity, status, progress].map((locator) => locator.boundingBox()));
  if (boxes.some((box) => !box)) throw new Error("Missing genuine export status geometry");
  const visible = boxes.map((box) => box!);
  const x = Math.floor(Math.min(...visible.map((box) => box.x)));
  const y = Math.floor(Math.min(...visible.map((box) => box.y)));
  const right = Math.ceil(Math.max(...visible.map((box) => box.x + box.width)));
  const bottom = Math.ceil(Math.max(...visible.map((box) => box.y + box.height)));
  const viewport = page.viewportSize();
  if (!viewport || x < 0 || y < 0 || right > viewport.width || bottom > viewport.height) throw new Error("Status crop must be fully visible");
  // Check every input, including the plain-text TOTP, before capturing pixels.
  // No DOM mutation, masking, fabricated labels, or proof-bearing page capture.
  for (const input of await page.locator("input").all()) {
    const box = await input.boundingBox();
    if (box && box.x < right && box.x + box.width > x && box.y < bottom && box.y + box.height > y) throw new Error("Status crop intersects an input");
  }
  const bytes = await page.screenshot({ clip: { x, y, width: right - x, height: bottom - y }, timeout: 1500 });
  return { bytes, metadata: { filename, sha256: hash(bytes), inputsExcluded: true, taskID, state } };
}
export async function saveRunningStatus(info: TestInfo, capture: CapturedStatus) {
  await writeFile(info.outputPath(capture.metadata.filename), capture.bytes);
  await info.attach(capture.metadata.filename, { path: info.outputPath(capture.metadata.filename), contentType: "image/png" });
}
export async function writeRunningEvidence(info: TestInfo, setup: Awaited<ReturnType<typeof terminalSetup>>, cases: unknown, worker: unknown) {
  const after = await sensitiveBudget(setup.actorId, setup.fixture.actorId);
  expect(after).toEqual({ ...setup.before, owner: 9, exportTasks: 1 });
  const evidence = { format: 1, suite: "runningCancel", provenance: evidenceProvenance(), source: setup.source, actorId: setup.actorId,
    sensitiveBudget: { before: setup.before, after }, worker, cases };
  setup.fixture.safe(evidence); setup.fixture.safe(setup.fixture.consoles);
  const filename = "user-asset-export-v3-running-cancel-evidence.json";
  await writeFile(info.outputPath(filename), JSON.stringify(evidence, null, 2));
  await info.attach(filename, { path: info.outputPath(filename), contentType: "application/json" });
}

// Read-only observation across every artifact fence, never task/lease writes.
export async function runningExport(taskID: string) {
  const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container) || !/^[A-Za-z0-9_.-]{1,128}$/u.test(taskID)) throw new Error("Owned isolated export identity required");
  const sql = `SELECT jsonb_build_object('taskID',t.task_id,'actorId',t.actor_id::text,'kind',t.kind,'state',t.state,'error',t.error_code,'attempt',t.attempt,'maxAttempts',t.max_attempts,'progress',t.progress,'resultVersion',t.result_version,'result',t.result,'idempotencyKey',t.idempotency_key,'leaseOwner',t.lease_owner,'leaseUntil',t.lease_until,'fencingToken',t.fencing_token,'databaseClock',extract(epoch FROM clock_timestamp()),'leaseUntilEpoch',extract(epoch FROM t.lease_until),'sourceSHA256',(SELECT source_sha256 FROM adtr.user_asset_export_snapshots WHERE task_id=t.task_id),'observationID',(SELECT observation_id FROM adtr.user_asset_export_snapshots WHERE task_id=t.task_id),'snapshotCount',(SELECT count(*) FROM adtr.user_asset_export_snapshots WHERE task_id=t.task_id),'manifestCount',(SELECT count(*) FROM adtr.user_asset_export_manifests WHERE task_id=t.task_id),'chunkCount',(SELECT count(*) FROM adtr.user_asset_export_chunks WHERE task_id=t.task_id),'events',(SELECT jsonb_agg(jsonb_build_object('action',e.action,'state',e.state,'attempt',e.attempt,'resultVersion',e.result_version) ORDER BY e.id) FROM adtr.task_events e WHERE e.task_id=t.task_id)) FROM adtr.tasks t WHERE t.tenant_id='default' AND t.task_id='${taskID}'`;
  try {
    const { stdout } = await promisify(execFile)("docker", ["exec", "-e", "PGOPTIONS=-c statement_timeout=1500 -c default_transaction_read_only=on", container, "psql", "-U", "postgres", "-d", "adtr_e2e", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", sql], { encoding: "utf8", timeout: 2500, maxBuffer: 2 << 20 });
    return JSON.parse(stdout);
  } catch { throw new Error("Read-only running export evidence query failed"); }
}
