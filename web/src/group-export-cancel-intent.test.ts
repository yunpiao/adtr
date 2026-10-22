// Synthetic protocol contracts. No claim of real browser/API/Worker evidence.
import { webcrypto } from "node:crypto";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./api";
import * as api from "./group-asset-export-v3-api";
import { exportProfile, exportRequest, exportReceipt, exportTask } from "./group-asset-export-v3-fixtures";
import { cancelMarkerBytes, cancelStorageKey, inspectCancelMarker, observeGroupExportCancel,
  submitGroupExportCancel, cancelUnknown, type CancelBinding } from "./group-export-cancel-intent";
const pending = { ...exportReceipt, downloadReady: false, downloadStatus: "pending" as const };
const ended = { ...pending, task: { ...exportTask, state: "cancelled" as const }, downloadStatus: "cancelled" as const };
const proof = { actorPassword: "SyntheticPassword123!", totpCode: "123456" };
function binding(): CancelBinding { return { profile: exportProfile, taskID: exportReceipt.taskID, request: structuredClone(exportRequest), signal: new AbortController().signal, assertCurrent: vi.fn() }; }
let held = false;
function locks() {
  Object.defineProperty(navigator, "locks", { configurable: true, value: { request: vi.fn(async (_name, options, callback) => {
    expect(options).toEqual({ mode: "exclusive", ifAvailable: true });
    if (held) return callback(null);
    held = true;
    try { return await callback({ name: _name }); } finally { held = false; }
  }) } });
}
beforeEach(() => {
  vi.stubGlobal("navigator", Object.create(navigator));
  localStorage.clear(); held = false; locks(); vi.stubGlobal("crypto", webcrypto);
  vi.spyOn(api.groupAssetExportAPI, "recheckCancelAuthority").mockResolvedValue(pending);
  vi.spyOn(api.groupAssetExportAPI, "detail").mockResolvedValue(pending);
  vi.spyOn(api.groupAssetExportAPI, "cancel").mockResolvedValue(exportTask);
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); localStorage.clear(); });
describe("durable original group export cancellation", () => {
  it("pins complete canonical request without proof/session/filter persistence", async () => {
    const b = binding();
    const bytes = await cancelMarkerBytes(b), marker = JSON.parse(bytes);
    expect(Object.keys(marker)).toEqual(["version", "kind", "actorID", "taskID", "domainId", "requestSHA256"]);
    expect(marker.requestSHA256).toMatch(/^[0-9a-f]{64}$/);
    expect(bytes).not.toMatch(/SyntheticPassword|123456|csrf|search|screenColumns|idempotencyKey/);
    const changes = { domainId: "other-domain", expectedRevision: "2", expectedCredentialRevision: "2", observationId: "other-observation", search: "different", creatTm: "desc", screenColumns: [...exportRequest.screenColumns].reverse() };
    for (const [key, value] of Object.entries(changes)) {
      const changed = { ...b, request: { ...b.request, [key]: value } };
      expect(await cancelMarkerBytes(changed)).not.toBe(bytes);
    }
  });
  it.each(["{", "{}", '{"version":1,"version":1}', "x".repeat(1025)])("never overwrites malformed marker %s", async (raw) => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, raw);
    expect(await inspectCancelMarker(b)).not.toBe("");
    await submitGroupExportCancel(b, proof, vi.fn());
    expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled(); expect(localStorage.getItem(key)).toBe(raw);
  });
  it("missing lock capability cannot conceal an already persisted unknown marker", async () => {
    const b = binding(); localStorage.setItem(cancelStorageKey(b.profile.ID, b.taskID), await cancelMarkerBytes(b));
    Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
    expect(await inspectCancelMarker(b)).toBe(cancelUnknown);
  });
  it("uses only actor/task lock identity, not request or domain", async () => {
    const b = binding(); await submitGroupExportCancel(b, proof, vi.fn());
    const changed = { ...b, request: { ...b.request, domainId: "another-domain" } };
    await submitGroupExportCancel(changed, proof, vi.fn());
    const calls = vi.mocked(navigator.locks.request).mock.calls;
    expect(calls[0][0]).toBe(calls[1][0]); expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledTimes(1);
  });
  it("reads authority before durable write/readback before dispatch", async () => {
    const order: string[] = [], b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID);
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockImplementation(async () => { expect(held).toBe(true); order.push("read"); return pending; });
    vi.mocked(api.groupAssetExportAPI.cancel).mockImplementation(async () => { expect(localStorage.getItem(key)).toBe(await cancelMarkerBytes(b)); order.push("post"); return exportTask; });
    await submitGroupExportCancel(b, proof, vi.fn()); expect(order).toEqual(["read", "post"]);
    expect(localStorage.getItem(key)).not.toBeNull();
  });
  it.each(["queued", "running", "retry_wait", "cancel_requested"] as const)("keeps unknown marker after %s observation", async (state) => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, await cancelMarkerBytes(b));
    vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValue({ ...pending, task: { ...exportTask, state } });
    const result = await observeGroupExportCancel(b); expect(result.queryOnly).toBe(true); expect(localStorage.getItem(key)).not.toBeNull();
    await submitGroupExportCancel(binding(), proof, vi.fn()); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it.each(["cancel_requested", "cancelled", "succeeded"] as const)("fresh %s before dispatch stops stale form", async (state) => {
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockResolvedValue({ ...pending, task: { ...exportTask, state } });
    await submitGroupExportCancel(binding(), proof, vi.fn()); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled(); expect(localStorage.length).toBe(0);
  });
  it.each(["getItem", "setItem"] as const)("storage %s failure sends zero mutations", async (method) => {
    vi.spyOn(Storage.prototype, method).mockImplementation(() => { throw new Error("private-canary"); });
    const result = await submitGroupExportCancel(binding(), proof, vi.fn()); expect(result.message).not.toContain("canary"); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it("mismatched durable writeback sends no mutation", async () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => undefined);
    await submitGroupExportCancel(binding(), proof, vi.fn()); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it.each(["missing", "busy", "rejected"])("lock %s has no fallback", async (mode) => {
    if (mode === "missing") Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
    if (mode === "busy") held = true;
    if (mode === "rejected") vi.mocked(navigator.locks.request).mockRejectedValue(new Error("private-canary"));
    const result = await submitGroupExportCancel(binding(), proof, vi.fn()); expect(result.queryOnly).toBe(true); expect(result.message).not.toContain("canary"); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it("two competing callers cannot dispatch twice", async () => {
    let release!: (v: typeof pending) => void;
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockReturnValue(new Promise((r) => { release = r; }));
    const one = submitGroupExportCancel(binding(), proof, vi.fn());
    await vi.waitFor(() => expect(held).toBe(true));
    const two = await submitGroupExportCancel(binding(), proof, vi.fn()); expect(two.queryOnly).toBe(true);
    release(pending); await one; expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledTimes(1);
  });
  it("unknown dispatch remains blocked on remount, with unchanged locator", async () => {
    sessionStorage.setItem("synthetic-locator", "unchanged");
    vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("network"));
    await expect(submitGroupExportCancel(binding(), proof, vi.fn())).rejects.toBeInstanceOf(ApiError);
    expect(await inspectCancelMarker(binding())).toBe(cancelUnknown);
    await submitGroupExportCancel(binding(), proof, vi.fn()); expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledTimes(1);
    expect(sessionStorage.getItem("synthetic-locator")).toBe("unchanged");
  });
  it("removes only matched terminal marker and verifies absence", async () => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, await cancelMarkerBytes(b));
    vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValue(ended);
    expect((await observeGroupExportCancel(b)).detail).toEqual(ended); expect(localStorage.getItem(key)).toBeNull();
  });
  it("terminal wrong request cannot clear marker", async () => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, await cancelMarkerBytes(b));
    vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValue({ ...ended, request: { ...exportRequest, search: "changed" } });
    await expect(observeGroupExportCancel(b)).rejects.toBeInstanceOf(ApiError); expect(localStorage.getItem(key)).not.toBeNull();
  });
  it("deletion uncertainty returns query-only without claiming persistence", async () => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, await cancelMarkerBytes(b));
    vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValue(ended);
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("private-canary"); });
    const result = await observeGroupExportCancel(b); expect(result.queryOnly).toBe(true); expect(result.message).toContain("不能确认记录是否仍保留");
  });
  it("uncertain absence readback does not claim a removed marker still exists", async () => {
    vi.spyOn(api, "isGroupExportCancelProofRejected").mockReturnValue(true);
    vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    const get = Storage.prototype.getItem, remove = Storage.prototype.removeItem;
    let removed = false;
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(function (this: Storage, key) { remove.call(this, key); removed = true; });
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (this: Storage, key) {
      if (removed) { removed = false; throw new Error("private-readback-canary"); }
      return get.call(this, key);
    });
    const result = await submitGroupExportCancel(binding(), proof, vi.fn());
    expect(result.state).toBe("blocked"); expect(result.queryOnly).toBe(true);
    expect(result.message).toContain("不能确认记录是否仍保留"); expect(localStorage.length).toBe(0);
    expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce();
  });
  it("fake proof error never unlocks an unknown request", async () => {
    vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    await expect(submitGroupExportCancel(binding(), proof, vi.fn())).rejects.toMatchObject({ status: 401 });
    expect(await inspectCancelMarker(binding())).toBe(cancelUnknown);
  });
  it("verified current proof error keeps lock continuously through authority and exact clear", async () => {
    vi.spyOn(api, "isGroupExportCancelProofRejected").mockReturnValue(true);
    vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    const phases: boolean[] = [];
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockImplementation(async () => { phases.push(held); return pending; });
    const hide = vi.fn(), result = await submitGroupExportCancel(binding(), proof, hide);
    expect(result.state).toBe("proof-rejected"); expect(phases).toEqual([true, true]); expect(hide).toHaveBeenCalledTimes(2); expect(localStorage.length).toBe(0); expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledTimes(1);
  });
  it("rejected proof with failed fresh authority remains query-only", async () => {
    vi.spyOn(api, "isGroupExportCancelProofRejected").mockReturnValue(true);
    vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockResolvedValueOnce(pending).mockRejectedValueOnce(new ApiError("forbidden", 403));
    await expect(submitGroupExportCancel(binding(), proof, vi.fn())).rejects.toMatchObject({ status: 403 });
    expect(localStorage.length).toBe(1); expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce();
  });
  it.each([401, 403, 404])("pre-dispatch authority %i sends zero mutation", async (status) => {
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockRejectedValue(new ApiError("forbidden", status));
    await expect(submitGroupExportCancel(binding(), proof, vi.fn())).rejects.toMatchObject({ status });
    expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled(); expect(localStorage.length).toBe(0);
  });
  it("in-place original request rotation fences preflight before persistence", async () => {
    const b = binding();
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockImplementation(async () => { b.request.search = "changed"; return pending; });
    await expect(submitGroupExportCancel(b, proof, vi.fn())).rejects.toMatchObject({ code: "invalid_response" });
    expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled(); expect(localStorage.length).toBe(0);
  });
  it("marker replacement during same-lock proof reauthorization fails closed (ABA boundary)", async () => {
    vi.spyOn(api, "isGroupExportCancelProofRejected").mockReturnValue(true);
    vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID);
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockResolvedValueOnce(pending).mockImplementationOnce(async () => { expect(held).toBe(true); localStorage.setItem(key, "replacement"); return pending; });
    const result = await submitGroupExportCancel(b, proof, vi.fn()); expect(result.queryOnly).toBe(true); expect(localStorage.getItem(key)).toBe("replacement");
  });
  it("aborted late response cannot remove marker", async () => {
    const b = binding(), control = new AbortController(); b.signal = control.signal;
    vi.mocked(api.groupAssetExportAPI.cancel).mockImplementation(async () => { control.abort(); return ended.task; });
    await expect(submitGroupExportCancel(b, proof, vi.fn())).rejects.toBeDefined(); expect(localStorage.length).toBe(1);
  });
});

