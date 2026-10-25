import { ApiError, validPassword, validCode, type Profile } from "./api";
import type { Proof } from "./access-api";
import { terminal } from "./task-api";
import {
  exportKind, validExportID, validExportRequest, sameExportRequest,
  userAssetExportAPI, isUserExportCancelProofRejected,
  type ExportRequest, type ExportDetail,
} from "./user-asset-export-v3-api";

export const cancelUnknown = "可能已发出取消请求，结果尚未确认；只能手动核对同一原任务，不能再次取消。";
export const cancelUnavailable = "浏览器缺少可靠的本地存储或独占锁，无法防止重复取消；仅停用取消，读取和下载不受影响。";
export const cancelLocalUncertain = "本地取消记录未能可靠核对，本次面板仅可读取原任务；不能确认记录是否仍保留。";
export const cancelProofRejected = "密码或验证码未通过；请重新输入。";
export type CancelBinding = { profile: Profile; taskID: string; request: ExportRequest; signal: AbortSignal; assertCurrent: () => void };
type Marker = { version: 1; kind: typeof exportKind; actorID: number; taskID: string; domainId: string; requestSHA256: string };
export type CancelResult = { state: "observed" | "proof-rejected" | "blocked"; detail?: ExportDetail; message: string; queryOnly: boolean };
const blocked = (message = cancelUnknown): CancelResult => ({ state: "blocked", message, queryOnly: true });
const eligible = (detail: ExportDetail) => ["queued", "running", "retry_wait"].includes(detail.task.state);
export const copyCancelRequest = (r: ExportRequest): ExportRequest => ({ domainId: r.domainId,
  expectedRevision: r.expectedRevision, expectedCredentialRevision: r.expectedCredentialRevision,
  observationId: r.observationId, search: r.search, sAMAccountName: r.sAMAccountName,
  startTm: r.startTm, endTm: r.endTm, creatTm: r.creatTm, screenColumns: [...r.screenColumns],
  ...(Object.hasOwn(r, "uacDisableBit") ? { uacDisableBit: r.uacDisableBit } : {}) });
