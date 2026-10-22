import { afterEach, describe, expect, it, vi } from "vitest";
import { webcrypto } from "node:crypto";
import { exportJSON, exportProfile, exportReceipt, exportRequest, exportTask, readyExport } from "./group-asset-export-v3-fixtures";
import { downloadStatuses, exportColumns, parseExportReceipt, requestFromQuery, groupAssetExportAPI, validExportRequest, isGroupExportCancelProofRejected, type ExportRequest, type GroupExportCancelBinding } from "./group-asset-export-v3-api";
import { ApiError, type Profile } from "./api";
import { computerAssetExportAPI, isComputerExportCancelProofRejected } from "./computer-asset-export-v3-api";
const signal = () => new AbortController().signal;
afterEach(() => vi.unstubAllGlobals());
describe("group asset export immutable parser", () => {
  it("projects every filter and four pins, excludes pagination, preserves selected order", () => {
    const q = { ...exportRequest, creatTm: "desc" as const, search: " Alice ", pageIdx: 9, pageSize: 10 };
    const request = requestFromQuery(q, ["sid", "guid"]);
    expect(request).toEqual({ ...exportRequest, ...Object.fromEntries(Object.entries(q).filter(([key]) => !["pageIdx", "pageSize", "screenColumns"].includes(key))), screenColumns: ["sid", "guid"] });
    expect(request).not.toHaveProperty("pageIdx"); expect(request).not.toHaveProperty("pageSize");
  });
  it("resolves omitted and empty selection to the exact five canonical columns", () => {
    const q = { ...exportRequest, creatTm: undefined, pageIdx: 1, pageSize: 10 };
    expect(exportColumns).toEqual(["guid", "sAMAccountName", "sid", "dn", "whenCreated"]);
    expect(requestFromQuery(q).screenColumns).toEqual(exportColumns);
    expect(requestFromQuery(q, []).screenColumns).toEqual(exportColumns);
  });
  it.each(["sAMAccountName", "startTm", "endTm", "status", "groupType", "pageIdx", "exactSAM", "overviewType"])("rejects unsupported request field %s", (key) => {
    expect(validExportRequest({ ...exportRequest, [key]: "x" })).toBe(false);
  });
  it.each(["mail", "description", "groupType", "displayName"])("rejects unsupported group column %s", (column) => {
    expect(validExportRequest({ ...exportRequest, screenColumns: [column] })).toBe(false);
  });
  it.each(["user_assets.v3.export", "audit.export"])("rejects cross-kind %s", (taskName) => {
    expect(() => parseExportReceipt({ ...exportReceipt, task: { ...exportTask, taskName } }, exportRequest.domainId)).toThrow();
  });
  it.each(["domainId", "expectedRevision", "expectedCredentialRevision", "observationId", "search", "creatTm"])("rejects missing canonical %s", (key) => {
    const request = { ...exportRequest } as Record<string, unknown>; delete request[key];
    expect(validExportRequest(request)).toBe(false);
  });
  it.each([[], ["guid", "guid"], ["displayName"], ["guid", "status"], null])("rejects malformed resolved columns %j", (screenColumns) => {
    expect(validExportRequest({ ...exportRequest, screenColumns })).toBe(false);
  });
  it("rejects unknown request, task and response fields and non-sanitized metadata", () => {
    for (const value of [
      { ...exportReceipt, url: "https://untrusted.invalid" },
      { ...exportReceipt, request: { ...exportRequest, pageIdx: 1 } },
      { ...exportReceipt, task: { ...exportTask, payload: {} } },
      { ...exportReceipt, task: { ...exportTask, result: { digest: "private" } } },
      { ...exportReceipt, task: { ...exportTask, cursor: { rows: [] } } },
    ]) expect(() => parseExportReceipt(value, exportRequest.domainId)).toThrow();
  });
  it.each([
    { taskID: "other" }, { task: { ...exportTask, taskName: "audit.export" } },
    { task: { ...exportTask, domainId: "another-domain" } },
    { request: { ...exportRequest, expectedRevision: "2" } },
    { request: { ...exportRequest, observationId: "other-observation" } },
    { request: { ...exportRequest, screenColumns: [...exportColumns].reverse() } },
  ])("rejects a mismatched binding %j", (patch) => {
    expect(() => parseExportReceipt({ ...exportReceipt, ...patch }, exportRequest.domainId, exportRequest)).toThrow();
  });
  it.each(downloadStatuses.filter((s) => s !== "ready"))("accepts explicit %s without artifact", (downloadStatus) => {
    const state = downloadStatus === "pending" ? "queued" : downloadStatus === "failed" ? "failed" : downloadStatus === "cancelled" ? "cancelled" : "succeeded";
    const value = { ...exportReceipt, task: { ...exportTask, state }, downloadReady: false, downloadStatus };
    expect(parseExportReceipt(value, exportRequest.domainId, exportRequest, "detail")).toEqual(value);
    expect(() => parseExportReceipt({ ...value, artifact: readyExport.artifact }, exportRequest.domainId, exportRequest, "detail")).toThrow();
  });
  it("requires ready/succeeded, bounded exact manifest, and fixed filename", () => {
    expect(parseExportReceipt(readyExport, exportRequest.domainId, exportRequest, "detail")).toEqual(readyExport);
    for (const patch of [{ byteCount: 33554433 }, { rowCount: 10001 }, { rowCount: -1 }, { sha256: "A".repeat(64) }, { filename: "other.xlsx" }, { url: "/other" }])
      expect(() => parseExportReceipt({ ...readyExport, artifact: { ...readyExport.artifact, ...patch } }, exportRequest.domainId, exportRequest, "detail")).toThrow();
    expect(() => parseExportReceipt({ ...readyExport, task: exportTask }, exportRequest.domainId, exportRequest, "detail")).toThrow();
    expect(() => parseExportReceipt({ ...readyExport, downloadStatus: "pending" }, exportRequest.domainId, exportRequest, "detail")).toThrow();
  });
});
function file(body = "abc", headerPatch: Record<string, string> = {}) {
  return new Response(body, { headers: {
    "Content-Type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "Content-Disposition": `attachment; filename="${readyExport.artifact!.filename}"`,
    "Content-Length": "3", "X-ADTR-Artifact-SHA256": readyExport.artifact!.sha256, "X-ADTR-Row-Count": "0",
    "X-ADTR-User-ID": "1", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", ...headerPatch,
  } });
}
describe("group asset export bounded transport", () => {
  it("checks both fresh manifests and digest before returning bytes", async () => {
    vi.stubGlobal("crypto", webcrypto);
    const fetcher = vi.fn().mockResolvedValueOnce(exportJSON(readyExport)).mockResolvedValueOnce(file()).mockResolvedValueOnce(exportJSON(readyExport)); vi.stubGlobal("fetch", fetcher);
    const blob = await groupAssetExportAPI.download(readyExport, 1, signal());
    expect(blob.size).toBe(3); expect(fetcher).toHaveBeenCalledTimes(3);
  });
  it.each([
    ["Content-Type", "text/html"], ["Content-Length", "33554433"], ["Content-Length", "03"],
    ["Content-Disposition", 'attachment; filename="foreign.xlsx"'], ["X-ADTR-Artifact-SHA256", "0".repeat(64)],
    ["X-ADTR-Row-Count", "1"], ["X-ADTR-User-ID", "2"], ["Cache-Control", "public"],
  ])("rejects download header %s", async (name, value) => {
    const fetcher = vi.fn().mockResolvedValueOnce(exportJSON(readyExport)).mockResolvedValueOnce(file("abc", { [name]: value })); vi.stubGlobal("fetch", fetcher);
    await expect(groupAssetExportAPI.download(readyExport, 1, signal())).rejects.toThrow(); expect(fetcher).toHaveBeenCalledTimes(2);
  });
  it.each(["abd", "abcd", "ab"])("rejects corrupted or wrong length body %s", async (body) => {
    vi.stubGlobal("crypto", webcrypto); vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(exportJSON(readyExport)).mockResolvedValueOnce(file(body)));
    await expect(groupAssetExportAPI.download(readyExport, 1, signal())).rejects.toThrow();
  });
  it("discards verified bytes on authority loss after download", async () => {
    vi.stubGlobal("crypto", webcrypto); vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(exportJSON(readyExport)).mockResolvedValueOnce(file()).mockResolvedValueOnce(exportJSON({ error: "forbidden" }, 403)));
    await expect(groupAssetExportAPI.download(readyExport, 1, signal())).rejects.toMatchObject({ code: "forbidden" });
  });
  it("rejects cross-actor receipts and bounds metadata before parsing", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(exportJSON(exportReceipt, 200, 2)).mockResolvedValueOnce(new Response(" ".repeat(65537), { headers: { "X-ADTR-User-ID": "1", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff" } })));
    await expect(groupAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toMatchObject({ status: 401 });
    await expect(groupAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toThrow();
  });
});

describe("malformed export JSON and lifecycle combinations", () => {
  it("rejects duplicate response keys instead of accepting the last value", async () => {
    const body = JSON.stringify(exportReceipt).replace('"taskID":', '"taskID":"ignored","taskID":');
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(body, { headers: { "Content-Type": "application/json", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-ADTR-User-ID": "1" } })));
    await expect(groupAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toThrow();
  });
  it.each(["ready", "failed", "cancelled", "source_changed", "source_unavailable", "artifact_invalid"])("rejects queued task claiming %s", (downloadStatus) => {
    expect(() => parseExportReceipt({ ...exportReceipt, downloadReady: downloadStatus === "ready", downloadStatus, ...(downloadStatus === "ready" ? { artifact: readyExport.artifact } : {}) }, exportRequest.domainId, exportRequest, "detail")).toThrow();
  });
  it("does not require private metadata on receipt after source revisions change", () => {
    expect(parseExportReceipt(exportReceipt, exportRequest.domainId)).toEqual(exportReceipt);
  });
  it("rejects oversized chunked XLSX even when manifest and headers claim a bounded size", async () => {
    const headers = file().headers;
    const chunks = new ReadableStream<Uint8Array>({ start(controller) { controller.enqueue(new Uint8Array(32 * 1024 * 1024)); controller.enqueue(new Uint8Array(1)); controller.close(); } });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(exportJSON(readyExport)).mockResolvedValueOnce(new Response(chunks, { headers })));
    await expect(groupAssetExportAPI.download(readyExport, 1, signal())).rejects.toThrow();
  });
});

describe("response body connection failure", () => {
  it("normalizes a receipt stream reset after headers as network", async () => {
    const stream = new ReadableStream<Uint8Array>({ start(controller) { controller.enqueue(new TextEncoder().encode('{"taskID":')); controller.error(new TypeError("socket reset")); } });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(stream, { headers: { "Content-Type": "application/json", "X-ADTR-User-ID": "1", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff" } })));
    await expect(groupAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toMatchObject({ code: "network" });
  });
});

describe("authority status survives invalid error pages", () => {
  it.each([401, 403])("preserves status %i for empty, HTML, and oversized bodies", async (status) => {
    for (const body of ["", "<html>proxy error</html>", "x".repeat(65537)]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(body, { status })));
      await expect(groupAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toMatchObject({ status, code: status === 401 ? "unauthenticated" : "forbidden" });
    }
  });
});

// Observe the reader methods, not merely the underlying source.cancel callback:
// a native reader can accept cancel() after EOF without calling source.cancel.
function observeReader(response: Response) {
  const stream = response.body!;
  const reader = stream.getReader();
  const cancel = vi.spyOn(reader, "cancel");
  const release = vi.spyOn(reader, "releaseLock");
  const acquire = vi.fn(() => reader);
  Object.defineProperty(stream, "getReader", { configurable: true, value: acquire });
  return { response, cancel, release, acquire };
}
describe("bounded reader completion proof", () => {
  it("releases a fully consumed JSON body once without cancelling after EOF", async () => {
    const tracked = observeReader(exportJSON(exportReceipt));
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(tracked.response));
    await expect(groupAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).resolves.toEqual(exportReceipt);
    expect(tracked.acquire).toHaveBeenCalledTimes(1);
    expect(tracked.cancel).not.toHaveBeenCalled();
    expect(tracked.release).toHaveBeenCalledTimes(1);
    expect(tracked.response.body!.locked).toBe(false);
  });
  it("keeps both manifest reads and the verified XLSX EOF cancel-free", async () => {
    vi.stubGlobal("crypto", webcrypto);
    const tracked = [observeReader(exportJSON(readyExport)), observeReader(file()), observeReader(exportJSON(readyExport))];
    const fetcher = vi.fn();
    for (const item of tracked) fetcher.mockResolvedValueOnce(item.response);
    vi.stubGlobal("fetch", fetcher);
    await expect(groupAssetExportAPI.download(readyExport, 1, signal())).resolves.toHaveProperty("size", 3);
    for (const item of tracked) {
      expect(item.acquire).toHaveBeenCalledTimes(1);
      expect(item.cancel).not.toHaveBeenCalled();
      expect(item.release).toHaveBeenCalledTimes(1);
    }
  });
  it("cancels and releases once on a read rejection without claiming completion", async () => {
    const stream = new ReadableStream<Uint8Array>({ pull(controller) { controller.error(new TypeError("connection interrupted")); } });
    const tracked = observeReader(new Response(stream, { headers: exportJSON(exportReceipt).headers }));
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(tracked.response));
    await expect(groupAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toMatchObject({ code: "network" });
    expect(tracked.cancel).toHaveBeenCalledTimes(1);
    expect(tracked.release).toHaveBeenCalledTimes(1);
  });
  it("deduplicates abort-listener and finally cancellation while a read is pending", async () => {
    const tracked = observeReader(new Response(new ReadableStream<Uint8Array>(), { headers: exportJSON(exportReceipt).headers }));
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(tracked.response));
    const control = new AbortController();
    const pending = groupAssetExportAPI.receipt(exportRequest.domainId, "key", 1, control.signal);
    await vi.waitFor(() => expect(tracked.acquire).toHaveBeenCalledTimes(1));
    control.abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(tracked.cancel).toHaveBeenCalledTimes(1);
    expect(tracked.release).toHaveBeenCalledTimes(1);
  });
  it.each(["oversized", "length mismatch"])("does not treat %s as a completed read", async (reason) => {
    const headers = exportJSON(exportReceipt).headers;
    const body = reason === "oversized" ? "x".repeat(65537) : JSON.stringify(exportReceipt);
    if (reason === "length mismatch") headers.set("Content-Length", "1");
    const tracked = observeReader(new Response(body, { headers }));
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(tracked.response));
    await expect(groupAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toMatchObject({ code: "invalid_response" });
    expect(tracked.cancel).toHaveBeenCalledTimes(1);
    expect(tracked.release).toHaveBeenCalledTimes(1);
  });
});

describe("task-ID-only original export read", () => {
  it("uses the protected detail GET without a recovery key and accepts canonical original request", async () => {
    const fetcher = vi.fn().mockResolvedValue(exportJSON(readyExport)); vi.stubGlobal("fetch", fetcher);
    expect(await groupAssetExportAPI.detailByTask(readyExport.taskID, exportRequest.domainId, 1, signal())).toEqual(readyExport);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0][0]).toBe(`/api/group-assets/v3/exports/detail?taskID=${readyExport.taskID}`);
    expect(fetcher.mock.calls[0][1]).toMatchObject({ method: "GET", cache: "no-store", redirect: "error", credentials: "same-origin" });
    expect(fetcher.mock.calls[0][1]).not.toHaveProperty("body");
  });
  it.each([
    { taskID: "other-task" },
    { request: { ...exportRequest, startTm: "" } },
    { request: { ...exportRequest, endTm: "" } },
    { task: { ...readyExport.task, taskUUID: "other-task" } },
    { task: { ...readyExport.task, taskName: "user_assets.v3.export" } },
    { task: { ...readyExport.task, taskName: "computer_assets.v3.export" } },
    { task: { ...readyExport.task, domainId: "other-domain" } },
    { request: { ...exportRequest, domainId: "other-domain" } },
    { request: { ...exportRequest, observationId: "" } },
    { artifact: { ...readyExport.artifact, filename: "foreign.xlsx" } },
    { task: { ...readyExport.task, result: { privatePayload: "not allowed" } } },
  ])("rejects unbound or invalid task metadata %j", async (patch) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(exportJSON({ ...readyExport, ...patch })));
    await expect(groupAssetExportAPI.detailByTask(readyExport.taskID, exportRequest.domainId, 1, signal())).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("rejects another actor even on the first original-task lookup", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(exportJSON(readyExport, 200, 2)));
    await expect(groupAssetExportAPI.detailByTask(readyExport.taskID, exportRequest.domainId, 1, signal())).rejects.toMatchObject({ status: 401 });
  });
  it("rejects bad lookup input without transport", async () => {
    vi.stubGlobal("fetch", vi.fn());
    await expect(groupAssetExportAPI.detailByTask("../other", exportRequest.domainId, 1, signal())).rejects.toMatchObject({ code: "invalid_input" });
    await expect(groupAssetExportAPI.detailByTask(readyExport.taskID, "", 1, signal())).rejects.toMatchObject({ code: "invalid_input" });
    expect(fetch).not.toHaveBeenCalled();
  });
});

const cancelProof = { actorPassword: "synthetic-proof-only", totpCode: "123456" };
const copyRequest = (): ExportRequest => ({ ...exportRequest, screenColumns: [...exportRequest.screenColumns] });
const cancelBinding = (): GroupExportCancelBinding => ({ taskID: exportTask.taskUUID, request: copyRequest(), actor: { ...exportProfile }, signal: signal() });
function proofResponse(body: BodyInit | null = '{"error":"invalid_credentials"}', headerPatch: Record<string, string> = {}, status = 401) {
  return new Response(body, { status, headers: { "Content-Type": "application/json", "Cache-Control": "no-store",
    "X-Content-Type-Options": "nosniff", ...headerPatch } });
}
async function rejectedCancel(binding: GroupExportCancelBinding): Promise<unknown> {
  try { await groupAssetExportAPI.cancel(binding.taskID, binding.request, cancelProof, binding.actor.ID, binding.actor.csrfToken, binding.signal, binding.actor); }
  catch (error) { return error; }
  throw new Error("Expected cancellation rejection");
}
describe("exact current-request cancel proof rejection", () => {
  it.each([undefined, "1"])("accepts the actual error without requiring an actor header (%s)", async (actor) => {
    const binding = cancelBinding();
    const fetcher = vi.fn().mockResolvedValue(proofResponse(undefined, actor === undefined ? {} : { "X-ADTR-User-ID": actor }));
    vi.stubGlobal("fetch", fetcher);
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ code: "invalid_credentials", status: 401 });
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(true);
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0][0]).toBe("/api/group-assets/v3/exports/cancel");
    expect(fetcher.mock.calls[0][1]).toMatchObject({ method: "POST", credentials: "same-origin", cache: "no-store", redirect: "error", signal: binding.signal });
  });
  it("binds the immutable full request, actor, exact task and owned signal", async () => {
    const binding = cancelBinding(); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(proofResponse()));
    const error = await rejectedCancel(binding);
    for (const wrong of [{ ...binding, taskID: "another-task" }, { ...binding, actor: { ...binding.actor, ID: 2 } }, { ...binding, signal: signal() },
      ...Object.entries({ domainId: "another-domain", expectedRevision: "2", expectedCredentialRevision: "3", observationId: "another-observation",
        search: "other", creatTm: "asc",
        screenColumns: [...exportColumns].reverse() }).map(([key, value]) => ({ ...binding, request: { ...binding.request, [key]: value } }))]) {
      expect(isGroupExportCancelProofRejected(error, wrong)).toBe(false);
    }
    binding.request.search = "changed after response";
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
    expect(isGroupExportCancelProofRejected(error, { ...binding, request: copyRequest() })).toBe(true);
    expect(error).not.toHaveProperty("request"); expect(error).not.toHaveProperty("binding");
    expect(error).not.toHaveProperty("body"); expect(error).not.toHaveProperty("headers");
    expect(error).not.toHaveProperty("actorPassword"); expect(error).not.toHaveProperty("totpCode");
  });
  it("cannot brand a fabricated error or a similar response from another operation", async () => {
    const binding = cancelBinding();
    expect(isGroupExportCancelProofRejected(new ApiError("invalid_credentials", 401), binding)).toBe(false);
    expect(isGroupExportCancelProofRejected({ code: "invalid_credentials", status: 401 }, binding)).toBe(false);
    for (const operation of [
      () => groupAssetExportAPI.create(binding.request, "original-key", cancelProof, 1, exportProfile.csrfToken, binding.signal),
      () => groupAssetExportAPI.receipt(binding.request.domainId, "original-key", 1, binding.signal),
      () => groupAssetExportAPI.detail(binding.taskID, binding.request, 1, binding.signal),
    ]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(proofResponse()));
      const error: unknown = await operation().catch((reason: unknown) => reason);
      expect(error).toMatchObject({ code: "unauthenticated", status: 401 });
      expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
    }
  });
  it("never trusts another export API's genuine rejection or a copied error identity", async () => {
    const binding = cancelBinding();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(proofResponse()));
    const foreign = await computerAssetExportAPI.cancel(binding.taskID, { ...binding.request, startTm: "", endTm: "" },
      cancelProof, binding.actor.ID, binding.actor.csrfToken, binding.signal).catch((error: unknown) => error);
    expect(foreign).toMatchObject({ code: "invalid_credentials", status: 401 });
    expect(isGroupExportCancelProofRejected(foreign, binding)).toBe(false);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(proofResponse()));
    const own = await rejectedCancel(binding);
    expect(isGroupExportCancelProofRejected(Object.assign(new ApiError("invalid_credentials", 401), own), binding)).toBe(false);
    expect(isGroupExportCancelProofRejected(Object.create(own as object), binding)).toBe(false);
    expect(isComputerExportCancelProofRejected(own, { ...binding, actor: binding.actor.ID, request: { ...binding.request, startTm: "", endTm: "" } })).toBe(false);
    expect(isGroupExportCancelProofRejected(own, binding)).toBe(true);
    expect(isGroupExportCancelProofRejected(own, binding)).toBe(false);
  });
  it.each(["Content-Type", "Cache-Control", "X-Content-Type-Options"])("requires each error metadata header: %s", async (name) => {
    const binding = cancelBinding(), response = proofResponse(); response.headers.delete(name);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ code: "unauthenticated", status: 401 });
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
  });
  it.each([
    ["wrong actor", () => proofResponse(undefined, { "X-ADTR-User-ID": "2" })],
    ["empty actor", () => proofResponse(undefined, { "X-ADTR-User-ID": "" })],
    ["wrong MIME", () => proofResponse(undefined, { "Content-Type": "text/html" })],
    ["missing metadata", () => new Response('{"error":"invalid_credentials"}', { status: 401 })],
    ["wrong cache", () => proofResponse(undefined, { "Cache-Control": "public" })],
    ["combined cache", () => proofResponse(undefined, { "Cache-Control": "no-store, private" })],
    ["wrong nosniff", () => proofResponse(undefined, { "X-Content-Type-Options": "other" })],
    ["absent body", () => proofResponse(null)],
    ["empty body", () => proofResponse("")],
    ["wrong error", () => proofResponse('{"error":"unauthenticated"}')],
    ["extra field", () => proofResponse('{"error":"invalid_credentials","result":true}')],
    ["duplicate key", () => proofResponse('{"error":"invalid_credentials","error":"invalid_credentials"}')],
    ["escaped duplicate", () => proofResponse('{"error":"invalid_credentials","\\u0065rror":"invalid_credentials"}')],
    ["array", () => proofResponse('[{"error":"invalid_credentials"}]')],
    ["truncated JSON", () => proofResponse('{"error":"invalid_credentials"')],
    ["trailing JSON", () => proofResponse('{"error":"invalid_credentials"}{}')],
    ["invalid UTF-8", () => proofResponse(new Uint8Array([0xc3, 0x28]))],
    ["oversize body", () => proofResponse(" ".repeat(4097))],
    ["oversize length", () => proofResponse(undefined, { "Content-Length": "4097" })],
    ["noncanonical length", () => proofResponse(undefined, { "Content-Length": "030" })],
    ["length mismatch", () => proofResponse(undefined, { "Content-Length": "1" })],
    ["read failure", () => proofResponse(new ReadableStream<Uint8Array>({ pull(controller) { controller.error(new TypeError("synthetic connection failure")); } }))],
  ] as const)("keeps %s as an unbranded authority failure", async (_label, response) => {
    const binding = cancelBinding(); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response()));
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ code: "unauthenticated", status: 401 });
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
  });
  it("requires complete bytes at the inclusive 4096-byte boundary", async () => {
    const raw = '{"error":"invalid_credentials"}', body = raw + " ".repeat(4096 - raw.length);
    const tracked = observeReader(proofResponse(body, { "Content-Length": "4096", "Content-Type": "application/json; charset=utf-8" }));
    const binding = cancelBinding(); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(tracked.response));
    expect(isGroupExportCancelProofRejected(await rejectedCancel(binding), binding)).toBe(true);
    expect(tracked.release).toHaveBeenCalledTimes(1); expect(tracked.cancel).not.toHaveBeenCalled();
  });
  it.each([400, 403, 404, 409, 429, 500])("never brands invalid_credentials with status %i", async (status) => {
    const binding = cancelBinding(); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(proofResponse(undefined, {}, status)));
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ status }); expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
  });
  it("never applies a late proof rejection after abort, including a transport ignoring abort", async () => {
    const control = new AbortController(), binding = { ...cancelBinding(), signal: control.signal };
    vi.stubGlobal("fetch", vi.fn().mockImplementation(async () => { control.abort(); return proofResponse(); }));
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ name: "AbortError" }); expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
  });
  it("aborts a pending proof-error stream without branding it", async () => {
    const tracked = observeReader(proofResponse(new ReadableStream<Uint8Array>()));
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(tracked.response));
    const control = new AbortController(), binding = { ...cancelBinding(), signal: control.signal };
    const pending = rejectedCancel(binding);
    await vi.waitFor(() => expect(tracked.acquire).toHaveBeenCalledTimes(1)); control.abort();
    const error = await pending;
    expect(error).toMatchObject({ name: "AbortError" }); expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
    expect(tracked.release).toHaveBeenCalledTimes(1);
  });
  it("revokes an otherwise valid unconsumed rejection when its invocation aborts", async () => {
    const control = new AbortController(), binding = { ...cancelBinding(), signal: control.signal };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(proofResponse()));
    const error = await rejectedCancel(binding); control.abort();
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
    expect(isGroupExportCancelProofRejected(error, { ...binding, signal: signal() })).toBe(false);
  });
  it("retains status 401 even if discarding malformed error bytes fails", async () => {
    const binding = cancelBinding(), response = proofResponse(undefined, { "Content-Type": "text/html" });
    const cancel = vi.spyOn(response.body!, "cancel").mockRejectedValue(new TypeError("synthetic cleanup failure"));
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ code: "unauthenticated", status: 401 });
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
  });
  it("does not turn a fetch rejection into proof rejection or retry cancellation", async () => {
    const binding = cancelBinding(), fetcher = vi.fn().mockRejectedValue(new TypeError("synthetic disconnect"));
    vi.stubGlobal("fetch", fetcher);
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ code: "network" });
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it("rejects an in-flight original request mutation before applying a successful cancel result", async () => {
    const binding = cancelBinding();
    const fetcher = vi.fn().mockImplementation(async () => {
      binding.request.screenColumns.reverse();
      return exportJSON({ taskID: binding.taskID, task: { ...exportTask, state: "cancel_requested" } });
    }); vi.stubGlobal("fetch", fetcher);
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ code: "invalid_response" });
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it("preserves the existing successful Task result and exact dedicated request", async () => {
    const task = { ...exportTask, state: "cancel_requested" }, binding = cancelBinding();
    const fetcher = vi.fn().mockResolvedValue(exportJSON({ taskID: binding.taskID, task })); vi.stubGlobal("fetch", fetcher);
    expect(await groupAssetExportAPI.cancel(binding.taskID, binding.request, cancelProof, 1, exportProfile.csrfToken, binding.signal)).toEqual(task);
    const submitted = JSON.parse(fetcher.mock.calls[0][1].body) as Record<string, unknown>;
    expect(Object.keys(submitted).sort()).toEqual(["actorPassword", "taskID", "totpCode"]);
    expect(submitted.taskID).toBe(binding.taskID);
    expect(submitted.actorPassword === cancelProof.actorPassword && submitted.totpCode === cancelProof.totpCode).toBe(true);
  });
});

