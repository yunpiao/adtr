// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { APIRequestContext, APIResponse } from "@playwright/test";
import type { SafeRequestEvent } from "../e2e/safe-request-diagnostics";
let directory = "";
const path = "/api/directory-credential-use/v2/effective?accountId=CANARY";
const response = (status: number) => ({ status: () => status }) as APIResponse;
const request = (get: ReturnType<typeof vi.fn>) =>
  ({ get }) as unknown as Pick<APIRequestContext, "get">;
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllEnvs();
  if (directory) rmSync(directory, { recursive: true, force: true });
});
async function setup() {
  vi.resetModules();
  directory = mkdtempSync(join(tmpdir(), "adtr-v2-get-"));
  const file = join(directory, "browser.json");
  vi.stubEnv("ADTR_E2E_SAFE_DIAGNOSTICS", file);
  const receipt = vi.spyOn(console, "log").mockImplementation(() => {});
  return {
    ...(await import("../e2e/directory-v2-verification-get")),
    receipt,
    events: () =>
      JSON.parse(readFileSync(file, "utf8")).events as SafeRequestEvent[],
    raw: () => readFileSync(file, "utf8"),
  };
}
describe("directory-v2 verification GET bounded recovery", () => {
  it.each([200, 401, 403, 429, 500])(
    "returns HTTP %s unchanged without retry",
    async (status) => {
      const h = await setup();
      const value = response(status);
      const get = vi.fn().mockResolvedValue(value);
      expect(await h.directoryV2VerificationGET(request(get), path)).toBe(
        value,
      );
      expect(get.mock.calls).toEqual([[path]]);
      expect(h.events().map((e) => [e.attempt, e.outcome, e.status])).toEqual([
        [1, "start", null],
        [1, "response", status],
      ]);
      expect(h.receipt).not.toHaveBeenCalled();
    },
  );
  it.each([
    "ECONNRESET",
    "read ECONNRESET",
    "write ECONNRESET",
    "connect ECONNRESET",
    "socket hang up",
  ])(
    "immediately retries exact %s with one operation and no new arguments",
    async (message) => {
      const h = await setup();
      const value = response(200);
      const get = vi
        .fn()
        .mockRejectedValueOnce(
          new Error(`apiRequestContext.get: ${message}\nCall log: CANARY`),
        )
        .mockResolvedValue(value);
      const timer = vi.spyOn(globalThis, "setTimeout");
      expect(await h.directoryV2VerificationGET(request(get), path)).toBe(
        value,
      );
      expect(timer).not.toHaveBeenCalled();
      expect(get.mock.calls).toEqual([[path], [path]]);
      const events = h.events();
      expect(new Set(events.map((e) => e.operation)).size).toBe(1);
      expect(events.map((e) => [e.attempt, e.outcome])).toEqual([
        [1, "start"],
        [1, "transport-error"],
        [2, "start"],
        [2, "response"],
      ]);
      expect(h.raw()).not.toContain("CANARY");
      expect(h.receipt.mock.calls).toEqual([
        ["Directory-v2 verification GET recovered: attempt=2/2"],
      ]);
    },
  );
  it.each([401, 403, 429, 500])(
    "preserves HTTP %s after a reset, with no third request",
    async (status) => {
      const h = await setup();
      const value = response(status);
      const get = vi
        .fn()
        .mockRejectedValueOnce(
          new Error("apiRequestContext.get: socket hang up"),
        )
        .mockResolvedValue(value);
      expect(await h.directoryV2VerificationGET(request(get), path)).toBe(
        value,
      );
      expect(get).toHaveBeenCalledTimes(2);
      expect(h.events().at(-1)?.status).toBe(status);
    },
  );
  it.each(["Error", "TimeoutError", "TargetClosedError", "AbortError"])(
    "preserves actual terminal second %s object and never calls a third time",
    async (name) => {
      const h = await setup();
      const terminal = new Error("apiRequestContext.get: socket hang up");
      terminal.name = name;
      const get = vi
        .fn()
        .mockRejectedValueOnce(new Error("apiRequestContext.get: ECONNRESET"))
        .mockRejectedValue(terminal);
      await expect(
        h.directoryV2VerificationGET(request(get), path),
      ).rejects.toBe(terminal);
      expect(get).toHaveBeenCalledTimes(2);
      expect(h.events().map((e) => e.attempt)).toEqual([1, 1, 2, 2]);
      expect(h.receipt).not.toHaveBeenCalled();
    },
  );
  it.each([
    ["TimeoutError", "apiRequestContext.get: socket hang up"],
    ["TargetClosedError", "apiRequestContext.get: ECONNRESET"],
    ["AbortError", "apiRequestContext.get: socket hang up"],
    [
      "Error",
      "apiRequestContext.get: Target page, context or browser has been closed",
    ],
    ["Error", "apiRequestContext.get: Request context disposed."],
    ["Error", "apiRequestContext.get: Timeout 100ms exceeded."],
    ["Error", "apiRequestContext.post: socket hang up"],
    ["Error", "apiRequestContext.get: socket hang up suffix"],
    ["Error", "unrelated\napiRequestContext.get: ECONNRESET"],
  ])(
    "does not retry cancellation/closed/unknown %s %s",
    async (name, message) => {
      const h = await setup();
      const error = new Error(message);
      error.name = name;
      const get = vi.fn().mockRejectedValue(error);
      await expect(
        h.directoryV2VerificationGET(request(get), path),
      ).rejects.toBe(error);
      expect(get).toHaveBeenCalledTimes(1);
    },
  );
  it("handles cancellation during the second pending GET without a third call", async () => {
    const h = await setup();
    let reject!: (error: unknown) => void;
    const pending = new Promise<APIResponse>((_, no) => {
      reject = no;
    });
    const closed = new Error(
      "apiRequestContext.get: Target page, context or browser has been closed",
    );
    const get = vi
      .fn()
      .mockRejectedValueOnce(new Error("apiRequestContext.get: socket hang up"))
      .mockReturnValue(pending);
    const result = h.directoryV2VerificationGET(request(get), path);
    const rejected = expect(result).rejects.toBe(closed);
    await Promise.resolve();
    expect(get).toHaveBeenCalledTimes(2);
    reject(closed);
    await rejected;
    expect(get).toHaveBeenCalledTimes(2);
    expect(h.events().at(-1)?.error_class).toBe("target_closed");
    expect(h.receipt).not.toHaveBeenCalled();
  });
  it("preserves primitive and hostile errors without retry or masking", async () => {
    const h = await setup();
    const hostile = new Error();
    Object.defineProperty(hostile, "message", {
      get() {
        throw new Error("CANARY");
      },
    });
    for (const error of [null, 42, hostile]) {
      const get = vi.fn().mockRejectedValue(error);
      await expect(
        h.directoryV2VerificationGET(request(get), path),
      ).rejects.toBe(error);
      expect(get).toHaveBeenCalledTimes(1);
    }
  });
  it("leaves caller assertions and JSON parsing outside recovery", async () => {
    const h = await setup();
    const parseError = new Error("parse");
    const value = {
      status: () => 200,
      json: vi.fn().mockRejectedValue(parseError),
    } as unknown as APIResponse;
    const get = vi.fn().mockResolvedValue(value);
    const result = await h.directoryV2VerificationGET(request(get), path);
    await expect(result.json()).rejects.toBe(parseError);
    expect(() => {
      expect(result.status()).toBe(401);
    }).toThrow();
    expect(get).toHaveBeenCalledTimes(1);
  });
  it("retains the same request receiver and never caches across actors", async () => {
    const h = await setup();
    const a = response(200),
      b = response(403);
    let owner: unknown;
    const get = vi.fn(function (this: unknown) {
      owner = this;
      return Promise.resolve(a);
    });
    const actor = request(get);
    expect(await h.directoryV2VerificationGET(actor, path)).toBe(a);
    expect(owner).toBe(actor);
    const other = vi.fn().mockResolvedValue(b);
    expect(await h.directoryV2VerificationGET(request(other), path)).toBe(b);
    expect(other).toHaveBeenCalledTimes(1);
    expect(new Set(h.events().map((e) => e.operation)).size).toBe(2);
  });
  it("does not mask a recovered response when a receipt write fails", async () => {
    const h = await setup();
    h.receipt.mockImplementation(() => {
      throw new Error("CANARY");
    });
    const value = response(200);
    const get = vi
      .fn()
      .mockRejectedValueOnce(new Error("apiRequestContext.get: socket hang up"))
      .mockResolvedValue(value);
    expect(await h.directoryV2VerificationGET(request(get), path)).toBe(value);
  });
});
