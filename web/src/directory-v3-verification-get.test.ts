// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import type { APIRequestContext, APIResponse } from "@playwright/test";
import { directoryV3VerificationGET } from "../e2e/directory-v3-verification-get";

const path = "/api/directory-credential-use/v3/effective?accountId=synthetic-account";
const canary = "private-transport-canary";
const request = (get: ReturnType<typeof vi.fn>) =>
  ({ get }) as unknown as Pick<APIRequestContext, "get">;

afterEach(() => vi.restoreAllMocks());

describe("directory-v3 verification GET private failure boundary", () => {
  it.each([200, 401, 403, 409, 500])(
    "returns the identical HTTP %s response without inspection or retry",
    async (status) => {
      const value = {
        status: vi.fn(() => status),
        headers: vi.fn(),
        json: vi.fn(),
      } as unknown as APIResponse;
      const get = vi.fn().mockResolvedValue(value);
      const originalRequest = request(get);
      const timer = vi.spyOn(globalThis, "setTimeout");
      expect(await directoryV3VerificationGET(originalRequest, path)).toBe(value);
      expect(get.mock.calls).toEqual([[path]]);
      expect(get.mock.contexts).toEqual([originalRequest]);
      expect(value.status).not.toHaveBeenCalled();
      expect(value.headers).not.toHaveBeenCalled();
      expect(value.json).not.toHaveBeenCalled();
      expect(timer).not.toHaveBeenCalled();
    },
  );

  it.each([
    ["Error", "apiRequestContext.get: socket hang up", "socket_hang_up"],
    ["TimeoutError", "apiRequestContext.get: socket hang up", "timeout"],
    ["Error", "apiRequestContext.get: Request timed out", "timeout"],
    ["Error", "apiRequestContext.get: ECONNRESET", "request_failed"],
    ["Error", "apiRequestContext.get: failed", "request_failed"],
    ["Error", "apiRequestContext.get: socket hang up unrecognized", "request_failed"],
  ])("emits only a closed category for %s / %s", async (name, line, category) => {
    const raw = new Error(
      `${line}\nCall log:\nCookie: ${canary}\nAuthorization: ${canary}\n` +
        `https://synthetic.invalid/${canary}\nbody: ${canary}\n` +
        "apiRequestContext.get: socket hang up",
    );
    raw.name = name;
    raw.stack = `private stack ${canary}`;
    Object.defineProperty(raw, "cause", { value: new Error(canary) });
    const get = vi.fn().mockRejectedValue(raw);
    const log = vi.spyOn(console, "log");
    const warn = vi.spyOn(console, "warn");
    const stderr = vi.spyOn(console, "error");
    const timer = vi.spyOn(globalThis, "setTimeout");
    const failure = await directoryV3VerificationGET(request(get), path).catch(
      (error: unknown) => error,
    );
    expect(get.mock.calls).toEqual([[path]]);
    expect(failure).toBeInstanceOf(Error);
    expect(failure).not.toBe(raw);
    const safe = failure as Error & { cause?: unknown };
    expect(safe.message).toBe(`Directory-v3 verification GET failed: ${category}`);
    expect(safe.cause).toBeUndefined();
    expect(Object.keys(safe)).toEqual([]);
    expect(`${safe.message}\n${safe.stack}\n${JSON.stringify(safe)}`).not.toMatch(
      /private-transport-canary|Cookie|Authorization|synthetic\.invalid|Call log|body:/u,
    );
    expect(log).not.toHaveBeenCalled();
    expect(warn).not.toHaveBeenCalled();
    expect(stderr).not.toHaveBeenCalled();
    expect(timer).not.toHaveBeenCalled();
  });

  it.each([null, canary, { message: canary, cause: canary }])(
    "fails closed for a non-Error rejection",
    async (raw) => {
      const get = vi.fn().mockRejectedValue(raw);
      await expect(directoryV3VerificationGET(request(get), path)).rejects.toThrow(
        "Directory-v3 verification GET failed: request_failed",
      );
      expect(get.mock.calls).toEqual([[path]]);
    },
  );

  it("censors a synchronous throw and a hostile error-message getter once", async () => {
    const raw = new Error();
    Object.defineProperty(raw, "message", {
      get() { throw new Error(canary); },
    });
    const get = vi.fn(() => { throw raw; });
    const failure = await directoryV3VerificationGET(request(get), path).catch(
      (error: unknown) => error,
    );
    expect((failure as Error).message).toBe(
      "Directory-v3 verification GET failed: request_failed",
    );
    expect(String((failure as Error).stack)).not.toContain(canary);
    expect(get.mock.calls).toEqual([[path]]);
  });

  it("keeps the original readJSON status, metadata and JSON assertions outside the transport catch", () => {
    const source = readFileSync(
      new URL("../e2e/directory-v3-readers.spec.ts", import.meta.url),
      "utf8",
    );
    const start = source.indexOf("async function readJSON(");
    const end = source.indexOf("\nasync function submit(", start);
    expect(start).toBeGreaterThanOrEqual(0);
    expect(end).toBeGreaterThan(start);
    const body = source.slice(start, end);
    expect(body).toContain("await directoryV3VerificationGET(context.request, path)");
    expect(body).toContain('expect(response.status(), `GET ${path}`).toBe(200)');
    expect(body).toContain('expect(response.headers()["cache-control"]).toBe("no-store")');
    expect(body).toContain('expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId))');
    expect(body).toContain("return response.json()");
    expect(body).not.toMatch(/\b(?:try|catch)\b/u);
  });
});