const cancelMenu = () => ({ menu: ["domains", "directory_assets", "tasks", "group_asset_exports"].map((mark) =>
  ({ mark, auth: { readable: true, writeable: mark === "tasks" || mark === "group_asset_exports" } })) });
const cancelDetail = () => ({ ...exportReceipt, downloadReady: false, downloadStatus: "pending" });
function authorityJSON(value: unknown) {
  return new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff" } });
}
function recheckResponses() {
  return [authorityJSON(exportProfile), authorityJSON(cancelMenu()), authorityJSON({ results: [true, true] }),
    exportJSON(cancelDetail()), authorityJSON(exportProfile)];
}
function installRecheck(responses = recheckResponses()) {
  const fetcher = vi.fn(); for (const response of responses) fetcher.mockResolvedValueOnce(response);
  vi.stubGlobal("fetch", fetcher); return fetcher;
}
describe("cancel-specific current authority recheck", () => {
  it("checks both session boundaries and only the two existing dedicated paths", async () => {
    const owned = signal(), fetcher = installRecheck();
    expect(await groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, owned)).toEqual(cancelDetail());
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual(["/api/auth/me", "/api/access/menu", "/api/access/check",
      `/api/group-assets/v3/exports/detail?taskID=${exportTask.taskUUID}`, "/api/auth/me"]);
    for (const [path, init] of fetcher.mock.calls) {
      expect(init).toMatchObject({ credentials: "same-origin", cache: "no-store", redirect: "error", signal: owned });
      expect(init.method).toBe(path === "/api/access/check" ? "POST" : "GET");
      if (path !== "/api/access/check") expect(init).not.toHaveProperty("body");
    }
    expect(JSON.parse(fetcher.mock.calls[2][1].body)).toEqual({ paths: ["GET /api/group-assets/v3/exports/detail", "POST /api/group-assets/v3/exports/cancel"] });
    expect(Object.keys(fetcher.mock.calls[2][1].headers).sort()).toEqual(["Content-Type", "X-CSRF-Token"]);
    expect(fetcher.mock.calls[2][1].headers["Content-Type"]).toBe("application/json");
    expect(fetcher.mock.calls[2][1].headers["X-CSRF-Token"] === exportProfile.csrfToken).toBe(true);
  });
  it.each(["ID", "username", "role", "priv", "csrfToken", "hasMfa", "needChangePwd", "isExpired"] as const)("rejects changed or malformed %s at either session boundary", async (key) => {
    const values: Record<string, unknown> = { ID: 2, username: "another-actor", role: "other-role", priv: 1,
      csrfToken: "another-session", hasMfa: false, needChangePwd: true, isExpired: true };
    for (const index of [0, 4]) {
      const responses = recheckResponses(); responses[index] = authorityJSON({ ...exportProfile, [key]: values[key] });
      const fetcher = installRecheck(responses);
      await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).rejects.toMatchObject({ status: 401 });
      expect(fetcher).toHaveBeenCalledTimes(index + 1);
    }
  });
  it.each([
    ["non-array menu", () => ({ menu: {} })],
    ["extra top-level field", () => ({ ...cancelMenu(), other: true })],
    ["duplicate required mark", () => ({ menu: [...cancelMenu().menu, cancelMenu().menu[0]] })],
    ["missing required mark", () => ({ menu: cancelMenu().menu.slice(1) })],
    ["unreadable mark", () => ({ menu: cancelMenu().menu.map((node) => ({ ...node, auth: { ...node.auth, readable: false } })) })],
    ["nonwriteable tasks", () => ({ menu: cancelMenu().menu.map((node) => node.mark === "tasks" ? { ...node, auth: { readable: true, writeable: false } } : node) })],
    ["nonwriteable exporter", () => ({ menu: cancelMenu().menu.map((node) => node.mark === "group_asset_exports" ? { ...node, auth: { readable: true, writeable: false } } : node) })],
    ["malformed auth", () => ({ menu: [{ mark: "domains", auth: { readable: "true", writeable: false } }, ...cancelMenu().menu.slice(1)] })],
    ["malformed writeable", () => ({ menu: cancelMenu().menu.map((node) => ({ ...node, auth: { ...node.auth, writeable: 1 } })) })],
    ["missing boolean", () => ({ menu: cancelMenu().menu.map((node) => ({ ...node, auth: { readable: true } })) })],
    ["foreign exporter grant", () => ({ menu: cancelMenu().menu.map((node) => node.mark === "group_asset_exports" ? { ...node, mark: "user_asset_exports" } : node) })],
    ["unknown grant field", () => ({ menu: cancelMenu().menu.map((node) => ({ ...node, auth: { ...node.auth, other: true } })) })],
  ] as const)("rejects %s before checking or reading the task", async (_label, menu) => {
    const responses = recheckResponses(); responses[1] = authorityJSON(menu()); const fetcher = installRecheck(responses);
    await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).rejects.toThrow();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
  it.each([{ results: [true, false] }, { results: [false, true] }, { results: [true] }, { results: [true, true, true] },
    { results: [true, "true"] }, { results: null }, { results: [true, true], other: true }])("rejects non-exact/all-true checks %j", async (value) => {
    const responses = recheckResponses(); responses[2] = authorityJSON(value); const fetcher = installRecheck(responses);
    await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).rejects.toThrow();
    expect(fetcher).toHaveBeenCalledTimes(3);
  });
  it.each([
    { taskID: "another-task" }, { task: { ...exportTask, taskName: "computer_assets.v3.export" } },
    { task: { ...exportTask, domainId: "another-domain" } }, { request: { ...exportRequest, expectedRevision: "2" } },
    { request: { ...exportRequest, expectedCredentialRevision: "3" } }, { request: { ...exportRequest, observationId: "another-observation" } },
    { request: { ...exportRequest, search: "other" } }, { request: { ...exportRequest, screenColumns: [...exportColumns].reverse() } },
  ])("binds the original detail and all source pins %j", async (patch) => {
    const responses = recheckResponses(); responses[3] = exportJSON({ ...cancelDetail(), ...patch }); const fetcher = installRecheck(responses);
    await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).rejects.toMatchObject({ code: "invalid_response" });
    expect(fetcher).toHaveBeenCalledTimes(4);
  });
  it("requires the dedicated actor header while auth/access omit it", async () => {
    const responses = recheckResponses(); responses[3] = authorityJSON(cancelDetail()); installRecheck(responses);
    await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).rejects.toMatchObject({ status: 401 });
  });
  it.each([0, 1, 2, 4])("accepts a matching optional actor header and rejects a conflicting one at step %i", async (index) => {
    const matching = recheckResponses(); matching[index].headers.set("X-ADTR-User-ID", "1");
    installRecheck(matching);
    await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).resolves.toEqual(cancelDetail());
    for (const actor of ["2", "", "01"]) {
      const responses = recheckResponses(); responses[index].headers.set("X-ADTR-User-ID", actor);
      const fetcher = installRecheck(responses);
      await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).rejects.toMatchObject({ status: 401 });
      expect(fetcher).toHaveBeenCalledTimes(index + 1);
    }
  });
  it.each([0, 1, 2, 3, 4])("keeps unparseable authority failures at step %i authoritative and unbranded", async (index) => {
    for (const status of [401, 403]) {
      const responses = recheckResponses(); responses[index] = new Response("x".repeat(70000), { status });
      const fetcher = installRecheck(responses), binding = cancelBinding();
      const error: unknown = await groupAssetExportAPI.recheckCancelAuthority(binding.taskID, binding.request, exportProfile, binding.signal).catch((reason: unknown) => reason);
      expect(error).toMatchObject({ status });
      expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
      expect(fetcher).toHaveBeenCalledTimes(index + 1);
    }
  });
  it.each([0, 1, 2, 4])("bounds unique complete JSON and metadata at authority step %i", async (index) => {
    for (const bad of [
      () => new Response(" ".repeat(65537), { headers: authorityJSON({}).headers }),
      () => new Response('{"menu":[],"menu":[]}', { headers: authorityJSON({}).headers }),
      () => new Response(new Uint8Array([0xc3, 0x28]), { headers: authorityJSON({}).headers }),
      () => new Response("{}", { headers: { ...Object.fromEntries(authorityJSON({}).headers), "Content-Length": "3" } }),
      () => new Response("{}", { headers: { "Content-Type": "application/json" } }),
      () => new Response("{}", { headers: { ...Object.fromEntries(authorityJSON({}).headers), "Content-Type": "text/html" } }),
      () => new Response("{}", { status: 201, headers: authorityJSON({}).headers }),
    ]) {
      const responses = recheckResponses(); responses[index] = bad(); const fetcher = installRecheck(responses);
      await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).rejects.toThrow();
      expect(fetcher).toHaveBeenCalledTimes(index + 1);
    }
  });
  it.each([0, 1, 2, 3, 4])("stops after an abort at step %i even when fetch resolves late", async (index) => {
    const control = new AbortController(), responses = recheckResponses(); let count = 0;
    const fetcher = vi.fn().mockImplementation(async () => { const current = count++; if (current === index) control.abort(); return responses[current]; });
    vi.stubGlobal("fetch", fetcher);
    await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, control.signal)).rejects.toMatchObject({ name: "AbortError" });
    expect(fetcher).toHaveBeenCalledTimes(index + 1);
  });
  it("rejects in-place identity or original-request changes without continuing", async () => {
    for (const field of ["profile", "request"]) {
      const profile: Profile = { ...exportProfile }, request = copyRequest();
      const fetcher = vi.fn().mockImplementation(async () => {
        if (field === "profile") profile.csrfToken = "changed-session"; else request.screenColumns.reverse();
        return authorityJSON(exportProfile);
      }); vi.stubGlobal("fetch", fetcher);
      await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, request, profile, signal())).rejects.toThrow();
      expect(fetcher).toHaveBeenCalledTimes(1);
    }
  });
  it.each([0, 1, 2, 3, 4])("rechecks the captured identity and complete request after await %i", async (index) => {
    for (const field of ["profile", "request"]) {
      const profile: Profile = { ...exportProfile }, request = copyRequest(), responses = recheckResponses(); let count = 0;
      const fetcher = vi.fn().mockImplementation(async () => {
        const step = count++;
        if (step === index) {
          if (field === "profile") profile.csrfToken = "changed-session"; else request.screenColumns.reverse();
        }
        return responses[step];
      }); vi.stubGlobal("fetch", fetcher);
      await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, request, profile, signal())).rejects.toThrow();
      expect(fetcher).toHaveBeenCalledTimes(index + 1);
    }
  });
  it("pins exactly seven valid group fields, preserving spaces and column order", async () => {
    const original: ExportRequest = { ...copyRequest(), search: " Group ", creatTm: "asc" };
    const changes: Partial<ExportRequest>[] = [
      { domainId: "another-domain" }, { expectedRevision: "2" }, { expectedCredentialRevision: "3" },
      { observationId: "another-observation" }, { search: "Group" }, { creatTm: "desc" }, { screenColumns: [...exportColumns].reverse() },
    ];
    for (const patch of changes) {
      const request = { ...original, ...patch }; expect(validExportRequest(request)).toBe(true);
      const responses = recheckResponses(); responses[3] = exportJSON({ ...cancelDetail(), request,
        task: { ...exportTask, domainId: request.domainId } });
      const fetcher = installRecheck(responses);
      await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, original, exportProfile, signal())).rejects.toMatchObject({ code: "invalid_response" });
      expect(fetcher).toHaveBeenCalledTimes(4);
    }
  });
  it.each(["queued", "running", "retry_wait", "cancel_requested", "cancelled", "failed", "succeeded"] as const)("reports %s without posting or interpreting it as cancellation success", async (state) => {
    const detail = state === "succeeded" ? readyExport : { ...cancelDetail(), task: { ...exportTask, state },
      downloadStatus: state === "cancelled" ? "cancelled" : state === "failed" ? "failed" : "pending" };
    const responses = recheckResponses(); responses[3] = exportJSON(detail);
    const fetcher = installRecheck(responses);
    expect(await groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).toEqual(detail);
    expect(fetcher.mock.calls.filter(([, init]) => init.method === "POST").map(([path]) => path)).toEqual(["/api/access/check"]);
  });
  it("rejects expired, forced-change or missing-MFA captured profiles without fetching", async () => {
    const fetcher = vi.fn(); vi.stubGlobal("fetch", fetcher);
    for (const patch of [{ hasMfa: false }, { needChangePwd: true }, { isExpired: true }])
      await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), { ...exportProfile, ...patch }, signal())).rejects.toMatchObject({ status: 401 });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it("rejects malformed captured inputs and pre-aborted invocations before any fetch", async () => {
    const fetcher = vi.fn(); vi.stubGlobal("fetch", fetcher);
    for (const patch of [{ ID: 0 }, { ID: Number.MAX_SAFE_INTEGER + 1 }, { username: "" }, { role: "" }, { csrfToken: "" }, { priv: 0.5 }])
      await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), { ...exportProfile, ...patch }, signal())).rejects.toMatchObject({ status: 401 });
    await expect(groupAssetExportAPI.recheckCancelAuthority("../task", copyRequest(), exportProfile, signal())).rejects.toMatchObject({ code: "invalid_input" });
    await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, { ...copyRequest(), screenColumns: [] }, exportProfile, signal())).rejects.toMatchObject({ code: "invalid_input" });
    const control = new AbortController(); control.abort();
    await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, control.signal)).rejects.toMatchObject({ name: "AbortError" });
    expect(fetcher).not.toHaveBeenCalled();
  });
});

