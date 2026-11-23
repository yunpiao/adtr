import { afterEach, describe, expect, it, vi } from "vitest";

import { userAssetExportAPI } from "./user-asset-export-v3-api";
import { exportJSON, exportReceipt, exportRequest } from "./user-asset-export-v3-fixtures";

afterEach(() => vi.unstubAllGlobals());

describe("user asset export request binding", () => {
  it.each(["create", "receipt", "detail"] as const)(
    "%s validates a pending response against its original request",
    async (operation) => {
      const original = structuredClone(exportRequest);
      const draft = structuredClone(original);
      const initialReceipt = { ...exportReceipt, request: original };
      const response =
        operation === "create"
          ? { ...initialReceipt, replayed: false }
          : operation === "detail"
            ? { ...initialReceipt, downloadReady: false, downloadStatus: "pending" }
            : initialReceipt;
      let respond!: (response: Response) => void;
      const deferred = new Promise<Response>((resolve) => {
        respond = resolve;
      });
      const fetch = vi.fn<typeof globalThis.fetch>().mockReturnValue(deferred);
      vi.stubGlobal("fetch", fetch);

      const signal = new AbortController().signal;
      const pending =
        operation === "create"
          ? userAssetExportAPI.create(
              draft,
              "fixture-key",
              { actorPassword: "test-only", totpCode: "000000" },
              1,
              "fixture-csrf",
              signal,
            )
          : operation === "receipt"
            ? userAssetExportAPI.receipt(original.domainId, "fixture-key", 1, signal, draft)
            : userAssetExportAPI.detail(initialReceipt.taskID, draft, 1, signal);

      expect(fetch).toHaveBeenCalledTimes(1);
      if (operation === "create") {
        const submitted = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body));
        expect(submitted.screenColumns).toEqual(original.screenColumns);
        expect(submitted.search).toBe(original.search);
      }
      draft.search = "new draft";
      draft.screenColumns.reverse();
      respond(exportJSON(response));

      await expect(pending).resolves.toMatchObject({ request: original });
      expect(fetch).toHaveBeenCalledTimes(1);
    },
  );

  it("still permits receipt lookup without a stored submission request", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(exportJSON(exportReceipt));
    vi.stubGlobal("fetch", fetch);
    const signal = new AbortController().signal;
    await expect(
      userAssetExportAPI.receipt(exportRequest.domainId, "fixture-key", 1, signal),
    ).resolves.toMatchObject(exportReceipt);
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});
