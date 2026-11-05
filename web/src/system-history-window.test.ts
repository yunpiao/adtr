import { describe, expect, it } from "vitest";
import { fixedHistoryWindow, parseHistoryUTC } from "./system-history-window";

describe("explicit UTC history window", () => {
  it.each([
    "",
    "-1",
    "1969-12-31T23:59:59Z",
    "2026-02-29T12:00:00Z",
    "2026-04-31T12:00:00Z",
    "2026-01-01T24:00:00Z",
    "2026-01-01T00:60:00Z",
    "2026-01-01T00:00:60Z",
    "2026-01-01T00:00:00.000Z",
    "2026-01-01T00:00:00",
    "2026-01-01T00:00:00+00:00",
    " 2026-01-01T00:00:00Z",
    "2026-1-01T00:00:00Z",
  ])("rejects invalid or noncanonical %s", (value) => {
    expect(parseHistoryUTC(value)).toBeNull();
  });
  it("accepts epoch, leap calendar and exact 24 hour boundary", () => {
    expect(parseHistoryUTC("1970-01-01T00:00:00Z")).toBe(0);
    expect(parseHistoryUTC("2024-02-29T12:00:00Z")).toBe(
      Date.UTC(2024, 1, 29, 12) / 1000,
    );
    expect(
      fixedHistoryWindow("1970-01-01T00:00:00Z", "1970-01-02T00:00:00Z"),
    ).toEqual({ startTime: 0, endTime: 86400 });
    expect(
      fixedHistoryWindow("1970-01-01T00:00:00Z", "1970-01-02T00:00:01Z"),
    ).toBeNull();
    expect(
      fixedHistoryWindow("1970-01-01T00:00:01Z", "1970-01-01T00:00:01Z"),
    ).toBeNull();
    expect(
      fixedHistoryWindow("1970-01-01T00:00:01Z", "1970-01-01T00:00:00Z"),
    ).toBeNull();
  });
  it("parses DST transition text as UTC, independent of local timezone", () => {
    expect(parseHistoryUTC("2026-03-08T02:30:00Z")).toBe(
      Date.UTC(2026, 2, 8, 2, 30) / 1000,
    );
    expect(parseHistoryUTC("2026-11-01T01:30:00Z")).toBe(
      Date.UTC(2026, 10, 1, 1, 30) / 1000,
    );
  });
});