export function cancelStorageKey(actorID: number, taskID: string): string {
  if (!Number.isSafeInteger(actorID) || actorID < 1 || !validExportID(taskID)) throw new ApiError("invalid_input");
  return `adtr.user-export-cancel.v1.${actorID}.${taskID}`;
}
function current(binding: CancelBinding) { binding.signal.throwIfAborted(); binding.assertCurrent(); }
export function cancelCapability(): string {
  try {
    if (!navigator.locks || typeof navigator.locks.request !== "function" || !globalThis.crypto?.subtle || !window.localStorage) return cancelUnavailable;
    // Reading does not probe by modifying an unrelated key.
    return "";
  } catch { return cancelUnavailable; }
}
export async function cancelMarkerBytes(binding: CancelBinding): Promise<string> {
  current(binding);
  if (!validExportRequest(binding.request)) throw new ApiError("invalid_input");
  cancelStorageKey(binding.profile.ID, binding.taskID);
  const canonical = JSON.stringify(copyCancelRequest(binding.request)), actorID = binding.profile.ID, taskID = binding.taskID;
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(canonical));
  current(binding);
  if (actorID !== binding.profile.ID || taskID !== binding.taskID || canonical !== JSON.stringify(copyCancelRequest(binding.request))) throw new ApiError("invalid_response");
  const marker: Marker = { version: 1, kind: exportKind, actorID: binding.profile.ID, taskID: binding.taskID,
    domainId: binding.request.domainId, requestSHA256: Array.from(new Uint8Array(digest), (v) => v.toString(16).padStart(2, "0")).join("") };
  return JSON.stringify(marker);
}
function matching(raw: string, expected: string): boolean {
  // Byte equality against our canonical representation rejects duplicate/extra
  // keys, whitespace, malformed values and bindings, without trusting JSON.parse.
  return raw.length <= 1024 && new TextEncoder().encode(raw).byteLength <= 1024 && raw === expected;
}
export async function inspectCancelMarker(binding: CancelBinding): Promise<string> {
  current(binding);
  let raw: string | null;
  try { raw = localStorage.getItem(cancelStorageKey(binding.profile.ID, binding.taskID)); }
  catch { return cancelUnavailable; }
  // A missing lock must not conceal a real marker from the old panel's read
  // policy. Conversely, capability loss alone must not stop normal polling.
  if (raw === null) return cancelCapability();
  if (!globalThis.crypto?.subtle) return cancelLocalUncertain;
  const expected = await cancelMarkerBytes(binding); current(binding);
  return matching(raw, expected) ? cancelUnknown : cancelLocalUncertain;
}
function checkDetail(detail: ExportDetail, binding: CancelBinding) {
  current(binding);
  if (detail.taskID !== binding.taskID || detail.task.taskUUID !== binding.taskID || detail.task.taskName !== exportKind ||
    detail.task.domainId !== binding.request.domainId || !sameExportRequest(detail.request, binding.request)) throw new ApiError("invalid_response");
}
function clearExact(binding: CancelBinding, expected: string): boolean {
  current(binding);
  const key = cancelStorageKey(binding.profile.ID, binding.taskID);
  try {
    if (!matching(localStorage.getItem(key) ?? "", expected)) return false;
    localStorage.removeItem(key);
    return localStorage.getItem(key) === null;
  } catch { return false; }
}
async function underLock(binding: CancelBinding, work: (expected: string) => Promise<CancelResult>): Promise<CancelResult> {
  current(binding);
  if (cancelCapability()) return blocked(cancelUnavailable);
  const expected = await cancelMarkerBytes(binding); current(binding);
  let entered = false;
  try {
    return await navigator.locks.request(cancelStorageKey(binding.profile.ID, binding.taskID), { mode: "exclusive", ifAvailable: true }, async (lock) => {
      current(binding);
      if (!lock) return blocked("另一标签页正在核对或取消此任务；请手动重新核对状态。");
      entered = true;
      return work(expected);
    });
  } catch (error) {
    current(binding);
    if (entered) throw error;
    return blocked(cancelUnavailable);
  }
}
// The lock is continuous from fresh authority through dispatch, proof rejection
// reauthorization and exact removal. No release/reacquire (ABA) or lease fallback.
export async function submitUserExportCancel(binding: CancelBinding, proof: Proof, hidePrivate: () => void, markAttempt: () => void = () => undefined): Promise<CancelResult> {
  if (!validPassword(proof.actorPassword) || !validCode(proof.totpCode)) throw new ApiError("invalid_input");
  const request = copyCancelRequest(binding.request);
  const profile = { ...binding.profile }, taskID = binding.taskID, signal = binding.signal;
  const owned = { ...binding, request, profile, assertCurrent: () => {
    binding.assertCurrent();
    if (binding.taskID !== taskID || binding.signal !== signal) throw new ApiError("invalid_response");
    if (["ID", "username", "role", "priv", "csrfToken", "hasMfa", "needChangePwd", "isExpired"].some((key) => binding.profile[key as keyof Profile] !== profile[key as keyof Profile])) throw new ApiError("unauthenticated", 401);
    if (!validExportRequest(binding.request) || !sameExportRequest(binding.request, request)) throw new ApiError("invalid_response");
  } };
  return underLock(owned, async (expected) => {
    const key = cancelStorageKey(owned.profile.ID, owned.taskID);
    try { if (localStorage.getItem(key) !== null) return blocked(); } catch { return blocked(cancelUnavailable); }
    const before = await userAssetExportAPI.recheckCancelAuthority(owned.taskID, request, owned.profile, owned.signal);
    checkDetail(before, owned);
    if (!eligible(before)) return { state: "observed", detail: before, message: "已核对当前任务状态，未发送取消请求。", queryOnly: true };
    current(owned); markAttempt();
    try {
      localStorage.setItem(key, expected);
      if (localStorage.getItem(key) !== expected) return blocked(cancelLocalUncertain);
    } catch { return blocked(cancelUnavailable); }
    current(owned);
    try {
      await userAssetExportAPI.cancel(owned.taskID, request, proof, owned.profile.ID, owned.profile.csrfToken, owned.signal);
      current(owned);
    } catch (error) {
      current(owned);
      if (!isUserExportCancelProofRejected(error, { taskID: owned.taskID, request, actor: owned.profile.ID, signal: owned.signal })) throw error;
      hidePrivate(); current(owned);
      const verified = await userAssetExportAPI.recheckCancelAuthority(owned.taskID, request, owned.profile, owned.signal);
      checkDetail(verified, owned);
      if (verified.task.state === "cancel_requested") return { state: "observed", detail: verified, message: "取消请求已登记，仍需等待服务器确认。", queryOnly: true };
      if (!clearExact(owned, expected)) return { ...blocked(cancelLocalUncertain), detail: verified };
      return { state: eligible(verified) ? "proof-rejected" : "observed", detail: verified,
        message: eligible(verified) ? cancelProofRejected : "已核对当前任务终态。", queryOnly: !eligible(verified) };
    }
    const detail = await userAssetExportAPI.detail(owned.taskID, request, owned.profile.ID, owned.signal);
    checkDetail(detail, owned);
    if (terminal(detail.task.state) && !clearExact(owned, expected)) return { ...blocked(cancelLocalUncertain), detail };
    return { state: "observed", detail, message: terminal(detail.task.state) ? "已核对当前任务终态。" : "取消请求已登记，仍需等待服务器确认。", queryOnly: true };
  });
}
// Explicit observation only; no event-driven fetching and no mutation replay.
export async function observeUserExportCancel(binding: CancelBinding): Promise<CancelResult> {
  const detail = await userAssetExportAPI.detail(binding.taskID, binding.request, binding.profile.ID, binding.signal);
  checkDetail(detail, binding);
  const message = await inspectCancelMarker(binding); current(binding);
  if (!terminal(detail.task.state) || !message || cancelCapability()) return { state: "observed", detail, message, queryOnly: !!message || !eligible(detail) };
  const result = await underLock(binding, async (expected) => {
    const fresh = await userAssetExportAPI.detail(binding.taskID, binding.request, binding.profile.ID, binding.signal);
    checkDetail(fresh, binding);
    if (!terminal(fresh.task.state)) return { state: "observed", detail: fresh, message: cancelUnknown, queryOnly: true };
    let raw: string | null;
    try { raw = localStorage.getItem(cancelStorageKey(binding.profile.ID, binding.taskID)); } catch { return blocked(cancelLocalUncertain); }
    if (raw !== null && (!matching(raw, expected) || !clearExact(binding, expected))) return blocked(cancelLocalUncertain);
    return { state: "observed", detail: fresh, message: "已核对当前任务终态。", queryOnly: true };
  });
  current(binding);
  // A local marker/lock failure disables mutation, not an already authorized
  // state read. Never manufacture a cancellation receipt or clear on this path.
  return { ...result, detail: result.detail ?? detail };
}
