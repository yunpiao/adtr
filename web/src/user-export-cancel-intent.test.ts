// Synthetic protocol contracts. No claim of real browser/API/Worker evidence.
import { webcrypto } from "node:crypto";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./api";
import * as api from "./user-asset-export-v3-api";
import { exportProfile, exportRequest, exportReceipt, exportTask } from "./user-asset-export-v3-fixtures";
import { cancelMarkerBytes, cancelStorageKey, inspectCancelMarker, observeUserExportCancel,
  submitUserExportCancel, cancelUnknown, type CancelBinding } from "./user-export-cancel-intent";
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
  vi.spyOn(api.userAssetExportAPI, "recheckCancelAuthority").mockResolvedValue(pending);
  vi.spyOn(api.userAssetExportAPI, "detail").mockResolvedValue(pending);
  vi.spyOn(api.userAssetExportAPI, "cancel").mockResolvedValue(exportTask);
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); localStorage.clear(); });
describe("durable original user export cancellation", () => {
  it("pins complete canonical request without proof/session/filter persistence", async () => {
    const b = binding(), bytes = await cancelMarkerBytes(b), marker = JSON.parse(bytes);
    expect(Object.keys(marker)).toEqual(["version", "kind", "actorID", "taskID", "domainId", "requestSHA256"]);
    expect(marker.requestSHA256).toMatch(/^[0-9a-f]{64}$/);
    expect(bytes).not.toMatch(/SyntheticPassword|123456|csrf|search|screenColumns|idempotencyKey/);
    const changes = { domainId: "other-domain", expectedRevision: "2", expectedCredentialRevision: "2", observationId: "other-observation", search: "different", sAMAccountName: "different", startTm: "2026-01-01T00:00:00Z", endTm: "2026-12-31T00:00:00Z", creatTm: "desc", screenColumns: [...exportRequest.screenColumns].reverse() };
    for (const [key, value] of Object.entries(changes)) {
      const changed = { ...b, request: { ...b.request, [key]: value } };
      expect(await cancelMarkerBytes(changed)).not.toBe(bytes);
    }
  });
  it.each(["{", "{}", '{"version":1,"version":1}', "x".repeat(1025)])("never overwrites malformed marker %s", async (raw) => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, raw);
    expect(await inspectCancelMarker(b)).not.toBe("");
    await submitUserExportCancel(b, proof, vi.fn());
    expect(api.userAssetExportAPI.cancel).not.toHaveBeenCalled(); expect(localStorage.getItem(key)).toBe(raw);
  });
  it("missing lock capability cannot conceal an already persisted unknown marker", async () => {
    const b = binding(); localStorage.setItem(cancelStorageKey(b.profile.ID, b.taskID), await cancelMarkerBytes(b));
    Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
    expect(await inspectCancelMarker(b)).toBe(cancelUnknown);
  });
  it("uses only actor/task lock identity, not request or domain", async () => {
    const b = binding(); await submitUserExportCancel(b, proof, vi.fn());
    const changed = { ...b, request: { ...b.request, domainId: "another-domain" } };
    await submitUserExportCancel(changed, proof, vi.fn());
    const calls = vi.mocked(navigator.locks.request).mock.calls;
    expect(calls[0][0]).toBe(calls[1][0]); expect(api.userAssetExportAPI.cancel).toHaveBeenCalledTimes(1);
  });
  it("reads authority before durable write/readback before dispatch", async () => {
    const order: string[] = [], b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID);
    vi.mocked(api.userAssetExportAPI.recheckCancelAuthority).mockImplementation(async () => { expect(held).toBe(true); order.push("read"); return pending; });
    vi.mocked(api.userAssetExportAPI.cancel).mockImplementation(async () => { expect(localStorage.getItem(key)).toBe(await cancelMarkerBytes(b)); order.push("post"); return exportTask; });
    await submitUserExportCancel(b, proof, vi.fn()); expect(order).toEqual(["read", "post"]);
    expect(localStorage.getItem(key)).not.toBeNull();
  });
  it.each(["queued", "running", "retry_wait", "cancel_requested"] as const)("keeps unknown marker after %s observation", async (state) => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, await cancelMarkerBytes(b));
    vi.mocked(api.userAssetExportAPI.detail).mockResolvedValue({ ...pending, task: { ...exportTask, state } });
    const result = await observeUserExportCancel(b); expect(result.queryOnly).toBe(true); expect(localStorage.getItem(key)).not.toBeNull();
    await submitUserExportCancel(binding(), proof, vi.fn()); expect(api.userAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it.each(["cancel_requested", "cancelled", "succeeded"] as const)("fresh %s before dispatch stops stale form", async (state) => {
    vi.mocked(api.userAssetExportAPI.recheckCancelAuthority).mockResolvedValue({ ...pending, task: { ...exportTask, state } });
    await submitUserExportCancel(binding(), proof, vi.fn()); expect(api.userAssetExportAPI.cancel).not.toHaveBeenCalled(); expect(localStorage.length).toBe(0);
  });
  it.each(["getItem", "setItem"] as const)("storage %s failure sends zero mutations", async (method) => {
    vi.spyOn(Storage.prototype, method).mockImplementation(() => { throw new Error("private-canary"); });
    const result = await submitUserExportCancel(binding(), proof, vi.fn()); expect(result.message).not.toContain("canary"); expect(api.userAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it("mismatched durable writeback sends no mutation", async () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => undefined);
    await submitUserExportCancel(binding(), proof, vi.fn()); expect(api.userAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it.each(["missing", "busy", "rejected"])("lock %s has no fallback", async (mode) => {
    if (mode === "missing") Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
    if (mode === "busy") held = true;
    if (mode === "rejected") vi.mocked(navigator.locks.request).mockRejectedValue(new Error("private-canary"));
    const result = await submitUserExportCancel(binding(), proof, vi.fn()); expect(result.queryOnly).toBe(true); expect(result.message).not.toContain("canary"); expect(api.userAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it("two competing callers cannot dispatch twice", async () => {
    let release!: (v: typeof pending) => void;
    vi.mocked(api.userAssetExportAPI.recheckCancelAuthority).mockReturnValue(new Promise((r) => { release = r; }));
    const one = submitUserExportCancel(binding(), proof, vi.fn());
    await vi.waitFor(() => expect(held).toBe(true));
    const two = await submitUserExportCancel(binding(), proof, vi.fn()); expect(two.queryOnly).toBe(true);
    release(pending); await one; expect(api.userAssetExportAPI.cancel).toHaveBeenCalledTimes(1);
  });
  it("unknown dispatch remains blocked on remount, with unchanged locator", async () => {
    sessionStorage.setItem("synthetic-locator", "unchanged");
    vi.mocked(api.userAssetExportAPI.cancel).mockRejectedValue(new ApiError("network"));
    await expect(submitUserExportCancel(binding(), proof, vi.fn())).rejects.toBeInstanceOf(ApiError);
    expect(await inspectCancelMarker(binding())).toBe(cancelUnknown);
    await submitUserExportCancel(binding(), proof, vi.fn()); expect(api.userAssetExportAPI.cancel).toHaveBeenCalledTimes(1);
    expect(sessionStorage.getItem("synthetic-locator")).toBe("unchanged");
  });
  it("removes only matched terminal marker and verifies absence", async () => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, await cancelMarkerBytes(b));
    vi.mocked(api.userAssetExportAPI.detail).mockResolvedValue(ended);
    expect((await observeUserExportCancel(b)).detail).toEqual(ended); expect(localStorage.getItem(key)).toBeNull();
  });
  it("terminal wrong request cannot clear marker", async () => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, await cancelMarkerBytes(b));
    vi.mocked(api.userAssetExportAPI.detail).mockResolvedValue({ ...ended, request: { ...exportRequest, search: "changed" } });
    await expect(observeUserExportCancel(b)).rejects.toBeInstanceOf(ApiError); expect(localStorage.getItem(key)).not.toBeNull();
  });
  it("deletion uncertainty returns query-only without claiming persistence", async () => {
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID); localStorage.setItem(key, await cancelMarkerBytes(b));
    vi.mocked(api.userAssetExportAPI.detail).mockResolvedValue(ended);
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("private-canary"); });
    const result = await observeUserExportCancel(b); expect(result.queryOnly).toBe(true); expect(result.message).toContain("不能确认记录是否仍保留");
  });
  it("uncertain absence readback does not claim a removed marker still exists", async () => {
    vi.spyOn(api, "isUserExportCancelProofRejected").mockReturnValue(true);
    vi.mocked(api.userAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    const get = Storage.prototype.getItem, remove = Storage.prototype.removeItem;
    let removed = false;
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(function (this: Storage, key) { remove.call(this, key); removed = true; });
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (this: Storage, key) {
      if (removed) { removed = false; throw new Error("private-readback-canary"); }
      return get.call(this, key);
    });
    const result = await submitUserExportCancel(binding(), proof, vi.fn());
    expect(result.state).toBe("blocked"); expect(result.queryOnly).toBe(true);
    expect(result.message).toContain("不能确认记录是否仍保留"); expect(localStorage.length).toBe(0);
    expect(api.userAssetExportAPI.cancel).toHaveBeenCalledOnce();
  });
  it("fake proof error never unlocks an unknown request", async () => {
    vi.mocked(api.userAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    await expect(submitUserExportCancel(binding(), proof, vi.fn())).rejects.toMatchObject({ status: 401 });
    expect(await inspectCancelMarker(binding())).toBe(cancelUnknown);
  });
  it("verified current proof error keeps lock continuously through authority and exact clear", async () => {
    vi.spyOn(api, "isUserExportCancelProofRejected").mockReturnValue(true);
    vi.mocked(api.userAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    const phases: boolean[] = [];
    vi.mocked(api.userAssetExportAPI.recheckCancelAuthority).mockImplementation(async () => { phases.push(held); return pending; });
    const hide = vi.fn(), result = await submitUserExportCancel(binding(), proof, hide);
    expect(result.state).toBe("proof-rejected"); expect(phases).toEqual([true, true]); expect(hide).toHaveBeenCalledOnce(); expect(localStorage.length).toBe(0); expect(api.userAssetExportAPI.cancel).toHaveBeenCalledTimes(1);
  });
  it("rejected proof with failed fresh authority remains query-only", async () => {
    vi.spyOn(api, "isUserExportCancelProofRejected").mockReturnValue(true);
    vi.mocked(api.userAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    vi.mocked(api.userAssetExportAPI.recheckCancelAuthority).mockResolvedValueOnce(pending).mockRejectedValueOnce(new ApiError("forbidden", 403));
    await expect(submitUserExportCancel(binding(), proof, vi.fn())).rejects.toMatchObject({ status: 403 });
    expect(localStorage.length).toBe(1); expect(api.userAssetExportAPI.cancel).toHaveBeenCalledOnce();
  });
  it.each([401, 403, 404])("pre-dispatch authority %i sends zero mutation", async (status) => {
    vi.mocked(api.userAssetExportAPI.recheckCancelAuthority).mockRejectedValue(new ApiError("forbidden", status));
    await expect(submitUserExportCancel(binding(), proof, vi.fn())).rejects.toMatchObject({ status });
    expect(api.userAssetExportAPI.cancel).not.toHaveBeenCalled(); expect(localStorage.length).toBe(0);
  });
  it("in-place original request rotation fences preflight before persistence", async () => {
    const b = binding();
    vi.mocked(api.userAssetExportAPI.recheckCancelAuthority).mockImplementation(async () => { b.request.search = "changed"; return pending; });
    await expect(submitUserExportCancel(b, proof, vi.fn())).rejects.toMatchObject({ code: "invalid_response" });
    expect(api.userAssetExportAPI.cancel).not.toHaveBeenCalled(); expect(localStorage.length).toBe(0);
  });
  it("marker replacement during same-lock proof reauthorization fails closed (ABA boundary)", async () => {
    vi.spyOn(api, "isUserExportCancelProofRejected").mockReturnValue(true);
    vi.mocked(api.userAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    const b = binding(), key = cancelStorageKey(b.profile.ID, b.taskID);
    vi.mocked(api.userAssetExportAPI.recheckCancelAuthority).mockResolvedValueOnce(pending).mockImplementationOnce(async () => { expect(held).toBe(true); localStorage.setItem(key, "replacement"); return pending; });
    const result = await submitUserExportCancel(b, proof, vi.fn()); expect(result.queryOnly).toBe(true); expect(localStorage.getItem(key)).toBe("replacement");
  });
  it("aborted late response cannot remove marker", async () => {
    const b = binding(), control = new AbortController(); b.signal = control.signal;
    vi.mocked(api.userAssetExportAPI.cancel).mockImplementation(async () => { control.abort(); return ended.task; });
    await expect(submitUserExportCancel(b, proof, vi.fn())).rejects.toBeDefined(); expect(localStorage.length).toBe(1);
  });
});
