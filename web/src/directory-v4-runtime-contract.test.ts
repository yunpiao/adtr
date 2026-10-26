import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { parseWorkerControlState, workerControl, safeScreenshots, v4ScreenshotTargetVisible, v4ScreenshotTargetStatus, v4CancelUnknownUIReady, recover, readNativeFocusIdentity, classifyIdentityJSONFailure, classifyIdentityRequestFailure, identityJSONFailureMessage, invariant, type Actor, type Original } from "../e2e/directory-v4-runtime";
import type { Page, Request, Response, TestInfo } from "@playwright/test";
import { EventEmitter } from "node:events";
import { runInNewContext } from "node:vm";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import ts from "typescript";
import { readJSON } from "../e2e/directory-v4-fixture";
import type { BrowserContext, APIResponse } from "@playwright/test";

describe("committed cancel query-only unknown UI barrier", () => {
  const notice = "结果尚未确认或响应未通过校验。请查询原操作；不会自动重新采集或取消。 请点击“重新核验并恢复”查询原回执。";
  function ready() {
    const root = document.createElement("section"), status = document.createElement("p"), button = document.createElement("button");
    status.setAttribute("role", "status"); status.textContent = notice;
    button.textContent = "重新核验并恢复"; root.append(status, button);
    return { root, status, button };
  }
  it("requires the real generic notice and enabled recovery rather than absence alone or cancelState text", () => {
    const { root, status, button } = ready();
    expect(v4CancelUnknownUIReady(root)).toBe(true);
    button.disabled = true; expect(v4CancelUnknownUIReady(root)).toBe(false); button.disabled = false;
    status.textContent = "取消请求的结果尚未确认，可能已提交；只查询原任务，不会重复发送取消。";
    expect(v4CancelUnknownUIReady(root)).toBe(false);
    status.remove(); expect(v4CancelUnknownUIReady(root)).toBe(false);
  });
  it("rejects remaining proof forms, private facts and any mutation or retry affordance", () => {
    for (const label of ["原操作恢复", "原任务状态", "本任务的原始组成员结果", "组原始成员详情", "字典 4 已保存来源"]) {
      const { root } = ready(), region = document.createElement("section"); region.setAttribute("aria-label", label); root.append(region);
      expect(v4CancelUnknownUIReady(root)).toBe(false);
    }
    for (const label of ["准备采集字典 4", "申请取消原任务", "确认提交采集", "确认取消原任务", "使用原键和原版本显式重试", "查看本任务的原始组成员结果", "确认结束原操作并清除恢复记录"]) {
      const { root } = ready(), button = document.createElement("button"); button.textContent = label; root.append(button);
      expect(v4CancelUnknownUIReady(root)).toBe(false);
    }
    for (const element of [document.createElement("form"), Object.assign(document.createElement("input"), { type: "password" }), Object.assign(document.createElement("input"), { inputMode: "numeric" })]) {
      const { root } = ready(); root.append(element); expect(v4CancelUnknownUIReady(root)).toBe(false);
    }
  });
});

describe("bounded safe reload diagnostics", () => {
  it("reports only main-document lifecycle enums and removes listeners after failure", async () => {
    const emitter = new EventEmitter(), frame = {}, secret = "private-query-proof";
    const request = { isNavigationRequest: () => true, resourceType: () => "document", frame: () => frame, url: () => { throw new Error(secret); } };
    const unrelated = { isNavigationRequest: () => false, resourceType: () => "fetch", frame: () => frame };
    const page = Object.assign(emitter, {
      mainFrame: () => frame,
      waitForResponse: () => Promise.reject(new Error("Receipt absent")),
      reload: async () => {
        emitter.emit("request", unrelated); emitter.emit("requestfailed", unrelated);
        emitter.emit("request", request);
        emitter.emit("response", { request: () => request, status: () => 503, headers: () => { throw new Error(secret); } });
        emitter.emit("requestfailed", request); emitter.emit("requestfinished", request);
        emitter.emit("close"); emitter.emit("crash");
        throw new Error("Reload withheld");
      },
    }) as unknown as Page;
    const log = vi.spyOn(console, "log").mockImplementation(() => {});
    try {
      await expect(recover(page, {} as Actor, {} as Original)).rejects.toThrow("Reload withheld");
      const messages = log.mock.calls.map(args => args.join(" "));
      expect(messages.filter(value => value === "[v4-recovery] navigation-request")).toHaveLength(1);
      expect(messages).toContain("[v4-recovery] navigation-response-5xx");
      expect(messages).toContain("[v4-recovery] navigation-failed");
      expect(messages.every(value => /^\[v4-recovery\] (start|receipt-not-observed|navigation-request|navigation-response-5xx|navigation-failed|navigation-finished|page-closed|page-crashed)$/u.test(value))).toBe(true);
      expect(messages.join(" ")).not.toContain(secret);
      expect(emitter.eventNames()).toEqual([]);
    } finally { log.mockRestore(); }
  });
});

describe("targeted v4 safe screenshot ordering", () => {
  it("rejects header occlusion, clipping and foreign overlays using measured geometry and hit tests", () => {
    const header = document.createElement("header"), node = document.createElement("p"), child = document.createElement("span"), overlay = document.createElement("div");
    header.className = "soc-topbar"; node.style.visibility = "visible"; child.textContent = "Expected original label";
    node.append(child); document.body.append(header, node, overlay);
    let top = 80, headerBottom = 70, foreignHit = false, clippedText = false, clippedChild = false;
    const prior = Object.getOwnPropertyDescriptor(document, "elementFromPoint");
    // Rounded control corners legitimately hit the parent, not the control.
    Object.defineProperty(document, "elementFromPoint", { configurable: true, value: (x: number, y: number) => x < 20 || y < top + 10 || foreignHit ? overlay : clippedChild ? node : child });
    vi.spyOn(header, "getBoundingClientRect").mockImplementation(() => ({ bottom: headerBottom }) as DOMRect);
    vi.spyOn(node, "getBoundingClientRect").mockImplementation(() => ({ top, bottom: top + 40, left: 10, right: 210, width: 200, height: 40 }) as DOMRect);
    vi.spyOn(document, "createRange").mockImplementation(() => ({ selectNodeContents: () => {}, getClientRects: () => [
      { top: top + 10, bottom: top + 30, left: 30, right: clippedText ? window.innerWidth + 1 : 190, width: clippedText ? window.innerWidth : 160, height: 20 },
    ] }) as unknown as Range);
    try {
      expect(v4ScreenshotTargetVisible(node)).toBe(true);
      expect(document.elementFromPoint(12, top + 2)).toBe(overlay);
      top = 50; expect(v4ScreenshotTargetVisible(node)).toBe(false);
      top = 80; headerBottom = 90; expect(v4ScreenshotTargetVisible(node)).toBe(false);
      headerBottom = 70; foreignHit = true; expect(v4ScreenshotTargetStatus(node)).toBe("text_occluded");
      foreignHit = false; clippedChild = true; expect(v4ScreenshotTargetStatus(node)).toBe("text_occluded");
      clippedChild = false; child.style.visibility = "hidden"; expect(v4ScreenshotTargetStatus(node)).toBe("style_hidden");
      child.style.visibility = "visible";
      foreignHit = false; clippedText = true; expect(v4ScreenshotTargetStatus(node)).toBe("text_bounds");
      clippedText = false;
      foreignHit = false; top = window.innerHeight - 20; expect(v4ScreenshotTargetVisible(node)).toBe(false);
      top = 80; header.remove(); expect(v4ScreenshotTargetVisible(node)).toBe(false);
    } finally {
      header.remove(); node.remove(); overlay.remove(); vi.restoreAllMocks();
      if (prior) Object.defineProperty(document, "elementFromPoint", prior);
      else Reflect.deleteProperty(document, "elementFromPoint");
    }
  });
  function fixture(unsafe = false) {
    const events: string[] = [];
    const page = {
      setViewportSize: async ({ width, height }: { width: number; height: number }) => { events.push(`viewport:${width}:${height}`); },
      locator: () => ({ evaluateAll: async () => { events.push("proof-empty"); return true; } }),
      evaluate: async () => { events.push("body-storage-safe"); return { bodyClean: !unsafe, storageClean: true, storageAccessible: true }; },
      screenshot: async ({ fullPage }: { fullPage: boolean }) => { expect(fullPage).toBe(false); events.push("capture"); },
    } as unknown as Page;
    const info = { outputPath: (name: string) => name } as TestInfo;
    return { page, info, events };
  }
  it("repositions and validates each resized scene before immediate safety checks and capture", async () => {
    const { page, info, events } = fixture();
    await safeScreenshots(page, info, "original-result-values", [], async () => { events.push("target-validated"); });
    expect(events).toEqual(["viewport:1440:900", "target-validated", "proof-empty", "proof-empty", "body-storage-safe", "capture", "viewport:390:844", "target-validated", "proof-empty", "proof-empty", "body-storage-safe", "capture"]);
  });
  it("never captures a scene whose target assertion or safety guard fails", async () => {
    const first = fixture();
    await expect(safeScreenshots(first.page, first.info, "original-result-values", [], async () => { throw new Error("Target not visible"); })).rejects.toThrow("Target not visible");
    expect(first.events).not.toContain("capture");
    const second = fixture(true);
    await expect(safeScreenshots(second.page, second.info, "original-result-values", [], async () => {})).rejects.toThrow("Screenshot body/storage proof safety not established");
    expect(second.events).not.toContain("capture");
  });
});

