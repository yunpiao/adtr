// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { EventEmitter } from "node:events";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Page, Request, Response } from "@playwright/test";
import { observeAction, readSourceEvidence, readExportEvidence, isolatedQuery, sha, sourceCellSummary, multichunkSetupFailureSuffix, type EvidenceQuery, type SetupDiagnosticCommand } from "../e2e/computer-asset-export-v3-multichunk-evidence";
import type { ExportRequest } from "./computer-asset-export-v3-api";

vi.mock("../e2e/directory-v2-response-observer", () => ({ directoryV2ResponseJSON: (response: Response) => response.json() }));
const directories: string[] = [];
afterEach(async () => { vi.useRealTimers(); for (const directory of directories.splice(0)) await rm(directory, { recursive: true, force: true }); });
const task = "00000001-1122-3344-5566-778899aabbcc", domain = "SyntheticDomainId0123456";
const canary = "TEST_ONLY_PRIVATE_CANARY_DO_NOT_REPORT";
async function output() { const directory = await mkdtemp(join(tmpdir(), "adtr-multichunk-unit-")); directories.push(directory); return join(directory, "captured.bin"); }
function deferred<T>() { let resolve!: (value: T) => void, reject!: (reason: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
class FakePage extends EventEmitter {
  frame = {};
  old: Request[] = [];
  url() { return "http://127.0.0.1:8080/#computer-assets-v3"; }
  mainFrame() { return this.frame; }
  requests() { return Promise.resolve(this.old); }
  asPage() { return this as unknown as Page; }
}
function request(page: FakePage, changes: Record<string, unknown> = {}) {
  return { url: () => "http://127.0.0.1:8080/api/computer-assets/v3/exports/detail?taskID=" + task, method: () => "GET", resourceType: () => "fetch", frame: () => page.frame, redirectedFrom: () => null, redirectedTo: () => null, ...changes } as unknown as Request;
}
function response(req: Request, json: () => Promise<any> = vi.fn(async () => ({ marker: "selected" })), changes: Record<string, unknown> = {}) {
  return { request: () => req, status: () => 200, fromServiceWorker: () => false, headers: () => ({ "x-adtr-user-id": "7", "cache-control": "no-store", "x-content-type-options": "nosniff", "content-type": "application/json" }), json, ...changes } as unknown as Response;
}
const route = { path: "/api/computer-assets/v3/exports/detail", method: "GET" as const, query: (query: URLSearchParams) => [...query].length === 1 && query.get("taskID") === task };
const listenerNames = ["request", "response", "download", "requestfailed", "close", "crash", "framenavigated"];
function noListeners(page: FakePage) { for (const event of listenerNames) expect(page.listenerCount(event)).toBe(0); }

describe("first original browser response and owned waiter lifecycle", () => {
  it("reads the locked object once before action resolves, preserving return identity", async () => {
    const page = new FakePage(), actionDone = deferred<void>(), value = { marker: "selected" };
    const json = vi.fn(async () => value), req = request(page);
    const action = vi.fn(() => { page.emit("request", req); page.emit("response", response(req, json)); expect(json).toHaveBeenCalledTimes(1); return actionDone.promise; });
    let returned = false;
    const result = observeAction(page.asPage(), 7, [route], action).then((out) => { returned = true; return out; });
    await Promise.resolve(); await Promise.resolve(); expect(returned).toBe(false);
    actionDone.resolve(); expect((await result).values[0]).toBe(value); expect(action).toHaveBeenCalledTimes(1); noListeners(page);
  });
  it.each(["actor", "status", "mime", "cache", "nosniff", "service_worker", "method", "query", "origin", "frame", "resource", "redirect"])('rejects first bad %s even when a later candidate is correct', async (kind) => {
    const page = new FakePage();
    const requestChanges: Record<string, unknown> = {};
    if (kind === "method") requestChanges.method = () => "POST";
    if (kind === "query") requestChanges.url = () => "http://127.0.0.1:8080" + route.path + "?taskID=wrong";
    if (kind === "origin") requestChanges.url = () => "http://localhost:8080" + route.path + "?taskID=" + task;
    if (kind === "frame") requestChanges.frame = () => ({});
    if (kind === "resource") requestChanges.resourceType = () => "document";
    if (kind === "redirect") requestChanges.redirectedFrom = () => ({});
    const first = request(page, requestChanges), second = request(page), secondJSON = vi.fn(async () => ({ marker: "later" }));
    const firstResponse = response(first);
    const headers = firstResponse.headers();
    if (kind === "actor") headers["x-adtr-user-id"] = "8";
    if (kind === "mime") headers["content-type"] = "text/html";
    if (kind === "cache") headers["cache-control"] = "public";
    if (kind === "nosniff") headers["x-content-type-options"] = "";
    const changes = { headers: () => headers, ...(kind === "status" ? { status: () => 500 } : {}), ...(kind === "service_worker" ? { fromServiceWorker: () => true } : {}) };
    await expect(observeAction(page.asPage(), 7, [route], async () => { page.emit("request", first); page.emit("response", response(first, undefined, changes)); page.emit("request", second); page.emit("response", response(second, secondJSON)); })).rejects.toThrow("Multichunk original action failed");
    expect(secondJSON).not.toHaveBeenCalled(); noListeners(page);
  });
  it("does not replace malformed first JSON with a later response", async () => {
    const page = new FakePage(), first = request(page), second = request(page);
    const firstJSON = vi.fn(() => Promise.reject(new SyntaxError(canary))), secondJSON = vi.fn(async () => ({}));
    const result = observeAction(page.asPage(), 7, [route], async () => { page.emit("request", first); page.emit("response", response(first, firstJSON)); page.emit("request", second); page.emit("response", response(second, secondJSON)); });
    await expect(result).rejects.toThrow(/^Multichunk original action failed$/u); expect(firstJSON).toHaveBeenCalledTimes(1); expect(secondJSON).not.toHaveBeenCalled(); noListeners(page);
  });
  it.each(["synchronous", "asynchronous"])('fails a %s action without leaking its error or accepting parsed success', async (kind) => {
    const page = new FakePage(), req = request(page);
    const error = Object.assign(new Error(canary), { cause: { secret: canary }, headers: { cookie: canary } });
    const result = observeAction(page.asPage(), 7, [route], () => { page.emit("request", req); page.emit("response", response(req)); if (kind === "synchronous") throw error; return Promise.reject(error); });
    try { await result; throw new Error("unexpected success"); } catch (caught) { expect(String(caught)).toBe("Error: Multichunk original action failed"); expect((caught as Error).cause).toBeUndefined(); }
    noListeners(page);
  });
  it("excludes old snapshot objects permanently and rejects unknown response objects", async () => {
    const page = new FakePage(), old = request(page), fresh = request(page); page.old = [old];
    const oldJSON = vi.fn(async () => ({}));
    const out = await observeAction(page.asPage(), 7, [route], async () => { page.emit("request", old); page.emit("response", response(old, oldJSON)); page.emit("request", fresh); page.emit("response", response(fresh)); });
    expect(out.values[0]).toEqual({ marker: "selected" }); expect(oldJSON).not.toHaveBeenCalled(); noListeners(page);
    await expect(observeAction(page.asPage(), 7, [route], async () => { page.emit("response", response(request(page))); })).rejects.toThrow("Multichunk original action failed"); noListeners(page);
  });
  it.each(["close", "crash", "framenavigated", "requestfailed"])('cleans up on %s and ignores late body settlement', async (event) => {
    const page = new FakePage(), req = request(page), body = deferred<any>();
    const result = observeAction(page.asPage(), 7, [route], async () => { page.emit("request", req); page.emit("response", response(req, () => body.promise)); page.emit(event, event === "framenavigated" ? page.frame : req); });
    await expect(result).rejects.toThrow("Multichunk original action failed"); body.reject(new Error(canary)); await Promise.resolve(); noListeners(page);
  });
  it.each(["snapshot", "body", "action"])('has one deadline when %s never settles; late completion cannot revive it', async (kind) => {
    vi.useFakeTimers(); const page = new FakePage(), blocker = deferred<any>(), req = request(page), action = vi.fn(() => { page.emit("request", req); page.emit("response", response(req, kind === "body" ? () => blocker.promise : undefined)); return kind === "action" ? blocker.promise : Promise.resolve(); });
    if (kind === "snapshot") page.requests = () => blocker.promise;
    const result = observeAction(page.asPage(), 7, [route], action, false, 30_000);
    const rejection = expect(result).rejects.toThrow("Multichunk original action failed");
    await vi.advanceTimersByTimeAsync(30_000); await rejection; noListeners(page); expect(vi.getTimerCount()).toBe(0);
    blocker.resolve(kind === "snapshot" ? [] : { marker: canary }); await Promise.resolve();
    expect(action).toHaveBeenCalledTimes(kind === "snapshot" ? 0 : 1);
  });
  it("releases partially installed listeners after a synchronous installation failure", async () => {
    const page = new FakePage(), on = page.on.bind(page), action = vi.fn(async () => undefined);
    page.on = ((event: string, listener: (...args: any[]) => void) => { if (event === "response") throw new Error(canary); return on(event, listener); }) as typeof page.on;
    await expect(observeAction(page.asPage(), 7, [route], action)).rejects.toThrow(/^Multichunk original action failed$/u);
    expect(action).not.toHaveBeenCalled(); noListeners(page);
  });
  it("rejects two download events instead of choosing one", async () => {
    const page = new FakePage(), req = request(page);
    await expect(observeAction(page.asPage(), 7, [route], async () => { page.emit("request", req); page.emit("response", response(req)); page.emit("download", {}); page.emit("download", {}); }, true)).rejects.toThrow("Multichunk original action failed");
    noListeners(page);
  });
  it("joins two selected responses and one actual download without reading binary body", async () => {
    const page = new FakePage(), detail = request(page), binary = request(page, { url: () => "http://127.0.0.1:8080/api/computer-assets/v3/exports/download?taskID=" + task });
    const binaryJSON = vi.fn(), download = { suggestedFilename: () => "file.xlsx" };
    const result = await observeAction(page.asPage(), 7, [route, { ...route, path: "/api/computer-assets/v3/exports/download", binary: true }], async () => { page.emit("request", detail); page.emit("request", binary); page.emit("response", response(binary, binaryJSON, { headers: () => ({ "x-adtr-user-id": "7", "cache-control": "no-store", "x-content-type-options": "nosniff", "content-type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" }) })); page.emit("download", download); page.emit("response", response(detail)); }, true);
    expect(result.download).toBe(download); expect(binaryJSON).not.toHaveBeenCalled(); noListeners(page);
  });
});

function sourceMetadata(bytes: Buffer) {
  const opener = { owner: "a".repeat(32) + "-0", fencingToken: "1", attempt: 1 };
  return { taskUUID: task, tenantId: "default", actorId: "2", domainId: domain, dictionaryVersion: 3, count: 1024, byteCount: bytes.length, sha256: sha(bytes), selection: { domainId: domain, revision: "1", credentialRevision: "1" }, sourcePins: { accountId: domain, accountCredentialRevision: "1", policyRevision: "a".repeat(64), grantRoleId: "platform_admin", grantRevision: "1" }, producer: { taskID: task, tenantId: "default", domainId: domain, actorId: "2", kind: "domain.directory_read.v3", payloadVersion: 1, state: "succeeded", attempt: 1, maxAttempts: 1, fencingToken: "1" }, observationOpener: opener, useOpener: { ...opener }, purpose: "domain.directory_read.v3", useState: "quiesced", reason: "executor_returned", dependencies: 0 };
}
const requestPayload: ExportRequest = { domainId: domain, expectedRevision: "1", expectedCredentialRevision: "1", observationId: task, search: "computer-", startTm: "2026-01-01T00:00:00Z", endTm: "2026-01-02T00:00:00Z", creatTm: "asc", screenColumns: ["guid", "sAMAccountName", "sid", "dn", "whenCreated"] };
function exportMetadata(bytes: Buffer) {
  const checksum = sha(bytes), sourceSHA256 = "b".repeat(64), payloadHash = sha("1::" + JSON.stringify(Object.fromEntries(Object.entries(requestPayload).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0))));
  const chunks = [bytes.subarray(0, 1048576), bytes.subarray(1048576)].map((data, ordinal) => ({ taskID: task, fencingToken: "1", ordinal, byteCount: data.length, sha256: sha(data) }));
  return { task: { taskID: task, tenantId: "default", actorId: "7", domainId: domain, kind: "computer_assets.v3.export", payloadVersion: 1, state: "succeeded", progress: 100, attempt: 1, maxAttempts: 3, resultVersion: "3", fencingToken: "1", authorizationVersion: "1", payloadHash, payload: requestPayload, result: { artifactToken: "1", rowCount: 1020, byteCount: bytes.length, sha256: checksum } }, snapshot: { taskID: task, tenantId: "default", actorId: "7", authorizationVersion: "1", observationId: task, sourceSHA256, payloadHash, rowCount: 1020 }, manifest: { taskID: task, fencingToken: "1", byteCount: bytes.length, chunkCount: 2, rowCount: 1020, sha256: checksum }, chunks, events: ["submitted", "leased", "started", "progress", "finished"].map((action, index) => ({ action, state: index < 2 ? "queued" : index === 4 ? "succeeded" : "running", resultVersion: String(Math.max(0, index - 1)) })) };
}
describe("bounded actual database readers, not exporter-provided expected bytes", () => {
  it("reads original source in contiguous 512KiB pieces and rechecks metadata", async () => {
    const bytes = Buffer.alloc(524300, 97), metadata = sourceMetadata(bytes), calls: [string, number][] = [];
    const query: EvidenceQuery = async (sql, limit) => { calls.push([sql, limit]); const match = sql.match(/substring\(body FROM (\d+) FOR (\d+)\)/u); if (!match) return structuredClone(metadata); const offset = Number(match[1]) - 1, size = Number(match[2]); return { offset, byteCount: size, bodyBase64: bytes.subarray(offset, offset + size).toString("base64") }; };
    const path = await output(), value = await readSourceEvidence(task, 2, path, query);
    expect(await readFile(path)).toEqual(bytes); expect(value.segments.map((part: { byteCount: number }) => part.byteCount)).toEqual([524288, 12]); expect(value.file.sha256).toBe(sha(bytes));
    expect(calls).toHaveLength(4); expect(calls.every((call) => call[1] === (1 << 20))).toBe(true); expect(calls[0][0]).toBe(calls[3][0]); expect("bodyBase64" in value).toBe(false);
  });
  it.each(["metadata_changed", "wrong_offset", "wrong_size", "bad_base64", "wrong_digest", "oversize", "wrong_actor", "opener_mismatch", "domain_id", "account_id"])('rejects source %s without exposing data', async (kind) => {
    const bytes = Buffer.alloc(20, 97), metadata = sourceMetadata(bytes); let metadataReads = 0;
    if (kind === "oversize") metadata.byteCount = (16 << 20) + 1;
    if (kind === "wrong_actor") metadata.actorId = "3";
    if (kind === "domain_id") metadata.domainId = task;
    if (kind === "account_id") metadata.sourcePins.accountId = "short";
    if (kind === "opener_mismatch") metadata.useOpener.fencingToken = "2";
    if (kind === "wrong_digest") metadata.sha256 = "d".repeat(64);
    const query: EvidenceQuery = async (sql) => { if (!sql.includes("substring(body")) { metadataReads++; return { ...metadata, ...(kind === "metadata_changed" && metadataReads === 2 ? { count: 1023 } : {}) }; } return { offset: kind === "wrong_offset" ? 1 : 0, byteCount: kind === "wrong_size" ? 19 : 20, bodyBase64: kind === "bad_base64" ? canary : bytes.toString("base64") }; };
    await expect(readSourceEvidence(task, 2, await output(), query)).rejects.toThrow(/^Multichunk source evidence failed$/u);
  });
  it("rejects invalid identity before invoking a query", async () => { const query = vi.fn(); await expect(readSourceEvidence("bad'" + canary, 2, await output(), query)).rejects.toThrow("Multichunk source evidence failed"); expect(query).not.toHaveBeenCalled(); expect(() => isolatedQuery("external-database")).toThrow("Multichunk evidence rejected"); });
  it("writes distinct DB chunks with pinned token and unchanged 2MiB buffers", async () => {
    const bytes = Buffer.alloc(1048593, 65), metadata = exportMetadata(bytes), calls: [string, number][] = [];
    const query: EvidenceQuery = async (sql, limit) => { calls.push([sql, limit]); const match = sql.match(/AND chunk_no=(\d+)$/u); if (!match) return structuredClone(metadata); const ordinal = Number(match[1]); return { ...metadata.chunks[ordinal], bodyBase64: bytes.subarray(ordinal * 1048576, (ordinal + 1) * 1048576).toString("base64") }; };
    const path = await output(), value = await readExportEvidence(task, 7, { domainId: domain, taskUUID: task, file: { sha256: "b".repeat(64) } }, requestPayload, path, query);
    expect(await readFile(path)).toEqual(bytes); expect(value.file.sha256).toBe(sha(bytes)); expect(calls.map((call) => call[1])).toEqual([1 << 20, 2 << 20, 2 << 20, 1 << 20]); expect(calls[1][0]).toContain("fencing_token=1 AND chunk_no=0"); expect(calls[0][0]).toBe(calls[3][0]);
  });
  it.each(["token", "missing", "duplicate", "ordinal", "tail", "digest", "payload", "payload_hash", "epoch", "event_order", "metadata_changed", "raw_failure"])('rejects export %s without using a browser file', async (kind) => {
    const bytes = Buffer.alloc(1048580, 65), metadata = exportMetadata(bytes); let metadataReads = 0;
    if (kind === "token") metadata.chunks[0].fencingToken = "2";
    if (kind === "missing") metadata.chunks.pop();
    if (kind === "duplicate") metadata.chunks.push(metadata.chunks[0]);
    if (kind === "ordinal") metadata.chunks[0].ordinal = 1;
    if (kind === "tail") metadata.chunks[1].byteCount++;
    if (kind === "digest") metadata.chunks[0].sha256 = "d".repeat(64);
    if (kind === "payload") metadata.task.payload = { ...requestPayload, search: "other" };
    if (kind === "payload_hash") metadata.task.payloadHash = metadata.snapshot.payloadHash = "c".repeat(64);
    if (kind === "epoch") metadata.snapshot.authorizationVersion = "2";
    if (kind === "event_order") metadata.events.reverse();
    const query: EvidenceQuery = async (sql) => { if (kind === "raw_failure") throw new Error(canary); const match = sql.match(/AND chunk_no=(\d+)$/u); if (!match) { metadataReads++; return { ...metadata, ...(kind === "metadata_changed" && metadataReads === 2 ? { extra: canary } : {}) }; } const ordinal = Number(match[1]); return { ...metadata.chunks[ordinal], bodyBase64: bytes.subarray(ordinal * 1048576, (ordinal + 1) * 1048576).toString("base64") }; };
    await expect(readExportEvidence(task, 7, { domainId: domain, taskUUID: task, file: { sha256: "b".repeat(64) } }, requestPayload, await output(), query)).rejects.toThrow(/^Multichunk export evidence failed$/u);
  });
  it("derives the complete sorted matrix only from actual stored fields", () => {
    const objects = Array.from({ length: 1024 }, (_, index) => {
      const row = index + 1, sid = Buffer.alloc(12); sid[0] = 1; sid[1] = 1; sid[7] = 5; sid.writeUInt32LE(row, 8);
      return { base: { kind: row <= 1020 ? "computer" : row <= 1022 ? "user" : "group", objectGUID: row.toString(16).padStart(8, "0") + "-1122-3344-5566-778899aabbcc", samAccountName: `computer-capacity-${String(row).padStart(4, "0")}$`, distinguishedName: `CN=computer-capacity-${row},DC=synthetic,DC=invalid` }, supplemental: { objectSidBytes: sid.toString("base64"), whenCreatedBytes: Buffer.from("20260101000000.0Z").toString("base64") } };
    });
    const value = sourceCellSummary(Buffer.from(JSON.stringify({ dictionaryVersion: 3, objects: [...objects].reverse() })));
    const rows = objects.slice(0, 1020).map((object, index) => [object.base.objectGUID, object.base.samAccountName, `S-1-5-${index + 1}`, object.base.distinguishedName, "2026-01-01T00:00:00Z"]);
    expect(value).toEqual({ rowCount: 1020, columnCount: 5, firstRowSHA256: sha(JSON.stringify(rows[0])), lastRowSHA256: sha(JSON.stringify(rows[1019])), matrixSHA256: sha(JSON.stringify([["GUID", "SAM", "SID", "DN", "whenCreated"], ...rows])) });
    objects[1].base.objectGUID = objects[0].base.objectGUID;
    expect(() => sourceCellSummary(Buffer.from(JSON.stringify({ dictionaryVersion: 3, objects })))).toThrow("Multichunk evidence rejected");
  });
  it("does not interpret arbitrary source JSON as a row summary", () => { expect(() => sourceCellSummary(Buffer.from('{"objects":[],"dictionaryVersion":3}'))).toThrow("Multichunk evidence rejected"); expect(() => sourceCellSummary(Buffer.from([0xff]))).toThrow("Multichunk evidence rejected"); });
});

// A fake command owns all diagnostic unit outcomes. No Docker, SQL or browser
// runs here; actual failure-time snapshots remain an exact-head CI obligation.
describe("closed failure-time setup snapshot", () => {
  const unavailable = "; setup_db_snapshot=unavailable";
  const container = "adtr-auth-e2e-012345abcdef";
  const states = ["queued", "running", "retry_wait", "cancel_requested", "succeeded", "failed", "partial_failed", "dead_letter", "cancelled", "unknown"];
  const reasons = ["none", "executor_unavailable", "invalid_input", "credential_key_unavailable", "credential_unavailable", "invalid_config", "destination_denied", "invalid_response", "cancelled", "authorization_revoked", "lease_lost", "connection_timeout", "checkpoint_failed", "domain_probe_disabled", "policy_revision_changed", "connection_revision_changed", "directory_read_disabled", "directory_use_changed", "connection_failed", "dns_failed", "connect_failed", "tls_untrusted", "tls_hostname", "tls_expired", "tls_failed", "starttls_required", "credentials_rejected", "ldap_access_denied", "ldap_timeout", "referral_rejected", "directory_limit_exceeded", "unsupported_directory_profile", "other"];
  const checkpoints = ["p0", "p10", "p15", "p20", "p25", "p30", "p35", "p40", "p41", "p42", "p95", "p100", "other"];
  const attempts = ["zero", "one", "other"], leases = ["absent", "unexpired", "expired", "inconsistent"];
  const uses = ["missing", "reserved", "opened", "quiesced", "other"], openers = ["not_applicable", "matched", "mismatch"], observations = ["absent", "present", "other"];
  const validFields = ["one", "running", "none", "p95", "one", "unexpired", "opened", "matched", "absent"];
  const emptyRow = (count: string) => [count, ...Array<string>(8).fill("not_applicable")].join("|") + "\n";
  const diagnosticRow = (state: string, reason: string) => ["one", state, reason, ...validFields.slice(3)].join("|") + "\n";
  const extraSuffix = "; checkpoint=p95; attempt=one; lease=unexpired; use=opened; opener=matched; observation=absent";
  const enable = () => { vi.stubEnv("ADTR_E2E_COMPUTER_ASSET_EXPORT_V3_MULTICHUNK", "true"); vi.stubEnv("ADTR_E2E_DB_CONTAINER", container); };
  afterEach(() => vi.unstubAllEnvs());

  it.each(["fixture_guard", "provenance", "source", "owner", "create", "ready", "download", "evidence", "", "setup "])('performs zero diagnostic commands outside setup: %s', async (phase) => {
    const command = vi.fn(async () => { throw new Error(canary); });
    expect(await multichunkSetupFailureSuffix(phase, command)).toBe("");
    expect(command).not.toHaveBeenCalled();
  });
  it.each(["disabled", "missing_container", "external_container", "long_container", "shell_container"])('rejects %s before dispatch', async (kind) => {
    enable();
    if (kind === "disabled") vi.stubEnv("ADTR_E2E_COMPUTER_ASSET_EXPORT_V3_MULTICHUNK", "TRUE");
    if (kind === "missing_container") vi.stubEnv("ADTR_E2E_DB_CONTAINER", "");
    if (kind === "external_container") vi.stubEnv("ADTR_E2E_DB_CONTAINER", "production-db");
    if (kind === "long_container") vi.stubEnv("ADTR_E2E_DB_CONTAINER", container + "a");
    if (kind === "shell_container") vi.stubEnv("ADTR_E2E_DB_CONTAINER", container + ";" + canary);
    const command = vi.fn(async () => ({ stdout: emptyRow("none") }));
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe(unavailable);
    expect(command).not.toHaveBeenCalled();
  });
  it("uses one fixed read-only command with bounded rows, SQL time, process time and output", async () => {
    enable();
    const command = vi.fn<SetupDiagnosticCommand>(async () => ({ stdout: emptyRow("none") }));
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe("; setup_db_snapshot=none");
    const expectedSQL = `WITH sampled AS MATERIALIZED (SELECT task_id,tenant_id,domain_id,state,error_code,progress,attempt,lease_owner,lease_until,fencing_token FROM adtr.tasks WHERE tenant_id='default' AND kind='domain.directory_read.v3' LIMIT 2), counts AS (SELECT count(*) AS n FROM sampled), single AS MATERIALIZED (SELECT sampled.* FROM sampled CROSS JOIN counts WHERE counts.n=1), projected AS (SELECT CASE t.state ${states.filter((value) => value !== "unknown").map((value) => `WHEN '${value}' THEN '${value}'`).join(" ")} ELSE 'unknown' END AS state,CASE t.error_code WHEN '' THEN 'none' ${reasons.filter((value) => value !== "none" && value !== "other").map((value) => `WHEN '${value}' THEN '${value}'`).join(" ")} ELSE 'other' END AS reason,CASE t.progress ${checkpoints.filter((value) => value !== "other").map((value) => `WHEN ${value.slice(1)} THEN '${value}'`).join(" ")} ELSE 'other' END AS checkpoint,CASE t.attempt WHEN 0 THEN 'zero' WHEN 1 THEN 'one' ELSE 'other' END AS attempt,CASE WHEN t.lease_owner='' AND t.lease_until IS NULL THEN 'absent' WHEN t.lease_owner<>'' AND t.lease_until IS NOT NULL AND t.lease_until>statement_timestamp() THEN 'unexpired' WHEN t.lease_owner<>'' AND t.lease_until IS NOT NULL AND t.lease_until<=statement_timestamp() THEN 'expired' ELSE 'inconsistent' END AS lease,CASE WHEN u.task_id IS NULL THEN 'missing' WHEN u.tenant_id IS DISTINCT FROM t.tenant_id OR u.domain_id IS DISTINCT FROM t.domain_id OR u.purpose IS DISTINCT FROM 'domain.directory_read.v3' OR u.dictionary_version IS DISTINCT FROM 3 THEN 'other' ELSE CASE u.state WHEN 'reserved' THEN 'reserved' WHEN 'opened' THEN 'opened' WHEN 'quiesced' THEN 'quiesced' ELSE 'other' END END AS use_state,CASE WHEN u.task_id IS NULL OR u.state IS DISTINCT FROM 'opened' THEN 'not_applicable' WHEN u.tenant_id=t.tenant_id AND u.domain_id=t.domain_id AND u.purpose='domain.directory_read.v3' AND u.dictionary_version=3 AND t.lease_owner<>'' AND t.fencing_token>0 AND t.attempt=1 AND ROW(t.lease_owner,t.fencing_token,t.attempt) IS NOT DISTINCT FROM ROW(u.opener_owner,u.opener_fencing_token,u.opener_attempt) THEN 'matched' ELSE 'mismatch' END AS opener,CASE WHEN o.task_id IS NULL THEN 'absent' WHEN o.tenant_id=t.tenant_id AND o.domain_id=t.domain_id AND o.dictionary_version=3 THEN 'present' ELSE 'other' END AS observation FROM single t LEFT JOIN adtr.domain_directory_task_uses u ON u.task_id=t.task_id LEFT JOIN adtr.domain_directory_observations o ON o.task_id=t.task_id) SELECT CASE WHEN c.n=0 THEN 'none|not_applicable|not_applicable|not_applicable|not_applicable|not_applicable|not_applicable|not_applicable|not_applicable' WHEN c.n=1 THEN 'one|'||p.state||'|'||p.reason||'|'||p.checkpoint||'|'||p.attempt||'|'||p.lease||'|'||p.use_state||'|'||p.opener||'|'||p.observation ELSE 'multiple|not_applicable|not_applicable|not_applicable|not_applicable|not_applicable|not_applicable|not_applicable|not_applicable' END FROM counts c LEFT JOIN projected p ON true`;
    expect(command).toHaveBeenCalledTimes(1);
    expect(command).toHaveBeenCalledWith("docker", ["exec", "-e", "PGOPTIONS=-c statement_timeout=2000 -c default_transaction_read_only=on", container, "psql", "-U", "postgres", "-d", "adtr_e2e", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", expectedSQL], { encoding: "utf8", timeout: 5000, maxBuffer: 4096 });
  });
  it.each(["none", "multiple"])('reports %s without choosing or exposing any row', async (count) => {
    enable(); const command = vi.fn(async () => ({ stdout: emptyRow(count) }));
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe(`; setup_db_snapshot=${count}`);
    expect(command).toHaveBeenCalledTimes(1);
  });
  it.each(states)('preserves permitted state %s even with an unusual reason', async (state) => {
    enable(); const command = vi.fn(async () => ({ stdout: diagnosticRow(state, "directory_limit_exceeded") }));
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe(`; setup_db_snapshot=one; state=${state}; reason=directory_limit_exceeded${extraSuffix}`);
    expect(command).toHaveBeenCalledTimes(1);
  });
  it.each(reasons)('admits only fixed reason %s', async (reason) => {
    enable(); const command = vi.fn(async () => ({ stdout: diagnosticRow("failed", reason) }));
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe(`; setup_db_snapshot=one; state=failed; reason=${reason}${extraSuffix}`);
    expect(command).toHaveBeenCalledTimes(1);
  });
  it.each([
    ["empty", ""], ["count", "two|failed|none|p95|one|unexpired|opened|matched|absent\n"], ["state", diagnosticRow("not_applicable", "none")],
    ["raw_state", diagnosticRow(canary, "none")], ["raw_reason", diagnosticRow("failed", canary)],
    ["none_row", "none|failed|none|p95|one|unexpired|opened|matched|absent\n"], ["multiple_row", "multiple|running|none|p95|one|unexpired|opened|matched|absent\n"],
    ["extra_token", diagnosticRow("failed", "none").slice(0, -1) + "|extra\n"], ["missing_token", "one|failed|p95|one|unexpired|opened|matched|absent\n"],
    ["extra_row", diagnosticRow("failed", "none") + diagnosticRow("succeeded", "none")], ["missing_newline", diagnosticRow("failed", "none").slice(0, -1)],
    ["double_newline", diagnosticRow("failed", "none") + "\n"], ["crlf", diagnosticRow("failed", "none").replace("\n", "\r\n")],
    ["leading_space", " " + diagnosticRow("failed", "none")], ["trailing_space", diagnosticRow("failed", "none").slice(0, -1) + " \n"],
    ["nul", diagnosticRow("failed", "none").slice(0, -1) + "\0\n"], ["non_ascii", diagnosticRow("failed", "非枚举")],
    ["oversize", "x".repeat(257) + "\n"], ["non_string", null], ["object", { canary }],
  ])('maps malformed %s to unavailable without leaking data', async (_label, stdout) => {
    enable(); const command = vi.fn(async () => ({ stdout }));
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe(unavailable);
    expect(command).toHaveBeenCalledTimes(1);
  });
  it.each(["synchronous", "rejected"])('does not inspect the %s original command error', async (mode) => {
    enable(); const touched = vi.fn();
    const error = Object.defineProperties({}, Object.fromEntries(["message", "stack", "cause", "headers", "stdout", "stderr"].map((key) => [key, { get: () => { touched(); throw new Error(canary); } }])));
    const command = vi.fn(() => { if (mode === "synchronous") throw error; return Promise.reject(error); });
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe(unavailable);
    expect(command).toHaveBeenCalledTimes(1); expect(touched).not.toHaveBeenCalled();
  });
  it("never reads or reports successful-command stderr", async () => {
    enable(); const touched = vi.fn();
    const result = Object.defineProperty({ stdout: diagnosticRow("failed", "other") }, "stderr", { get: () => { touched(); throw new Error(canary); } });
    const command = vi.fn(async () => result);
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe("; setup_db_snapshot=one; state=failed; reason=other" + extraSuffix);
    expect(touched).not.toHaveBeenCalled();
  });
  it.each(["resolve", "reject"])('waits for the original one-shot command to %s, without detached racing', async (ending) => {
    enable(); const pending = deferred<{ stdout: unknown }>(), command = vi.fn(() => pending.promise);
    let returned = false;
    const result = multichunkSetupFailureSuffix("setup", command).then((value) => { returned = true; return value; });
    await Promise.resolve(); expect(returned).toBe(false); expect(command).toHaveBeenCalledTimes(1);
    if (ending === "resolve") pending.resolve({ stdout: diagnosticRow("running", "none") });
    else pending.reject(new Error(canary));
    expect(await result).toBe(ending === "resolve" ? "; setup_db_snapshot=one; state=running; reason=none" + extraSuffix : unavailable);
    expect(command).toHaveBeenCalledTimes(1);
  });

  // These fake rows exercise the closed host grammar, not PostgreSQL execution.
  for (const [index, values] of [[3, checkpoints], [4, attempts], [5, leases], [6, uses], [7, openers], [8, observations]] as const) {
    it.each(values)('admits the closed token at field ' + index + ': %s', async (value) => {
      enable(); const fields = [...validFields]; fields[index] = value;
      const command = vi.fn(async () => ({ stdout: fields.join("|") + "\n" }));
      expect(await multichunkSetupFailureSuffix("setup", command)).toBe(`; setup_db_snapshot=one; state=${fields[1]}; reason=${fields[2]}; checkpoint=${fields[3]}; attempt=${fields[4]}; lease=${fields[5]}; use=${fields[6]}; opener=${fields[7]}; observation=${fields[8]}`);
      expect(command).toHaveBeenCalledTimes(1);
    });
  }
  it.each([
    ["unknown", "other", "other", "other", "inconsistent", "other", "mismatch", "other"],
    ["succeeded", "none", "p100", "one", "absent", "quiesced", "not_applicable", "present"],
    ["running", "none", "p95", "one", "expired", "opened", "matched", "absent"],
    ["queued", "other", "p0", "zero", "unexpired", "missing", "not_applicable", "present"],
  ])('preserves admitted surprising combinations: %s/%s/%s/%s/%s/%s/%s/%s', async (state, reason, checkpoint, attempt, lease, use, opener, observation) => {
    enable(); const command = vi.fn(async () => ({ stdout: ["one", state, reason, checkpoint, attempt, lease, use, opener, observation].join("|") + "\n" }));
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe(`; setup_db_snapshot=one; state=${state}; reason=${reason}; checkpoint=${checkpoint}; attempt=${attempt}; lease=${lease}; use=${use}; opener=${opener}; observation=${observation}`);
    expect(command).toHaveBeenCalledTimes(1);
  });
  for (const count of ["none", "multiple"]) {
    it.each([1, 2, 3, 4, 5, 6, 7, 8])('suppresses every row field for ' + count + ': %s', async (index) => {
      enable(); const fields = [count, ...Array<string>(8).fill("not_applicable")]; fields[index] = validFields[index];
      const command = vi.fn(async () => ({ stdout: fields.join("|") + "\n" }));
      expect(await multichunkSetupFailureSuffix("setup", command)).toBe(unavailable);
      expect(command).toHaveBeenCalledTimes(1);
    });
  }
  for (const mutation of ["canary", "missing", "duplicate", "uppercase"] as const) {
    it.each([0, 1, 2, 3, 4, 5, 6, 7, 8])('rejects an actual ' + mutation + ' change at field %s', async (index) => {
      enable(); const fields = [...validFields];
      if (mutation === "canary") fields[index] = canary;
      if (mutation === "missing") fields.splice(index, 1);
      if (mutation === "duplicate") fields.splice(index, 0, fields[index]);
      if (mutation === "uppercase") fields[index] = fields[index].toUpperCase();
      const raw = fields.join("|") + "\n";
      expect(raw).not.toBe(validFields.join("|") + "\n");
      const command = vi.fn(async () => ({ stdout: raw }));
      expect(await multichunkSetupFailureSuffix("setup", command)).toBe(unavailable);
      expect(command).toHaveBeenCalledTimes(1);
    });
  }
  it.each([0, 1, 2, 3, 4, 5, 6, 7])('rejects the actual invalid adjacent field shift at %s', async (index) => {
    enable(); const fields = [...validFields];
    [fields[index], fields[index + 1]] = [fields[index + 1], fields[index]];
    const raw = fields.join("|") + "\n";
    expect(raw).not.toBe(validFields.join("|") + "\n");
    const command = vi.fn(async () => ({ stdout: raw }));
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe(unavailable);
    expect(command).toHaveBeenCalledTimes(1);
  });
  it.each(["none|not_applicable|not_applicable\n", "multiple|not_applicable|not_applicable\n", "one|running|none\n"])('rejects an obsolete three-field row without fabricating evidence: %s', async (stdout) => {
    enable(); const command = vi.fn(async () => ({ stdout }));
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe(unavailable);
    expect(command).toHaveBeenCalledTimes(1);
  });
  it.each([[3, "95"], [4, "1"], [5, "true"], [6, "OPENED"], [7, "true"], [8, "1"]] as const)('rejects a raw nonliteral value at field %s', async (index, value) => {
    enable(); const fields = [...validFields]; fields[index] = value;
    const command = vi.fn(async () => ({ stdout: fields.join("|") + "\n" }));
    expect(await multichunkSetupFailureSuffix("setup", command)).toBe(unavailable);
    expect(command).toHaveBeenCalledTimes(1);
  });
});
