import { afterEach, describe, expect, it, vi } from "vitest";
import { auditAPI } from "./audit-api";
import {
  emptyHistoryQuery,
  exportFilename,
  type ExportHistoryRow,
} from "./audit-history";

const id = "00000000-0000-4000-8000-000000000011";
const startTm = "2026-10-07T00:00:00.123455Z";
const endTm = "2026-10-07T00:00:00.123457Z";
const row = (createdAt: string): ExportHistoryRow => ({
  taskUUID: id,
  fileName: exportFilename(id),
  modelType: "Audit",
  fileType: "xlsx",
  state: "failed",
  progress: 37,
  error: "export_failed",
  createdAt,
  updatedAt: "2026-10-07T00:00:01Z",
  attempt: 2,
  maxAttempts: 3,
  downloadReady: false,
  downloadStatus: "not_ready",
});
afterEach(() => vi.unstubAllGlobals());

describe("audit export history creation time window", () => {
  it.each([
    ["2026-10-07T00:00:00.123454Z", startTm, endTm, false],
    ["2026-10-07T00:00:00.123455Z", startTm, endTm, true],
    ["2026-10-07T00:00:00.123456Z", startTm, endTm, true],
    ["2026-10-07T00:00:00.123457Z", startTm, endTm, false],
    ["2026-10-07T00:00:00.123454Z", startTm, "", false],
    ["2026-10-07T00:00:00.123458Z", "", endTm, false],
    ["2026-10-07T00:00:00.123458Z", startTm, "", true],
    ["2026-10-07T00:00:00.123454Z", "", endTm, true],
    ["2026-10-07T00:00:00.123458Z", "", "", true],
    ["2026-10-07T00:00:00.1234Z", "2026-10-07T00:00:00.123400Z", "", true],
    ["2026-10-07T00:00:00.1234Z", "", "2026-10-07T00:00:00.123400Z", false],
  ] as const)(
    "validates returned creation %s against [%s,%s) (accepted: %s)",
    async (createdAt, start, end, accepted) => {
      const expected = row(createdAt);
      const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
        Response.json(
          {
            page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
            list: [expected],
            exhausted: true,
          },
          { headers: { "X-ADTR-User-ID": "1" } },
        ),
      );
      vi.stubGlobal("fetch", fetcher);
      const result = auditAPI.history(
        { ...emptyHistoryQuery(), startTm: start, endTm: end },
        new AbortController().signal,
        1,
      );
      expect(fetcher).toHaveBeenCalledTimes(1);
      const [url, request] = fetcher.mock.calls[0];
      const sent = new URL(String(url), "https://audit-fixture.invalid");
      expect(sent.pathname).toBe("/api/audit/exports/history");
      expect(sent.searchParams.get("startTm")).toBe(start || null);
      expect(sent.searchParams.get("endTm")).toBe(end || null);
      expect(request?.method).toBe("GET");
      if (accepted) {
        expect((await result).list).toEqual([expected]);
      } else {
        await expect(result).rejects.toMatchObject({
          code: "invalid_response",
        });
      }
    },
  );
  it("rejects the entire page if a later task is outside the requested window", async () => {
    const outside = row("2026-10-07T00:00:00.123454Z");
    outside.taskUUID = "00000000-0000-4000-8000-000000000012";
    outside.fileName = exportFilename(outside.taskUUID);
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>().mockResolvedValue(
        Response.json(
          {
            page: { pageIdx: 1, pageSize: 20, total: 2, totalPage: 1 },
            list: [row(startTm), outside],
            exhausted: true,
          },
          { headers: { "X-ADTR-User-ID": "1" } },
        ),
      ),
    );
    await expect(
      auditAPI.history(
        { ...emptyHistoryQuery(), startTm, endTm },
        new AbortController().signal,
        1,
      ),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
});
