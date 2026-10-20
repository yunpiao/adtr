import { afterEach, describe, expect, it, vi } from "vitest";
import { observedV4BodyClass, recordV4BodyClass, readV4ObservedObject, waitForV4RawMembers } from "../e2e/directory-v4-result-observation";
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });
const bytes = (text: string) => new TextEncoder().encode(text);

describe("safe genuine response observation classification", () => {
  it("exposes only the closed transport/media-type category", () => {
    expect(observedV4BodyClass(true, "application/json; charset=utf-8")).toBe("browser_json");
    expect(observedV4BodyClass(false, "application/json")).toBe("api_json");
    expect(observedV4BodyClass(true, "private-header-value")).toBe("browser_other");
    expect(observedV4BodyClass(false, undefined)).toBe("api_other");
  });
  it("caps metadata output without printing raw headers", () => {
    const log = vi.spyOn(console, "log").mockImplementation(() => {});
    for (let index = 0; index < 300; index++) recordV4BodyClass(true, "private-header-value");
    expect(log).toHaveBeenCalledTimes(256);
    expect(log.mock.calls.every(args => args.length === 1 && args[0] === "[v4-body] browser_other")).toBe(true);
  });
  it("preserves real object bytes without reissuing the read", async () => {
    const read = vi.fn(async () => bytes('{"task":{"state":"queued"}}'));
    expect(await readV4ObservedObject(read)).toEqual({ task: { state: "queued" } });
    expect(read).toHaveBeenCalledTimes(1);
  });
  it("does not expose transport errors or malformed response data", async () => {
    await expect(readV4ObservedObject(async () => { throw new Error("private-transport-data"); })).rejects.toThrow(/^V4 observed body: retrieval failed$/u);
    await expect(readV4ObservedObject(async () => new Uint8Array([0xff]))).rejects.toThrow(/^V4 observed body: invalid UTF-8$/u);
    for (const text of ['{"private":', '{"key":1,"key":2}', '{"key":1,"\\u006bey":2}'])
      await expect(readV4ObservedObject(async () => bytes(text))).rejects.toThrow(/^V4 observed body: invalid unique-key JSON$/u);
    for (const text of ["null", "[]", '"private"'])
      await expect(readV4ObservedObject(async () => bytes(text))).rejects.toThrow(/^V4 observed body: non-object envelope$/u);
    await expect(readV4ObservedObject(async () => new Uint8Array(8 * 1024 * 1024 + 1))).rejects.toThrow(/^V4 observed body: size exceeded$/u);
  });
  it("separates a real deadline from immediate retrieval failure", async () => {
    vi.useFakeTimers();
    const result = readV4ObservedObject(() => new Promise<Uint8Array>(() => {}));
    const rejected = expect(result).rejects.toThrow(/^V4 observed body: deadline$/u);
    await vi.advanceTimersByTimeAsync(15_000); await rejected;
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe("complete original raw member rendering", () => {
  it("waits through empty and partial DOM before exact ordered values arrive", async () => {
    vi.useFakeTimers();
    const expected = [" first ", "escaped\\\\value", " first "];
    const read = vi.fn<() => Promise<string[]>>().mockResolvedValueOnce([]).mockResolvedValueOnce(expected.slice(0, 1)).mockResolvedValue(expected);
    const result = waitForV4RawMembers(read, expected);
    await vi.advanceTimersByTimeAsync(100); await result;
    expect(read).toHaveBeenCalledTimes(3); expect(vi.getTimerCount()).toBe(0);
  });
  it("does not accept wrong order, missing duplicates or normalized values", async () => {
    vi.useFakeTimers();
    for (const wrong of [["b", " a ", " a "], [" a ", "b"], ["a", "b", "a"]]) {
      const result = waitForV4RawMembers(async () => wrong, [" a ", "b", " a "]);
      const rejected = expect(result).rejects.toThrow(/^Raw member values\/order not preserved after render deadline$/u);
      await vi.advanceTimersByTimeAsync(5_000); await rejected;
    }
  });
});