describe("owned v4 Node controller boundary", () => {
  it("accepts only the closed C1 safe state grammar", () => {
    for (const value of [
      { state: "not_started", launchCount: 0, failure: null },
      { state: "starting", launchCount: 0, failure: null },
      { state: "starting", launchCount: 1, failure: null },
      { state: "ready", launchCount: 1, failure: null },
      { state: "failed", launchCount: 0, failure: "launch_failed" },
      { state: "closed", launchCount: 1, failure: "cleanup_failed" },
    ]) expect(parseWorkerControlState(JSON.stringify(value))).toEqual(value);
  });
  it("rejects unknown, duplicate, private, oversized and impossible states safely", () => {
    for (const text of [
      '{"state":"ready","launchCount":0,"failure":null}',
      '{"state":"not_started","launchCount":1,"failure":null}',
      '{"state":"ready","launchCount":1,"failure":"readiness_failed"}',
      '{"state":"failed","launchCount":1,"failure":null}',
      '{"state":"ready","state":"ready","launchCount":1,"failure":null}',
      '{"state":"ready","launchCount":1,"failure":null,"token":"private"}',
      '{"state":"ready","launchCount":2,"failure":null}',
      '{"state":"ready","launchCount":true,"failure":null}',
      '{"state":"ready","launchCount":1,"failure":"raw_exception"}',
      " ".repeat(2049), "null", "[]", "{",
    ]) expect(() => parseWorkerControlState(text)).toThrow("Invalid owned Worker controller state");
  });
  it("rejects non-owned endpoints before any HTTP and does not expose private values", () => {
    const oldURL = process.env.ADTR_E2E_V4_WORKER_CONTROL_URL;
    const oldToken = process.env.ADTR_E2E_V4_WORKER_CONTROL_TOKEN;
    try {
      process.env.ADTR_E2E_V4_WORKER_CONTROL_TOKEN = "a".repeat(43);
      for (const value of ["https://127.0.0.1:1234", "http://example.invalid:1234", "http://127.0.0.1:1234/start", "http://user:private@127.0.0.1:1234", "http://127.0.0.1:1234/?token=private"]) {
        process.env.ADTR_E2E_V4_WORKER_CONTROL_URL = value;
        expect(() => workerControl()).toThrow("Invalid private owned Worker controller");
      }
    } finally {
      if (oldURL === undefined) delete process.env.ADTR_E2E_V4_WORKER_CONTROL_URL; else process.env.ADTR_E2E_V4_WORKER_CONTROL_URL = oldURL;
      if (oldToken === undefined) delete process.env.ADTR_E2E_V4_WORKER_CONTROL_TOKEN; else process.env.ADTR_E2E_V4_WORKER_CONTROL_TOKEN = oldToken;
    }
  });
});

