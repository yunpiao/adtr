import { afterEach, describe, expect, it } from "vitest";
import {
  nativeStreamMode,
  nativeRequestTerminal,
  proveNativeStream,
  type ConsumedBodyProof,
  type NativeTerminal,
} from "../e2e/native-stream-proof";
const sha = "a".repeat(64);
const body = (): ConsumedBodyProof => ({
  uncanceledEOF: true,
  expectedByteLength: 54,
  byteLength: 54,
  expectedSHA256: sha,
  sha256: sha,
  readers: 1,
  released: 1,
  signalAborted: false,
  cancelCalls: 0,
  metadataMatches: true,
  identityMatches: true,
});
const finished: NativeTerminal = {
  event: "finished",
  failure: null,
  cancelled: null,
};
const failed: NativeTerminal = {
  event: "failed",
  failure: "net::ERR_ABORTED",
  cancelled: null,
};
const canceled = { ...failed, cancelled: true };
afterEach(() => {
  delete process.env.ADTR_E2E_NATIVE_STREAM_MODE;
});
describe("independent body and native completion evidence", () => {
  it("requires genuine native finish by default and in fixed-engine mode", () => {
    expect(nativeStreamMode()).toBe("native-finish");
    expect(
      proveNativeStream(
        "native-finish",
        "157.0.8092.0",
        body(),
        [finished],
        [finished],
      ),
    ).toMatchObject({
      application: "complete-consumed",
      native: "requestfinished",
    });
    expect(() =>
      proveNativeStream(
        "native-finish",
        "157.0.8092.0",
        body(),
        [failed],
        [canceled],
      ),
    ).toThrow();
  });
  it("records the exact baseline defect without calling it native delivery", () => {
    expect(
      proveNativeStream(
        "stable-body",
        "141.0.7390.37",
        body(),
        [failed],
        [canceled],
      ),
    ).toMatchObject({
      application: "complete-consumed",
      native: "requestfailed",
      failure: "net::ERR_ABORTED",
    });
    expect(
      proveNativeStream(
        "stable-body",
        "141.0.7390.37",
        body(),
        [finished],
        [finished],
      ),
    ).toMatchObject({ native: "requestfinished" });
    expect(() =>
      proveNativeStream(
        "stable-body",
        "157.0.8092.0",
        body(),
        [failed],
        [canceled],
      ),
    ).toThrow();
  });
  for (const [field, value] of Object.entries({
    uncanceledEOF: false,
    byteLength: 53,
    sha256: "b".repeat(64),
    readers: 2,
    released: 0,
    signalAborted: true,
    cancelCalls: 1,
    metadataMatches: false,
    identityMatches: false,
  }))
    it(`rejects native ERR_ABORTED without complete ${field} proof`, () => {
      expect(() =>
        proveNativeStream(
          "stable-body",
          "141.0.7390.37",
          { ...body(), [field]: value },
          [failed],
          [canceled],
        ),
      ).toThrow();
    });
  it("rejects absent, null and nonliteral witness fields", () => {
    for (const field of [
      "uncanceledEOF",
      "signalAborted",
      "metadataMatches",
      "identityMatches",
    ]) {
      for (const value of [undefined, null, 0, 1, "true"]) {
        expect(() =>
          proveNativeStream(
            "stable-body",
            "141.0.7390.37",
            { ...body(), [field]: value } as unknown as ConsumedBodyProof,
            [failed],
            [canceled],
          ),
        ).toThrow();
      }
    }
  });
  it("rejects missing, duplicate, contradictory and unknown terminals", () => {
    for (const [pw, cdp] of [
      [[], [finished]],
      [[finished], []],
      [[finished, finished], [finished]],
      [[finished], [finished, failed]],
      [[finished], [canceled]],
      [[failed], [{ ...canceled, cancelled: false }]],
      [[{ ...failed, failure: "other" }], [{ ...canceled, failure: "other" }]],
    ] as [NativeTerminal[], NativeTerminal[]][])
      expect(() =>
        proveNativeStream("stable-body", "141.0.7390.37", body(), pw, cdp),
      ).toThrow();
  });
  it("binds repeated identical URLs by exact request and ordinal despite reversed terminal order", () => {
    const first = {},
      second = {},
      unrelated = {};
    const terminal = new Map([
      ["second", [finished]],
      ["first", [canceled]],
    ]);
    expect(
      nativeRequestTerminal(
        first,
        [first, second],
        ["first", "second"],
        terminal,
      ),
    ).toEqual({ bound: true, events: [canceled] });
    expect(
      nativeRequestTerminal(
        second,
        [first, second],
        ["first", "second"],
        terminal,
      ),
    ).toEqual({ bound: true, events: [finished] });
    for (const [request, host, ids] of [
      [unrelated, [first, second], ["first", "second"]],
      [first, [first, second], ["first"]],
      [first, [first, first], ["first", "second"]],
      [first, [first, second], ["first", "first"]],
    ] as [object, object[], string[]][]) {
      expect(nativeRequestTerminal(request, host, ids, terminal)).toEqual({
        bound: false,
        events: [],
      });
    }
    expect(
      nativeRequestTerminal(first, [first], ["missing"], terminal),
    ).toEqual({ bound: true, events: [] });
  });
  it("fails closed on unsupported evidence configuration", () => {
    process.env.ADTR_E2E_NATIVE_STREAM_MODE = "ignore-errors";
    expect(() => nativeStreamMode()).toThrow();
  });
});