const authorityChanges: Partial<Profile>[] = [
  { ID: 2 }, { username: "another-actor" }, { role: "another-role" }, { priv: 1 },
  { csrfToken: "another-session" }, { hasMfa: false }, { needChangePwd: true }, { isExpired: true },
];
const requestChanges: Partial<ExportRequest>[] = [
  { domainId: "another-domain" }, { expectedRevision: "2" }, { expectedCredentialRevision: "3" },
  { observationId: "another-observation" }, { search: "Group" }, { creatTm: "desc" },
  { screenColumns: [...exportColumns].reverse() },
];
describe("cancel invocation authority value binding", () => {
  it("keeps old six-argument 401 handling unbranded even with a perfect proof-error body", async () => {
    const binding = cancelBinding(); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(proofResponse()));
    const error: unknown = await groupAssetExportAPI.cancel(binding.taskID, binding.request, cancelProof,
      binding.actor.ID, binding.actor.csrfToken, binding.signal).catch((reason: unknown) => reason);
    expect(error).toMatchObject({ code: "unauthenticated", status: 401 });
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
  });
  it.each(authorityChanges)("binds every captured authority field at consumption: %j", async (patch) => {
    const binding = cancelBinding(); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(proofResponse()));
    const error = await rejectedCancel(binding);
    expect(isGroupExportCancelProofRejected(error, { ...binding, actor: { ...binding.actor, ...patch } })).toBe(false);
    expect(isGroupExportCancelProofRejected(error, { ...binding, actor: { ...binding.actor } })).toBe(true);
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
  });
  it.each(authorityChanges)("does not brand an in-flight authority-field change: %j", async (patch) => {
    const binding = cancelBinding(), initial = { ...binding.actor };
    const fetcher = vi.fn().mockImplementation(async () => { Object.assign(binding.actor, patch); return proofResponse(); });
    vi.stubGlobal("fetch", fetcher);
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ code: "unauthenticated", status: 401 });
    expect(isGroupExportCancelProofRejected(error, { ...binding, actor: initial })).toBe(false);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it.each(requestChanges)("does not brand an in-flight seven-field request change: %j", async (patch) => {
    const binding = cancelBinding(), original = copyRequest();
    vi.stubGlobal("fetch", vi.fn().mockImplementation(async () => { Object.assign(binding.request, patch); return proofResponse(); }));
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ code: "unauthenticated", status: 401 });
    expect(isGroupExportCancelProofRejected(error, { ...binding, request: original })).toBe(false);
  });
  it("checks the captured profile again after reading the complete proof-error stream", async () => {
    const binding = cancelBinding(), initial = { ...binding.actor };
    const body = new ReadableStream<Uint8Array>({ pull(controller) {
      binding.actor.csrfToken = "changed-during-body";
      controller.enqueue(new TextEncoder().encode('{"error":"invalid_credentials"}')); controller.close();
    } });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(proofResponse(body)));
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ code: "unauthenticated", status: 401 });
    expect(isGroupExportCancelProofRejected(error, { ...binding, actor: initial })).toBe(false);
  });
  it("does not tie authority to display fields or JavaScript profile object identity", async () => {
    const binding = cancelBinding();
    const fetcher = vi.fn().mockImplementation(async () => {
      Object.assign(binding.actor, { mobile: "display", email: "display@example.invalid", remark: "changed display",
        passStrength: "low", passwordNotUpdatedDays: 9, pwdUpdateTm: "different display timestamp" });
      return proofResponse();
    }); vi.stubGlobal("fetch", fetcher);
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ code: "invalid_credentials", status: 401 });
    expect(isGroupExportCancelProofRejected(error, { ...binding, actor: { ...exportProfile } })).toBe(true);
  });
  it("rejects actor or CSRF disagreement and ineligible profile before native fetch", async () => {
    const fetcher = vi.fn(); vi.stubGlobal("fetch", fetcher); const binding = cancelBinding();
    for (const [actor, csrf] of [[2, exportProfile.csrfToken], [1, "another-session"]] as const)
      await expect(groupAssetExportAPI.cancel(binding.taskID, binding.request, cancelProof, actor, csrf, binding.signal, binding.actor))
        .rejects.toMatchObject({ code: "invalid_input" });
    for (const patch of [{ ID: 0 }, { ID: Number.MAX_SAFE_INTEGER + 1 }, { username: "" }, { role: "" },
      { priv: 0.5 }, { csrfToken: "" }, { hasMfa: false }, { needChangePwd: true }, { isExpired: true }])
      await expect(groupAssetExportAPI.cancel(binding.taskID, binding.request, cancelProof, 1, exportProfile.csrfToken,
        binding.signal, { ...binding.actor, ...patch })).rejects.toMatchObject({ code: "invalid_input" });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it("preserves successful results with the optional profile and never sends actor or profile headers", async () => {
    const binding = cancelBinding(), task = { ...exportTask, state: "cancel_requested" };
    const fetcher = vi.fn().mockResolvedValue(exportJSON({ taskID: binding.taskID, task })); vi.stubGlobal("fetch", fetcher);
    expect(await groupAssetExportAPI.cancel(binding.taskID, binding.request, cancelProof, binding.actor.ID,
      binding.actor.csrfToken, binding.signal, binding.actor)).toEqual(task);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(Object.keys(fetcher.mock.calls[0][1].headers).sort()).toEqual(["Content-Type", "X-CSRF-Token"]);
    expect(Object.keys(JSON.parse(fetcher.mock.calls[0][1].body) as object).sort()).toEqual(["actorPassword", "taskID", "totpCode"]);
  });
  it.each([
    { taskID: "foreign-task" }, { task: { ...exportTask, taskUUID: "foreign-task" } },
    { task: { ...exportTask, taskName: "computer_assets.v3.export" } },
    { task: { ...exportTask, domainId: "foreign-domain" } }, { request: exportRequest },
  ])("retains strict cancellation success DTO binding %j", async (patch) => {
    const binding = cancelBinding();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(exportJSON({ taskID: binding.taskID, task: exportTask, ...patch })));
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ code: "invalid_response" });
    expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
  });
  it.each(["X-ADTR-User-ID", "Cache-Control", "X-Content-Type-Options"])("retains mandatory success metadata %s", async (header) => {
    const binding = cancelBinding(), response = exportJSON({ taskID: binding.taskID, task: exportTask }); response.headers.delete(header);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));
    const error = await rejectedCancel(binding);
    expect(error).toMatchObject({ status: 401 }); expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
  });
  it("accepts current valid group IDs rather than silently imposing UUID-only cancellation", async () => {
    const taskID = "group.task_42-1", binding = { ...cancelBinding(), taskID };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(proofResponse()));
    expect(isGroupExportCancelProofRejected(await rejectedCancel(binding), binding)).toBe(true);
    const detail = { ...cancelDetail(), taskID, task: { ...exportTask, taskUUID: taskID } };
    const responses = recheckResponses(); responses[3] = exportJSON(detail); const fetcher = installRecheck(responses);
    expect(await groupAssetExportAPI.recheckCancelAuthority(taskID, copyRequest(), exportProfile, signal())).toEqual(detail);
    expect(fetcher.mock.calls[3][0]).toBe(`/api/group-assets/v3/exports/detail?taskID=${taskID}`);
  });
});

