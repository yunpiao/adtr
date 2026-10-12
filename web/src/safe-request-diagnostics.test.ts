// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { EventEmitter } from "node:events";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Page, Request, Response } from "@playwright/test";
import type { SafeRequestEvent } from "../e2e/safe-request-diagnostics";
const canary = "CANARY-secret-cookie-token-account-body";
let directory = "";
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllEnvs();
  if (directory) rmSync(directory, { recursive: true, force: true });
});
async function setup() {
  vi.resetModules();
  directory = mkdtempSync(join(tmpdir(), "adtr-request-diagnostics-"));
  const file = join(directory, "browser.json");
  vi.stubEnv("ADTR_E2E_SAFE_DIAGNOSTICS", file);
  return {
    file,
    read: () =>
      JSON.parse(readFileSync(file, "utf8")) as {
        events: SafeRequestEvent[];
        dropped: number;
      },
    ...(await import("../e2e/safe-request-diagnostics")),
  };
}
function event(): SafeRequestEvent {
  return {
    origin: "api-request",
    phase: "fixture-get",
    operation: 1,
    attempt: 1,
    method: "GET",
    route: "/api/auth/me",
    outcome: "start",
    status: null,
    error_class: "none",
    at_ms: 0,
    elapsed_ms: 0,
    snapshot: null,
  };
}
function page() {
  const emitter = new EventEmitter();
  Object.assign(emitter, {
    isClosed: () => false,
    evaluate: vi.fn().mockResolvedValue({
      view: "v3",
      picker: "visible",
      source: "radio-present",
      alert: false,
      permission_denied: false,
      page_closed: false,
      unavailable: false,
    }),
  });
  return emitter as unknown as Page & EventEmitter;
}

