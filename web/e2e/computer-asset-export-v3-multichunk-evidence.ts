import type { Download, Page, Request, Response } from "@playwright/test";
import { createHash } from "node:crypto";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { open, readFile } from "node:fs/promises";
import { directoryV2ResponseJSON } from "./directory-v2-response-observer";
import { parseExportReceipt, type ExportRequest } from "../src/computer-asset-export-v3-api";

const run = promisify(execFile);
export const suite = "computer-asset-export-v3-multichunk";
export const exportPath = "/api/computer-assets/v3/exports";
export const columns = ["guid", "sAMAccountName", "sid", "dn", "whenCreated"] as const;
export const sha = (data: Buffer | string) => createHash("sha256").update(data).digest("hex");
export const downloadHeaders = ["x-adtr-user-id", "cache-control", "x-content-type-options", "content-type", "content-length", "x-adtr-artifact-sha256", "x-adtr-row-count", "content-disposition"] as const;
type ObjectValue = Record<string, any>;
export function requireEvidence(ok: unknown): asserts ok {
  if (!ok) throw new Error("Multichunk evidence rejected");
}
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/u;
const digestPattern = /^[0-9a-f]{64}$/u;
const decimalPattern = /^[1-9][0-9]{0,18}$/u;
export function uuid(value: unknown): asserts value is string { requireEvidence(typeof value === "string" && uuidPattern.test(value)); }
function resourceID(value: unknown): asserts value is string { requireEvidence(typeof value === "string" && /^[A-Za-z0-9_-]{24}$/u.test(value)); }
function integer(value: unknown, min: number, max: number): asserts value is number { requireEvidence(Number.isSafeInteger(value) && (value as number) >= min && (value as number) <= max); }
function decimal(value: unknown): asserts value is string { requireEvidence(typeof value === "string" && decimalPattern.test(value) && BigInt(value) <= 9223372036854775807n); }
function record(value: unknown): asserts value is ObjectValue { requireEvidence(value !== null && typeof value === "object" && !Array.isArray(value)); }
function stable(value: any): string { return JSON.stringify(value, (_key, item) => item && typeof item === "object" && !Array.isArray(item) ? Object.fromEntries(Object.keys(item).sort().map((key) => [key, item[key]])) : item); }
function same(a: unknown, b: unknown) { requireEvidence(stable(a) === stable(b)); }
function exact(value: unknown, keys: string[]): asserts value is ObjectValue { record(value); requireEvidence(Object.keys(value).length === keys.length && keys.every((key) => Object.hasOwn(value, key))); }
function parse(raw: string): any { try { return JSON.parse(raw); } catch { throw new Error("Multichunk JSON rejected"); } }
export function fileReference(filename: string, data: Buffer) { return { filename, byteCount: data.length, sha256: sha(data) }; }
function base64(value: unknown, maximum: number) {
  requireEvidence(typeof value === "string" && value.length <= 4 * Math.ceil(maximum / 3));
  const bytes = Buffer.from(value, "base64");
  requireEvidence(bytes.length > 0 && bytes.length <= maximum && bytes.toString("base64") === value);
  return bytes;
}