describe("cancel authority independent of source production and prior permission snapshots", () => {
  it("rechecks current grants anew on every invocation", async () => {
    const first = recheckResponses(), second = recheckResponses();
    second[1] = authorityJSON({ menu: cancelMenu().menu.map((node) => node.mark === "tasks"
      ? { ...node, auth: { readable: true, writeable: false } } : node) });
    const fetcher = installRecheck([...first, ...second]);
    await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).resolves.toEqual(cancelDetail());
    await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).rejects.toMatchObject({ code: "forbidden", status: 403 });
    expect(fetcher).toHaveBeenCalledTimes(7);
  });
  it.each([401, 403, 404, 409])("retains dedicated backend owner/scope/epoch rejection %i without proof provenance", async (status) => {
    const binding = cancelBinding(), responses = recheckResponses();
    responses[3] = proofResponse('{"error":"authorization_epoch_changed"}', {}, status);
    const fetcher = installRecheck(responses);
    const error: unknown = await groupAssetExportAPI.recheckCancelAuthority(binding.taskID, binding.request, binding.actor, binding.signal).catch((reason: unknown) => reason);
    expect(error).toMatchObject({ status }); expect(isGroupExportCancelProofRejected(error, binding)).toBe(false);
    expect(fetcher).toHaveBeenCalledTimes(4);
  });
  it.each(["source_changed", "source_unavailable", "artifact_invalid"])("observes %s without asking a source to produce again", async (downloadStatus) => {
    const detail = { ...cancelDetail(), task: { ...readyExport.task }, downloadStatus };
    const responses = recheckResponses(); responses[3] = exportJSON(detail); const fetcher = installRecheck(responses);
    expect(await groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).toEqual(detail);
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual(["/api/auth/me", "/api/access/menu", "/api/access/check",
      `/api/group-assets/v3/exports/detail?taskID=${exportTask.taskUUID}`, "/api/auth/me"]);
  });
  it("ignores changed profile display data at both current-session boundaries", async () => {
    const responses = recheckResponses();
    for (const index of [0, 4]) responses[index] = authorityJSON({ ...exportProfile, mobile: "changed",
      email: "synthetic@example.invalid", remark: "display", passStrength: "low", passwordNotUpdatedDays: 2, pwdUpdateTm: "changed" });
    installRecheck(responses);
    await expect(groupAssetExportAPI.recheckCancelAuthority(exportTask.taskUUID, copyRequest(), exportProfile, signal())).resolves.toEqual(cancelDetail());
  });
});
