import { describe, expect, it } from "vitest";
import {
  userAssetsV2Time,
  validUserAssetsV2Filters,
  validUserAssetsV2SAM,
} from "./user-assets-v2-filters";

describe("user asset exact filter inputs", () => {
  it.each([
    "",
    " ",
    " +&%_*?'\\()\u0000\u202e😀 ",
    "e\u0301",
    "é",
    "x".repeat(256),
    "😀".repeat(128),
    "界".repeat(256),
  ])(
    "accepts raw SAM without trimming, case folding or Unicode normalization (%#)",
    (value) => expect(validUserAssetsV2SAM(value)).toBe(true),
  );
  it.each(["x".repeat(257), "😀".repeat(129), "\ud800", "\udfff", null, 1])(
    "rejects invalid or overlong SAM (%#)",
    (value) => expect(validUserAssetsV2SAM(value)).toBe(false),
  );
  it("preserves nanoseconds, timezone offsets, Gregorian leap days and year 0001", () => {
    expect(userAssetsV2Time("0001-01-01T00:00:00Z")).toBe(0n);
    expect(userAssetsV2Time("0001-01-01T08:00:00+08:00")).toBe(0n);
    expect(userAssetsV2Time("0001-01-01T00:00:00-00:00")).toBe(0n);
    expect(userAssetsV2Time("0001-01-01T00:00:00.000000001Z")).toBe(1n);
    expect(userAssetsV2Time("0001-01-01T23:59:00+23:59")).toBe(0n);
    expect(userAssetsV2Time("2026-10-01T08:00:00.123456789+08:00")).toBe(
      userAssetsV2Time("2026-10-01T00:00:00.123456789Z"),
    );
    expect(userAssetsV2Time("9999-12-31T23:59:59.999999999Z")).toBe(
      315537897599999999999n,
    );
    expect(
      userAssetsV2Time("2000-03-01T00:00:00Z")! -
        userAssetsV2Time("2000-02-28T00:00:00Z")!,
    ).toBe(172800000000000n);
    expect(
      userAssetsV2Time("1900-03-01T00:00:00Z")! -
        userAssetsV2Time("1900-02-28T00:00:00Z")!,
    ).toBe(86400000000000n);
  });
  it.each([
    undefined,
    null,
    0,
    "",
    " ",
    "2026-10-01",
    "2026-10-01T00:00:00",
    "0000-12-31T23:00:00-01:00",
    "0001-01-01T00:00:00+00:01",
    "9999-12-31T23:59:59-00:01",
    "+010000-01-01T00:00:00Z",
    "2026-10-01t00:00:00z",
    "2026-10-01 00:00:00Z",
    "2026-10-01T00:00:00,1Z",
    "2026-10-01T00:00:00.Z",
    "2026-10-01T00:00:00.1234567890Z",
    "2026-10-01T00:00:00Z\n",
    "2026-10-01T00:00:00Z\r",
    "2026-10-01T00:00:00Z\r\n",
    "2026-10-01T00:00:00Z\u2028",
    "2026-10-01T00:00:00Z\u2029",
    "2026-10-01T00:00:00+24:00",
    "2026-10-01T00:00:00+00:60",
    "2026-10-01T00:00:00+0800",
    "2026-10-01T00:00:00+8:00",
    "2026-00-01T00:00:00Z",
    "2026-13-01T00:00:00Z",
    "2026-10-00T00:00:00Z",
    "2026-04-31T00:00:00Z",
    "2026-02-29T00:00:00Z",
    "1900-02-29T00:00:00Z",
    "2026-10-01T24:00:00Z",
    "2026-10-01T00:60:00Z",
    "2026-10-01T00:00:60Z",
  ])("rejects non-contract timestamp (%#)", (value) => {
    expect(userAssetsV2Time(value)).toBeUndefined();
  });
  it("requires a valid increasing paired range, optional raw SAM and exact sort vocabulary", () => {
    for (const value of [
      {},
      { sAMAccountName: "" },
      { creatTm: "asc" },
      { creatTm: "desc" },
      {
        startTm: "0001-01-01T00:00:00Z",
        endTm: "0001-01-01T00:00:00.000000001Z",
      },
    ])
      expect(validUserAssetsV2Filters(value as any)).toBe(true);
    for (const value of [
      { startTm: "" },
      { startTm: "", endTm: "" },
      { startTm: "2026-10-01T00:00:00Z" },
      { endTm: "2026-10-01T00:00:00Z" },
      { creatTm: "" },
      { creatTm: "ASC" },
      { creatTm: 1 },
      { sAMAccountName: null },
      {
        startTm: "2026-10-01T08:00:00+08:00",
        endTm: "2026-10-01T00:00:00Z",
      },
      {
        startTm: "0001-01-01T00:00:00.000000002Z",
        endTm: "0001-01-01T00:00:00.000000001Z",
      },
    ])
      expect(validUserAssetsV2Filters(value as any)).toBe(false);
  });
});