describe("group-only binding and terminal fences", () => {
  it("uses the group namespace, complete fixed request order, and no user SAM filter", async () => {
    const b = binding(), bytes = await cancelMarkerBytes(b);
    const expectedRequest = JSON.stringify({ domainId: b.request.domainId, expectedRevision: b.request.expectedRevision,
      expectedCredentialRevision: b.request.expectedCredentialRevision, observationId: b.request.observationId,
      search: b.request.search, creatTm: b.request.creatTm,
      screenColumns: b.request.screenColumns });
    const hash = await webcrypto.subtle.digest("SHA-256", new TextEncoder().encode(expectedRequest));
    const digest = Array.from(new Uint8Array(hash), (v) => v.toString(16).padStart(2, "0")).join("");
    expect(bytes).toBe(JSON.stringify({ version: 1, kind: "group_assets.v3.export", actorID: b.profile.ID,
      taskID: b.taskID, domainId: b.request.domainId, requestSHA256: digest }));
    expect(cancelStorageKey(b.profile.ID, b.taskID)).toBe(`adtr.group-export-cancel.v1.${b.profile.ID}.${b.taskID}`);
    expect(Object.keys(JSON.parse(expectedRequest))).not.toContain("sAMAccountName");
  });
  it.each([0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1])("rejects unsafe actor %s", (actor) => {
    expect(() => cancelStorageKey(actor, exportTask.taskUUID)).toThrow(ApiError);
  });
  it.each(["", "../task", "task/id", "x".repeat(129), "任务"])("rejects noncanonical task %s", (taskID) => {
    expect(() => cancelStorageKey(1, taskID)).toThrow(ApiError);
  });
  it.each(["actorID", "taskID", "domainId", "kind", "requestSHA256", "version", "extra", "duplicate"])("keeps a mismatched %s marker unchanged", async (field) => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID), bytes = await cancelMarkerBytes(b), marker = JSON.parse(bytes);
    let raw: string;
    if (field === "duplicate") raw = bytes.replace('{"version":1,', '{"version":1,"version":1,');
    else { marker[field] = field === "actorID" || field === "version" ? 2 : "wrong-binding"; raw = JSON.stringify(marker); }
    localStorage.setItem(key, raw);
    vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValue(ended);
    expect((await observeGroupExportCancel(b)).queryOnly).toBe(true);
    await submitGroupExportCancel(b, proof, vi.fn());
    expect(localStorage.getItem(key)).toBe(raw); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it("reads only the selected actor/task key and never touches a user export marker", async () => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID), foreign = `adtr.user-export-cancel.v1.${b.profile.ID}.${b.taskID}`;
    localStorage.setItem(foreign, "unrelated-user-marker");
    const read = vi.spyOn(Storage.prototype, "getItem");
    await submitGroupExportCancel(b, proof, vi.fn());
    expect(read.mock.calls.every(([name]) => name === key)).toBe(true);
    read.mockRestore(); expect(localStorage.getItem(foreign)).toBe("unrelated-user-marker");
  });
  it("hides prepared private state synchronously before requesting the lock", async () => {
    const hide = vi.fn(), result = submitGroupExportCancel(binding(), proof, hide);
    expect(hide).toHaveBeenCalledOnce(); expect(navigator.locks.request).not.toHaveBeenCalled();
    await result;
  });
  it("verifies persistence readback immediately before the single dispatch", async () => {
    const events: string[] = [], get = Storage.prototype.getItem, set = Storage.prototype.setItem;
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (this: Storage, key) { events.push("get"); return get.call(this, key); });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, key, value) { events.push("set"); return set.call(this, key, value); });
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockImplementation(async () => { events.push("authority"); return pending; });
    vi.mocked(api.groupAssetExportAPI.cancel).mockImplementation(async () => { events.push("post"); return exportTask; });
    await submitGroupExportCancel(binding(), proof, vi.fn());
    expect(events).toEqual(["get", "authority", "set", "get", "post"]);
  });
  it("unknown malformed success never clears a barrier or authorizes another POST", async () => {
    vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_response"));
    await expect(submitGroupExportCancel(binding(), proof, vi.fn())).rejects.toBeInstanceOf(ApiError);
    await observeGroupExportCancel(binding()); await submitGroupExportCancel(binding(), proof, vi.fn());
    expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce(); expect(localStorage.length).toBe(1);
  });
  it("does not clear from a terminal response when the locked fresh read is running", async () => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, await cancelMarkerBytes(b));
    vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValueOnce(ended).mockResolvedValue(pending);
    const result = await observeGroupExportCancel(b);
    expect(result.detail?.task.state).toBe("queued"); expect(result.queryOnly).toBe(true); expect(localStorage.getItem(key)).not.toBeNull();
  });
  it("terminal clearance happens only while the exclusive lock owns a fresh original detail", async () => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, await cancelMarkerBytes(b));
    const states: boolean[] = [];
    vi.mocked(api.groupAssetExportAPI.detail).mockImplementation(async () => { states.push(held); return ended; });
    const remove = Storage.prototype.removeItem;
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(function (this: Storage, name) { expect(held).toBe(true); remove.call(this, name); });
    await observeGroupExportCancel(b); expect(states).toEqual([false, true]); expect(localStorage.getItem(key)).toBeNull();
  });
  it("an old prepared running form cannot dispatch after another entry observed terminal", async () => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, await cancelMarkerBytes(b));
    vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValue(ended); await observeGroupExportCancel(b);
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockResolvedValue(ended);
    const result = await submitGroupExportCancel(b, proof, vi.fn());
    expect(result.detail?.task.state).toBe("cancelled"); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it.each(["cancel_requested", "cancelled", "succeeded"] as const)("proof rejection observes %s without claiming a successful cancel", async (state) => {
    vi.spyOn(api, "isGroupExportCancelProofRejected").mockReturnValue(true);
    vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockResolvedValueOnce(pending).mockResolvedValueOnce({ ...pending, task: { ...exportTask, state } });
    const result = await submitGroupExportCancel(binding(), proof, vi.fn());
    expect(result.state).toBe("observed"); expect(result.queryOnly).toBe(true); expect(result.detail?.task.state).toBe(state);
    expect(localStorage.length).toBe(state === "cancel_requested" ? 1 : 0); expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce();
  });
  it("in-place session rotation during reauthorization cannot remove the marker", async () => {
    const b = { ...binding(), profile: { ...exportProfile } };
    vi.spyOn(api, "isGroupExportCancelProofRejected").mockReturnValue(true);
    vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockResolvedValueOnce(pending).mockImplementationOnce(async () => {
      b.profile.csrfToken = "rotated-session"; return pending;
    });
    await expect(submitGroupExportCancel(b, proof, vi.fn())).rejects.toMatchObject({ status: 401 });
    expect(localStorage.length).toBe(1); expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce();
  });
  it("a failed postcheck retains the marker despite a successful cancel response", async () => {
    vi.mocked(api.groupAssetExportAPI.detail).mockRejectedValue(new ApiError("not_found", 404));
    await expect(submitGroupExportCancel(binding(), proof, vi.fn())).rejects.toMatchObject({ status: 404 });
    expect(localStorage.length).toBe(1); expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce();
  });
});

