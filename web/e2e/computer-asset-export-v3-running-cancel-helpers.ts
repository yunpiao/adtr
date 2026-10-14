import { expect, type Page, type TestInfo } from "@playwright/test";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { writeFile } from "node:fs/promises";
import { evidenceProvenance, sensitiveBudget } from "./computer-asset-export-v3-helpers";
import { terminalSetup } from "./computer-asset-export-v3-terminal-helpers";
import { captureRunningStatus as captureStatus } from "./asset-export-v3-running-cancel-helpers";
export { runningGuard, saveRunningStatus } from "./asset-export-v3-running-cancel-helpers";

export function captureRunningStatus(page: Page, taskID: string, state: "running" | "cancel_requested" | "cancelled", filename: string) {
  return captureStatus(page, taskID, state, filename, "computer");
}

export async function writeRunningEvidence(info: TestInfo, setup: Awaited<ReturnType<typeof terminalSetup>>, cases: unknown, worker: unknown) {
  const after = await sensitiveBudget(setup.actorId, setup.fixture.actorId);
  expect(after).toEqual({ ...setup.before, owner: 9, exportTasks: 1 });
  const evidence = { format: 1, suite: "runningCancel", provenance: evidenceProvenance(), source: setup.source, actorId: setup.actorId,
    sensitiveBudget: { before: setup.before, after }, worker, cases };
  setup.fixture.safe(evidence); setup.fixture.safe(setup.fixture.consoles);
  const filename = "computer-asset-export-v3-running-cancel-evidence.json";
  await writeFile(info.outputPath(filename), JSON.stringify(evidence, null, 2));
  await info.attach(filename, { path: info.outputPath(filename), contentType: "application/json" });
}

// Read-only observation across every artifact fence, never task/lease writes.
export async function runningExport(taskID: string) {
  const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container) || !/^[A-Za-z0-9_.-]{1,128}$/u.test(taskID)) throw new Error("Owned isolated export identity required");
  const sql = `SELECT jsonb_build_object('taskID',t.task_id,'actorId',t.actor_id::text,'kind',t.kind,'state',t.state,'error',t.error_code,'attempt',t.attempt,'maxAttempts',t.max_attempts,'progress',t.progress,'resultVersion',t.result_version,'result',t.result,'idempotencyKey',t.idempotency_key,'leaseOwner',t.lease_owner,'leaseUntil',t.lease_until,'fencingToken',t.fencing_token,'databaseClock',extract(epoch FROM clock_timestamp()),'leaseUntilEpoch',extract(epoch FROM t.lease_until),'sourceSHA256',(SELECT source_sha256 FROM adtr.computer_asset_export_snapshots WHERE task_id=t.task_id),'observationID',(SELECT observation_id FROM adtr.computer_asset_export_snapshots WHERE task_id=t.task_id),'snapshotCount',(SELECT count(*) FROM adtr.computer_asset_export_snapshots WHERE task_id=t.task_id),'manifestCount',(SELECT count(*) FROM adtr.computer_asset_export_manifests WHERE task_id=t.task_id),'chunkCount',(SELECT count(*) FROM adtr.computer_asset_export_chunks WHERE task_id=t.task_id),'events',(SELECT jsonb_agg(jsonb_build_object('action',e.action,'state',e.state,'attempt',e.attempt,'resultVersion',e.result_version) ORDER BY e.id) FROM adtr.task_events e WHERE e.task_id=t.task_id)) FROM adtr.tasks t WHERE t.tenant_id='default' AND t.task_id='${taskID}'`;
  try {
    const { stdout } = await promisify(execFile)("docker", ["exec", "-e", "PGOPTIONS=-c statement_timeout=1500 -c default_transaction_read_only=on", container, "psql", "-U", "postgres", "-d", "adtr_e2e", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", sql], { encoding: "utf8", timeout: 2500, maxBuffer: 2 << 20 });
    return JSON.parse(stdout);
  } catch { throw new Error("Read-only running export evidence query failed"); }
}