describe("bounded request diagnostics", () => {
  it("publishes actual mixed TypeScript output through the independent Python schema", async () => {
    const h = await setup();
    const { recordSafeEvent } = await import("../e2e/safe-runtime-diagnostics");
    recordSafeEvent("read-json", "/api/auth/me", "response", 1, 200);
    for (let i = 0; i < 1000; i++) {
      await h.diagnosticGET(
        async () => ({ status: () => 500 }),
        "/api/auth/me",
      );
      await h.checkpoint(page(), "source-select", "start");
    }
    const result = spawnSync(
      "python3",
      [
        "-c",
        `
import json, sys
from pathlib import Path
from e2e_runtime_diagnostics import RuntimeDiagnostics, publish
source = Path(sys.argv[1])
RuntimeDiagnostics().write(source / "runtime.json")
publish(source, source / "validated")
report = json.loads((source / "validated" / "diagnostics.json").read_bytes())
assert report["browser_capture"] == "present"
assert report["browser_dropped"] > 0
assert report["browser"][-1]["phase"] == "source-select"
`,
        directory,
      ],
      {
        cwd: fileURLToPath(new URL("../../scripts", import.meta.url)),
        encoding: "utf8",
      },
    );
    expect(result.status, result.stderr).toBe(0);
    expect(
      readFileSync(join(directory, "validated", "diagnostics.json"), "utf8"),
    ).not.toContain(canary);
  });

  it.each([200, 401, 403, 429, 500])(
    "returns exact GET response for status %s",
    async (status) => {
      const h = await setup();
      const result = { status: () => status };
      const get = vi.fn().mockResolvedValue(result);
      expect(await h.diagnosticGET(get, `/api/auth/me?token=${canary}`)).toBe(
        result,
      );
      expect(get).toHaveBeenCalledTimes(1);
      expect(
        h.read().events.map((e) => [e.phase, e.outcome, e.status, e.operation]),
      ).toEqual([
        ["directory-get", "start", null, 1],
        ["directory-get", "response", status, 1],
      ]);
      expect(readFileSync(h.file, "utf8")).not.toContain(canary);
    },
  );
  it.each([
    [
      new Error(`apiRequestContext.get: socket hang up\n${canary}`),
      "socket_hang_up",
    ],
    [
      new Error(`apiRequestContext.get: read ECONNRESET\n${canary}`),
      "econnreset",
    ],
    [
      new Error(`apiRequestContext.get: socket hang up ${canary}`),
      "unknown_error",
    ],
    [
      new Error(
        `apiRequestContext.get: failed\napiRequestContext.get: socket hang up ${canary}`,
      ),
      "unknown_error",
    ],
    [canary, "non_error"],
  ])(
    "rethrows original errors and only retains a constant class",
    async (error, label) => {
      const h = await setup();
      const get = vi.fn().mockRejectedValue(error);
      await expect(h.diagnosticGET(get, `/api/${canary}`)).rejects.toBe(error);
      expect(get).toHaveBeenCalledTimes(1);
      expect(h.read().events.at(-1)).toMatchObject({
        outcome: "transport-error",
        status: null,
        error_class: label,
        route: "unknown",
      });
      expect(readFileSync(h.file, "utf8")).not.toContain(canary);
    },
  );
  it("records each existing ECONNRESET attempt, without changing call options or retry policy", async () => {
    const h = await setup();
    const { fixtureGET } = await import("../e2e/fixture-get");
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] });
    vi.spyOn(console, "log").mockImplementation(() => {});
    const result = { status: () => 500 };
    const get = vi
      .fn()
      .mockRejectedValueOnce(
        new Error("apiRequestContext.get: read ECONNRESET"),
      )
      .mockResolvedValue(result);
    const value = fixtureGET({ get }, `/api/auth/me?token=${canary}`);
    await vi.advanceTimersByTimeAsync(250);
    expect(await value).toBe(result);
    expect(get.mock.calls.map((c) => c[1])).toEqual([
      { maxRetries: 0, timeout: 30000 },
      { maxRetries: 0, timeout: 29750 },
    ]);
    expect(
      h
        .read()
        .events.map((e) => [
          e.attempt,
          e.operation,
          e.outcome,
          e.error_class,
          e.status,
        ]),
    ).toEqual([
      [1, 1, "start", "none", null],
      [1, 1, "transport-error", "econnreset", null],
      [2, 1, "start", "none", null],
      [2, 1, "response", "none", 500],
    ]);
  });
  it("records socket hang up once and preserves the fixture's sanitized error", async () => {
    const h = await setup();
    const { fixtureGET } = await import("../e2e/fixture-get");
    const get = vi
      .fn()
      .mockRejectedValue(
        new Error(`apiRequestContext.get: socket hang up\n${canary}`),
      );
    await expect(fixtureGET({ get }, "/api/auth/me")).rejects.toThrow(
      "transport_error; reason=socket_hang_up; attempt=1/2",
    );
    expect(get).toHaveBeenCalledTimes(1);
    expect(h.read().events.at(-1)).toMatchObject({
      attempt: 1,
      error_class: "socket_hang_up",
      status: null,
    });
  });
  it("records the unchanged shared deadline as timeout, without another attempt", async () => {
    const h = await setup();
    const { fixtureGET } = await import("../e2e/fixture-get");
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] });
    const get = vi.fn().mockReturnValue(new Promise(() => {}));
    const caught = fixtureGET({ get }, "/api/auth/me").catch((e) => e);
    await vi.advanceTimersByTimeAsync(30000);
    expect((await caught).message).toBe(
      "User-assets fixture GET failed: deadline_exceeded",
    );
    expect(get).toHaveBeenCalledTimes(1);
    expect(h.read().events.at(-1)).toMatchObject({
      error_class: "timeout",
      status: null,
    });
  });
  it("distinguishes HTTP errors, missing headers, and failure after headers with read-only page listeners", async () => {
    const h = await setup();
    const p = page();
    const stop = h.observeSafePageRequests(p);
    const req = (path: string, method = "GET") =>
      ({
        url: () => `http://127.0.0.1${path}?token=${canary}`,
        method: () => method,
      }) as Request;
    const emitResponse = (r: Request, status: number) =>
      p.emit("response", {
        request: () => r,
        status: () => status,
      } as Response);
    const a = req("/api/domain-selection"),
      b = req("/api/access/check", "POST"),
      c = req("/api/auth/me");
    p.emit("request", a);
    emitResponse(a, 500);
    p.emit("requestfinished", a);
    p.emit("request", b);
    p.emit("requestfailed", b);
    p.emit("request", c);
    emitResponse(c, 200);
    p.emit("requestfailed", c);
    p.emit("request", req(`/api/${canary}`));
    expect(
      h.read().events.map((e) => [e.operation, e.outcome, e.status]),
    ).toEqual([
      [1, "start", null],
      [1, "response", 500],
      [1, "finished", 500],
      [2, "start", null],
      [2, "transport-error", null],
      [3, "start", null],
      [3, "response", 200],
      [3, "transport-error", 200],
    ]);
    expect(h.read().events[3]).toMatchObject({
      method: "POST",
      route: "/api/access/check",
    });
    expect(readFileSync(h.file, "utf8")).not.toContain(canary);
    stop();
    p.emit("request", a);
    expect(h.read().events).toHaveLength(8);
    for (const name of [
      "request",
      "response",
      "requestfinished",
      "requestfailed",
    ])
      expect((p as unknown as EventEmitter).listenerCount(name)).toBe(0);
  });
  it("fails closed for every event/snapshot field, unknown key, invalid enum and number", async () => {
    const h = await setup();
    const original = event();
    for (const field of [
      ...Object.keys(original),
      "headers",
      "body",
      "secret",
    ]) {
      const bad = { ...original, [field]: canary };
      expect(h.validRequestEvent(bad as SafeRequestEvent)).toBe(false);
      h.recordRequestEvent(bad as SafeRequestEvent);
    }
    for (const field of [
      "operation",
      "attempt",
      "at_ms",
      "elapsed_ms",
      "status",
    ])
      for (const value of [true, -1, NaN, Infinity, 100000000, 1.5, [], {}])
        expect(
          h.validRequestEvent({
            ...original,
            [field]: value,
          } as SafeRequestEvent),
        ).toBe(false);
    const step = {
      ...original,
      origin: "step",
      phase: "source-select",
      attempt: 0,
      method: "none",
      route: "unknown",
      snapshot: await h.safeSnapshot(page()),
    } as SafeRequestEvent;
    expect(h.validRequestEvent(step)).toBe(true);
    for (const field of [
      ...Object.keys(step.snapshot!),
      "text",
      "html",
      "value",
    ])
      expect(
        h.validRequestEvent({
          ...step,
          snapshot: { ...step.snapshot!, [field]: canary },
        }),
      ).toBe(false);
    for (const bad of [
      { attempt: 2, phase: "directory-get" },
      { outcome: "response" },
      { origin: "page-request", phase: "page-request", attempt: 1 },
      { outcome: "transport-error", error_class: "none" },
      { outcome: "start", status: 500 },
    ])
      expect(
        h.validRequestEvent({ ...original, ...bad } as SafeRequestEvent),
      ).toBe(false);
    h.recordRequestEvent(original);
    expect(h.read().events).toEqual([original]);
    expect(readFileSync(h.file, "utf8")).not.toContain(canary);
  });
  it("bounds mixed encoded events and retains latest failure with explicit dropped count", async () => {
    const h = await setup();
    for (let i = 0; i < 1000; i++)
      h.recordRequestEvent({ ...event(), operation: i + 1 });
    h.recordRequestEvent({
      ...event(),
      operation: 1001,
      outcome: "transport-error",
      error_class: "socket_hang_up",
    });
    const capture = h.read();
    expect(capture.events.length).toBeLessThanOrEqual(256);
    expect(capture.dropped + capture.events.length).toBe(1001);
    expect(Buffer.byteLength(readFileSync(h.file))).toBeLessThanOrEqual(61472);
    expect(capture.events.at(-1)?.error_class).toBe("socket_hang_up");
  });
  it("closed pages, stalled snapshots and unwritable output cannot mask the original step failure", async () => {
    const h = await setup();
    const p = page();
    const error = new Error(canary);
    vi.spyOn(p, "isClosed").mockReturnValue(true);
    await expect(
      h.diagnosticStep(p, "version-switch", async () => {
        throw error;
      }),
    ).rejects.toBe(error);
    expect(p.evaluate).not.toHaveBeenCalled();
    expect(h.read().events.map((e) => e.outcome)).toEqual(["start", "failed"]);
    expect(h.read().events.at(-1)?.snapshot).toMatchObject({
      unavailable: true,
      page_closed: true,
      view: "unavailable",
    });
    vi.spyOn(p, "isClosed").mockReturnValue(false);
    vi.mocked(p.evaluate).mockReturnValue(new Promise(() => {}));
    vi.useFakeTimers();
    const snapshot = h.safeSnapshot(p);
    await vi.advanceTimersByTimeAsync(100);
    expect(await snapshot).toMatchObject({
      unavailable: true,
      page_closed: false,
    });
    vi.mocked(p.evaluate).mockRejectedValue(error);
    vi.stubEnv("ADTR_E2E_SAFE_DIAGNOSTICS", directory);
    await expect(
      h.diagnosticStep(p, "source-select", async () => {
        throw error;
      }),
    ).rejects.toBe(error);
    await expect(
      h.diagnosticGET(async () => {
        throw error;
      }, "/api/auth/me"),
    ).rejects.toBe(error);
  });
  it("handles late page evaluation rejection after the 100ms ceiling", async () => {
    const h = await setup();
    const p = page();
    vi.useFakeTimers();
    let reject!: (reason: unknown) => void;
    vi.mocked(p.evaluate).mockReturnValue(
      new Promise((_resolve, fail) => {
        reject = fail;
      }),
    );
    const result = h.safeSnapshot(p);
    await vi.advanceTimersByTimeAsync(100);
    expect(await result).toMatchObject({ unavailable: true });
    reject(new Error(canary));
    await vi.advanceTimersByTimeAsync(0);
    expect(vi.getTimerCount()).toBe(0);
  });
  it("hostile diagnostic getters and cleanup failures never replace the original result", async () => {
    const h = await setup();
    const original = new Error("original");
    Object.defineProperty(original, "message", {
      get() {
        throw new Error(canary);
      },
    });
    const captured = await h
      .diagnosticGET(async () => {
        throw original;
      }, "/api/auth/me")
      .catch((error) => error);
    expect(captured === original).toBe(true);
    expect(h.read().events.at(-1)?.error_class).toBe("unknown_error");
    const result = {
      status: () => {
        throw new Error(canary);
      },
    };
    expect(await h.diagnosticGET(async () => result, "/api/auth/me")).toBe(
      result,
    );
    const { fixtureGET } = await import("../e2e/fixture-get");
    const get = vi.fn().mockResolvedValue(result);
    expect(await fixtureGET({ get }, "/api/auth/me")).toBe(result);
    expect(get).toHaveBeenCalledTimes(1);
    const p = page();
    const stop = h.observeSafePageRequests(p);
    const off = vi.spyOn(p, "off").mockImplementation(() => {
      throw new Error(canary);
    });
    expect(stop).not.toThrow();
    expect(off).toHaveBeenCalledTimes(4);
    const before = h.read().events.length;
    p.emit("request", {
      url: () => "http://127.0.0.1/api/auth/me",
      method: () => "GET",
    });
    expect(h.read().events).toHaveLength(before);
    const on = vi.spyOn(p, "on").mockImplementation(() => {
      throw new Error(canary);
    });
    expect(() => h.observeSafePageRequests(p)).not.toThrow();
    expect(on).toHaveBeenCalledTimes(1);
    expect(off).toHaveBeenCalledTimes(8);
    vi.spyOn(p, "isClosed").mockImplementation(() => {
      throw new Error(canary);
    });
    expect(await h.safeSnapshot(p)).toMatchObject({
      unavailable: true,
      page_closed: false,
    });
    await expect(
      h
        .diagnosticStep(p, "source-select", async () => {
          throw original;
        })
        .catch((error) => error === original),
    ).resolves.toBe(true);
    expect(readFileSync(h.file, "utf8")).not.toContain(canary);
  });
});
