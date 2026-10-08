// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  directoryObserverQuery,
  parseDirectoryUseObservation,
  readDirectoryUse,
} from "../e2e/directory-ledger-observer";
import {
  directoryV2ObserverQuery,
  readDirectoryV2Use,
} from "../e2e/directory-v2-ledger-observer";

describe("read-only directory test observer", () => {
  it("pins the v2 ledger, dependency and task purpose with only SELECT statements", async () => {
    const sql = directoryV2ObserverQuery("v2-task_1");
    expect(sql).toContain("d.consumer_kind='domain.directory_read.v2'");
    expect(sql).toContain("u.purpose='domain.directory_read.v2'");
    expect(sql).toContain("t.kind='domain.directory_read.v2'");
    expect(sql).toContain("u.task_id='v2-task_1'");
    expect(sql).not.toMatch(
      /\b(UPDATE|INSERT|DELETE|ALTER|DROP|CREATE|TRUNCATE)\b/u,
    );
    expect(directoryObserverQuery("v1-task")).not.toContain(
      "directory_read.v2",
    );
    for (const id of [
      "",
      "x' OR true --",
      "x;UPDATE",
      "x\n",
      "x\\y",
      "x".repeat(129),
    ]) {
      expect(() => directoryV2ObserverQuery(id)).toThrow();
      await expect(readDirectoryV2Use(id)).rejects.toThrow(
        "Invalid isolated directory task identifier",
      );
    }
  });
  it("rejects unsafe task identifiers before any process", async () => {
    for (const id of [
      "",
      "x' OR true --",
      "x;UPDATE",
      "x\n",
      "x\\y",
      "x".repeat(129),
    ]) {
      expect(() => directoryObserverQuery(id)).toThrow();
      await expect(readDirectoryUse(id)).rejects.toThrow(
        "Invalid isolated directory task identifier",
      );
    }
    expect(directoryObserverQuery("valid-task_1")).toContain(
      "u.task_id='valid-task_1'",
    );
  });
  it("distinguishes absent, opened and actual-return observations", () => {
    expect(parseDirectoryUseObservation("\n")).toBeNull();
    expect(
      parseDirectoryUseObservation(
        JSON.stringify({
          state: "opened",
          reason: null,
          dependencies: 1,
          taskState: "cancelled",
        }),
      ),
    ).toMatchObject({ state: "opened", dependencies: 1 });
    expect(
      parseDirectoryUseObservation(
        JSON.stringify({
          state: "quiesced",
          reason: "executor_returned",
          dependencies: 0,
          taskState: "cancelled",
        }),
      ),
    ).toMatchObject({ reason: "executor_returned", dependencies: 0 });
  });
  it("refuses malformed or contradictory evidence", () => {
    for (const value of [
      null,
      [],
      {},
      {
        state: "opened",
        reason: "executor_returned",
        dependencies: 1,
        taskState: "running",
      },
      {
        state: "quiesced",
        reason: null,
        dependencies: 0,
        taskState: "cancelled",
      },
      {
        state: "quiesced",
        reason: "executor_returned",
        dependencies: 1,
        taskState: "cancelled",
      },
      { state: "opened", reason: null, dependencies: 1, taskState: "unknown" },
      {
        state: "opened",
        reason: null,
        dependencies: 1,
        taskState: "running",
        opener: "invented",
      },
    ]) {
      expect(() =>
        parseDirectoryUseObservation(JSON.stringify(value)),
      ).toThrow();
    }
    expect(() => parseDirectoryUseObservation("x".repeat(4097))).toThrow();
    expect(() => parseDirectoryUseObservation("not JSON")).toThrow();
  });
});