describe("eager original Playwright focus identity", () => {
  const canonical = "https://synthetic.invalid/api/auth/me", privateText = "private-profile-proof-canary";
  const rawError = () => Object.assign(new Error(privateText), { stack: privateText, cause: privateText });
  function deferred<T>() {
    let resolve!: (value: T) => void, reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
    return { promise, resolve, reject };
  }
  function harness() {
    const emitter = new EventEmitter(), snapshot = deferred<Request[]>(), action = deferred<void>();
    let onFocus = () => {};
    const focus = vi.fn(() => { onFocus(); return action.promise; }), requests = vi.fn(() => snapshot.promise);
    const forbidden = Array.from({ length: 7 }, () => vi.fn(() => { throw rawError(); }));
    const frame = { page: (): Page => page };
    const page = Object.assign(emitter, {
      mainFrame: () => frame, url: () => "https://synthetic.invalid/#directory-v4", isClosed: () => false,
      requests, bringToFront: focus, evaluate: forbidden[0], addInitScript: forbidden[1], route: forbidden[2],
      request: { get: forbidden[3], fetch: forbidden[4] }, close: forbidden[5], context: forbidden[6],
    }) as unknown as Page;
    const start = () => {
      const result = readNativeFocusIdentity(page), settled = vi.fn();
      void result.then(settled, settled);
      return { result, settled };
    };
    return { emitter, snapshot, action, frame, page, focus, requests, forbidden, start, onFocus: (fn: () => void) => { onFocus = fn; } };
  }
  function request(h: ReturnType<typeof harness>, overrides: Record<string, unknown> = {}) {
    return Object.assign({
      url: () => canonical, method: () => "GET", resourceType: () => "fetch", isNavigationRequest: () => false,
      frame: () => h.frame, redirectedFrom: () => null, redirectedTo: () => null, serviceWorker: () => null,
      headers: () => ({ "x-adtr-identity-purpose": "app-passive-session" }),
      allHeaders: () => { throw rawError(); }, headerValue: () => { throw rawError(); }, headersArray: () => { throw rawError(); },
      postData: () => { throw rawError(); }, postDataJSON: () => { throw rawError(); },
    }, overrides) as unknown as Request;
  }
  function response(req: Request, overrides: Record<string, unknown> = {}) {
    const body = deferred<unknown>(), json = vi.fn(() => body.promise);
    const value = Object.assign({
      url: () => req.url(), request: () => req, status: () => 200, fromServiceWorker: () => false, json,
      headerValue: () => { throw rawError(); }, headers: () => { throw rawError(); },
      body: () => { throw rawError(); }, text: () => { throw rawError(); }, finished: () => { throw rawError(); },
    }, overrides) as unknown as Response;
    return { value, json, body };
  }
  async function flush() { for (let index = 0; index < 6; index++) await Promise.resolve(); }
  async function arm(h: ReturnType<typeof harness>, old: Request[] = []) { h.snapshot.resolve(old); await flush(); }
  function clean(h: ReturnType<typeof harness>) {
    expect(h.emitter.eventNames()).toEqual([]); expect(vi.getTimerCount()).toBe(0);
    expect(h.requests).toHaveBeenCalledTimes(1);
    for (const call of h.forbidden) expect(call).not.toHaveBeenCalled();
  }
  beforeEach(() => { vi.useFakeTimers(); vi.spyOn(console, "log").mockImplementation(() => {}); });
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

  function diagnostic() {
    const calls = vi.mocked(console.log).mock.calls;
    expect(calls).toHaveLength(1); expect(calls[0]).toHaveLength(1);
    const text = String(calls[0][0]); expect(text).toMatch(/^\[v4-focus-identity\] /u);
    expect(text).not.toContain(privateText); expect(text.length).toBeLessThan(4096);
    const value = JSON.parse(text.slice("[v4-focus-identity] ".length));
    expect(Object.keys(value).sort()).toEqual(["schema", "phase", "failure", "jsonFailureClass", "events", "eventsDropped", "diagnosticFault", "cleanupFailed", "selected", "selectedStatus", "selectedRequest", "focusSettledWhenJSONRejected", "laterMatchingResponses", "repeatedSelectedResponses", "observedRequests", "counterWindow", "requestTerminalWindow", "eventWindow"].sort());
    expect(value.counterWindow).toBe("selected_until_first_terminal_state");
    expect(value.requestTerminalWindow).toBe("observed_request_until_first_terminal_state");
    expect(value.eventWindow).toBe("prepare_until_cleanup");
    expect(value.selected).toBe(value.selectedRequest !== null);
    expect(value.selectedStatus).toBe(value.selected ? 200 : null);
    expect(value.events.length).toBeLessThanOrEqual(32);
    for (const event of value.events) {
      expect(Object.keys(event).sort()).toEqual(["atMs", "event"]);
      expect(Number.isInteger(event.atMs)).toBe(true); expect(event.atMs).toBeGreaterThanOrEqual(0); expect(event.atMs).toBeLessThanOrEqual(15_000);
      expect(["prepare", "armed", "focus_call", "focus_resolved", "focus_rejected", "selected", "json_call", "json_resolved", "json_rejected", "request_finished", "request_failed", "first_failure", "success", "cleanup", "old_request", "fresh_request"]).toContain(event.event);
    }
    return value;
  }
  it.each([false, true])("records exact selected terminal and JSON order without awaiting finish (focus first=%s)", async focusFirst => {
    const h = harness(), failure = vi.fn(() => ({ errorText: "net::ERR_ABORTED", private: privateText }));
    const req = request(h, { failure }), other = request(h), selected = response(req), { result } = h.start(); await arm(h);
    if (focusFirst) { h.action.resolve(); await flush(); }
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    expect(selected.json).toHaveBeenCalledTimes(1); expect(console.log).not.toHaveBeenCalled();
    h.emitter.emit("requestfailed", other); expect(failure).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(7); h.emitter.emit("requestfinished", req);
    selected.body.reject(new Error("response.json: Protocol error (Network.getResponseBody): No data found for resource with given identifier")); await flush();
    h.emitter.emit("requestfailed", req); if (!focusFirst) h.action.resolve();
    await expect(result).rejects.toThrow("json_failure_class=cdp_body_no_data");
    const report = diagnostic(); expect(report.selectedRequest).toEqual({ finishedAtMs: 7, failedAtMs: null, failureClass: "not_observed" });
    expect(report.focusSettledWhenJSONRejected).toBe(focusFirst); expect(report.observedRequests).toEqual({ old: 0, fresh: 1 });
    const order = report.events.map((event: { event: string }) => event.event);
    expect(order.indexOf("armed")).toBeLessThan(order.indexOf("focus_call"));
    expect(order.indexOf("fresh_request")).toBeLessThan(order.indexOf("selected"));
    expect(order.indexOf("selected") + 1).toBe(order.indexOf("json_call"));
    expect(order.indexOf("request_finished")).toBeLessThan(order.indexOf("json_rejected"));
    expect(failure).not.toHaveBeenCalled(); clean(h);
  });
  it("observes exact Request failure safely without changing a successful original body", async () => {
    const h = harness(), failure = vi.fn(() => ({ errorText: "net::ERR_ABORTED" })), req = request(h, { failure });
    const selected = response(req), { result } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value); h.emitter.emit("requestfailed", req);
    selected.body.resolve({ ID: 42, profile: privateText }); h.action.resolve(); await expect(result).resolves.toBe(42);
    expect(diagnostic().selectedRequest).toEqual({ finishedAtMs: null, failedAtMs: 0, failureClass: "aborted" });
    expect(failure).toHaveBeenCalledTimes(1); clean(h);
  });
  it("counts only live matching responses and never reads a later body or post-failure metadata", async () => {
    const h = harness(), req = request(h), selected = response(req), laterReq = request(h), later = response(laterReq), { result } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    h.emitter.emit("response", selected.value); h.emitter.emit("request", laterReq); h.emitter.emit("response", later.value);
    selected.body.reject(rawError()); await flush();
    const url = vi.fn(() => { throw rawError(); }); h.emitter.emit("response", response(laterReq, { url }).value);
    h.action.resolve(); await expect(result).rejects.toThrow("identity_response_json_failed");
    const report = diagnostic(); expect(report.laterMatchingResponses).toBe(2); expect(report.repeatedSelectedResponses).toBe(1);
    expect(report.observedRequests).toEqual({ old: 0, fresh: 2 }); expect(later.json).not.toHaveBeenCalled(); expect(url).not.toHaveBeenCalled(); clean(h);
  });
  it("leaves absent host terminal events explicitly unobserved and never waits for them", async () => {
    const h = harness(), req = request(h), selected = response(req), { result } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value); selected.body.resolve({ ID: 42 }); h.action.resolve();
    await expect(result).resolves.toBe(42); expect(diagnostic().selectedRequest).toEqual({ finishedAtMs: null, failedAtMs: null, failureClass: "not_observed" }); clean(h);
  });
  it("reports no selected status when preparation fails before a candidate", async () => {
    const h = harness(), { result } = h.start(); h.snapshot.reject(rawError());
    await expect(result).rejects.toThrow("identity_prepare_failed");
    const report = diagnostic(); expect(report.selected).toBe(false); expect(report.selectedStatus).toBeNull(); clean(h);
  });
  it("bounds event floods while keeping counts and the original single body read", async () => {
    const h = harness(), req = request(h), selected = response(req), { result } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    for (let i = 0; i < 100; i++) h.emitter.emit("requestfinished", req);
    selected.body.resolve({ ID: 42 }); h.action.resolve(); await expect(result).resolves.toBe(42);
    const report = diagnostic(); expect(report.events).toHaveLength(32); expect(report.eventsDropped).toBeGreaterThan(0);
    expect(selected.json).toHaveBeenCalledTimes(1); clean(h);
  });
  it.each(["projection", "install", "remove", "log"])("diagnostic %s failure cannot change the original result", async source => {
    const h = harness(), req = request(h, { failure: () => { throw rawError(); } }), selected = response(req);
    if (source === "install") {
      const on = h.emitter.on.bind(h.emitter); vi.spyOn(h.emitter, "on").mockImplementation((event, listener) => { if (event === "requestfailed") throw rawError(); return on(event, listener); });
    }
    if (source === "remove") {
      const off = h.emitter.off.bind(h.emitter); vi.spyOn(h.emitter, "off").mockImplementation((event, listener) => { const value = off(event, listener); if (event === "requestfailed") throw rawError(); return value; });
    }
    if (source === "log") vi.mocked(console.log).mockImplementation(() => { throw rawError(); });
    const { result } = h.start(); await arm(h); h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    if (source === "projection") h.emitter.emit("requestfailed", req);
    selected.body.reject(new SyntaxError(privateText)); h.action.resolve();
    await expect(result).rejects.toThrow(/^identity_response_json_failed; json_failure_class=json_syntax_error$/u);
    if (source !== "log") expect(diagnostic().diagnosticFault).toBe(true);
    expect(selected.json).toHaveBeenCalledTimes(1); clean(h);
  });

  it("calls exactly the selected json synchronously during dispatch before pending focus completes", async () => {
    const h = harness(), req = request(h), selected = response(req), { result, settled } = h.start();
    h.onFocus(() => {
      h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
      expect(selected.json).toHaveBeenCalledTimes(1);
      h.emitter.emit("response", selected.value); expect(selected.json).toHaveBeenCalledTimes(1);
    });
    await arm(h);
    selected.body.resolve({ ID: 41, csrfToken: privateText, profile: privateText }); await flush();
    expect(settled).not.toHaveBeenCalled(); expect(h.focus).toHaveBeenCalledTimes(1);
    h.action.resolve(); await expect(result).resolves.toBe(41); clean(h);
  });
  it("also waits for the selected body when the original focus action fulfils first", async () => {
    const h = harness(), req = request(h), selected = response(req), { result, settled } = h.start();
    await arm(h); h.action.resolve(); await flush();
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value); await flush();
    expect(settled).not.toHaveBeenCalled(); selected.body.resolve({ ID: 42 });
    await expect(result).resolves.toBe(42); expect(selected.json).toHaveBeenCalledTimes(1); clean(h);
  });
  it("locks response-event order despite request order and cannot replace a wrong numeric ID", async () => {
    const h = harness(), firstReq = request(h), secondReq = request(h);
    const earlierResponse = response(secondReq), laterResponse = response(firstReq), { result } = h.start();
    await arm(h); h.emitter.emit("request", firstReq); h.emitter.emit("request", secondReq);
    h.emitter.emit("response", earlierResponse.value); h.emitter.emit("response", laterResponse.value);
    laterResponse.body.resolve({ ID: 42 }); earlierResponse.body.resolve({ ID: 7 }); h.action.resolve();
    const checkedID = await result;
    expect(checkedID).toBe(7); expect(earlierResponse.json).toHaveBeenCalledTimes(1); expect(laterResponse.json).not.toHaveBeenCalled();
    expect(() => invariant(checkedID === 42, "Native focus did not recheck actual changed actor"))
      .toThrow("Native focus did not recheck actual changed actor"); clean(h);
  });
  it.each(["snapshot", "request", "response"])("keeps %s objects OLD even after duplicate post-arm request events", async mode => {
    const h = harness(), oldReq = request(h, { method: () => "POST" }), oldResponse = response(oldReq);
    const newReq = request(h), selected = response(newReq), { result } = h.start();
    if (mode === "request") h.emitter.emit("request", oldReq);
    if (mode === "response") h.emitter.emit("response", oldResponse.value);
    await arm(h, mode === "snapshot" ? [oldReq] : []);
    h.emitter.emit("request", oldReq); h.emitter.emit("response", oldResponse.value);
    h.emitter.emit("request", newReq); h.emitter.emit("response", selected.value);
    selected.body.resolve({ ID: 42 }); h.action.resolve();
    await expect(result).resolves.toBe(42); expect(oldResponse.json).not.toHaveBeenCalled(); clean(h);
  });
  it.each([0, 100])("fails an unknown first 200 after a %i-record snapshot instead of treating it as complete", async count => {
    const h = harness(), unknown = response(request(h)), laterReq = request(h), later = response(laterReq), { result } = h.start();
    await arm(h, Array.from({ length: count }, () => request(h)));
    h.emitter.emit("response", unknown.value); h.emitter.emit("request", laterReq); h.emitter.emit("response", later.value);
    h.action.resolve(); await expect(result).rejects.toThrow(/^identity_request_ambiguous$/u);
    expect(unknown.json).not.toHaveBeenCalled(); expect(later.json).not.toHaveBeenCalled(); clean(h);
  });
  it("only fences host-observed objects, not an unobserved native fetch's initiation time", async () => {
    const h = harness(), absentFromSnapshot = request(h), selected = response(absentFromSnapshot), { result } = h.start();
    await arm(h, Array.from({ length: 100 }, () => request(h)));
    // The helper cannot tell when this request was initiated natively. This is
    // FRESH only by its first host event, not a claim of focus-exclusive causation.
    h.emitter.emit("request", absentFromSnapshot); h.emitter.emit("response", selected.value);
    selected.body.resolve({ ID: 42 }); h.action.resolve(); await expect(result).resolves.toBe(42); clean(h);
  });
  it.each(["path", "query", "origin", "fragment", "request-url", "response-url", "method", "resource", "navigation", "frame", "page", "redirect-from", "redirect-to", "request-worker", "response-worker"])
    ("fails the first non-OLD candidate's %s metadata without choosing a later body", async kind => {
      const h = harness(), reqOverrides: Record<string, unknown> = {}, responseOverrides: Record<string, unknown> = {};
      if (kind === "path") reqOverrides.url = () => "https://synthetic.invalid/api/other";
      if (kind === "query") reqOverrides.url = () => canonical + "?private=" + privateText;
      if (kind === "origin") reqOverrides.url = () => "https://other.invalid/api/auth/me";
      // A fake visible fragment is malformed host metadata only. Playwright
      // strips real input fragments before these host observations.
      if (kind === "fragment") reqOverrides.url = () => canonical + "#" + privateText;
      if (kind === "request-url") { reqOverrides.url = () => canonical + "?different=1"; responseOverrides.url = () => canonical; }
      if (kind === "response-url") responseOverrides.url = () => canonical + "?different=1";
      if (kind === "method") reqOverrides.method = () => "POST";
      if (kind === "resource") reqOverrides.resourceType = () => "xhr";
      if (kind === "navigation") reqOverrides.isNavigationRequest = () => true;
      if (kind === "frame") reqOverrides.frame = () => ({ page: () => h.page });
      if (kind === "page") h.frame.page = () => ({}) as Page;
      if (kind === "redirect-from") reqOverrides.redirectedFrom = () => ({});
      if (kind === "redirect-to") reqOverrides.redirectedTo = () => ({});
      if (kind === "request-worker") reqOverrides.serviceWorker = () => ({});
      if (kind === "response-worker") responseOverrides.fromServiceWorker = () => true;
      const badReq = request(h, reqOverrides), bad = response(badReq, responseOverrides), goodReq = request(h), good = response(goodReq);
      const { result } = h.start(); await arm(h);
      h.emitter.emit("request", badReq); h.emitter.emit("request", goodReq);
      h.emitter.emit("response", bad.value); h.frame.page = () => h.page;
      h.emitter.emit("response", good.value); h.action.resolve();
      await expect(result).rejects.toThrow(/^identity_response_metadata$/u);
      expect(bad.json).not.toHaveBeenCalled(); expect(good.json).not.toHaveBeenCalled(); clean(h);
    });
  it("ignores unrelated paths and non-200 responses, not a malformed first 200", async () => {
    const h = harness(), unrelated = response(request(h, { url: () => "https://synthetic.invalid/api/other", headers: () => ({}) }));
    const non200 = response(request(h), { status: () => 403 }), req = request(h), selected = response(req), { result } = h.start();
    await arm(h); h.emitter.emit("response", unrelated.value); h.emitter.emit("response", non200.value);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    selected.body.resolve({ ID: 42 }); h.action.resolve(); await expect(result).resolves.toBe(42);
    expect(unrelated.json).not.toHaveBeenCalled(); expect(non200.json).not.toHaveBeenCalled(); clean(h);
  });
  it.each(["before", "after"])("ignores an unmarked Workspace body/abort %s the selected App response without consuming it", async order => {
    const h = harness(), unmarked = request(h, { headers: () => ({}) }), workspace = response(unmarked), app = request(h), selected = response(app), { result } = h.start();
    await arm(h); h.emitter.emit("request", unmarked); h.emitter.emit("request", app);
    if (order === "before") { h.emitter.emit("response", workspace.value); h.emitter.emit("requestfailed", unmarked); }
    h.emitter.emit("response", selected.value); expect(selected.json).toHaveBeenCalledTimes(1);
    if (order === "after") { h.emitter.emit("response", workspace.value); h.emitter.emit("requestfailed", unmarked); }
    workspace.body.resolve({ ID: 7 }); selected.body.resolve({ ID: 42 }); h.action.resolve();
    await expect(result).resolves.toBe(42); expect(workspace.json).not.toHaveBeenCalled();
    expect(diagnostic().selectedRequest).toEqual({ finishedAtMs: null, failedAtMs: null, failureClass: "not_observed" }); clean(h);
  });
  it("does not rescue the first marked body's own abort with an unmarked or later marked success", async () => {
    const h = harness(), first = request(h), selected = response(first), unmarked = response(request(h, { headers: () => ({}) })), laterReq = request(h), later = response(laterReq), { result } = h.start();
    await arm(h); h.emitter.emit("request", first); h.emitter.emit("response", selected.value);
    h.emitter.emit("response", unmarked.value); h.emitter.emit("request", laterReq); h.emitter.emit("response", later.value);
    unmarked.body.resolve({ ID: 42 }); later.body.resolve({ ID: 42 });
    selected.body.reject(new Error("response.json: Protocol error (Network.getResponseBody): No data found for resource with given identifier")); h.action.resolve();
    await expect(result).rejects.toThrow("json_failure_class=cdp_body_no_data");
    expect(selected.json).toHaveBeenCalledTimes(1); expect(unmarked.json).not.toHaveBeenCalled(); expect(later.json).not.toHaveBeenCalled(); clean(h);
  });
  it("keeps the original deadline when every response is unmarked instead of falling back", async () => {
    const h = harness(), req = request(h, { headers: () => ({}) }), unmarked = response(req), { result } = h.start();
    await arm(h); h.emitter.emit("request", req); h.emitter.emit("response", unmarked.value); h.action.resolve();
    const rejection = expect(result).rejects.toThrow(/^identity_deadline$/u);
    await vi.advanceTimersByTimeAsync(15_000); await rejection;
    expect(unmarked.json).not.toHaveBeenCalled(); expect(diagnostic().selected).toBe(false); clean(h);
  });
  it.each(["empty", "unknown", "case", "space", "duplicate", "number", "boolean", "null", "object", "accessor", "inherited", "headers-throw", "descriptor-throw", "has-throw", "null-headers", "array-headers"])
    ("rejects present-invalid purpose %s before selecting a later original body", async kind => {
      const h = harness(), getter = vi.fn(() => "app-passive-session");
      const values: Record<string, unknown> = { empty: "", unknown: "workspace-session", case: "APP-PASSIVE-SESSION", space: " app-passive-session", duplicate: "app-passive-session, app-passive-session", number: 1, boolean: true, null: null, object: {} };
      let headers: unknown = { "x-adtr-identity-purpose": values[kind] };
      if (kind === "accessor") headers = Object.defineProperty({}, "x-adtr-identity-purpose", { get: getter });
      if (kind === "inherited") headers = Object.create({ "x-adtr-identity-purpose": "app-passive-session" });
      if (kind === "descriptor-throw") headers = new Proxy({}, { getOwnPropertyDescriptor: () => { throw rawError(); } });
      if (kind === "has-throw") headers = new Proxy({}, { has: () => { throw rawError(); } });
      if (kind === "null-headers") headers = null;
      if (kind === "array-headers") headers = [];
      const req = request(h, { headers: () => { if (kind === "headers-throw") throw rawError(); return headers; } }), bad = response(req), goodReq = request(h), good = response(goodReq), { result } = h.start();
      await arm(h); h.emitter.emit("request", req); h.emitter.emit("response", bad.value); h.emitter.emit("request", goodReq); h.emitter.emit("response", good.value); h.action.resolve();
      await expect(result).rejects.toThrow(/^identity_response_metadata$/u); expect(bad.json).not.toHaveBeenCalled(); expect(good.json).not.toHaveBeenCalled(); expect(getter).not.toHaveBeenCalled(); diagnostic(); clean(h);
    });
  it("checks declared purpose and canonical metadata before ignoring a non200 response", async () => {
    const h = harness(), bad = response(request(h, { url: () => "https://synthetic.invalid/api/other" }), { status: () => 403 }), { result } = h.start();
    await arm(h); h.emitter.emit("response", bad.value); h.action.resolve();
    await expect(result).rejects.toThrow(/^identity_response_metadata$/u); expect(bad.json).not.toHaveBeenCalled(); clean(h);
  });
  it("reads only the own fixed marker value, not private header getters or asynchronous header APIs", async () => {
    const h = harness(), other = vi.fn(() => { throw rawError(); });
    const headers = Object.defineProperty({ "x-adtr-identity-purpose": "app-passive-session" }, "cookie", { get: other });
    const req = request(h, { headers: () => headers, allHeaders: other, headerValue: other, headersArray: other }), selected = response(req), { result } = h.start();
    await arm(h); h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    expect(selected.json).toHaveBeenCalledTimes(1); expect(other).not.toHaveBeenCalled();
    selected.body.resolve({ ID: 42 }); h.action.resolve(); await expect(result).resolves.toBe(42); diagnostic(); clean(h);
  });
  it("rejects a conflicting Response for the locked Request and never reads its replacement", async () => {
    const h = harness(), req = request(h), selected = response(req), conflicting = response(req, { status: () => 201 });
    const readID = vi.fn(() => ({ value: 42, configurable: true })), { result, settled } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value); h.emitter.emit("response", conflicting.value);
    h.action.resolve(); await flush(); expect(settled).not.toHaveBeenCalled();
    selected.body.resolve(new Proxy({}, { getOwnPropertyDescriptor: readID }));
    await expect(result).rejects.toThrow(/^identity_request_ambiguous$/u);
    expect(readID).not.toHaveBeenCalled(); expect(selected.json).toHaveBeenCalledTimes(1); expect(conflicting.json).not.toHaveBeenCalled(); clean(h);
  });
  it.each(["request", "response", "close", "crash", "framenavigated"])("cleans every completed registration when installing %s throws", async event => {
    const h = harness(), originalOn = h.emitter.on.bind(h.emitter);
    vi.spyOn(h.emitter, "on").mockImplementation((name, listener) => { if (name === event) throw rawError(); return originalOn(name, listener); });
    const { result } = h.start(); await expect(result).rejects.toThrow(/^identity_prepare_failed$/u);
    expect(h.focus).not.toHaveBeenCalled(); expect(h.requests).not.toHaveBeenCalled();
    expect(h.emitter.eventNames()).toEqual([]); expect(vi.getTimerCount()).toBe(0);
  });
  it.each(["reject", "throw"])("safely handles snapshot %s without invoking focus", async mode => {
    const h = harness(); if (mode === "throw") h.requests.mockImplementation(() => { throw rawError(); });
    const { result } = h.start(); if (mode === "reject") h.snapshot.reject(rawError());
    await expect(result).rejects.toThrow(/^identity_prepare_failed$/u); expect(h.focus).not.toHaveBeenCalled(); clean(h);
  });
  it("cleans up a setup metadata failure before any listener is installed", async () => {
    const h = harness(); vi.spyOn(h.page, "mainFrame").mockImplementation(() => { throw rawError(); });
    const { result } = h.start(); await expect(result).rejects.toThrow(/^identity_prepare_failed$/u);
    expect(h.requests).not.toHaveBeenCalled(); expect(h.focus).not.toHaveBeenCalled();
    expect(h.emitter.eventNames()).toEqual([]); expect(vi.getTimerCount()).toBe(0);
  });
  it("fails an already closed page before starting the snapshot or action", async () => {
    const h = harness(); vi.spyOn(h.page, "isClosed").mockReturnValue(true);
    const { result } = h.start(); await expect(result).rejects.toThrow(/^identity_page_changed$/u);
    expect(h.requests).not.toHaveBeenCalled(); expect(h.focus).not.toHaveBeenCalled();
    expect(h.emitter.eventNames()).toEqual([]); expect(vi.getTimerCount()).toBe(0);
  });
  it.each(["resolve", "reject"])("bounds a never-settling snapshot and discards its late %s without inspecting data", async mode => {
    const h = harness(), { result } = h.start(), inspect = vi.fn(() => { throw rawError(); });
    await vi.advanceTimersByTimeAsync(15_000); await expect(result).rejects.toThrow(/^identity_deadline$/u);
    if (mode === "resolve") h.snapshot.resolve(new Proxy([] as Request[], { get: (target, key, receiver) => {
      if (key === Symbol.iterator) inspect(); return Reflect.get(target, key, receiver);
    } }));
    else h.snapshot.reject(rawError());
    await flush(); expect(inspect).not.toHaveBeenCalled(); expect(h.focus).not.toHaveBeenCalled(); clean(h);
  });
  it.each(["body-only", "focus-only", "body-first", "focus-first"])("preserves the first safe failure and joins bounded outcomes: %s", async mode => {
    const h = harness(), req = request(h), selected = response(req), { result, settled } = h.start();
    await arm(h); h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    const bodyFirst = mode === "body-only" || mode === "body-first";
    if (bodyFirst) selected.body.reject(rawError()); else h.action.reject(rawError());
    await flush(); expect(settled).not.toHaveBeenCalled();
    if (mode === "body-only") h.action.resolve();
    else if (mode === "focus-only") selected.body.resolve({ ID: 42, csrfToken: privateText });
    else if (bodyFirst) h.action.reject(rawError()); else selected.body.reject(rawError());
    await expect(result).rejects.toThrow(bodyFirst ? /^identity_response_json_failed; json_failure_class=unknown$/u : /^identity_focus_failed$/u); clean(h);
  });
  it("fails an action rejection even after successful JSON, without exporting the ID", async () => {
    const h = harness(), req = request(h), selected = response(req), { result, settled } = h.start();
    await arm(h); h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    selected.body.resolve({ ID: 42 }); await flush(); expect(settled).not.toHaveBeenCalled();
    h.action.reject(rawError()); await expect(result).rejects.toThrow(/^identity_focus_failed$/u); clean(h);
  });
  it("fails a JSON rejection even after successful action, without reading a later response", async () => {
    const h = harness(), req = request(h), selected = response(req), laterReq = request(h), later = response(laterReq), { result } = h.start();
    await arm(h); h.action.resolve(); await flush(); h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    selected.body.reject(rawError()); await flush(); h.emitter.emit("request", laterReq); h.emitter.emit("response", later.value);
    await expect(result).rejects.toThrow(/^identity_response_json_failed; json_failure_class=unknown$/u); expect(later.json).not.toHaveBeenCalled(); clean(h);
  });
  it.each(["focus", "json"])("catches a synchronous %s throw with no raw error/cause", async source => {
    const h = harness(), req = request(h), selected = response(req);
    if (source === "focus") h.focus.mockImplementation(() => { throw rawError(); });
    else selected.json.mockImplementation(() => { throw rawError(); });
    const { result } = h.start(); await arm(h);
    if (source === "json") { h.emitter.emit("request", req); h.emitter.emit("response", selected.value); h.action.resolve(); }
    await expect(result).rejects.toThrow(source === "focus" ? /^identity_focus_failed$/u : /^identity_response_json_failed; json_failure_class=unknown$/u); clean(h);
  });
  it.each([0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1, NaN, Infinity, "42", null, undefined])("rejects a non-positive-safe-integer ID (%s)", async ID => {
    const h = harness(), req = request(h), selected = response(req), { result } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value); h.action.resolve(); selected.body.resolve({ ID, csrfToken: privateText });
    await expect(result).rejects.toThrow(/^identity_invalid_id$/u); clean(h);
  });
  it.each([null, [], 42, {}, Object.create({ ID: 42 })])("rejects malformed profiles and inherited IDs (%s)", async value => {
    const h = harness(), req = request(h), selected = response(req), { result } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value); h.action.resolve(); selected.body.resolve(value);
    await expect(result).rejects.toThrow(/^identity_invalid_id$/u); clean(h);
  });
  it("rejects an accessor ID without invoking it or any private field getter", async () => {
    const h = harness(), req = request(h), selected = response(req), getter = vi.fn(() => { throw rawError(); });
    const { result } = h.start(); await arm(h); h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    selected.body.resolve(Object.defineProperties({}, { ID: { get: getter }, csrfToken: { get: getter } })); h.action.resolve();
    await expect(result).rejects.toThrow(/^identity_invalid_id$/u); expect(getter).not.toHaveBeenCalled(); clean(h);
  });
  it.each(["close", "crash", "framenavigated"])("fails on %s and does not read a late ID", async event => {
    const h = harness(), req = request(h), selected = response(req), readID = vi.fn(() => ({ value: 42, configurable: true })), { result } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value); h.emitter.emit(event, h.frame);
    selected.body.resolve(new Proxy({}, { getOwnPropertyDescriptor: readID })); h.action.resolve();
    await expect(result).rejects.toThrow(/^identity_page_changed$/u); expect(readID).not.toHaveBeenCalled(); clean(h);
  });
  it("does not mistake a subframe navigation for a main-frame change", async () => {
    const h = harness(), req = request(h), selected = response(req), { result } = h.start(); await arm(h);
    h.emitter.emit("framenavigated", {}); h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    selected.body.resolve({ ID: 42 }); h.action.resolve(); await expect(result).resolves.toBe(42); clean(h);
  });
  it("bounds a missing response after successful focus with the same deadline", async () => {
    const h = harness(), { result } = h.start(); await arm(h); h.action.resolve();
    await vi.advanceTimersByTimeAsync(15_000); await expect(result).rejects.toThrow(/^identity_deadline$/u); clean(h);
  });
  it("does not restart the 15-second deadline after slow preparation or early JSON success", async () => {
    const h = harness(), req = request(h), selected = response(req), { result, settled } = h.start();
    await vi.advanceTimersByTimeAsync(14_900); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value); selected.body.resolve({ ID: 42 });
    await vi.advanceTimersByTimeAsync(99); expect(settled).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1); await expect(result).rejects.toThrow(/^identity_deadline$/u);
    h.action.resolve(); await flush(); clean(h);
  });
  it("refuses to select after the absolute deadline even before the timer callback is dispatched", async () => {
    const h = harness(), req = request(h), selected = response(req), { result } = h.start(); await arm(h);
    h.emitter.emit("request", req); vi.setSystemTime(Date.now() + 15_000); h.emitter.emit("response", selected.value);
    await expect(result).rejects.toThrow(/^identity_deadline$/u); expect(selected.json).not.toHaveBeenCalled(); clean(h);
    h.action.resolve(); await flush();
  });
  it.each(["both-pending", "failed-body", "failed-focus"])("returns on the common deadline despite an unsettled original: %s", async mode => {
    const h = harness(), req = request(h), selected = response(req), { result } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    if (mode === "failed-body") selected.body.reject(rawError());
    if (mode === "failed-focus") h.action.reject(rawError());
    await vi.advanceTimersByTimeAsync(15_000);
    const code = mode === "failed-body" ? "identity_response_json_failed; json_failure_class=unknown" : mode === "failed-focus" ? "identity_focus_failed" : "identity_deadline";
    await expect(result).rejects.toThrow(new RegExp("^" + code + "$", "u")); clean(h);
  });
  it.each(["resolve", "reject"])("quarantines late original %s after timeout without contaminating the next invocation", async mode => {
    const h = harness(), req = request(h), selected = response(req), readID = vi.fn(() => ({ value: 7, configurable: true })), first = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    await vi.advanceTimersByTimeAsync(15_000); await expect(first.result).rejects.toThrow(/^identity_deadline$/u); clean(h);
    h.focus.mockImplementation(() => Promise.resolve());
    const second = h.start(); await flush(); const nextReq = request(h), next = response(nextReq);
    h.emitter.emit("request", nextReq); h.emitter.emit("response", next.value);
    if (mode === "resolve") { selected.body.resolve(new Proxy({}, { getOwnPropertyDescriptor: readID })); h.action.resolve(); }
    else { selected.body.reject(rawError()); h.action.reject(rawError()); }
    next.body.resolve({ ID: 42 }); await expect(second.result).resolves.toBe(42); await flush();
    expect(readID).not.toHaveBeenCalled(); expect(first.settled).toHaveBeenCalledTimes(1);
    expect(h.emitter.eventNames()).toEqual([]); expect(vi.getTimerCount()).toBe(0);
    expect(selected.json).toHaveBeenCalledTimes(1); expect(next.json).toHaveBeenCalledTimes(1); expect(h.focus).toHaveBeenCalledTimes(2);
  });
  it.each(["preparing", "armed"])("caps distinct matching objects while %s and does not count duplicates twice", async phase => {
    const h = harness(), { result } = h.start(); if (phase === "armed") await arm(h);
    const records = Array.from({ length: 256 }, () => request(h));
    for (const req of records) { h.emitter.emit("request", req); h.emitter.emit("request", req); }
    h.emitter.emit("request", request(h));
    if (phase === "preparing") h.snapshot.resolve([]); else h.action.resolve();
    await expect(result).rejects.toThrow(/^identity_record_limit$/u); clean(h);
  });
  it("redacts metadata exceptions, body error messages/stacks/causes and profile canaries", async () => {
    const log = vi.spyOn(console, "log").mockImplementation(() => {}), warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const errorLog = vi.spyOn(console, "error").mockImplementation(() => {});
    for (const source of ["metadata", "body"] as const) {
      const h = harness(), req = request(h, source === "metadata" ? { method: () => { throw rawError(); } } : {});
      const selected = response(req), { result } = h.start(); await arm(h);
      h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
      if (source === "body") selected.body.reject(rawError()); h.action.resolve();
      const failure = await result.catch(value => value);
      expect(failure).toBeInstanceOf(Error); expect(failure.message).toBe(source === "body" ? "identity_response_json_failed; json_failure_class=unknown" : "identity_response_metadata");
      expect(failure.cause).toBeUndefined(); expect(String(failure.stack) + JSON.stringify(failure)).not.toContain(privateText); clean(h);
    }
    for (const args of log.mock.calls) {
      expect(args).toHaveLength(1); expect(args[0]).toMatch(/^\[v4-focus-identity\] /u);
      expect(args[0]).not.toContain(privateText);
    }
    expect(warn).not.toHaveBeenCalled(); expect(errorLog).not.toHaveBeenCalled();
  });
  it.each(["success", "failure"])("reports cleanup failure separately and preserves the primary %s outcome", async mode => {
    const h = harness(), originalOff = h.emitter.off.bind(h.emitter), removed: (string | symbol)[] = [];
    vi.spyOn(h.emitter, "off").mockImplementation((event, listener) => {
      removed.push(event); const value = originalOff(event, listener); if (event === "response") throw rawError(); return value;
    });
    const req = request(h), selected = response(req), { result } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value); h.action.resolve();
    if (mode === "success") selected.body.resolve({ ID: 42 }); else selected.body.reject(rawError());
    const failure = await result.catch(value => value);
    expect(failure.message).toBe(mode === "success" ? "identity_cleanup_failed" : "identity_response_json_failed; json_failure_class=unknown");
    expect(failure.cleanupFailure).toBe("identity_cleanup_failed"); expect(failure.cause).toBeUndefined();
    expect(String(failure.stack) + JSON.stringify(failure)).not.toContain(privateText);
    expect(removed).toEqual(["request", "response", "close", "crash", "framenavigated", "requestfinished", "requestfailed"]); clean(h);
  });

  it.each(["sync", "async"])("projects each original JSON %s failure without changing the selected object", async mode => {
    const cases = [
      ["json_syntax_error", new SyntaxError(privateText)],
      ["cdp_body_no_data", new Error("response.json: Protocol error (Network.getResponseBody): No data found for resource with given identifier")],
      ["cdp_body_error", new Error("response.json: Protocol error (IO.read): " + privateText)],
      ["playwright_api_wrapper", new Error("response.json: " + privateText)],
      ["unknown", rawError()],
    ] as const;
    const log = vi.spyOn(console, "log").mockImplementation(() => {}), warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const errorLog = vi.spyOn(console, "error").mockImplementation(() => {});
    for (const [kind, reason] of cases) {
      const h = harness(), req = request(h), selected = response(req), laterReq = request(h), later = response(laterReq);
      Object.defineProperty(reason, "cause", { value: privateText });
      if (mode === "sync") selected.json.mockImplementation(() => { throw reason; });
      const { result } = h.start(); await arm(h); h.action.resolve(); await flush();
      h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
      if (mode === "async") selected.body.reject(reason);
      await flush(); h.emitter.emit("request", laterReq); h.emitter.emit("response", later.value);
      const failure = await result.catch(value => value);
      expect(failure.message).toBe("identity_response_json_failed; json_failure_class=" + kind);
      expect(failure.cause).toBeUndefined(); expect(String(failure.stack) + JSON.stringify(failure)).not.toContain(privateText);
      expect(selected.json).toHaveBeenCalledTimes(1); expect(later.json).not.toHaveBeenCalled(); clean(h);
    }
    for (const args of log.mock.calls) {
      expect(args).toHaveLength(1); expect(args[0]).toMatch(/^\[v4-focus-identity\] /u);
      expect(args[0]).not.toContain(privateText);
    }
    expect(warn).not.toHaveBeenCalled(); expect(errorLog).not.toHaveBeenCalled();
  });
  it.each(["focus", "page", "metadata", "after-cleanup"])("does not inspect the late Error after %s, with a positive descriptor-read control", async mode => {
    const reason = new Error("response.json: Protocol error (Network.getResponseBody): No data found for resource with given identifier");
    const ownDescriptor = Object.getOwnPropertyDescriptor;
    let reasonReads = 0;
    vi.spyOn(Object, "getOwnPropertyDescriptor").mockImplementation((target, key) => {
      if (target === reason && key === "message") reasonReads++;
      return ownDescriptor(target, key);
    });
    // The same watched native Error must be read when it is the first failure.
    const control = harness(), controlReq = request(control), controlBody = response(controlReq), first = control.start();
    await arm(control); control.emitter.emit("request", controlReq); control.emitter.emit("response", controlBody.value);
    controlBody.body.reject(reason); control.action.resolve();
    await expect(first.result).rejects.toThrow(/^identity_response_json_failed; json_failure_class=cdp_body_no_data$/u);
    expect(reasonReads).toBe(1); clean(control); reasonReads = 0;

    const getter = vi.fn(() => { throw rawError(); });
    Object.defineProperty(reason, "message", { configurable: true, get: getter });
    const h = harness(), req = request(h), selected = response(req), { result, settled } = h.start();
    await arm(h); h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    if (mode === "after-cleanup") {
      await vi.advanceTimersByTimeAsync(15_000); await expect(result).rejects.toThrow(/^identity_deadline$/u); clean(h);
      h.focus.mockImplementation(() => Promise.resolve());
      const next = h.start(); await flush(); const nextReq = request(h), nextBody = response(nextReq);
      h.emitter.emit("request", nextReq); h.emitter.emit("response", nextBody.value);
      selected.body.reject(reason); h.action.reject(rawError()); nextBody.body.resolve({ ID: 42 });
      await expect(next.result).resolves.toBe(42); await flush();
      expect(h.requests).toHaveBeenCalledTimes(2); expect(nextBody.json).toHaveBeenCalledTimes(1);
      expect(h.emitter.eventNames()).toEqual([]); expect(vi.getTimerCount()).toBe(0);
      for (const call of h.forbidden) expect(call).not.toHaveBeenCalled();
    } else {
      if (mode === "focus") h.action.reject(rawError());
      else if (mode === "page") h.emitter.emit("close");
      else h.emitter.emit("response", response(request(h), { url: () => { throw rawError(); } }).value);
      await flush(); expect(settled).not.toHaveBeenCalled();
      selected.body.reject(reason); if (mode !== "focus") h.action.resolve();
      await expect(result).rejects.toThrow(mode === "focus" ? /^identity_focus_failed$/u : mode === "page" ? /^identity_page_changed$/u : /^identity_response_metadata$/u);
      clean(h);
    }
    expect(reasonReads).toBe(0); expect(getter).not.toHaveBeenCalled();
    expect(selected.json).toHaveBeenCalledTimes(1); expect(settled).toHaveBeenCalledTimes(1);
  });
  it("keeps the original JSON failure winner when the deadline time passed but its timer has not run", async () => {
    const h = harness(), req = request(h), selected = response(req), { result } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    vi.setSystemTime(Date.now() + 15_000); selected.body.reject(new SyntaxError(privateText)); await flush();
    // The existing focus success handler observes an already committed failure.
    h.action.resolve();
    await expect(result).rejects.toThrow(/^identity_response_json_failed; json_failure_class=json_syntax_error$/u); clean(h);
  });
  it("preserves the classified primary failure through pending focus, deadline and cleanup failure", async () => {
    const h = harness(), originalOff = h.emitter.off.bind(h.emitter);
    vi.spyOn(h.emitter, "off").mockImplementation((event, listener) => {
      const value = originalOff(event, listener); if (event === "response") throw rawError(); return value;
    });
    const req = request(h), selected = response(req), { result, settled } = h.start(); await arm(h);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value); selected.body.reject(new SyntaxError(privateText));
    await flush(); expect(settled).not.toHaveBeenCalled(); await vi.advanceTimersByTimeAsync(15_000);
    const failure = await result.catch(value => value);
    expect(failure.message).toBe("identity_response_json_failed; json_failure_class=json_syntax_error");
    expect(failure.cleanupFailure).toBe("identity_cleanup_failed"); expect(failure.cause).toBeUndefined(); clean(h);
    h.action.reject(rawError()); await flush(); expect(settled).toHaveBeenCalledTimes(1);
  });
});

