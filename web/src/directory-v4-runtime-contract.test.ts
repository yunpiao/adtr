import { describe, expect, it, vi } from "vitest";
import { parseWorkerControlState, workerControl, safeScreenshots, v4ScreenshotTargetVisible, v4ScreenshotTargetStatus, v4CancelUnknownUIReady, recover, type Actor, type Original } from "../e2e/directory-v4-runtime";
import type { Page, TestInfo } from "@playwright/test";
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
