import { afterEach, describe, expect, it, vi } from "vitest";
import { webcrypto } from "node:crypto";
import { exportJSON, exportReceipt, exportRequest, exportTask, readyExport } from "./user-asset-export-v3-fixtures";
import { downloadStatuses, exportColumns, parseExportReceipt, requestFromQuery, userAssetExportAPI, validExportRequest } from "./user-asset-export-v3-api";
const signal = () => new AbortController().signal;
afterEach(() => vi.unstubAllGlobals());
describe("user asset export immutable parser", () => {
  it("projects every filter and four pins, excludes pagination, preserves selected order", () => {
    const q = { ...exportRequest, creatTm: "desc" as const, search: " Alice ", sAMAccountName: "Alice", startTm: "2026-01-01T00:00:00.000000001Z", endTm: "2026-01-01T00:00:00.000000002Z", pageIdx: 9, pageSize: 10 };
    const request = requestFromQuery(q, ["mail", "guid"]);
    expect(request).toEqual({ ...exportRequest, ...Object.fromEntries(Object.entries(q).filter(([key]) => !["pageIdx", "pageSize", "screenColumns"].includes(key))), screenColumns: ["mail", "guid"] });
    expect(request).not.toHaveProperty("pageIdx"); expect(request).not.toHaveProperty("pageSize");
  });
  it.each(["domainId", "expectedRevision", "expectedCredentialRevision", "observationId", "search", "sAMAccountName", "startTm", "endTm", "creatTm"])("rejects missing canonical %s", (key) => {
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
describe("user asset export bounded transport", () => {
  it("checks both fresh manifests and digest before returning bytes", async () => {
    vi.stubGlobal("crypto", webcrypto);
    const fetcher = vi.fn().mockResolvedValueOnce(exportJSON(readyExport)).mockResolvedValueOnce(file()).mockResolvedValueOnce(exportJSON(readyExport)); vi.stubGlobal("fetch", fetcher);
    const blob = await userAssetExportAPI.download(readyExport, 1, signal());
    expect(blob.size).toBe(3); expect(fetcher).toHaveBeenCalledTimes(3);
  });
  it.each([
    ["Content-Type", "text/html"], ["Content-Length", "33554433"], ["Content-Length", "03"],
    ["Content-Disposition", 'attachment; filename="foreign.xlsx"'], ["X-ADTR-Artifact-SHA256", "0".repeat(64)],
    ["X-ADTR-Row-Count", "1"], ["X-ADTR-User-ID", "2"], ["Cache-Control", "public"],
  ])("rejects download header %s", async (name, value) => {
    const fetcher = vi.fn().mockResolvedValueOnce(exportJSON(readyExport)).mockResolvedValueOnce(file("abc", { [name]: value })); vi.stubGlobal("fetch", fetcher);
    await expect(userAssetExportAPI.download(readyExport, 1, signal())).rejects.toThrow(); expect(fetcher).toHaveBeenCalledTimes(2);
  });
  it.each(["abd", "abcd", "ab"])("rejects corrupted or wrong length body %s", async (body) => {
    vi.stubGlobal("crypto", webcrypto); vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(exportJSON(readyExport)).mockResolvedValueOnce(file(body)));
    await expect(userAssetExportAPI.download(readyExport, 1, signal())).rejects.toThrow();
  });
  it("discards verified bytes on authority loss after download", async () => {
    vi.stubGlobal("crypto", webcrypto); vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(exportJSON(readyExport)).mockResolvedValueOnce(file()).mockResolvedValueOnce(exportJSON({ error: "forbidden" }, 403)));
    await expect(userAssetExportAPI.download(readyExport, 1, signal())).rejects.toMatchObject({ code: "forbidden" });
  });
  it("rejects cross-actor receipts and bounds metadata before parsing", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(exportJSON(exportReceipt, 200, 2)).mockResolvedValueOnce(new Response(" ".repeat(65537), { headers: { "X-ADTR-User-ID": "1", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff" } })));
    await expect(userAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toMatchObject({ status: 401 });
    await expect(userAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toThrow();
  });
});

describe("malformed export JSON and lifecycle combinations", () => {
  it("rejects duplicate response keys instead of accepting the last value", async () => {
    const body = JSON.stringify(exportReceipt).replace('"taskID":', '"taskID":"ignored","taskID":');
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(body, { headers: { "Content-Type": "application/json", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-ADTR-User-ID": "1" } })));
    await expect(userAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toThrow();
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
    await expect(userAssetExportAPI.download(readyExport, 1, signal())).rejects.toThrow();
  });
});

describe("response body connection failure", () => {
  it("normalizes a receipt stream reset after headers as network", async () => {
    const stream = new ReadableStream<Uint8Array>({ start(controller) { controller.enqueue(new TextEncoder().encode('{"taskID":')); controller.error(new TypeError("socket reset")); } });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(stream, { headers: { "Content-Type": "application/json", "X-ADTR-User-ID": "1", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff" } })));
    await expect(userAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toMatchObject({ code: "network" });
  });
});

describe("authority status survives invalid error pages", () => {
  it.each([401, 403])("preserves status %i for empty, HTML, and oversized bodies", async (status) => {
    for (const body of ["", "<html>proxy error</html>", "x".repeat(65537)]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(body, { status })));
      await expect(userAssetExportAPI.receipt(exportRequest.domainId, "key", 1, signal())).rejects.toMatchObject({ status, code: status === 401 ? "unauthenticated" : "forbidden" });
    }
  });
});
