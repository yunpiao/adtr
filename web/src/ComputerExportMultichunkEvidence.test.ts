// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { EventEmitter } from "node:events";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Page, Request, Response } from "@playwright/test";
import { observeAction, readSourceEvidence, readExportEvidence, isolatedQuery, sha, sourceCellSummary, type EvidenceQuery } from "../e2e/computer-asset-export-v3-multichunk-evidence";
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
    expect(await readFile(path)).toEqual(bytes); expect(value.segments.map((part) => part.byteCount)).toEqual([524288, 12]); expect(value.file.sha256).toBe(sha(bytes));
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
