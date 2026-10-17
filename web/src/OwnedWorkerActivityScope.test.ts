import { describe, expect, it } from "vitest";
import {
  activityAlias,
  assertActivityScope,
} from "../e2e/owned-worker-activity-scope";

const nonce = "000102030405060708090a0b0c0d0e0f";

describe("owned activity scope", () => {
  it("matches the Go and Python fixed vector", () => {
    const key = activityAlias(nonce, "task-one");
    expect(key).toBe(
      "ci-activity-xjhrbwphlzfwschmfnm4kqsunp2qeowpnarubert6q2qx5ospmha",
    );
    expect(key).toHaveLength(64);
    expect(key).toMatch(/^ci-activity-[a-z2-7]{52}$/);
    expect(activityAlias(nonce, "task-two")).not.toBe(key);
    expect(activityAlias("10".repeat(16), "task-one")).not.toBe(key);
  });

  it("rejects malformed identity with only a fixed error", () => {
    for (const bad of [
      null,
      true,
      1,
      "",
      "ab".repeat(15),
      "AB".repeat(16),
      nonce + "\n",
      "private\nsecret",
    ]) {
      expect(() => activityAlias(bad as string, "task-one")).toThrow(
        "invalid owned activity identity",
      );
    }
    for (const bad of [
      null,
      true,
      1,
      "",
      "x".repeat(129),
      "task/secret",
      "task\n",
      "private\nsecret",
      "中文",
    ]) {
      expect(() => activityAlias(nonce, bad as string)).toThrow(
        "invalid owned activity identity",
      );
    }
    expect(activityAlias(nonce, "A_.:-0".repeat(20))).toHaveLength(64);
  });

  it("accepts exactly one original, one alias, and no other row", () => {
    expect(() =>
      assertActivityScope({ original: 1, alias: 1, other: 0 }),
    ).not.toThrow();
  });

  it("rejects unknown fields and all unsafe or mismatched counts", () => {
    const good = { original: 1, alias: 1, other: 0 };
    const bad: unknown[] = [
      null,
      true,
      [],
      1,
      "private",
      {},
      { original: 1, alias: 1 },
      { ...good, private: "secret" },
      { ...good, [Symbol("private")]: 0 },
      Object.create(good),
    ];
    for (const field of ["original", "alias", "other"]) {
      for (const value of [
        -1,
        2,
        true,
        null,
        "1",
        1.5,
        NaN,
        Infinity,
        Number.MAX_SAFE_INTEGER + 1,
      ]) {
        bad.push({ ...good, [field]: value });
      }
    }
    bad.push(
      { original: 0, alias: 1, other: 0 },
      { original: 1, alias: 0, other: 0 },
      { original: 1, alias: 1, other: 1 },
    );
    bad.push(
      Object.defineProperty({ alias: 1, other: 0 }, "original", {
        get() {
          throw new Error("private secret");
        },
      }),
    );
    for (const value of bad) {
      expect(() => assertActivityScope(value)).toThrow(/^invalid owned activity scope$/);
    }
  });
});