describe("closed identity JSON error projection", () => {
  const secret = "synthetic-private-json-message-stack-cause-canary";
  const noData = "response.json: Protocol error (Network.getResponseBody): No data found for resource with given identifier";
  const bodyPrefixes = [
    "response.json: Protocol error (Network.getResponseBody): ",
    "response.json: Protocol error (Network.loadNetworkResource): ",
    "response.json: Protocol error (IO.read): ",
    "response.json: Protocol error (IO.close): ",
  ];
  afterEach(() => { vi.restoreAllMocks(); });
  it("recognizes only exact no-data and the four nonempty body-protocol prefixes", () => {
    expect(classifyIdentityJSONFailure(new Error(noData))).toBe("cdp_body_no_data");
    for (const prefix of bodyPrefixes) {
      expect(classifyIdentityJSONFailure(new Error(prefix + secret))).toBe("cdp_body_error");
      expect(classifyIdentityJSONFailure(new Error(prefix))).toBe("playwright_api_wrapper");
    }
    for (const suffix of [" ", "\n", " " + secret, "\nCall log:\n" + secret + "\n"])
      expect(classifyIdentityJSONFailure(new Error(noData + suffix))).toBe("cdp_body_error");
    for (const value of [" " + noData, secret + noData, noData.replace("response.json", "response.body"), noData.replace("response.json", "response.text"), noData.replace("response.json", "Response.json"), noData.slice("response.json: ".length)])
      expect(classifyIdentityJSONFailure(new Error(value))).toBe("unknown");
    for (const value of [noData.replace("Network.getResponseBody", "Network.other"), noData.replace("Network.getResponseBody", "network.getResponseBody"), noData.replace("): ", "):")])
      expect(classifyIdentityJSONFailure(new Error(value))).toBe("playwright_api_wrapper");
  });
  it("classifies wrapper shape without claiming the underlying transport reason", () => {
    for (const value of ["response.json: " + secret, "response.json: Target page, context or browser has been closed", "response.json: Protocol error (Other.method): " + secret])
      expect(classifyIdentityJSONFailure(new Error(value))).toBe("playwright_api_wrapper");
    for (const value of ["", "response.json: ", "response.json:", secret, "other: response.json: " + secret])
      expect(classifyIdentityJSONFailure(new Error(value))).toBe("unknown");
  });
  it("admits exactly 2048 UTF-16 units without exporting suffixes or normalizing strings", () => {
    const prefix = bodyPrefixes[0];
    const atLimit = (prefix + "\ud800\u0000\n" + secret).padEnd(2048, "x");
    expect(atLimit.length).toBe(2048);
    expect(classifyIdentityJSONFailure(new Error(atLimit))).toBe("cdp_body_error");
    expect(classifyIdentityJSONFailure(new Error(atLimit + "x"))).toBe("unknown");
    expect(classifyIdentityJSONFailure(new Error("response.json: ".padEnd(2048, "x")))).toBe("playwright_api_wrapper");
    expect(classifyIdentityJSONFailure(new Error("response.json: ".padEnd(2049, "x")))).toBe("unknown");
    expect(classifyIdentityJSONFailure(new SyntaxError(secret.repeat(2049)))).toBe("json_syntax_error");
  });
  it("recognizes local native SyntaxError without reading body-bearing message or name", () => {
    let parsedError: unknown;
    try { JSON.parse('{"' + secret + '":'); } catch (reason) { parsedError = reason; }
    expect(classifyIdentityJSONFailure(parsedError)).toBe("json_syntax_error");
    const reason = new SyntaxError(secret), descriptor = Object.getOwnPropertyDescriptor, getter = vi.fn(() => { throw new Error(secret); });
    let reads = 0;
    reason.stack = secret;
    Object.defineProperties(reason, { message: { get: getter }, name: { get: getter }, stack: { get: getter }, cause: { get: getter } });
    vi.spyOn(Object, "getOwnPropertyDescriptor").mockImplementation((target, key) => {
      if (target === reason) reads++;
      return descriptor(target, key);
    });
    expect(classifyIdentityJSONFailure(reason)).toBe("json_syntax_error");
    expect(reads).toBe(0); expect(getter).not.toHaveBeenCalled();
  });
  it("does not authenticate a SyntaxError from an own name or foreign/altered prototype", () => {
    const named = Object.assign(new Error(secret), { name: "SyntaxError" });
    const foreign: unknown = runInNewContext("new SyntaxError('synthetic-foreign-error')");
    const altered = Object.setPrototypeOf(new SyntaxError(secret), Error.prototype);
    for (const value of [named, foreign, altered, { name: "SyntaxError", message: secret }, Object.create(SyntaxError.prototype)])
      expect(classifyIdentityJSONFailure(value)).toBe("unknown");
    // These are independent shapes, not a claim of unforgeable provenance.
    expect(classifyIdentityJSONFailure(Object.setPrototypeOf(new Error(secret), SyntaxError.prototype))).toBe("json_syntax_error");
    expect(classifyIdentityJSONFailure(Object.setPrototypeOf(new SyntaxError(noData), Error.prototype))).toBe("cdp_body_no_data");
  });
  it("requires native Error branding and an own primitive data message without conversions", () => {
    const getter = vi.fn(() => { throw new Error(secret); });
    const boxed = Object.assign(new String(noData), { toString: getter, valueOf: getter });
    for (const value of [undefined, null, false, 1, NaN, Infinity, noData, Symbol(secret), () => {}, [], boxed, { message: noData }, Object.create(Error.prototype)])
      expect(classifyIdentityJSONFailure(value)).toBe("unknown");
    for (const value of [undefined, null, 1, Symbol(secret), boxed, { toString: getter, valueOf: getter }]) {
      const reason = new Error(); Object.defineProperty(reason, "message", { value });
      expect(classifyIdentityJSONFailure(reason)).toBe("unknown");
    }
    const accessor = new Error(); Object.defineProperty(accessor, "message", { get: getter });
    const inherited = new Error(); Object.setPrototypeOf(inherited, Object.create(Error.prototype, { message: { get: getter } }));
    for (const value of [new Error(), accessor, inherited]) expect(classifyIdentityJSONFailure(value)).toBe("unknown");
    expect(getter).not.toHaveBeenCalled();
  });
  it("rejects live/revoked proxies before reflection and never traverses a proxy prototype", () => {
    const trap = vi.fn(() => { throw new Error(secret); });
    const traps = { get: trap, getOwnPropertyDescriptor: trap, getPrototypeOf: trap, ownKeys: trap, has: trap, set: trap, defineProperty: trap, deleteProperty: trap, isExtensible: trap, preventExtensions: trap, setPrototypeOf: trap };
    for (const target of [new Error(noData), new SyntaxError(secret), {}]) {
      const live = new Proxy(target, traps), revoked = Proxy.revocable(target, traps); revoked.revoke();
      expect(classifyIdentityJSONFailure(live)).toBe("unknown"); expect(classifyIdentityJSONFailure(revoked.proxy)).toBe("unknown");
      expect(identityJSONFailureMessage(live)).toBe("identity_response_json_failed; json_failure_class=unknown");
      expect(identityJSONFailureMessage(revoked.proxy)).toBe("identity_response_json_failed; json_failure_class=unknown");
    }
    expect(classifyIdentityJSONFailure(Object.setPrototypeOf(new Error(secret), new Proxy({}, traps)))).toBe("unknown");
    expect(trap).not.toHaveBeenCalled();
  });
  it("never reads ancillary private fields or emits a raw reason", () => {
    const getter = vi.fn(() => { throw new Error(secret); }), reason = new Error(noData);
    reason.stack = secret;
    for (const field of ["name", "stack", "cause", "errors", "method", "type", "log", "body", "url", "headers", "cookies", "toJSON", "toString", "valueOf"])
      Object.defineProperty(reason, field, { configurable: true, get: getter });
    Object.defineProperty(reason, Symbol.toStringTag, { get: getter });
    Object.defineProperty(reason, Symbol.for("nodejs.util.inspect.custom"), { get: getter });
    const log = vi.spyOn(console, "log").mockImplementation(() => {}), warn = vi.spyOn(console, "warn").mockImplementation(() => {}), errorLog = vi.spyOn(console, "error").mockImplementation(() => {});
    const value = classifyIdentityJSONFailure(reason), message = identityJSONFailureMessage(value);
    expect(value).toBe("cdp_body_no_data"); expect(message).toBe("identity_response_json_failed; json_failure_class=cdp_body_no_data");
    expect(message).not.toContain(secret); expect(getter).not.toHaveBeenCalled();
    expect(log).not.toHaveBeenCalled(); expect(warn).not.toHaveBeenCalled(); expect(errorLog).not.toHaveBeenCalled();
  });
  it.each(["prototype", "descriptor"])("closes an unexpected %s reflection failure to unknown", operation => {
    const reason = new Error(noData);
    if (operation === "prototype") {
      const original = Object.getPrototypeOf;
      vi.spyOn(Object, "getPrototypeOf").mockImplementation(value => { if (value === reason) throw new Error(secret); return original(value); });
    } else {
      const original = Object.getOwnPropertyDescriptor;
      vi.spyOn(Object, "getOwnPropertyDescriptor").mockImplementation((value, key) => { if (value === reason) throw new Error(secret); return original(value, key); });
    }
    expect(classifyIdentityJSONFailure(reason)).toBe("unknown");
  });
  it("formats only complete closed literals and never converts an unknown input", () => {
    const classes = ["json_syntax_error", "cdp_body_no_data", "cdp_body_error", "playwright_api_wrapper", "unknown"];
    for (const value of classes) expect(identityJSONFailureMessage(value)).toBe("identity_response_json_failed; json_failure_class=" + value);
    const convert = vi.fn(() => { throw new Error(secret); });
    for (const value of [undefined, null, 1, Symbol(secret), secret, "__proto__", "toString", new String("cdp_body_no_data"), { toString: convert, valueOf: convert, [Symbol.toPrimitive]: convert }])
      expect(identityJSONFailureMessage(value)).toBe("identity_response_json_failed; json_failure_class=unknown");
    expect(convert).not.toHaveBeenCalled();
  });
});


