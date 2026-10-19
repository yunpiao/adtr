import { describe, expect, it } from "vitest";
import { messageTimelineDates } from "./message-timeline";

const rows = (...times: string[]) => times.map((occurredAt) => ({ occurredAt }));
describe("AD-F-157 accepted-page UTC date boundaries", () => {
  it("does not invent a heading for an empty page", () => {
    expect(messageTimelineDates([])).toEqual([]);
  });
  it.each([
    ["asc", ["2026-10-09T23:59:59.999999999Z", "2026-10-10T00:00:00Z", "2026-10-10T00:00:00.000000001Z"], ["2026-10-09", "2026-10-10", null]],
    ["desc", ["2026-10-10T00:00:00.000000001Z", "2026-10-10T00:00:00Z", "2026-10-09T23:59:59.999999999Z"], ["2026-10-10", null, "2026-10-09"]],
  ] as const)("preserves %s input order across midnight without rounding nanoseconds", (_, times, expected) => {
    const input = Object.freeze(rows(...times).map((row) => Object.freeze(row)));
    expect(messageTimelineDates(input)).toEqual(expected);
    expect(input.map((row) => row.occurredAt)).toEqual(times);
  });
  it("same timestamp and same-day entries have one adjacent heading", () => {
    expect(messageTimelineDates(rows("0001-01-01T00:00:00Z", "0001-01-01T00:00:00Z", "0001-01-01T23:59:59.1Z")))
      .toEqual(["0001-01-01", null, null]);
  });
  it("marks repeated non-adjacent dates separately instead of sorting or merging", () => {
    expect(messageTimelineDates(rows("2026-10-10T00:00:00Z", "2026-10-09T00:00:00Z", "2026-10-10T01:00:00Z")))
      .toEqual(["2026-10-10", "2026-10-09", "2026-10-10"]);
  });
  it("starts every page independently even when a date continues from the previous page", () => {
    const page = rows("9999-12-31T23:59:59.999999999Z");
    expect(messageTimelineDates(page)).toEqual(["9999-12-31"]);
    expect(messageTimelineDates(page)).toEqual(["9999-12-31"]);
  });
});
