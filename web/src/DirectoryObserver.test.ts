// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  directoryObserverQuery,
  parseDirectoryUseObservation,
  readDirectoryUse,
} from "../e2e/directory-ledger-observer";

describe("read-only directory test observer", () => {
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
