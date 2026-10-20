// Offline parser/query contracts. No Docker or process execution in these tests.
import { describe, expect, it } from "vitest";
import { parseDirectoryV4Use, directoryV4ObserverQuery, parseDirectoryV4ReaderCounts, directoryV4ReaderCountQuery, parseDirectoryV4KeyCounts, directoryV4KeyQuery, assertQueuedReserved, assertQueuedQuiesced, assertRunningOpened, assertExecutorReturned } from "../e2e/directory-v4-ledger-observer";
const original = { taskUUID: "task-v4-1", domainId: "domain-1", actorId: 1, expectedRevision: "2", expectedCredentialGeneration: "3" };
const time = "2026-10-20T12:00:00.000001Z";
const base = {
  taskUUID: original.taskUUID, domainId: original.domainId, actorId: "1", revision: "2", generation: "3", accountId: "account-1", accountRevision: "1", grantRole: "platform_admin", grantRevision: "1", policyRevision: "a".repeat(64),
  state: "reserved", reason: null, taskState: "queued", attempt: 0, progress: 0, fence: "0", leaseOwner: "", leaseUntil: null, resultVersion: "0", updatedAt: time, terminalAt: null,
  openedAt: null, quiescedAt: null, openerOwner: null, openerFence: null, openerAttempt: null,
  identityMatched: true, payloadMatched: true, emptyResult: true, emptyCursor: true, liveOpenerMatched: false,
  dependencies: 1, matchedDependencies: 1, bindingDependencies: 1, observations: 0, submittedEvents: 1, cancelledEvents: 0, executionEvents: 0, otherEvents: 0,
};
const parse = (patch: Record<string, unknown> = {}) => parseDirectoryV4Use(JSON.stringify({ ...base, ...patch }), original);
const openedPatch = { state: "opened", taskState: "running", attempt: 1, progress: 41, fence: "9007199254740993", leaseOwner: "worker-1:slot-1", leaseUntil: time, openedAt: time, openerOwner: "worker-1:slot-1", openerFence: "9007199254740993", openerAttempt: 1, liveOpenerMatched: true, executionEvents: 2 };
describe("fixed v4 ledger observer", () => {
  it("requires the exact original identity and safe flat grammar", () => {
    assertQueuedReserved(parse());
    for (const patch of [{ taskUUID: "other" }, { domainId: "other" }, { actorId: "2" }, { revision: "1" }, { generation: "1" }, { payloadMatched: false }, { identityMatched: false }, { unknown: true }, { observations: -1 }, { fence: 1 }, { fence: "01" }, { accountRevision: 9007199254740993 }, { updatedAt: "not-a-time" }, { openerOwner: "secret\"injection" }, { emptyResult: false }, { emptyCursor: false }, { bindingDependencies: 0 }, { matchedDependencies: 0 }, { dependencies: 2, matchedDependencies: 2 }]) expect(() => parse(patch)).toThrow("Invalid isolated v4 ledger evidence");
    for (const text of ["", "null", "[]", "{}", "x".repeat(16385), JSON.stringify(base).replace('"attempt":0', '"attempt":0,"attempt":0'), JSON.stringify(base) + JSON.stringify(base)]) expect(() => parseDirectoryV4Use(text, original)).toThrow();
    expect(() => parseDirectoryV4Use(JSON.stringify(base).replace('"state":"reserved"', '"state":"reserved","\\u0073tate":"reserved"'), original)).toThrow();
    expect(() => parseDirectoryV4Use(JSON.stringify(base).replace('"state":', '"\\u0073tate":'), original)).toThrow();
  });
  it("keeps raw identity/opener material out of artifacts", () => {
    const snapshot = parse();
    expect(Object.keys(snapshot)).toEqual([]);
    expect(() => JSON.stringify(snapshot)).toThrow();
    expect(Object.keys(snapshot.facts).sort()).toEqual(["state", "reason", "taskState", "attempt", "progress", "dependencies", "observations", "bindingPreserved", "identityMatched", "liveOpenerMatched", "submittedEvents", "cancelledEvents", "executionEvents"].sort());
  });
  it("proves never-opened original before and after a real Worker run", () => {
    const initial = parse();
    assertQueuedReserved(parse({ taskState: "cancelled", terminalAt: time, cancelledEvents: 1 }), true);
    const terminal = { state: "quiesced", reason: "never_opened_terminal", taskState: "cancelled", terminalAt: time, quiescedAt: time, dependencies: 0, matchedDependencies: 0, cancelledEvents: 1 };
    assertQueuedQuiesced(parse(terminal), initial);
    for (const patch of [{ fence: "1" }, { executionEvents: 1 }, { otherEvents: 1 }, { observations: 1 }, { accountId: "other-account" }, { grantRevision: "2" }]) expect(() => assertQueuedQuiesced(parse({ ...terminal, ...patch }), initial)).toThrow();
  });
  it("retains exact opened owner/fence/attempt/time across executor return", () => {
    const opened = parse(openedPatch);
    assertRunningOpened(opened);
    const terminal = { ...openedPatch, state: "quiesced", reason: "executor_returned", taskState: "cancelled", terminalAt: time, quiescedAt: time, dependencies: 0, matchedDependencies: 0, cancelledEvents: 1, executionEvents: 3, leaseOwner: "", leaseUntil: null, liveOpenerMatched: false };
    assertExecutorReturned(parse(terminal), opened);
    for (const patch of [{ openerOwner: "other-owner" }, { openerFence: "9007199254740994" }, { openedAt: "2026-10-20T12:00:01.000001Z" }, { observations: 1 }, { policyRevision: "b".repeat(64) }]) expect(() => assertExecutorReturned(parse({ ...terminal, ...patch }), opened)).toThrow();
    expect(() => assertRunningOpened(parse({ ...openedPatch, liveOpenerMatched: false }))).toThrow();
  });
  it("generates only the fixed SELECT for a validated task/domain/actor", () => {
    const sql = directoryV4ObserverQuery(original);
    expect(sql.startsWith("SELECT ")).toBe(true);
    expect(sql).not.toMatch(/\b(?:UPDATE|DELETE|INSERT|CALL|COPY|DO|TRUNCATE)\b/iu);
    expect(sql).not.toContain(";");
    expect(sql).toContain("'domain.directory_read.v4'");
    expect(sql).toContain("'domain.directory_read.v3'");
    expect(sql).toContain("'domain.connection_binding'");
    for (const patch of [{ taskUUID: "';DROP" }, { domainId: "\n" }, { actorId: NaN }, { actorId: 0 }, { expectedRevision: "0" }, { expectedCredentialGeneration: "1 OR true" }]) expect(() => directoryV4ObserverQuery({ ...original, ...patch })).toThrow();
  });
});

