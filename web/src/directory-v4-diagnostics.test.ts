import { afterEach, describe, expect, it, vi } from "vitest";
import type { BrowserContext } from "@playwright/test";
import { boundedV4, configureV4Deadlines, v4Phase } from "../e2e/directory-v4-diagnostics";

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });
describe("v4 localization without changing scenario acceptance", () => {
  it("sets only context action and navigation deadlines", () => {
    const context = { setDefaultTimeout: vi.fn(), setDefaultNavigationTimeout: vi.fn() };
    const log = vi.spyOn(console, "log").mockImplementation(() => {});
    configureV4Deadlines(context as unknown as BrowserContext);
    expect(context.setDefaultTimeout).toHaveBeenCalledExactlyOnceWith(15_000);
    expect(context.setDefaultNavigationTimeout).toHaveBeenCalledExactlyOnceWith(15_000);
    expect(log).toHaveBeenCalledExactlyOnceWith("[v4-phase] suite-start");
  });
  it("rejects arbitrary diagnostic payloads before logging", () => {
    const log = vi.spyOn(console, "log").mockImplementation(() => {});
    expect(() => v4Phase("private-value" as Parameters<typeof v4Phase>[0])).toThrow("Invalid v4 diagnostic phase");
    expect(log).not.toHaveBeenCalled();
  });
  it("bounds a never-settled body without inventing a successful response", async () => {
    vi.useFakeTimers();
    const pending = boundedV4(new Promise<never>(() => {}), 15_000, "Body deadline");
    const rejected = expect(pending).rejects.toThrow("Body deadline");
    await vi.advanceTimersByTimeAsync(15_000);
    await rejected;
    expect(vi.getTimerCount()).toBe(0);
  });
  it("preserves success and clears its deadline", async () => {
    vi.useFakeTimers();
    expect(await boundedV4(Promise.resolve(42), 15_000, "Body deadline")).toBe(42);
    expect(vi.getTimerCount()).toBe(0);
  });
});