describe("closed identity request failure projection", () => {
  it("accepts only exact allowlisted strings, without exporting arbitrary text", () => {
    const values = { ABORTED: "aborted", CONNECTION_RESET: "connection_reset", CONNECTION_CLOSED: "connection_closed", CONNECTION_REFUSED: "connection_refused", TIMED_OUT: "timed_out", FAILED: "failed" };
    for (const [code, kind] of Object.entries(values)) expect(classifyIdentityRequestFailure({ errorText: "net::ERR_" + code })).toBe(kind);
    for (const value of [null, undefined, "net::ERR_ABORTED", { errorText: "private-url-proof" }, { errorText: "net::ERR_ABORTED private" }, { errorText: 7 }, Object.create({ errorText: "net::ERR_ABORTED" })]) expect(classifyIdentityRequestFailure(value)).toBe("unknown");
  });
  it("does not invoke accessors, proxies or coercion", () => {
    const access = vi.fn(() => { throw new Error("private"); });
    for (const value of [Object.defineProperty({}, "errorText", { get: access }), new Proxy({}, { getOwnPropertyDescriptor: access }), { errorText: { toString: access } }]) expect(classifyIdentityRequestFailure(value)).toBe("unknown");
    expect(access).not.toHaveBeenCalled();
  });
});

