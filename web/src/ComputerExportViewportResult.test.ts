// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { classifyComputerViewportResult, computerViewportResultSuffix } from "../e2e/computer-asset-export-v3-viewport-result";

const canary = "CANARY-cookie-proof-url-task-id-stack-cause";
const resultFor = (actual: unknown) => ({ name: "toBeInViewport", expected: "in viewport", pass: false, actual });
const errorFor = (actual: unknown) => Object.assign(new Error("withheld"), { matcherResult: resultFor(actual) });
const classify = (actual: unknown) => classifyComputerViewportResult(errorFor(actual));

afterEach(() => vi.restoreAllMocks());

describe("closed original computer viewport matcher projection", () => {
  it.each([
    ["0", "zero"],
    ["0.5", "positive_not_matching"],
    ["1e-7", "positive_not_matching"],
    ["5e-324", "positive_not_matching"],
    [String((1 - 1e-9) - Number.EPSILON / 2), "positive_not_matching"],
    [String(1 - 1e-9), "positive_not_matching"],
    [String((1 - 1e-9) + Number.EPSILON / 2), "within_tolerance"],
    [String(1 - Number.EPSILON / 2), "within_tolerance"],
    ["1", "exact_one"],
  ])("classifies the pinned synthetic token %s without changing failure", (token, expected) => {
    expect(classify(`viewport ratio ${token}`)).toBe(expected);
  });

  it("keeps full-ratio failure evidence rather than treating it as success or filtering it", () => {
    const error = errorFor("viewport ratio 1");
    const before = Object.getOwnPropertyDescriptors(error.matcherResult);
    expect(classifyComputerViewportResult(error)).toBe("exact_one");
    expect(Object.getOwnPropertyDescriptors(error.matcherResult)).toEqual(before);
    expect(error.matcherResult.pass).toBe(false);
  });

  it("bounds actual before prefix parsing, including the 64/65 boundary", () => {
    const atLimit = `viewport ratio ${"0".repeat(49)}`;
    const overLimit = atLimit + "0";
    expect(atLimit.length).toBe(64);
    expect(overLimit.length).toBe(65);
    expect(classify(atLimit)).toBe("unavailable");
    const prefix = vi.spyOn(String.prototype, "startsWith");
    let result: unknown, calls = -1;
    try {
      const before = prefix.mock.calls.length;
      result = classify(overLimit);
      calls = prefix.mock.calls.length - before;
    } finally { prefix.mockRestore(); }
    expect(result).toBe("unavailable");
    expect(calls).toBe(0);
  });

  it.each([
    undefined, null, true, false, 0, 1, NaN, Infinity, -Infinity,
    [], {}, new String("viewport ratio 0"), new Number(0), Symbol(canary), () => canary,
    "", "viewport ratio ", "viewport ratio NaN", "viewport ratio Infinity",
    "viewport ratio -Infinity", "viewport ratio 1e309", "viewport ratio 1e-1000",
    "viewport ratio 1e-324", "viewport ratio 2", "viewport ratio -1", "viewport ratio -0",
    "viewport ratio +0", "viewport ratio 00", "viewport ratio .5", "viewport ratio 0.50",
    "viewport ratio 1.0", "viewport ratio 0x0", "viewport ratio 1E-7", "viewport ratio 1e-07",
    "viewport ratio 0.0000001", "viewport ratio 10e-8", "viewport ratio 0.1e-6",
    "viewport ratio 0 ", "viewport ratio  0", "viewport ratio 0\n", "viewport ratio 0\r\n",
    "viewport ratio ０", "viewport ratio 0\u0000", "viewport\tratio 0", "viewport ratio\u00a00",
    `prefix viewport ratio 0`, `viewport ratio 0 ${canary}`, `viewport ratio ${"0".repeat(100_000)}`,
  ].map((actual) => ({ actual })))("rejects malformed or noncanonical actual without coercion", ({ actual }) => {
    expect(classify(actual)).toBe("unavailable");
  });

  it.each([undefined, null, true, false, 0, 1, "error", Symbol(canary), () => canary, []].map((error) => ({ error })))(
    "rejects a non-object error without property access", ({ error }) => {
      expect(classifyComputerViewportResult(error)).toBe("unavailable");
    },
  );

  it.each([undefined, null, true, 1, "result", Symbol(canary), () => canary, []].map((matcherResult) => ({ matcherResult })))(
    "rejects a non-object nested result", ({ matcherResult }) => {
      expect(classifyComputerViewportResult({ matcherResult })).toBe("unavailable");
    },
  );

  it("requires each fixed own-data shape field without accepting inheritance", () => {
    for (const field of ["name", "expected", "pass", "actual"] as const) {
      const missing: Record<string, unknown> = resultFor("viewport ratio 0");
      delete missing[field];
      expect(classifyComputerViewportResult({ matcherResult: missing })).toBe("unavailable");
      const inherited = Object.assign(Object.create({ [field]: resultFor("viewport ratio 0")[field] }), missing);
      expect(classifyComputerViewportResult({ matcherResult: inherited })).toBe("unavailable");
    }
    expect(classifyComputerViewportResult({})).toBe("unavailable");
    expect(classifyComputerViewportResult(Object.create({ matcherResult: resultFor("viewport ratio 0") }))).toBe("unavailable");
    for (const [field, value] of [["name", "toBeVisible"], ["expected", 1], ["expected", "in viewport "], ["pass", true], ["pass", "false"]]) {
      expect(classifyComputerViewportResult({ matcherResult: { ...resultFor("viewport ratio 0"), [String(field)]: value } })).toBe("unavailable");
    }
  });

  it("accepts own data with a null or poisoned prototype without traversing it", () => {
    let calls = 0;
    const poisoned = Object.defineProperty({}, "actual", { get: () => { calls++; throw new Error(canary); } });
    const matcherResult = Object.defineProperties(Object.create(poisoned), Object.getOwnPropertyDescriptors(resultFor("viewport ratio 0")));
    const error = Object.create(null);
    Object.defineProperty(error, "matcherResult", { value: matcherResult });
    expect(classifyComputerViewportResult(error)).toBe("zero");
    expect(calls).toBe(0);
  });

  it("never invokes accessors for admitted field names", () => {
    let calls = 0;
    const getter = () => { calls++; throw new Error(canary); };
    expect(classifyComputerViewportResult(Object.defineProperty({}, "matcherResult", { get: getter }))).toBe("unavailable");
    for (const field of ["name", "expected", "pass", "actual"]) {
      const result = Object.defineProperty(resultFor("viewport ratio 0"), field, { get: getter });
      expect(classifyComputerViewportResult({ matcherResult: result })).toBe("unavailable");
    }
    expect(calls).toBe(0);
  });

  it("rejects live and revoked proxies at either level before any trap", () => {
    let calls = 0;
    const trap = () => { calls++; throw new Error(canary); };
    const handler = { get: trap, has: trap, ownKeys: trap, getOwnPropertyDescriptor: trap, getPrototypeOf: trap };
    const outer = new Proxy(errorFor("viewport ratio 0"), handler);
    const inner = new Proxy(resultFor("viewport ratio 0"), handler);
    const revokedOuter = Proxy.revocable(errorFor("viewport ratio 0"), handler);
    const revokedInner = Proxy.revocable(resultFor("viewport ratio 0"), handler);
    revokedOuter.revoke(); revokedInner.revoke();
    for (const error of [outer, { matcherResult: inner }, revokedOuter.proxy, { matcherResult: revokedInner.proxy }]) {
      expect(classifyComputerViewportResult(error)).toBe("unavailable");
    }
    expect(calls).toBe(0);
  });

  it("ignores sensitive extra fields and never inspects, serializes or coerces either object", () => {
    let calls = 0;
    const getter = () => { calls++; throw new Error(canary); };
    const matcherResult = resultFor("viewport ratio 0.5");
    const error = { matcherResult };
    for (const target of [error, matcherResult]) {
      for (const key of ["message", "log", "stack", "cause", "timeout", "toJSON", "toString", "valueOf"]) {
        Object.defineProperty(target, key, { get: getter });
      }
      Object.defineProperty(target, Symbol.for("nodejs.util.inspect.custom"), { get: getter });
      Object.defineProperty(target, Symbol.toPrimitive, { get: getter });
    }
    expect(classifyComputerViewportResult(error)).toBe("positive_not_matching");
    expect(computerViewportResultSuffix(classifyComputerViewportResult(error))).toBe("; viewport_ratio_class=positive_not_matching");
    expect(calls).toBe(0);
  });

  it("contains descriptor failures as unavailable without returning the raw exception", () => {
    const descriptor = vi.spyOn(Object, "getOwnPropertyDescriptor").mockImplementation(() => { throw new Error(canary); });
    let result: unknown;
    try { result = classifyComputerViewportResult(errorFor("viewport ratio 0")); }
    finally { descriptor.mockRestore(); }
    expect(result).toBe("unavailable");
  });

  it("formats only closed literals, with an empty non-target sentinel", () => {
    expect(computerViewportResultSuffix("")).toBe("");
    for (const value of ["zero", "positive_not_matching", "within_tolerance", "exact_one", "unavailable"]) {
      expect(computerViewportResultSuffix(value)).toBe(`; viewport_ratio_class=${value}`);
    }
    let calls = 0;
    const trap = () => { calls++; throw new Error(canary); };
    const proxy = new Proxy({}, { get: trap, getOwnPropertyDescriptor: trap });
    const revoked = Proxy.revocable({}, { get: trap }); revoked.revoke();
    for (const value of [canary, 0, null, undefined, Symbol(canary), proxy, revoked.proxy, { toString: trap, valueOf: trap }]) {
      expect(computerViewportResultSuffix(value)).toBe("; viewport_ratio_class=unavailable");
    }
    expect(calls).toBe(0);
  });
});