it.each(["startTm", "endTm", "sAMAccountName", "groupType", "groupScope"])("rejects unsupported group input %s before lock or mutation", async (field) => {
  const b = binding(); Object.assign(b.request, { [field]: "unsupported" });
  const write = vi.spyOn(Storage.prototype, "setItem");
  expect(api.validExportRequest(b.request)).toBe(false);
  await expect(cancelMarkerBytes(b)).rejects.toMatchObject({ code: "invalid_input" });
  await expect(submitGroupExportCancel(b, proof, vi.fn())).rejects.toMatchObject({ code: "invalid_input" });
  expect(write).not.toHaveBeenCalled(); expect(navigator.locks.request).not.toHaveBeenCalled();
  expect(api.groupAssetExportAPI.recheckCancelAuthority).not.toHaveBeenCalled(); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
});
it.each(["group-task.1", "not-a-uuid", "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF", "x".repeat(128)])("preserves existing valid group export ID %s", async (taskID) => {
  expect(api.validExportID(taskID)).toBe(true);
  expect(cancelStorageKey(1, taskID)).toBe(`adtr.group-export-cancel.v1.1.${taskID}`);
  const b = { ...binding(), taskID };
  expect(JSON.parse(await cancelMarkerBytes(b)).taskID).toBe(taskID);
});

