import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { parseWorkerControlState, workerControl, safeScreenshots, v4ScreenshotTargetVisible, v4ScreenshotTargetStatus, v4CancelUnknownUIReady, recover, readNativeFocusIdentity, invariant, type Actor, type Original } from "../e2e/directory-v4-runtime";
import type { Page, Request, Response, TestInfo } from "@playwright/test";
import { EventEmitter } from "node:events";

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
      headers: () => { throw rawError(); }, postData: () => { throw rawError(); }, postDataJSON: () => { throw rawError(); },
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
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

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
  it.each(["query", "origin", "fragment", "request-url", "response-url", "method", "resource", "navigation", "frame", "page", "redirect-from", "redirect-to", "request-worker", "response-worker"])
    ("fails the first non-OLD candidate's %s metadata without choosing a later body", async kind => {
      const h = harness(), reqOverrides: Record<string, unknown> = {}, responseOverrides: Record<string, unknown> = {};
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
    const h = harness(), unrelated = response(request(h, { url: () => "https://synthetic.invalid/api/other" }));
    const non200 = response(request(h), { status: () => 403 }), req = request(h), selected = response(req), { result } = h.start();
    await arm(h); h.emitter.emit("response", unrelated.value); h.emitter.emit("response", non200.value);
    h.emitter.emit("request", req); h.emitter.emit("response", selected.value);
    selected.body.resolve({ ID: 42 }); h.action.resolve(); await expect(result).resolves.toBe(42);
    expect(unrelated.json).not.toHaveBeenCalled(); expect(non200.json).not.toHaveBeenCalled(); clean(h);
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
    await expect(result).rejects.toThrow(bodyFirst ? /^identity_response_json_failed$/u : /^identity_focus_failed$/u); clean(h);
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
    await expect(result).rejects.toThrow(/^identity_response_json_failed$/u); expect(later.json).not.toHaveBeenCalled(); clean(h);
  });
  it.each(["focus", "json"])("catches a synchronous %s throw with no raw error/cause", async source => {
    const h = harness(), req = request(h), selected = response(req);
    if (source === "focus") h.focus.mockImplementation(() => { throw rawError(); });
    else selected.json.mockImplementation(() => { throw rawError(); });
    const { result } = h.start(); await arm(h);
    if (source === "json") { h.emitter.emit("request", req); h.emitter.emit("response", selected.value); h.action.resolve(); }
    await expect(result).rejects.toThrow(source === "focus" ? /^identity_focus_failed$/u : /^identity_response_json_failed$/u); clean(h);
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
    const code = mode === "failed-body" ? "identity_response_json_failed" : mode === "failed-focus" ? "identity_focus_failed" : "identity_deadline";
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
      expect(failure).toBeInstanceOf(Error); expect(failure.message).toBe(source === "body" ? "identity_response_json_failed" : "identity_response_metadata");
      expect(failure.cause).toBeUndefined(); expect(String(failure.stack) + JSON.stringify(failure)).not.toContain(privateText); clean(h);
    }
    expect(log).not.toHaveBeenCalled(); expect(warn).not.toHaveBeenCalled(); expect(errorLog).not.toHaveBeenCalled();
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
    expect(failure.message).toBe(mode === "success" ? "identity_cleanup_failed" : "identity_response_json_failed");
    expect(failure.cleanupFailure).toBe("identity_cleanup_failed"); expect(failure.cause).toBeUndefined();
    expect(String(failure.stack) + JSON.stringify(failure)).not.toContain(privateText);
    expect(removed).toEqual(["request", "response", "close", "crash", "framenavigated"]); clean(h);
  });
});
