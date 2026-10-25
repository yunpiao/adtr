import { describe, expect, it } from "vitest";
import {
  matchesUserUACDisableBit,
  uacDisableBits,
  uacDisableBitLabels,
  userUACDisableBit,
  validUserAssetsV3Filters,
  type UserAssetsV3Filters,
} from "./user-assets-v3-filters";

describe("observed UAC ACCOUNTDISABLE partition", () => {
  it.each([
    [null, "not_returned"],
    [0, "clear"],
    [512, "clear"],
    [16, "clear"],
    [2147483648, "clear"],
    [65538, "set"],
    [2147483650, "set"],
    [2, "set"],
    [514, "set"],
    [18, "set"],
    [4294967295, "set"],
  ] as const)(
    "classifies %s as %s without inferring activity or lock state",
    (uac, bit) => {
      expect(userUACDisableBit(uac)).toBe(bit);
      expect(
        uacDisableBits.filter((filter) =>
          matchesUserUACDisableBit(uac, filter),
        ),
      ).toEqual([bit]);
      expect(matchesUserUACDisableBit(uac)).toBe(true);
    },
  );
  it.each([
    undefined,
    null,
    "",
    "SET",
    " clear",
    "clear ",
    "active",
    0,
    2,
    false,
    [],
    ["set"],
  ])("rejects present non-enum %j", (uacDisableBit) => {
    expect(
      validUserAssetsV3Filters({ uacDisableBit } as UserAssetsV3Filters),
    ).toBe(false);
  });
  it("requires exact optional presence and retains the original paired time validation", () => {
    expect(validUserAssetsV3Filters({})).toBe(true);
    for (const uacDisableBit of uacDisableBits) {
      expect(validUserAssetsV3Filters({ uacDisableBit })).toBe(true);
      expect(
        validUserAssetsV3Filters({
          uacDisableBit,
          startTm: "2026-01-01T00:00:00Z",
        }),
      ).toBe(false);
    }
    expect(Object.values(uacDisableBitLabels)).toEqual([
      "禁用位已设置",
      "禁用位未设置",
      "UAC未返回",
    ]);
  });
});