// Only fixed SQL constructors below reach this adapter. No browser/environment
// task string, table name or arbitrary identifier is accepted as SQL.
export type EvidenceQuery = (sql: string, maxBuffer: number) => Promise<any>;
export function isolatedQuery(container = process.env.ADTR_E2E_DB_CONTAINER ?? ""): EvidenceQuery {
  requireEvidence(/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container));
  return async (sql, maxBuffer) => {
    requireEvidence(maxBuffer === (1 << 20) || maxBuffer === (2 << 20));
    try {
      const { stdout } = await run("docker", ["exec", "-e", "PGOPTIONS=-c statement_timeout=2000 -c default_transaction_read_only=on", container, "psql", "-U", "postgres", "-d", "adtr_e2e", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", sql], { encoding: "utf8", timeout: 5000, maxBuffer });
      requireEvidence(Buffer.byteLength(stdout) <= maxBuffer && stdout.trim().length > 0);
      return parse(stdout);
    } catch { throw new Error("Multichunk read-only query failed"); }
  };
}

function sourceSQL(taskID: string) {
  uuid(taskID);
  return `SELECT jsonb_build_object('taskUUID',o.task_id,'tenantId',o.tenant_id,'actorId',o.actor_id::text,'domainId',o.domain_id,'dictionaryVersion',o.dictionary_version,'count',o.object_count,'byteCount',octet_length(o.body),'sha256',o.sha256,'selection',jsonb_build_object('domainId',o.domain_id,'revision',u.connection_revision::text,'credentialRevision',u.connection_credential_generation::text),'sourcePins',jsonb_build_object('accountId',u.account_id,'accountCredentialRevision',u.account_credential_revision::text,'policyRevision',u.policy_revision,'grantRoleId',u.grant_role_id,'grantRevision',u.grant_revision::text),'producer',jsonb_build_object('taskID',t.task_id,'tenantId',t.tenant_id,'domainId',t.domain_id,'actorId',t.actor_id::text,'kind',t.kind,'payloadVersion',t.payload_version,'state',t.state,'attempt',t.attempt,'maxAttempts',t.max_attempts,'fencingToken',t.fencing_token::text),'observationOpener',jsonb_build_object('owner',o.opener_owner,'fencingToken',o.opener_fencing_token::text,'attempt',o.opener_attempt),'useOpener',jsonb_build_object('owner',u.opener_owner,'fencingToken',u.opener_fencing_token::text,'attempt',u.opener_attempt),'purpose',u.purpose,'useState',u.state,'reason',u.quiescence_reason,'dependencies',(SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.consumer_kind=u.purpose AND d.object_id=u.task_id)) FROM adtr.domain_directory_observations o JOIN adtr.tasks t ON (t.task_id,t.tenant_id,t.domain_id,t.actor_id)=(o.task_id,o.tenant_id,o.domain_id,o.actor_id) JOIN adtr.domain_directory_task_uses u ON (u.task_id,u.tenant_id,u.domain_id,u.actor_id)=(o.task_id,o.tenant_id,o.domain_id,o.actor_id) WHERE o.tenant_id='default' AND o.task_id='${taskID}'`;
}
function validateSource(value: any, taskID: string, actor: number) {
  exact(value, ["taskUUID", "tenantId", "actorId", "domainId", "dictionaryVersion", "count", "byteCount", "sha256", "selection", "sourcePins", "producer", "observationOpener", "useOpener", "purpose", "useState", "reason", "dependencies"]);
  resourceID(value.domainId); integer(value.byteCount, 1, 16 << 20);
  requireEvidence(value.taskUUID === taskID && value.actorId === String(actor) && value.tenantId === "default" && value.dictionaryVersion === 3 && value.count === 1024 && digestPattern.test(value.sha256));
  const producer = value.producer;
  requireEvidence(producer.taskID === taskID && producer.tenantId === value.tenantId && producer.domainId === value.domainId && producer.actorId === value.actorId && producer.kind === "domain.directory_read.v3" && producer.state === "succeeded" && producer.payloadVersion === 1 && producer.attempt === 1 && producer.maxAttempts === 1);
  decimal(producer.fencingToken); same(value.observationOpener, value.useOpener);
  requireEvidence(value.observationOpener.fencingToken === producer.fencingToken && value.observationOpener.attempt === producer.attempt && typeof value.observationOpener.owner === "string" && /^[0-9a-f]{32}-[0-3]$/u.test(value.observationOpener.owner));
  requireEvidence(value.purpose === "domain.directory_read.v3" && value.useState === "quiesced" && value.reason === "executor_returned" && value.dependencies === 0 && value.selection.domainId === value.domainId);
  exact(value.selection, ["domainId", "revision", "credentialRevision"]);
  exact(value.sourcePins, ["accountId", "accountCredentialRevision", "policyRevision", "grantRoleId", "grantRevision"]);
  resourceID(value.sourcePins.accountId); decimal(value.sourcePins.accountCredentialRevision); decimal(value.sourcePins.grantRevision);
  requireEvidence(typeof value.sourcePins.policyRevision === "string" && digestPattern.test(value.sourcePins.policyRevision) && value.sourcePins.grantRoleId === "platform_admin");
  decimal(value.selection.revision); decimal(value.selection.credentialRevision);
}
async function readSourceEvidenceInternal(taskID: string, actor: number, path: string, query: EvidenceQuery = isolatedQuery()): Promise<ObjectValue> {
  uuid(taskID); integer(actor, 1, Number.MAX_SAFE_INTEGER);
  const metadata = await query(sourceSQL(taskID), 1 << 20);
  validateSource(metadata, taskID, actor);
  const segments: { offset: number; byteCount: number; sha256: string }[] = [];
  const hash = createHash("sha256");
  const output = await open(path, "wx");
  try {
    for (let offset = 0; offset < metadata.byteCount; offset += 524288) {
      requireEvidence(segments.length < 32);
      const size = Math.min(524288, metadata.byteCount - offset);
      const slice = await query(`SELECT jsonb_build_object('offset',${offset},'byteCount',octet_length(substring(body FROM ${offset + 1} FOR ${size})),'bodyBase64',replace(encode(substring(body FROM ${offset + 1} FOR ${size}),'base64'),chr(10),'')) FROM adtr.domain_directory_observations WHERE tenant_id='default' AND task_id='${taskID}' AND sha256='${metadata.sha256}' AND octet_length(body)=${metadata.byteCount}`, 1 << 20);
      exact(slice, ["offset", "byteCount", "bodyBase64"]);
      const bytes = base64(slice.bodyBase64, 524288);
      requireEvidence(slice.offset === offset && slice.byteCount === size && bytes.length === size);
      await output.writeFile(bytes); hash.update(bytes);
      segments.push({ offset, byteCount: bytes.length, sha256: sha(bytes) });
    }
  } finally { await output.close(); }
  requireEvidence(hash.digest("hex") === metadata.sha256);
  same(metadata, await query(sourceSQL(taskID), 1 << 20));
  const { byteCount, sha256, ...source } = metadata;
  return { ...source, file: { filename: "source-observation.json", byteCount, sha256 }, segments };
}

function exportSQL(taskID: string) {
  uuid(taskID);
  return `SELECT jsonb_build_object('task',jsonb_build_object('taskID',t.task_id,'tenantId',t.tenant_id,'actorId',t.actor_id::text,'domainId',t.domain_id,'kind',t.kind,'payloadVersion',t.payload_version,'state',t.state,'progress',t.progress,'attempt',t.attempt,'maxAttempts',t.max_attempts,'resultVersion',t.result_version::text,'fencingToken',t.fencing_token::text,'authorizationVersion',t.authorization_version,'payloadHash',t.payload_hash,'payload',t.payload,'result',jsonb_build_object('artifactToken',t.result->>'artifactToken','rowCount',t.result->'rowCount','byteCount',t.result->'byteCount','sha256',t.result->'sha256'))),'snapshot',jsonb_build_object('taskID',s.task_id,'tenantId',s.tenant_id,'actorId',s.actor_id::text,'authorizationVersion',s.authorization_version,'observationId',s.observation_id,'sourceSHA256',s.source_sha256,'payloadHash',s.payload_hash,'rowCount',s.row_count),'manifest',jsonb_build_object('taskID',m.task_id,'fencingToken',m.fencing_token::text,'byteCount',m.byte_count,'chunkCount',m.chunk_count,'rowCount',m.row_count,'sha256',m.sha256),'chunks',(SELECT jsonb_agg(jsonb_build_object('taskID',c.task_id,'fencingToken',c.fencing_token::text,'ordinal',c.chunk_no,'byteCount',octet_length(c.data),'sha256',c.sha256) ORDER BY c.fencing_token,c.chunk_no) FROM adtr.computer_asset_export_chunks c WHERE c.task_id=t.task_id),'events',(SELECT jsonb_agg(jsonb_build_object('action',e.action,'state',e.state,'resultVersion',e.result_version::text) ORDER BY e.id) FROM adtr.task_events e WHERE e.task_id=t.task_id)) FROM adtr.tasks t JOIN adtr.computer_asset_export_snapshots s ON s.task_id=t.task_id JOIN adtr.computer_asset_export_manifests m ON m.task_id=t.task_id WHERE t.tenant_id='default' AND t.task_id='${taskID}'`;
}
async function readExportEvidenceInternal(taskID: string, actor: number, source: ObjectValue, request: ExportRequest, path: string, query: EvidenceQuery = isolatedQuery()): Promise<ObjectValue> {
  uuid(taskID); integer(actor, 1, Number.MAX_SAFE_INTEGER);
  const metadata = await query(exportSQL(taskID), 1 << 20);
  exact(metadata, ["task", "snapshot", "manifest", "chunks", "events"]);
  const { task, snapshot, manifest, chunks, events } = metadata;
  decimal(task.fencingToken); decimal(task.resultVersion); decimal(task.result.artifactToken);
  requireEvidence(task.taskID === taskID && task.tenantId === "default" && task.actorId === String(actor) && task.domainId === source.domainId && task.kind === "computer_assets.v3.export" && task.payloadVersion === 1 && task.state === "succeeded" && task.progress === 100 && task.attempt === 1 && task.maxAttempts === 3);
  same(task.payload, request);
  requireEvidence(task.payloadHash === sha("1::" + stable(request)));
  requireEvidence(snapshot.taskID === taskID && snapshot.tenantId === task.tenantId && snapshot.actorId === task.actorId && snapshot.authorizationVersion === task.authorizationVersion && snapshot.payloadHash === task.payloadHash && snapshot.observationId === source.taskUUID && snapshot.sourceSHA256 === source.file.sha256 && snapshot.rowCount === 1020);
  integer(manifest.byteCount, 1048577, 32 << 20); integer(manifest.chunkCount, 2, 32);
  requireEvidence(manifest.taskID === taskID && manifest.fencingToken === task.fencingToken && task.result.artifactToken === task.fencingToken && manifest.rowCount === 1020 && digestPattern.test(manifest.sha256) && manifest.chunkCount === Math.ceil(manifest.byteCount / 1048576));
  requireEvidence(task.result.byteCount === manifest.byteCount && task.result.sha256 === manifest.sha256 && task.result.rowCount === manifest.rowCount && Array.isArray(chunks) && chunks.length === manifest.chunkCount);
  requireEvidence(Array.isArray(events) && events.length > 0 && events.length <= 1000);
  let previous = 0n;
  for (const event of events) { requireEvidence(typeof event.resultVersion === "string" && /^(?:0|[1-9][0-9]{0,18})$/u.test(event.resultVersion) && BigInt(event.resultVersion) <= 9223372036854775807n && BigInt(event.resultVersion) >= previous); previous = BigInt(event.resultVersion); }
  const actions = events.map((event: ObjectValue) => event.action);
  let index = -1;
  for (const action of ["submitted", "leased", "started", "progress", "finished"]) { index = actions.indexOf(action, index + 1); requireEvidence(index >= 0); }
  requireEvidence(events[events.length - 1].state === "succeeded");
  const output = await open(path, "wx");
  const hash = createHash("sha256"); let bytesWritten = 0;
  try {
    for (let ordinal = 0; ordinal < chunks.length; ordinal++) {
      const chunk = chunks[ordinal];
      const expected = Math.min(1048576, manifest.byteCount - ordinal * 1048576);
      requireEvidence(chunk.taskID === taskID && chunk.fencingToken === task.fencingToken && chunk.ordinal === ordinal && chunk.byteCount === expected && digestPattern.test(chunk.sha256));
      const row = await query(`SELECT jsonb_build_object('taskID',task_id,'fencingToken',fencing_token::text,'ordinal',chunk_no,'byteCount',octet_length(data),'sha256',sha256,'bodyBase64',replace(encode(data,'base64'),chr(10),'')) FROM adtr.computer_asset_export_chunks WHERE task_id='${taskID}' AND fencing_token=${task.fencingToken} AND chunk_no=${ordinal}`, 2 << 20);
      exact(row, ["taskID", "fencingToken", "ordinal", "byteCount", "sha256", "bodyBase64"]);
      const { bodyBase64, ...readMetadata } = row; same(readMetadata, chunk);
      const bytes = base64(bodyBase64, 1048576);
      requireEvidence(bytes.length === expected && sha(bytes) === chunk.sha256);
      await output.writeFile(bytes); hash.update(bytes); bytesWritten += bytes.length;
    }
  } finally { await output.close(); }
  requireEvidence(bytesWritten === manifest.byteCount && hash.digest("hex") === manifest.sha256);
  same(metadata, await query(exportSQL(taskID), 1 << 20));
  return { ...metadata, file: { filename: "database-export.xlsx", byteCount: bytesWritten, sha256: manifest.sha256 } };
}

// Values originate in stored source, never workbook cells or the fixture builder.
export function sourceCellSummary(raw: Buffer) {
  requireEvidence(raw.length > 0 && raw.length <= (16 << 20));
  const text = raw.toString("utf8"); requireEvidence(Buffer.from(text).equals(raw));
  const value = parse(text); record(value);
  requireEvidence(value.dictionaryVersion === 3 && Array.isArray(value.objects) && value.objects.length === 1024);
  const rows: string[][] = [];
  for (const object of value.objects) {
    const { base, supplemental } = object;
    if (base.kind !== "computer") continue;
    uuid(base.objectGUID);
    requireEvidence(typeof base.samAccountName === "string" && /^computer-capacity-[0-9]{4}\$$/u.test(base.samAccountName));
    requireEvidence(typeof base.distinguishedName === "string" && base.distinguishedName.includes("computer-") && /^[\x20-\x7e]+$/u.test(base.distinguishedName));
    const sid = base64(supplemental.objectSidBytes, 68);
    requireEvidence(sid.length === 12 && sid[0] === 1 && sid[1] === 1);
    const authority = sid.readUIntBE(2, 6);
    const created = base64(supplemental.whenCreatedBytes, 32).toString("ascii");
    requireEvidence(created === "20260101000000.0Z");
    rows.push([base.objectGUID, base.samAccountName, `S-1-${authority}-${sid.readUInt32LE(8)}`, base.distinguishedName, "2026-01-01T00:00:00Z"]);
  }
  rows.sort((a, b) => a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0);
  requireEvidence(rows.length === 1020 && new Set(rows.map((row) => row[0])).size === rows.length);
  return { rowCount: rows.length, columnCount: 5, firstRowSHA256: sha(JSON.stringify(rows[0])), lastRowSHA256: sha(JSON.stringify(rows[rows.length - 1])), matrixSHA256: sha(JSON.stringify([["GUID", "SAM", "SID", "DN", "whenCreated"], ...rows])) };
}

export function receiptProjection(value: unknown, request: ExportRequest, mode: "create" | "receipt" | "detail", taskID?: string) {
  const parsed = parseExportReceipt(value, request.domainId, request, mode, taskID);
  uuid(parsed.taskID);
  const task = parsed.task;
  const projected = { taskID: parsed.taskID, request: parsed.request, task: { taskUUID: task.taskUUID, taskName: task.taskName, domainId: task.domainId, payloadVersion: task.payloadVersion, state: task.state, progress: task.progress, attempt: task.attempt, maxAttempts: task.maxAttempts, archived: task.archived } };
  if (mode === "create") { requireEvidence(parsed.replayed === false); return { ...projected, replayed: parsed.replayed }; }
  requireEvidence(task.state === "succeeded" && task.progress === 100 && task.attempt === 1 && task.maxAttempts === 3);
  if (mode === "detail") { requireEvidence(parsed.downloadReady === true && parsed.downloadStatus === "ready" && parsed.artifact); return { ...projected, downloadReady: parsed.downloadReady, downloadStatus: parsed.downloadStatus, artifact: parsed.artifact }; }
  return projected;
}

export type RouteObservation = { path: string; method: "GET" | "POST"; query: (query: URLSearchParams) => boolean; binary?: boolean };
export type ActionEvidence = { values: any[]; headers: Record<string, string>[]; download?: Download };
// Host-window evidence only. Owned waiter outcomes are settled/joined on every
// exit; the original CDP/action promises get handlers immediately, not cancelled.
export async function observeAction(page: Page, actor: number, routes: RouteObservation[], action: () => Promise<unknown>, withDownload = false, timeout = 30_000): Promise<ActionEvidence> {
  integer(actor, 1, Number.MAX_SAFE_INTEGER); requireEvidence(routes.length > 0 && routes.length <= 2 && new Set(routes.map((route) => route.path)).size === routes.length);
  const origin = new URL(page.url()).origin;
  const old = new Set<Request>(); const fresh = new Set<Request>();
  const selected: (Request | undefined)[] = routes.map(() => undefined);
  const responseSeen = routes.map(() => false);
  const values: any[] = routes.map(() => undefined), headers: Record<string, string>[] = routes.map(() => ({}));
  let live = true, armed = false, completed = false, failed = false, downloaded: Download | undefined;
  let actionDone = false, downloadDone = !withDownload;
  const pending = routes.map(() => { let resolve!: () => void; const promise = new Promise<void>((done) => { resolve = done; }); return { promise, resolve }; });
  let finish!: () => void;
  const done = new Promise<void>((resolve) => { finish = resolve; });
  const settle = (bad: boolean) => { if (!live) return; failed ||= bad; if (bad || (actionDone && downloadDone && responseSeen.every(Boolean) && pending.every((_, i) => values[i] !== undefined))) { live = false; completed = !bad; for (const item of pending) item.resolve(); finish(); } };
  const fail = () => settle(true);
  const match = (request: Request) => { const url = new URL(request.url()); return routes.findIndex((route) => route.path === url.pathname); };
  const onRequest = (request: Request) => {
    if (!live) return;
    try {
      const index = match(request); if (index < 0) return;
      if (!armed) { old.add(request); return; }
      if (old.has(request)) return;
      fresh.add(request);
      if (selected[index]) return;
      selected[index] = request; // Lock before ANY method/identity/query admission.
      const url = new URL(request.url()), route = routes[index];
      requireEvidence(url.origin === origin && !url.username && !url.password && !url.hash && route.query(url.searchParams) && request.method() === route.method && request.resourceType() === "fetch" && request.frame() === page.mainFrame() && !request.redirectedFrom() && !request.redirectedTo());
    } catch { fail(); }
  };
  const onResponse = (response: Response) => {
    if (!live || !armed) return;
    try {
      const request = response.request(), index = match(request);
      if (index < 0 || old.has(request)) return;
      requireEvidence(fresh.has(request) && selected[index]);
      if (selected[index] !== request || responseSeen[index]) return;
      responseSeen[index] = true;
      requireEvidence(response.status() === 200 && !response.fromServiceWorker());
      const metadata = response.headers();
      requireEvidence(metadata["x-adtr-user-id"] === String(actor) && metadata["cache-control"] === "no-store" && metadata["x-content-type-options"] === "nosniff");
      headers[index] = Object.fromEntries(downloadHeaders.filter((key) => metadata[key] !== undefined).map((key) => [key, metadata[key]]));
      if (routes[index].binary) { requireEvidence(metadata["content-type"] === "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"); values[index] = null; pending[index].resolve(); settle(false); return; }
      requireEvidence(/^application\/json(?:;\s*charset=utf-8)?$/iu.test(metadata["content-type"] ?? ""));
      // Invoke parser once on the selected actual Response before another wait.
      const body = directoryV2ResponseJSON(response);
      void body.then((value) => { if (!live) return; values[index] = value; pending[index].resolve(); settle(false); }, fail);
    } catch { fail(); }
  };
  const onDownload = (download: Download) => { if (!live || !armed) return; if (downloaded) { fail(); return; } downloaded = download; downloadDone = true; settle(false); };
  const onRequestFailed = (request: Request) => { if (selected.includes(request)) fail(); };
  const onFrame = (frame: unknown) => { if (armed && frame === page.mainFrame()) fail(); };
  const timer = setTimeout(fail, timeout);
  try {
    page.on("request", onRequest); page.on("response", onResponse); page.on("download", onDownload); page.on("requestfailed", onRequestFailed); page.on("close", fail); page.on("crash", fail); page.on("framenavigated", onFrame);
    const snapshot = page.requests();
    void snapshot.then((requests) => { if (!live) return; for (const request of requests) old.add(request); armed = true; try { const result = action(); void Promise.resolve(result).then(() => { if (!live) return; actionDone = true; settle(false); }, fail); } catch { fail(); } }, fail);
    await done;
  } catch { failed = true; } finally {
    live = false; clearTimeout(timer);
    page.off("request", onRequest); page.off("response", onResponse); page.off("download", onDownload); page.off("requestfailed", onRequestFailed); page.off("close", fail); page.off("crash", fail); page.off("framenavigated", onFrame);
    for (const item of pending) item.resolve();
    await Promise.allSettled(pending.map((item) => item.promise));
    old.clear(); fresh.clear(); selected.fill(undefined);
  }
  if (failed || !completed) throw new Error("Multichunk original action failed");
  return { values, headers, ...(downloaded ? { download: downloaded } : {}) };
}

export async function captureProvenance(output: string) {
  try {
    const { stdout } = await run("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8", timeout: 5000, maxBuffer: 1 << 20 });
    const root = stdout.trim(); requireEvidence(root.startsWith("/"));
    await run("python3", ["scripts/computer_asset_export_v3_multichunk_ci.py", "capture", "--repo", root, "--output", output], { cwd: root, encoding: "utf8", timeout: 30_000, maxBuffer: 2 << 20 });
    const raw = await readFile(output); requireEvidence(raw.length <= (2 << 20));
    const value = parse(raw.toString("utf8")); exact(value, ["checkoutCommit", "checkoutTree", "runID", "runAttempt", "sourceSHA256"]);
    return value;
  } catch { throw new Error("Multichunk provenance capture failed"); }
}

