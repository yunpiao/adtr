// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { EventEmitter } from "node:events";
import { mkdtempSync, readFileSync, rmSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import type { Page, Request, Response } from "@playwright/test";
import type { SafeRequestEvent } from "../e2e/safe-request-diagnostics";
const canary = "CANARY-cookie-password-totp-url-body";
const route = "/api/credential-use/grant";
let directory = "";
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllEnvs();
  if (directory) rmSync(directory, { recursive: true, force: true });
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
async function setup() {
  vi.resetModules();
  directory = mkdtempSync(join(tmpdir(), "adtr-grant-submit-"));
  const file = join(directory, "browser.json");
  vi.stubEnv("ADTR_E2E_SAFE_DIAGNOSTICS", file);
  const page = new EventEmitter() as Page & EventEmitter;
  const read = () =>
    JSON.parse(readFileSync(file, "utf8")) as {
      events: SafeRequestEvent[];
      dropped: number;
    };
  return {
    file,
    page,
    read,
    ...(await import("../e2e/safe-request-diagnostics")),
  };
}
const request = (path = route) =>
  ({
    url: () => `http://127.0.0.1${path}?token=${canary}`,
    method: () => "POST",
    postData: () => {
      throw new Error(canary);
    },
  }) as unknown as Request;
const response = (r: Request, status: number) =>
  ({
    request: () => r,
    status: () => status,
    body: () => {
      throw new Error(canary);
    },
  }) as unknown as Response;

describe("credential grant wait/click evidence", () => {
  it.each([200, 401, 403, 429, 500])(
    "keeps response/status %s and registration-before-click ordering",
    async (status) => {
      const h = await setup();
      const pending = deferred<Response>();
      const calls: string[] = [];
      const req = request();
      const value = response(req, status);
      const wait = vi.fn(() => {
        calls.push("wait");
        expect(h.page.listenerCount("request")).toBe(1);
        return pending.promise;
      });
      const click = vi.fn(() => {
        calls.push("click");
        expect(h.read().events.map((e) => [e.phase, e.outcome])).toEqual([
          ["grant-wait", "start"],
          ["grant-wait", "registered"],
          ["grant-click", "start"],
        ]);
        h.page.emit("request", req);
        h.page.emit("response", value);
        h.page.emit("requestfinished", req);
        pending.resolve(value);
        return Promise.resolve();
      });
      const result = await h.diagnosticGrantSubmit(h.page, route, wait, click);
      expect(result).toEqual([value, undefined]);
      expect(result[0]).toBe(value);
      expect(result[0].status()).toBe(status);
      expect(calls).toEqual(["wait", "click"]);
      expect(wait).toHaveBeenCalledExactlyOnceWith();
      expect(click).toHaveBeenCalledExactlyOnceWith();
      const events = h.read().events;
      expect(
        events
          .filter((e) => e.origin === "page-request")
          .map((e) => [e.outcome, e.status]),
      ).toEqual([
        ["start", null],
        ["response", status],
        ["finished", status],
      ]);
      expect(
        events
          .filter((e) => e.origin === "step")
          .map((e) => [e.phase, e.outcome]),
      ).toEqual([
        ["grant-wait", "start"],
        ["grant-wait", "registered"],
        ["grant-click", "start"],
        ["grant-wait", "complete"],
        ["grant-click", "complete"],
      ]);
      expect(
        new Set(
          events.filter((e) => e.origin === "step").map((e) => e.operation),
        ).size,
      ).toBe(1);
      expect(readFileSync(h.file, "utf8")).not.toContain(canary);
      expect(h.page.eventNames()).toEqual([]);
    },
  );
  it.each([null, 200])(
    "distinguishes failed page request with status %s and preserves wait error",
    async (status) => {
      const h = await setup();
      const wait = deferred<Response>();
      const error = new Error(canary);
      const req = request();
      const result = h.diagnosticGrantSubmit(
        h.page,
        route,
        () => wait.promise,
        () => {
          h.page.emit("request", req);
          if (status !== null) h.page.emit("response", response(req, status));
          h.page.emit("requestfailed", req);
          return Promise.resolve();
        },
      );
      await Promise.resolve();
      wait.reject(error);
      await expect(result).rejects.toBe(error);
      expect(
        h.read().events.find((e) => e.outcome === "transport-error"),
      ).toMatchObject({ status, error_class: "page_request_failed" });
      expect(h.read().events.at(-1)).toMatchObject({
        phase: "grant-wait",
        outcome: "failed",
        status: null,
        snapshot: null,
      });
      expect(h.page.eventNames()).toEqual([]);
    },
  );
  it("records a pending wait and a settled click when no request was sent, without adding a timer", async () => {
    const h = await setup();
    const wait = deferred<Response>();
    const timer = vi.spyOn(globalThis, "setTimeout");
    const result = h.diagnosticGrantSubmit(
      h.page,
      route,
      () => wait.promise,
      () => Promise.resolve(),
    );
    await Promise.resolve();
    expect(h.read().events.at(-1)).toMatchObject({
      phase: "grant-click",
      outcome: "complete",
    });
    expect(h.read().events.every((e) => e.origin === "step")).toBe(true);
    expect(timer).not.toHaveBeenCalled();
    const error = new Error("original deadline");
    wait.reject(error);
    await expect(result).rejects.toBe(error);
  });
  it("preserves asynchronous click failure and handles late wait rejection without late records", async () => {
    const h = await setup();
    const wait = deferred<Response>();
    const error = new Error(canary);
    await expect(
      h.diagnosticGrantSubmit(
        h.page,
        route,
        () => wait.promise,
        () => Promise.reject(error),
      ),
    ).rejects.toBe(error);
    expect(h.read().events.at(-1)).toMatchObject({
      phase: "grant-click",
      outcome: "failed",
    });
    const count = h.read().events.length;
    wait.reject(new Error("late"));
    await Promise.resolve();
    await Promise.resolve();
    expect(h.read().events).toHaveLength(count);
    expect(h.page.eventNames()).toEqual([]);
  });
  it("preserves synchronous callback failures and never clicks if wait registration throws", async () => {
    const h = await setup();
    const error = new Error(canary);
    const click = vi.fn(() => Promise.resolve());
    await expect(
      h.diagnosticGrantSubmit(
        h.page,
        route,
        () => {
          throw error;
        },
        click,
      ),
    ).rejects.toBe(error);
    expect(click).not.toHaveBeenCalled();
    expect(h.read().events.at(-1)).toMatchObject({
      phase: "grant-wait",
      outcome: "failed",
    });
    const wait = deferred<Response>();
    await expect(
      h.diagnosticGrantSubmit(
        h.page,
        route,
        () => wait.promise,
        () => {
          throw error;
        },
      ),
    ).rejects.toBe(error);
    wait.reject(new Error("late"));
    await Promise.resolve();
    await Promise.resolve();
    expect(h.read().events.at(-1)).toMatchObject({
      phase: "grant-click",
      outcome: "failed",
    });
  });
  it("does not access response status, DOM, bodies, or unrelated routes", async () => {
    const h = await setup();
    const wait = deferred<{ status(): never }>();
    const result = {
      status: () => {
        throw new Error(canary);
      },
    };
    const observed = h.diagnosticGrantSubmit(
      h.page,
      route,
      () => wait.promise,
      () => {
        const unrelated = request("/api/auth/me");
        h.page.emit("request", unrelated);
        h.page.emit("response", response(unrelated, 200));
        h.page.emit("requestfinished", unrelated);
        wait.resolve(result);
        return Promise.resolve();
      },
    );
    expect((await observed)[0]).toBe(result);
    expect(
      h
        .read()
        .events.every(
          (e) =>
            e.origin === "step" && e.route === route && e.snapshot === null,
        ),
    ).toBe(true);
  });
  it.each(["unconfigured", "other-route"])(
    "leaves %s submits uninstrumented",
    async (mode) => {
      const h = await setup();
      if (mode === "unconfigured") vi.stubEnv("ADTR_E2E_SAFE_DIAGNOSTICS", "");
      const value = {};
      const wait = vi.fn(() => Promise.resolve(value));
      const click = vi.fn(() => Promise.resolve());
      expect(
        await h.diagnosticGrantSubmit(
          h.page,
          mode === "other-route" ? "/api/access/roles/save" : route,
          wait,
          click,
        ),
      ).toEqual([value, undefined]);
      expect(h.page.eventNames()).toEqual([]);
      expect(existsSync(h.file)).toBe(false);
      expect(wait).toHaveBeenCalledTimes(1);
      expect(click).toHaveBeenCalledTimes(1);
    },
  );
  it("write and detach failures do not replace an original error or response", async () => {
    const h = await setup();
    const off = vi.spyOn(h.page, "off").mockImplementation(() => {
      throw new Error(canary);
    });
    vi.stubEnv("ADTR_E2E_SAFE_DIAGNOSTICS", directory);
    const error = new Error(canary);
    await expect(
      h.diagnosticGrantSubmit(
        h.page,
        route,
        () => Promise.reject(error),
        () => Promise.resolve(),
      ),
    ).rejects.toBe(error);
    expect(off).toHaveBeenCalledTimes(4);
    const value = {};
    expect(
      (
        await h.diagnosticGrantSubmit(
          h.page,
          route,
          () => Promise.resolve(value),
          () => Promise.resolve(),
        )
      )[0],
    ).toBe(value);
    expect(off).toHaveBeenCalledTimes(8);
  });
  it("rejects unknown fields/enums and invalid cross-field combinations; Python accepts real output", async () => {
    const h = await setup();
    await h.diagnosticGrantSubmit(
      h.page,
      route,
      () => Promise.resolve({}),
      () => Promise.resolve(),
    );
    const base = h.read().events[1];
    for (const field of [...Object.keys(base), "body", "error", "dom", "value"])
      expect(
        h.validRequestEvent({ ...base, [field]: canary } as SafeRequestEvent),
      ).toBe(false);
    for (const changes of [
      { phase: "grant-click" },
      { origin: "page-request" },
      { method: "POST" },
      { attempt: 1 },
      { route: "/api/auth/me" },
      { status: 200 },
      { error_class: "timeout" },
      { snapshot: {} },
      { operation: 0 },
      { elapsed_ms: NaN },
      { at_ms: true },
    ])
      expect(
        h.validRequestEvent({ ...base, ...changes } as SafeRequestEvent),
      ).toBe(false);
    const result = spawnSync(
      "python3",
      [
        "-c",
        `import json,sys
from e2e_runtime_diagnostics import RuntimeDiagnostics,validate_report
r=RuntimeDiagnostics().report
r['browser']=json.load(open(sys.argv[1]))['events']
validate_report(r)
`,
        h.file,
      ],
      {
        cwd: fileURLToPath(new URL("../../scripts", import.meta.url)),
        encoding: "utf8",
      },
    );
    expect(result.status, result.stderr).toBe(0);
  });
  it("retains bounded latest submit evidence with explicit drops", async () => {
    const h = await setup();
    for (let i = 0; i < 300; i++)
      await h.diagnosticGrantSubmit(
        h.page,
        route,
        () => Promise.resolve({}),
        () => Promise.resolve(),
      );
    const capture = h.read();
    expect(capture.events.length).toBeLessThanOrEqual(256);
    expect(capture.dropped + capture.events.length).toBe(1500);
    expect(Buffer.byteLength(readFileSync(h.file))).toBeLessThan(65536);
    expect(capture.events.at(-1)).toMatchObject({
      phase: "grant-click",
      outcome: "complete",
    });
  });
  it("does not expand the existing unscoped v3 observer to grant requests", async () => {
    const h = await setup();
    const stop = h.observeSafePageRequests(h.page);
    const req = request();
    h.page.emit("request", req);
    h.page.emit("response", response(req, 200));
    h.page.emit("requestfinished", req);
    expect(existsSync(h.file)).toBe(false);
    stop();
  });
});
