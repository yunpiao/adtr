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

import { captureComputerButtonGeometry, projectComputerButtonGeometry, computerButtonGeometrySuffix } from "../e2e/computer-asset-export-v3-viewport-result";

const geometrySample = () => ({ status: "sampled", connected: true, left: 10, top: 10, right: 90, bottom: 70, width: 80, height: 60, innerWidth: 100, innerHeight: 80, clientWidth: 100, clientHeight: 80 });
const geometryElement = () => {
  const rect: Record<string, unknown> = { left: 10, top: 10, right: 90, bottom: 70, width: 80, height: 60 };
  const view: Record<string, unknown> = { innerWidth: 100, innerHeight: 80 };
  const root: Record<string, unknown> = { clientWidth: 100, clientHeight: 80 };
  const document = { defaultView: view, documentElement: root };
  const getBoundingClientRect = vi.fn(() => rect);
  const element = { ownerDocument: document, isConnected: true, getBoundingClientRect };
  return { rect, view, root, document, element, getBoundingClientRect, capture: () => captureComputerButtonGeometry(element as unknown as Element) };
};

describe("single pre-assert computer button geometry", () => {
  it("reads one rect and emits only twelve primitive fields without mutating its input", () => {
    const f = geometryElement();
    const before = { ...f.rect };
    const result = f.capture();
    expect(result).toEqual(geometrySample());
    expect(Object.keys(result)).toHaveLength(12);
    expect(f.getBoundingClientRect).toHaveBeenCalledTimes(1);
    expect(f.rect).toEqual(before);
    expect(result).not.toBe(f.rect);
    const projected = projectComputerButtonGeometry(result);
    expect(projected).toEqual(result);
    expect(projected).not.toBe(result);
  });

  it("does not inspect text, identifiers, attributes, styles, storage or network", () => {
    const f = geometryElement();
    let calls = 0;
    const forbidden = () => { calls++; throw new Error(canary); };
    for (const [target, keys] of [
      [f.element, ["innerText", "textContent", "innerHTML", "outerHTML", "id", "style", "getAttribute", "scrollIntoView", "getClientRects"]],
      [f.document, ["URL", "cookie", "querySelector", "elementFromPoint"]],
      [f.view, ["location", "localStorage", "sessionStorage", "fetch", "getComputedStyle", "setTimeout", "IntersectionObserver"]],
    ] as [object, string[]][]) {
      for (const key of keys) Object.defineProperty(target, key, { get: forbidden });
    }
    expect(f.capture()).toEqual(geometrySample());
    expect(calls).toBe(0);
  });

  it("contains acquisition-data failures without returning the exception or DOM reference", () => {
    const throwing = geometryElement();
    throwing.getBoundingClientRect.mockImplementation(() => { throw new Error(canary); });
    expect(throwing.capture()).toEqual({ status: "unavailable" });
    for (const key of ["defaultView", "documentElement"]) {
      const f = geometryElement();
      Object.defineProperty(f.document, key, { value: null });
      expect(f.capture()).toEqual({ status: "unavailable" });
      expect(f.getBoundingClientRect).not.toHaveBeenCalled();
    }
    const detached = geometryElement();
    detached.element.isConnected = false;
    detached.rect.width = 0; detached.rect.right = 10;
    expect(detached.capture()).toEqual({ ...geometrySample(), connected: false, width: 0, right: 10 });
  });

  it("rejects nonfinite, malformed and out-of-envelope numbers before transfer and again on the host", () => {
    for (const field of ["left", "top", "right", "bottom", "width", "height"]) {
      for (const value of [NaN, Infinity, -Infinity, 1_000_001, -1_000_001, null, "20", new Number(20)]) {
        const f = geometryElement(); f.rect[field] = value;
        expect(f.capture()).toEqual({ status: "unavailable" });
        expect(projectComputerButtonGeometry({ ...geometrySample(), [field]: value })).toEqual({ status: "unavailable" });
      }
    }
    for (const field of ["innerWidth", "innerHeight", "clientWidth", "clientHeight"]) {
      for (const value of [0, -1, 16_385, 1.5, NaN, Infinity, undefined, "100", new Number(100)]) {
        const f = geometryElement(); (field.startsWith("inner") ? f.view : f.root)[field] = value;
        expect(f.capture()).toEqual({ status: "unavailable" });
        expect(projectComputerButtonGeometry({ ...geometrySample(), [field]: value })).toEqual({ status: "unavailable" });
      }
      for (const value of [1, 16_384]) {
        const f = geometryElement(); (field.startsWith("inner") ? f.view : f.root)[field] = value;
        expect(f.capture()).toEqual({ ...geometrySample(), [field]: value });
      }
    }
    for (const override of [{ width: -1 }, { height: -1 }, { right: 9 }, { bottom: 9 }, { connected: "true" }]) {
      expect(projectComputerButtonGeometry({ ...geometrySample(), ...override })).toEqual({ status: "unavailable" });
    }
  });

  it("supports finite envelope edges and subpixel differences without rounding them away", () => {
    const f = geometryElement();
    Object.assign(f.rect, { left: -1_000_000, top: -1_000_000, right: 0, bottom: 0, width: 1_000_000, height: 1_000_000 });
    expect(f.capture().status).toBe("sampled");
    const excess = 100 + Number.EPSILON * 100;
    const value = { ...geometrySample(), right: excess, width: excess - 10 };
    const suffix = computerButtonGeometrySuffix(value);
    expect(suffix).toContain(`right:${String(excess)},`);
    expect(suffix).toContain("clip_inner:right,clip_client:right");
    expect(computerButtonGeometrySuffix({ ...value, right: 100, width: 90 })).toContain("clip_inner:none,clip_client:none");
  });

  it("maps all sixteen edge combinations for each viewport independently", () => {
    const expected = ["none", "top", "right", "top_right", "bottom", "top_bottom", "right_bottom", "top_right_bottom", "left", "top_left", "right_left", "top_right_left", "bottom_left", "top_bottom_left", "right_bottom_left", "top_right_bottom_left"];
    for (let mask = 0; mask < 16; mask++) {
      const left = mask & 8 ? -10 : 10, top = mask & 1 ? -10 : 10;
      const right = mask & 2 ? 110 : 90, bottom = mask & 4 ? 90 : 70;
      const value = { ...geometrySample(), left, top, right, bottom, width: right - left, height: bottom - top };
      expect(computerButtonGeometrySuffix(value)).toContain(`clip_inner:${expected[mask]},clip_client:${expected[mask]}`);
    }
    const value = { ...geometrySample(), left: 0, top: 0, right: 100, bottom: 80, width: 100, height: 80, clientWidth: 96, clientHeight: 76 };
    expect(computerButtonGeometrySuffix(value)).toContain("clip_inner:none,clip_client:right_bottom");
    expect(computerButtonGeometrySuffix({ ...geometrySample(), right: 10, width: 0 })).toContain("zero_area:true");
  });

  it("admits only the exact own-data schema and ignores no hidden extra fields", () => {
    const valid = geometrySample();
    expect(projectComputerButtonGeometry({ status: "unavailable" })).toEqual({ status: "unavailable" });
    for (const key of Object.keys(valid)) {
      const missing: Record<string, unknown> = { ...valid }; delete missing[key];
      const inherited = Object.assign(Object.create({ [key]: (valid as Record<string, unknown>)[key] }), missing);
      expect(projectComputerButtonGeometry(missing)).toEqual({ status: "unavailable" });
      expect(projectComputerButtonGeometry(inherited)).toEqual({ status: "unavailable" });
    }
    for (const value of [null, undefined, true, 1, canary, [], () => valid, { ...valid, extra: canary }, { ...valid, [Symbol(canary)]: 1 }, { ...valid, status: "other" }]) {
      expect(projectComputerButtonGeometry(value)).toEqual({ status: "unavailable" });
    }
    const nullPrototype = Object.assign(Object.create(null), valid);
    expect(projectComputerButtonGeometry(nullPrototype)).toEqual(valid);
  });

  it("rejects proxies and accessors without running traps, getters or coercion", () => {
    let calls = 0;
    const trap = () => { calls++; throw new Error(canary); };
    const proxy = new Proxy(geometrySample(), { get: trap, ownKeys: trap, getOwnPropertyDescriptor: trap, getPrototypeOf: trap });
    const revoked = Proxy.revocable(geometrySample(), { get: trap }); revoked.revoke();
    for (const value of [proxy, revoked.proxy]) {
      expect(projectComputerButtonGeometry(value)).toEqual({ status: "unavailable" });
      expect(computerButtonGeometrySuffix(value)).toBe("; viewport_geometry=source:pre_assert,status:unavailable");
    }
    for (const field of Object.keys(geometrySample())) {
      const value = Object.defineProperty(geometrySample(), field, { get: trap });
      expect(projectComputerButtonGeometry(value)).toEqual({ status: "unavailable" });
    }
    for (const key of ["message", "log", "stack", "cause", "toJSON", "toString", "valueOf", Symbol.toPrimitive, Symbol.for("nodejs.util.inspect.custom")]) {
      const value = Object.defineProperty(geometrySample(), key, { get: trap });
      expect(computerButtonGeometrySuffix(value)).toBe("; viewport_geometry=source:pre_assert,status:unavailable");
    }
    expect(calls).toBe(0);
  });

  it("contains host reflection failures and produces only fixed or bounded ASCII output", () => {
    const descriptor = vi.spyOn(Object, "getOwnPropertyDescriptor").mockImplementation(() => { throw new Error(canary); });
    let result: unknown;
    try { result = projectComputerButtonGeometry(geometrySample()); }
    finally { descriptor.mockRestore(); }
    expect(result).toEqual({ status: "unavailable" });
    const value = geometrySample();
    const suffix = computerButtonGeometrySuffix(value);
    expect(suffix).toBe("; viewport_geometry=source:pre_assert,status:sampled,connected:true,left:10,top:10,right:90,bottom:70,width:80,height:60,innerWidth:100,innerHeight:80,clientWidth:100,clientHeight:80,zero_area:false,clip_inner:none,clip_client:none");
    expect(suffix.length).toBeLessThanOrEqual(512);
    expect(suffix).toMatch(/^[\x20-\x7e]+$/);
    expect(suffix).not.toContain(canary);
  });
});