it("accepts genuine terminal-before-ack snapshots without inventing return proof", () => {
  const pending = parse({ ...openedPatch, taskState: "cancelled", terminalAt: time, leaseOwner: "", leaseUntil: null, liveOpenerMatched: false, cancelledEvents: 1, executionEvents: 3 });
  expect(pending.facts.state).toBe("opened");
  expect(() => assertExecutorReturned(pending, parse(openedPatch))).toThrow();
});
// Reader probes use a second fixed SELECT and a closed projection. These are
// authored parser/query contracts, not executed database or browser evidence.
describe("closed v4 digest and reader-count witnesses", () => {
  it("emits hashes instead of private source/account/grant/opener pins", () => {
    const initial = parse().evidence, opened = parse(openedPatch).evidence;
    expect(initial.openerSHA256).toBeNull(); expect(initial.fencingZero).toBe(true);
    expect(initial.taskSHA256).toMatch(/^[0-9a-f]{64}$/u);
    expect(opened.identitySHA256).toBe(initial.identitySHA256);
    expect(opened.openerSHA256).toMatch(/^[0-9a-f]{64}$/u);
    expect(opened.fencingZero).toBe(false);
    const text = JSON.stringify(opened);
    for (const secret of [base.taskUUID, base.domainId, base.accountId, base.grantRole, openedPatch.leaseOwner, openedPatch.openerFence, time]) expect(text.includes(secret)).toBe(false);
    expect(parse({ accountId: "different-account" }).evidence.identitySHA256).not.toBe(initial.identitySHA256);
  });
  it("requires genuine succeeded original plus zero reader-owned creation", () => {
    const row = { originalTaskUUID: original.taskUUID, originalState: "succeeded", originalResultVersion: "5", observationSHA256: "b".repeat(64), tasks: 1, uses: 1, dependencies: 1, observations: 1, readerTasks: 0, readerUses: 0 };
    const parsed = parseDirectoryV4ReaderCounts(JSON.stringify(row), original.taskUUID);
    expect(parsed).toMatchObject({ originalState: "succeeded", tasks: 1, readerTasks: 0, readerUses: 0 });
    expect("originalTaskUUID" in parsed).toBe(false);
    for (const patch of [{ originalTaskUUID: "different" }, { originalState: "cancelled" }, { readerTasks: 1 }, { readerUses: 1 }, { observations: 0 }, { dependencies: -1 }, { originalResultVersion: 5 }, { observationSHA256: "raw" }, { extra: true }]) expect(() => parseDirectoryV4ReaderCounts(JSON.stringify({ ...row, ...patch }), original.taskUUID)).toThrow();
    expect(() => parseDirectoryV4ReaderCounts(JSON.stringify(row).replace('"tasks":1', '"tasks":1,"tasks":1'), original.taskUUID)).toThrow();
    expect(() => parseDirectoryV4ReaderCounts(JSON.stringify(row).replace('"tasks":1', '"tasks":1,"\\u0074asks":1'), original.taskUUID)).toThrow();
    expect(() => parseDirectoryV4ReaderCounts(JSON.stringify(row).replace('"tasks":', '"\\u0074asks":'), original.taskUUID)).toThrow();
  });
  it("keeps reader snapshots one bounded SELECT with validated original inputs", () => {
    const sql = directoryV4ReaderCountQuery(original);
    expect(sql.startsWith("SELECT ")).toBe(true);
    expect(sql).not.toMatch(/\b(?:INSERT|UPDATE|DELETE|TRUNCATE|ALTER|DROP|COMMIT|BEGIN|CALL)\b|;/iu);
    expect(sql).toContain("t.kind='domain.directory_read.v4'");
    expect(sql).toContain("t.state='succeeded'");
    for (const patch of [{ taskUUID: "x'; DELETE FROM adtr.tasks;--" }, { actorId: 0 }, { actorId: 1.5 }, { domainId: "invalid value" }]) expect(() => directoryV4ReaderCountQuery({ ...original, ...patch })).toThrow();
  });
});
describe("actual original-key persistence observer", () => {
  const input = { ...original, idempotencyKey: "actual-ui-key" };
  const absent = { tasks: 0, uses: 0, observations: 0, taskUUID: null, taskState: null, observationSHA256: null, identityMatched: true };
  const found = { tasks: 1, uses: 1, observations: 1, taskUUID: original.taskUUID, taskState: "succeeded", observationSHA256: "c".repeat(64), identityMatched: true };
  it("distinguishes genuine zero rows from one exact persisted task/use/observation", () => {
    expect(parseDirectoryV4KeyCounts(JSON.stringify(absent), input)).toMatchObject({ tasks: 0, uses: 0, taskSHA256: null });
    expect(parseDirectoryV4KeyCounts(JSON.stringify(found), input)).toMatchObject({ tasks: 1, uses: 1, observations: 1, taskState: "succeeded" });
    for (const patch of [{ tasks: 2 }, { uses: 0 }, { observations: 0 }, { identityMatched: false }, { taskUUID: null }, { taskState: "running" }, { observationSHA256: "bad" }, { extra: true }]) expect(() => parseDirectoryV4KeyCounts(JSON.stringify({ ...found, ...patch }), input)).toThrow();
    expect(() => parseDirectoryV4KeyCounts(JSON.stringify(found).replace('"tasks":1', '"tasks":1,"\\u0074asks":1'), input)).toThrow();
  });
  it("uses the authentic domain/kind/key and verifies actor rather than masking conflicts", () => {
    const sql = directoryV4KeyQuery(input);
    expect(sql).toContain("t.idempotency_key='actual-ui-key'");
    expect(sql).toContain("t.actor_id=1"); expect(sql).toContain("t.kind='domain.directory_read.v4'");
    expect(sql).not.toMatch(/\b(?:INSERT|UPDATE|DELETE|TRUNCATE|ALTER|DROP|COMMIT|BEGIN|CALL)\b|;/iu);
    expect(() => directoryV4KeyQuery({ ...input, idempotencyKey: "'; DELETE FROM adtr.tasks;--" })).toThrow();
  });
});