export async function readSourceEvidence(...args: Parameters<typeof readSourceEvidenceInternal>) {
  try { return await readSourceEvidenceInternal(...args); }
  catch { throw new Error("Multichunk source evidence failed"); }
}
export async function readExportEvidence(...args: Parameters<typeof readExportEvidenceInternal>) {
  try { return await readExportEvidenceInternal(...args); }
  catch { throw new Error("Multichunk export evidence failed"); }
}

// Failure-time snapshot only, never a witness of the failed HTTP poll. The SQL
// maps every value to a literal before stdout; no row identity or raw code exits.
const setupDiagnosticStates = ["queued", "running", "retry_wait", "cancel_requested", "succeeded", "failed", "partial_failed", "dead_letter", "cancelled", "unknown"] as const;
const setupDiagnosticReasons = ["none", "executor_unavailable", "invalid_input", "credential_key_unavailable", "credential_unavailable", "invalid_config", "destination_denied", "invalid_response", "cancelled", "authorization_revoked", "lease_lost", "connection_timeout", "checkpoint_failed", "domain_probe_disabled", "policy_revision_changed", "connection_revision_changed", "directory_read_disabled", "directory_use_changed", "connection_failed", "dns_failed", "connect_failed", "tls_untrusted", "tls_hostname", "tls_expired", "tls_failed", "starttls_required", "credentials_rejected", "ldap_access_denied", "ldap_timeout", "referral_rejected", "directory_limit_exceeded", "unsupported_directory_profile", "other"] as const;
const setupDiagnosticSQL = `WITH sampled AS (SELECT state,error_code FROM adtr.tasks WHERE tenant_id='default' AND kind='domain.directory_read.v3' LIMIT 2), summary AS (SELECT count(*) AS n,min(CASE state ${setupDiagnosticStates.filter((value) => value !== "unknown").map((value) => `WHEN '${value}' THEN '${value}'`).join(" ")} ELSE 'unknown' END) AS state,min(CASE error_code WHEN '' THEN 'none' ${setupDiagnosticReasons.filter((value) => value !== "none" && value !== "other").map((value) => `WHEN '${value}' THEN '${value}'`).join(" ")} ELSE 'other' END) AS reason FROM sampled) SELECT CASE WHEN n=0 THEN 'none|not_applicable|not_applicable' WHEN n=1 THEN 'one|'||state||'|'||reason ELSE 'multiple|not_applicable|not_applicable' END FROM summary`;
export type SetupDiagnosticCommand = (file: string, args: string[], options: { encoding: "utf8"; timeout: number; maxBuffer: number }) => Promise<{ stdout: unknown }>;
const setupUnavailable = "; setup_db_snapshot=unavailable";
function setupDiagnosticSuffix(stdout: unknown): string {
  if (typeof stdout !== "string" || stdout.length > 256 || !/^[\x20-\x7e]+\n$/u.test(stdout) || stdout.indexOf("\n") !== stdout.length - 1) return setupUnavailable;
  const parts = stdout.slice(0, -1).split("|");
  if (parts.length !== 3) return setupUnavailable;
  if (parts[0] === "none" && parts[1] === "not_applicable" && parts[2] === "not_applicable") return "; setup_db_snapshot=none";
  if (parts[0] === "multiple" && parts[1] === "not_applicable" && parts[2] === "not_applicable") return "; setup_db_snapshot=multiple";
  if (parts[0] !== "one") return setupUnavailable;
  const state = setupDiagnosticStates.find((value) => value === parts[1]);
  const reason = setupDiagnosticReasons.find((value) => value === parts[2]);
  if (!state || !reason) return setupUnavailable;
  return `; setup_db_snapshot=one; state=${state}; reason=${reason}`;
}
export async function multichunkSetupFailureSuffix(phase: string, command: SetupDiagnosticCommand = (file, args, options) => run(file, args, options)): Promise<string> {
  if (phase !== "setup") return "";
  try {
    if (process.env.ADTR_E2E_COMPUTER_ASSET_EXPORT_V3_MULTICHUNK !== "true") return setupUnavailable;
    const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
    if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container)) return setupUnavailable;
    const result = await command("docker", ["exec", "-e", "PGOPTIONS=-c statement_timeout=2000 -c default_transaction_read_only=on", container, "psql", "-U", "postgres", "-d", "adtr_e2e", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", setupDiagnosticSQL], { encoding: "utf8", timeout: 5000, maxBuffer: 4096 });
    return setupDiagnosticSuffix(result.stdout);
  } catch { return setupUnavailable; }
}
