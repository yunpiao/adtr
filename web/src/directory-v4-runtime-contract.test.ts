import { describe, expect, it, vi } from "vitest";
import { parseWorkerControlState, workerControl, safeScreenshots, v4ScreenshotTargetVisible } from "../e2e/directory-v4-runtime";
import type { Page, TestInfo } from "@playwright/test";

describe("targeted v4 safe screenshot ordering", () => {
  it("rejects header occlusion, clipping and foreign overlays using measured geometry and hit tests", () => {
    const header = document.createElement("header"), node = document.createElement("p"), child = document.createElement("span"), overlay = document.createElement("div");
    header.className = "soc-topbar"; node.style.visibility = "visible";
    node.append(child); document.body.append(header, node, overlay);
    let top = 80, headerBottom = 70, foreignHit = false;
    const prior = Object.getOwnPropertyDescriptor(document, "elementFromPoint");
    Object.defineProperty(document, "elementFromPoint", { configurable: true, value: (_x: number, y: number) => foreignHit && y < top + 3 ? overlay : child });
    vi.spyOn(header, "getBoundingClientRect").mockImplementation(() => ({ bottom: headerBottom }) as DOMRect);
    vi.spyOn(node, "getBoundingClientRect").mockImplementation(() => ({ top, bottom: top + 40, left: 10, right: 210, width: 200, height: 40 }) as DOMRect);
    try {
      expect(v4ScreenshotTargetVisible(node)).toBe(true);
      top = 50; expect(v4ScreenshotTargetVisible(node)).toBe(false);
      top = 80; headerBottom = 90; expect(v4ScreenshotTargetVisible(node)).toBe(false);
      headerBottom = 70; foreignHit = true; expect(v4ScreenshotTargetVisible(node)).toBe(false);
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