it.each(["missing", "rejected"])("SHA256 %s never downgrades to a memory cancellation barrier", async (mode) => {
  vi.stubGlobal("crypto", mode === "missing" ? {} : { subtle: { digest: vi.fn().mockRejectedValue(new Error("private-crypto-canary")) } });
  const result = await submitGroupExportCancel(binding(), proof, vi.fn());
  expect(result.queryOnly).toBe(true); expect(result.message).toContain("浏览器缺少可靠");
  expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled(); expect(localStorage.length).toBe(0);
  expect(navigator.locks.request).not.toHaveBeenCalled();
});
it("a digest failure cannot conceal the currently authorized task state during observation", async () => {
  const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID);
  localStorage.setItem(key, "existing-unknown-marker");
  vi.stubGlobal("crypto", { subtle: { digest: vi.fn().mockRejectedValue(new Error("private-crypto-canary")) } });
  const result = await observeGroupExportCancel(b);
  expect(result.detail).toEqual(pending); expect(result.queryOnly).toBe(true);
  expect(result.message).not.toContain("canary"); expect(localStorage.getItem(key)).toBe("existing-unknown-marker");
});
it("observer captures the authority tuple before reads and cannot clear after in-place session rotation", async () => {
  const b = { ...binding(), profile: { ...exportProfile } }, key = cancelStorageKey(b.profile.ID, b.taskID);
  localStorage.setItem(key, await cancelMarkerBytes(b));
  vi.mocked(api.groupAssetExportAPI.detail).mockImplementation(async () => { b.profile.csrfToken = "changed"; return ended; });
  await expect(observeGroupExportCancel(b)).rejects.toMatchObject({ status: 401 });
  expect(localStorage.getItem(key)).not.toBeNull(); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
});
it("passes a defensive authority Profile into the single cancel invocation and matching proof brand", async () => {
  const b = binding(), branded = vi.spyOn(api, "isGroupExportCancelProofRejected").mockReturnValue(true);
  vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
  await submitGroupExportCancel(b, proof, vi.fn());
  const args = vi.mocked(api.groupAssetExportAPI.cancel).mock.calls[0];
  expect(args[6]).toEqual(b.profile); expect(args[6]).not.toBe(b.profile);
  expect(branded.mock.calls[0][1]).toEqual({ taskID: b.taskID, request: b.request, actor: args[6], signal: b.signal });
  expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce();
});
it("invalidation during durable-write preparation cannot write or dispatch", async () => {
  const b = binding(), controller = new AbortController(); b.signal = controller.signal;
  const write = vi.spyOn(Storage.prototype, "setItem");
  await expect(submitGroupExportCancel(b, proof, vi.fn(), () => controller.abort())).rejects.toBeDefined();
  expect(write).not.toHaveBeenCalled(); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
});
