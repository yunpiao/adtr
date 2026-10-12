import { createHash } from "node:crypto";
import { describe, expect, it } from "vitest";
import {
  directorySearchScalarLowerSHA256,
  directorySearchUnicodeVersion,
} from "./directory-search-case-map";
import {
  directoryLiteralSearchMatches,
  directorySearchLowercase,
} from "./directory-search-lowercase";

describe("backend-pinned directory literal search", () => {
  it("matches Go Unicode simple lowercase over every scalar", () => {
    expect(directorySearchUnicodeVersion).toBe("17.0.0");
    const hash = createHash("sha256");
    for (let point = 0; point <= 0x10ffff; point++) {
      if (point >= 0xd800 && point <= 0xdfff) continue;
      hash.update(directorySearchLowercase(String.fromCodePoint(point)));
    }
    expect(hash.digest("hex")).toBe(directorySearchScalarLowerSHA256);
  });
  it("preserves literal structure and differs from contextual/expanding JS lower", () => {
    expect(directorySearchLowercase("İ ΟΣ ẞ K 𐐀")).toBe("i οσ ß k 𐐨");
    expect(directoryLiteralSearchMatches(["İ"], "i\u0307")).toBe(false);
    expect(directoryLiteralSearchMatches(["ΟΣ"], "οσ")).toBe(true);
    expect(directoryLiteralSearchMatches(["ΟΣ"], "ος")).toBe(false);
    expect(directoryLiteralSearchMatches(["é"], "e\u0301")).toBe(false);
    expect(directoryLiteralSearchMatches(["Straße"], "STRASSE")).toBe(false);
    expect(
      directoryLiteralSearchMatches([" A\u0000[B]*%_ "], "a\u0000[b]*%_"),
    ).toBe(true);
    expect(directoryLiteralSearchMatches(["A"], " A ")).toBe(false);
    expect(directoryLiteralSearchMatches(["a", "b"], "ab")).toBe(false);
    expect(directoryLiteralSearchMatches([null], "null")).toBe(false);
    expect(directoryLiteralSearchMatches([null], "")).toBe(true);
  });
});
