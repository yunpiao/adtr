import { describe, expect, it } from "vitest";
import { parseWorkerControlState, workerControl } from "../e2e/directory-v4-runtime";

describe("owned v4 Node controller boundary", () => {
  it("accepts only the closed C1 safe state grammar", () => {
    for (const value of [
      { state: "not_started", launchCount: 0, failure: null },
      { state: "starting", launchCount: 0, failure: null },
      { state: "starting", launchCount: 1, failure: null },
      { state: "ready", launchCount: 1, failure: null },
      { state: "failed", launchCount: 0, failure: "launch_failed" },
      { state: "closed", launchCount: 1, failure: "cleanup_failed" },
    ]) expect(parseWorkerControlState(JSON.stringify(value))).toEqual(value);
  });
  it("rejects unknown, duplicate, private, oversized and impossible states safely", () => {
    for (const text of [
      '{"state":"ready","launchCount":0,"failure":null}',
      '{"state":"not_started","launchCount":1,"failure":null}',
      '{"state":"ready","launchCount":1,"failure":"readiness_failed"}',
      '{"state":"failed","launchCount":1,"failure":null}',
      '{"state":"ready","state":"ready","launchCount":1,"failure":null}',
      '{"state":"ready","launchCount":1,"failure":null,"token":"private"}',
      '{"state":"ready","launchCount":2,"failure":null}',
      '{"state":"ready","launchCount":true,"failure":null}',
      '{"state":"ready","launchCount":1,"failure":"raw_exception"}',
      " ".repeat(2049), "null", "[]", "{",
    ]) expect(() => parseWorkerControlState(text)).toThrow("Invalid owned Worker controller state");
  });
  it("rejects non-owned endpoints before any HTTP and does not expose private values", () => {
    const oldURL = process.env.ADTR_E2E_V4_WORKER_CONTROL_URL;
    const oldToken = process.env.ADTR_E2E_V4_WORKER_CONTROL_TOKEN;
    try {
      process.env.ADTR_E2E_V4_WORKER_CONTROL_TOKEN = "a".repeat(43);
      for (const value of ["https://127.0.0.1:1234", "http://example.invalid:1234", "http://127.0.0.1:1234/start", "http://user:private@127.0.0.1:1234", "http://127.0.0.1:1234/?token=private"]) {
        process.env.ADTR_E2E_V4_WORKER_CONTROL_URL = value;
        expect(() => workerControl()).toThrow("Invalid private owned Worker controller");
      }
    } finally {
      if (oldURL === undefined) delete process.env.ADTR_E2E_V4_WORKER_CONTROL_URL; else process.env.ADTR_E2E_V4_WORKER_CONTROL_URL = oldURL;
      if (oldToken === undefined) delete process.env.ADTR_E2E_V4_WORKER_CONTROL_TOKEN; else process.env.ADTR_E2E_V4_WORKER_CONTROL_TOKEN = oldToken;
    }
  });
});