describe("reader auth-session transport and actual spec contract", () => {
  const source = readFileSync(resolve(process.cwd(), "e2e/directory-v4-readers.spec.ts"), "utf8");
  const reader = { actorId: 73, username: "synthetic-reader" };
  const fields = ["ID", "username", "role", "priv", "hasMfa", "needChangePwd", "isExpired", "csrfToken"] as const;
  const secret = "synthetic-private-reader-csrf";
  function profile(): Record<string, unknown> {
    return { ID: reader.actorId, username: reader.username, role: "viewer", priv: 0, hasMfa: true, needChangePwd: false, isExpired: false, csrfToken: secret };
  }
  type Session = { sample(): Promise<void>; count(): number; setAuthMutations(value: number): void };
  // Execute the complete current spec statements, not a copy of their predicates.
  // Two- and legacy three-argument shapes are recognized solely so the unchanged
  // old-source sensitivity oracle reaches the real helper's transport check.
  function fromActualSpec(text: string) {
    const tree = ts.createSourceFile("readers.ts", text, ts.ScriptTarget.Latest, true), nodes: ts.Node[] = [];
    const visit = (node: ts.Node) => { nodes.push(node); ts.forEachChild(node, visit); }; visit(tree);
    const fail = () => { throw new Error("Reader session contract source shape changed"); };
    const variable = (name: string) => {
      const matches = nodes.filter((node): node is ts.VariableStatement => ts.isVariableStatement(node) && node.declarationList.declarations.some(value => ts.isIdentifier(value.name) && value.name.text === name));
      if (matches.length !== 1 || matches[0].declarationList.declarations.length !== 1) return fail();
      return matches[0];
    };
    const me = variable("me"), count = variable("readerSessionSamples"), check = variable("assertReaderSessionUnchanged");
    if (!ts.isBlock(me.parent) || count.parent !== me.parent || check.parent !== me.parent) return fail();
    const siblings = me.parent.statements, index = siblings.indexOf(me);
    const enrollment = siblings[index + 1], assertion = siblings[index + 2];
    if (!enrollment || !assertion || enrollment.getText(tree) !== "fixture.secrets.push(me.csrfToken);" ||
        !ts.isExpressionStatement(assertion) || !ts.isCallExpression(assertion.expression) ||
        assertion.expression.expression.getText(tree) !== "invariant" || assertion.expression.arguments.length !== 2 ||
        !ts.isStringLiteral(assertion.expression.arguments[1]) || assertion.expression.arguments[1].text !== "Actual reader session identity mismatch") return fail();
    const firstCall = me.declarationList.declarations[0].initializer;
    if (!firstCall || !ts.isAwaitExpression(firstCall) || !ts.isCallExpression(firstCall.expression)) return fail();
    const checkInitializer = check.declarationList.declarations[0].initializer;
    if (!checkInitializer || !ts.isArrowFunction(checkInitializer) || !ts.isBlock(checkInitializer.body)) return fail();
    const laterCalls: ts.CallExpression[] = [];
    const findRead = (node: ts.Node) => { if (ts.isCallExpression(node) && node.expression.getText(tree) === "readJSON") laterCalls.push(node); ts.forEachChild(node, findRead); }; findRead(checkInitializer);
    if (laterCalls.length !== 1) return fail();
    for (const call of [firstCall.expression, laterCalls[0]]) {
      if (call.expression.getText(tree) !== "readJSON" || ![2, 3].includes(call.arguments.length) ||
          call.arguments[0].getText(tree) !== "context" || !ts.isStringLiteral(call.arguments[1]) || call.arguments[1].text !== "/api/auth/me" ||
          (call.arguments.length === 3 && call.arguments[2].getText(tree) !== "reader.actorId")) return fail();
    }
    const body = `return async (context, reader, fixture) => {
      let readerBrowserAuthMutations = 0;
      ${[me, enrollment, assertion, count, check].map(node => node.getText(tree)).join("\n")}
      return { sample: assertReaderSessionUnchanged, count: () => readerSessionSamples,
        setAuthMutations: value => { readerBrowserAuthMutations = value; } };
    };`;
    const javascript = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None }, reportDiagnostics: true });
    if (javascript.diagnostics?.some(diagnostic => diagnostic.category === ts.DiagnosticCategory.Error)) return fail();
    return new Function("readJSON", "invariant", javascript.outputText)(readJSON, invariant) as
      (context: BrowserContext, expected: typeof reader, fixture: { secrets: string[] }) => Promise<Session>;
  }
  const start = fromActualSpec(source);
  function response(body: unknown = profile(), options: { status?: number; cache?: string | null; actor?: string; rejectJSON?: boolean } = {}) {
    const headers: Record<string, string> = { "content-type": "application/json", "x-content-type-options": "nosniff" };
    if (options.cache !== null) headers["cache-control"] = options.cache ?? "no-store";
    if (options.actor !== undefined) headers["x-adtr-user-id"] = options.actor;
    const json = vi.fn(async () => { if (options.rejectJSON) throw new Error("Synthetic JSON rejection"); return body; });
    return { value: { status: () => options.status ?? 200, headers: () => headers, json } as unknown as APIResponse, json };
  }
  function context(...responses: ReturnType<typeof response>[]) {
    let index = 0;
    const get = vi.fn(async () => { if (index >= responses.length) throw new Error("Unexpected extra fixture read"); return responses[index++].value; });
    return { value: { request: { get } } as unknown as BrowserContext, get };
  }
  it("accepts legitimate auth /me without a business actor header and keeps four real-body samples", async () => {
    const replies = Array.from({ length: 4 }, () => response()), transport = context(...replies), fixture = { secrets: [] as string[] };
    const session = await start(transport.value, reader, fixture);
    for (let index = 0; index < 3; index++) await session.sample();
    expect(session.count()).toBe(4); expect(transport.get).toHaveBeenCalledTimes(4);
    for (const reply of replies) expect(reply.json).toHaveBeenCalledTimes(1);
    for (const call of transport.get.mock.calls) expect(call).toEqual(["/api/auth/me", { maxRedirects: 0, timeout: 15000 }]);
    expect(fixture.secrets.length === 4 && fixture.secrets.every(value => value === secret)).toBe(true);
  });
  it("preserves the old actor-argument failure as a real-helper sensitivity oracle", async () => {
    const reply = response(), transport = context(reply);
    await expect(readJSON(transport.value, "/api/auth/me", reader.actorId)).rejects.toThrow("Real fixture read failed identity or status checks");
    expect(reply.json).not.toHaveBeenCalled();
  });
  it.each(fields)("rejects a missing first-profile field %s", async field => {
    const body = profile(); delete body[field];
    await expect(start(context(response(body)).value, reader, { secrets: [] })).rejects.toThrow("Actual reader session identity mismatch");
  });
  it.each(fields)("rejects a wrong-typed first-profile field %s", async field => {
    const body = profile(); body[field] = typeof body[field] === "string" ? 73 : "wrong-type";
    await expect(start(context(response(body)).value, reader, { secrets: [] })).rejects.toThrow("Actual reader session identity mismatch");
  });
  it.each(["ID", "username", "role", "priv", "hasMfa", "needChangePwd", "isExpired", "csrfToken"])("rejects an invalid first-profile value %s", async field => {
    const body = profile(), wrong: Record<string, unknown> = { ID: 74, username: "other-reader", role: "platform_admin", priv: -1, hasMfa: false, needChangePwd: true, isExpired: true, csrfToken: "" };
    body[field] = wrong[field];
    await expect(start(context(response(body)).value, reader, { secrets: [] })).rejects.toThrow("Actual reader session identity mismatch");
  });
  it.each(fields)("rejects a missing continuity field %s", async field => {
    const body = profile(); delete body[field];
    const session = await start(context(response(), response(body)).value, reader, { secrets: [] });
    await expect(session.sample()).rejects.toThrow("Reader session interval changed"); expect(session.count()).toBe(1);
  });
  it.each(fields)("rejects a wrong-typed continuity field %s", async field => {
    const body = profile(); body[field] = typeof body[field] === "string" ? 73 : "wrong-type";
    const session = await start(context(response(), response(body)).value, reader, { secrets: [] });
    await expect(session.sample()).rejects.toThrow("Reader session interval changed"); expect(session.count()).toBe(1);
  });
  it.each(fields)("rejects a changed continuity field %s", async field => {
    const body = profile(), changed: Record<string, unknown> = { ID: 74, username: "other-reader", role: "platform_admin", priv: 1, hasMfa: false, needChangePwd: true, isExpired: true, csrfToken: "new-synthetic-private-csrf" };
    body[field] = changed[field]; const fixture = { secrets: [] as string[] };
    const session = await start(context(response(), response(body)).value, reader, fixture);
    await expect(session.sample()).rejects.toThrow("Reader session interval changed"); expect(session.count()).toBe(1);
    if (field === "csrfToken") expect(fixture.secrets.includes(String(changed[field]))).toBe(true);
  });
  it("rejects a browser authentication write despite otherwise identical samples", async () => {
    const session = await start(context(response(), response()).value, reader, { secrets: [] }); session.setAuthMutations(1);
    await expect(session.sample()).rejects.toThrow("Reader session interval changed"); expect(session.count()).toBe(1);
  });
  it.each([201, 401, 403, 500])("rejects HTTP status %s before reading JSON", async status => {
    const reply = response(profile(), { status });
    await expect(start(context(reply).value, reader, { secrets: [] })).rejects.toThrow("Real fixture read failed identity or status checks"); expect(reply.json).not.toHaveBeenCalled();
  });
  it.each([null, "", "private", "no-store, max-age=0"])("rejects wrong cache policy %s before reading JSON", async cache => {
    const reply = response(profile(), { cache });
    await expect(start(context(reply).value, reader, { secrets: [] })).rejects.toThrow("Real fixture read failed identity or status checks"); expect(reply.json).not.toHaveBeenCalled();
  });
  it("propagates actual JSON rejection at both profile boundaries without accepting a sample", async () => {
    const first = response(profile(), { rejectJSON: true });
    await expect(start(context(first).value, reader, { secrets: [] })).rejects.toThrow("Synthetic JSON rejection"); expect(first.json).toHaveBeenCalledTimes(1);
    const later = response(profile(), { rejectJSON: true }), session = await start(context(response(), later).value, reader, { secrets: [] });
    await expect(session.sample()).rejects.toThrow("Synthetic JSON rejection"); expect(later.json).toHaveBeenCalledTimes(1); expect(session.count()).toBe(1);
  });
  it.each([undefined, "74"])("keeps missing or wrong business actor header %s rejected", async actor => {
    const reply = response({ ID: reader.actorId }, { actor });
    await expect(readJSON(context(reply).value, "/api/domains/detail?domainId=synthetic", reader.actorId)).rejects.toThrow("Real fixture read failed identity or status checks"); expect(reply.json).not.toHaveBeenCalled();
  });
  it("continues accepting the actual business actor header contract", async () => {
    const body = { connection: "synthetic" }, reply = response(body, { actor: String(reader.actorId) });
    await expect(readJSON(context(reply).value, "/api/domains/detail?domainId=synthetic", reader.actorId)).resolves.toBe(body); expect(reply.json).toHaveBeenCalledTimes(1);
  });
  it("rejects missing or ambiguous extraction anchors without a fallback", () => {
    for (const changed of [source.replace("const me =", "const renamedProfile ="), source + "\nlet readerSessionSamples = 1;", source.replace('fixture.secrets.push(me.csrfToken);', "void 0;")]) {
      expect(() => fromActualSpec(changed)).toThrow("Reader session contract source shape changed");
    }
  });
});
