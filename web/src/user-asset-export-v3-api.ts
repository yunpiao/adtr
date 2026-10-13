import { ApiError } from "./api";
import type { Proof } from "./access-api";
import { validDomainID, validRevision } from "./domain-api";
import { integer, object, validTask, type Task } from "./task-api";
import { validUserAssetsV3Search, type UserAssetsV3Query } from "./user-assets-v3-api";
import { validUserAssetsV2Filters } from "./user-assets-v2-filters";

export const exportColumns = ["guid", "sAMAccountName", "sid", "mail", "dn", "whenCreated"] as const;
export type ExportColumn = (typeof exportColumns)[number];
export const exportKind = "user_assets.v3.export";
export const exportPrefix = "/api/user-assets/v3/exports";
export const exportOperations = [
  `POST ${exportPrefix}`, `GET ${exportPrefix}/receipt`,
  `GET ${exportPrefix}/detail`, `POST ${exportPrefix}/cancel`,
  `GET ${exportPrefix}/download`,
] as const;
export interface ExportRequest {
  domainId: string;
  expectedRevision: string;
  expectedCredentialRevision: string;
  observationId: string;
  search: string;
  sAMAccountName: string;
  startTm: string;
  endTm: string;
  creatTm: "" | "asc" | "desc";
  screenColumns: ExportColumn[];
}
export interface ExportReceipt { taskID: string; task: Task; request: ExportRequest }
export interface ExportArtifact { rowCount: number; byteCount: number; sha256: string; filename: string }
export const downloadStatuses = ["pending", "ready", "failed", "cancelled", "source_changed", "source_unavailable", "artifact_invalid"] as const;
export type DownloadStatus = (typeof downloadStatuses)[number];
export interface ExportDetail extends ExportReceipt { downloadReady: boolean; downloadStatus: DownloadStatus; artifact?: ExportArtifact }
const requestKeys = ["domainId", "expectedRevision", "expectedCredentialRevision", "observationId", "search", "sAMAccountName", "startTm", "endTm", "creatTm", "screenColumns"];
const exact = (value: Record<string, unknown>, keys: string[]) =>
  Object.keys(value).length === keys.length && Object.keys(value).every((key) => keys.includes(key));
const invalid = (): never => { throw new ApiError("invalid_response"); };
export const validExportID = (value: unknown): value is string =>
  typeof value === "string" && /^[A-Za-z0-9_.-]{1,128}$/u.test(value);
export const exportFilename = (id: string) => `user-assets-v3-${id}.xlsx`;
export function validExportRequest(value: unknown): value is ExportRequest {
  return object(value) && exact(value, requestKeys) &&
    validDomainID(value.domainId) && validRevision(value.expectedRevision) &&
    validRevision(value.expectedCredentialRevision) && validDomainID(value.observationId) &&
    validUserAssetsV3Search(value.search) &&
    ["sAMAccountName", "startTm", "endTm", "creatTm"].every((key) => typeof value[key] === "string") &&
    ["", "asc", "desc"].includes(value.creatTm as string) &&
    validUserAssetsV2Filters({ sAMAccountName: value.sAMAccountName as string || undefined,
      startTm: value.startTm as string || undefined, endTm: value.endTm as string || undefined,
      creatTm: value.creatTm as "asc" | "desc" || undefined }) &&
    Array.isArray(value.screenColumns) && value.screenColumns.length >= 1 && value.screenColumns.length <= 6 &&
    value.screenColumns.every((c) => exportColumns.includes(c as ExportColumn)) &&
    new Set(value.screenColumns).size === value.screenColumns.length;
}
export function requestFromQuery(q: UserAssetsV3Query, columns: ExportColumn[] = [...exportColumns]): ExportRequest {
  const request: ExportRequest = { domainId: q.domainId, expectedRevision: q.expectedRevision,
    expectedCredentialRevision: q.expectedCredentialRevision, observationId: q.observationId ?? "",
    search: q.search, sAMAccountName: q.sAMAccountName ?? "", startTm: q.startTm ?? "",
    endTm: q.endTm ?? "", creatTm: q.creatTm ?? "", screenColumns: [...columns] };
  if (!validExportRequest(request)) throw new ApiError("invalid_input");
  return request;
}
export function sameExportRequest(a: ExportRequest, b: ExportRequest) {
  return requestKeys.every((key) => key === "screenColumns"
    ? a.screenColumns.length === b.screenColumns.length && a.screenColumns.every((v, i) => b.screenColumns[i] === v)
    : a[key as keyof ExportRequest] === b[key as keyof ExportRequest]);
}
function exportTask(value: unknown, domain: string, id?: string): value is Task {
  if (!validTask(value)) return false;
  const keys = ["taskUUID", "taskName", "domainId", "payloadVersion", "state", "sourceState", "error", "createdAt", "updatedAt", "attempt", "maxAttempts", "progress", "resultVersion", "result", "cursor", "parentTaskUUID", "terminalAt", "archived", "visibilityVersion", ...(value.nextAttemptAt === undefined ? [] : ["nextAttemptAt"])];
  return exact(value as unknown as Record<string, unknown>, keys) && validExportID(value.taskUUID) &&
    (!id || value.taskUUID === id) && value.taskName === exportKind && value.domainId === domain &&
    value.payloadVersion === 1 && value.parentTaskUUID === "" && !value.archived &&
    value.maxAttempts === 3 && value.attempt <= value.maxAttempts &&
    object(value.result) && exact(value.result, []) && object(value.cursor) && exact(value.cursor, []);
}
export function parseExportReceipt(value: unknown, domain: string, expected?: ExportRequest, mode: "receipt" | "create" | "detail" = "receipt", id?: string): ExportReceipt & { replayed?: boolean; downloadReady?: boolean; downloadStatus?: DownloadStatus; artifact?: ExportArtifact } {
  if (!object(value) || !validExportID(value.taskID) || (id !== undefined && id !== value.taskID) ||
    !validExportRequest(value.request) || value.request.domainId !== domain ||
    (expected && !sameExportRequest(value.request, expected)) || !exportTask(value.task, domain, value.taskID) ||
    !exact(value, ["taskID", "task", "request", ...(mode === "create" ? ["replayed"] : []), ...(mode === "detail" ? ["downloadReady", "downloadStatus", ...(value.downloadReady === true ? ["artifact"] : [])] : [])]) ||
    (mode === "create" && typeof value.replayed !== "boolean") ||
    (mode === "detail" && (typeof value.downloadReady !== "boolean" || !downloadStatuses.includes(value.downloadStatus as DownloadStatus) || value.downloadReady !== (value.downloadStatus === "ready")))) return invalid();
  if (mode === "detail") {
    const state = value.task.state, status = value.downloadStatus;
    if ((["queued", "running", "retry_wait", "cancel_requested"].includes(state) && status !== "pending") ||
      (["failed", "partial_failed", "dead_letter"].includes(state) && status !== "failed") ||
      (state === "cancelled" && status !== "cancelled") ||
      (state === "succeeded" && !["ready", "source_changed", "source_unavailable", "artifact_invalid"].includes(status as string))) return invalid();
  }
  if (mode === "detail" && value.downloadReady) {
    const a = value.artifact;
    if (value.task.state !== "succeeded" || !object(a) || !exact(a, ["rowCount", "byteCount", "sha256", "filename"]) ||
      !integer(a.rowCount, 0, 10000) || !integer(a.byteCount, 1, 32 * 1024 * 1024) ||
      typeof a.sha256 !== "string" || !/^[0-9a-f]{64}$/u.test(a.sha256) || a.filename !== exportFilename(value.taskID)) return invalid();
  }
  return value as unknown as ExportReceipt & { replayed?: boolean; downloadReady?: boolean; downloadStatus?: DownloadStatus; artifact?: ExportArtifact };
}
async function bytes(response: Response, signal: AbortSignal, limit: number): Promise<Uint8Array<ArrayBuffer>> {
  const length = response.headers.get("Content-Length");
  if (length !== null && (!/^(0|[1-9][0-9]*)$/u.test(length) || Number(length) > limit)) {
    await response.body?.cancel(); return invalid();
  }
  if (!response.body) return invalid();
  const reader = response.body.getReader(), chunks: Uint8Array[] = [];
  let size = 0, completed = false;
  let cancellation: Promise<void> | undefined;
  const cancel = () => cancellation ??= reader.cancel().catch(() => undefined);
  const abort = () => { void cancel(); };
  signal.addEventListener("abort", abort, { once: true });
  try {
    while (true) {
      signal.throwIfAborted();
      const part = await reader.read();
      signal.throwIfAborted();
      if (part.done) break;
      if (part.value.byteLength > limit - size) return invalid();
      size += part.value.byteLength; chunks.push(part.value);
    }
    if (length !== null && Number(length) !== size) return invalid();
    const result = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) { result.set(chunk, offset); offset += chunk.byteLength; }
    completed = true;
    return result;
  } catch (error) {
    signal.throwIfAborted();
    if (error instanceof ApiError) throw error;
    // A transport can fail after successful headers, while consuming the body.
    // This is still ambiguous network failure, never permission to resubmit.
    throw new ApiError("network");
  } finally {
    signal.removeEventListener("abort", abort);
    if (!completed) await cancel();
    reader.releaseLock();
  }
}
async function fetchExport(path: string, actor: number, signal: AbortSignal, body?: unknown, csrf?: string) {
  signal.throwIfAborted();
  if (!integer(actor, 1)) throw new ApiError("invalid_input");
  let response: Response;
  try {
    response = await fetch(`${exportPrefix}${path}`, { method: body === undefined ? "GET" : "POST",
      credentials: "same-origin", cache: "no-store", redirect: "error", signal,
      headers: body === undefined ? {} : { "Content-Type": "application/json", "X-CSRF-Token": csrf ?? "" },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
  } catch (error) { signal.throwIfAborted(); throw new ApiError("network"); }
  if (response.ok && response.status !== 200) {
    await response.body?.cancel().catch(() => undefined); return invalid();
  }
  if (response.ok && (response.headers.get("X-ADTR-User-ID") !== String(actor) ||
    response.headers.get("Cache-Control") !== "no-store" || response.headers.get("X-Content-Type-Options") !== "nosniff")) {
    await response.body?.cancel(); throw new ApiError("unauthenticated", 401);
  }
  return response;
}
// JSON.parse alone silently accepts duplicate object keys. Scan structural
// tokens first; JSON.parse remains responsible for complete grammar validation.
function uniqueJSON(text: string): unknown {
  const stack: (Set<string> | null)[] = [];
  for (let index = 0; index < text.length; index++) {
    const char = text[index];
    if (char === "{") stack.push(new Set());
    else if (char === "[") stack.push(null);
    else if (char === "}" || char === "]") stack.pop();
    else if (char === '"') {
      const start = index++;
      while (index < text.length) {
        if (text[index] === "\\") index += 2;
        else if (text[index] === '"') break;
        else index++;
      }
      let after = index + 1;
      while (/\s/u.test(text[after] ?? "") && after < text.length) after++;
      if (text[after] === ":") {
        const key: unknown = JSON.parse(text.slice(start, index + 1));
        const keys = stack[stack.length - 1];
        if (typeof key !== "string" || !keys || keys.has(key)) return invalid();
        keys.add(key);
      }
    }
  }
  return JSON.parse(text) as unknown;
}
async function json(response: Response, signal: AbortSignal) {
  // HTTP authority failures invalidate state even when a proxy replaces the
  // JSON body with an empty/oversized/HTML error page.
  if (response.status === 401 || response.status === 403) {
    await response.body?.cancel().catch(() => undefined);
    throw new ApiError(response.status === 401 ? "unauthenticated" : "forbidden", response.status);
  }
  const raw = await bytes(response, signal, 65536);
  let value: unknown;
  try { value = uniqueJSON(new TextDecoder("utf-8", { fatal: true }).decode(raw)); } catch { return invalid(); }
  if (!response.ok) throw new ApiError(object(value) && typeof value.error === "string" && /^[a-z][a-z0-9_]{0,63}$/u.test(value.error) ? value.error : "internal", response.status);
  if (response.headers.get("Content-Type")?.split(";")[0] !== "application/json") return invalid();
  return value;
}
export const userAssetExportAPI = {
  async create(request: ExportRequest, idempotencyKey: string, proof: Proof, actor: number, csrf: string, signal: AbortSignal) {
    if (!validExportRequest(request) || !validExportID(idempotencyKey)) throw new ApiError("invalid_input");
    return parseExportReceipt(await json(await fetchExport("", actor, signal, { ...request, idempotencyKey, ...proof }, csrf), signal), request.domainId, request, "create");
  },
  async receipt(domainId: string, idempotencyKey: string, actor: number, signal: AbortSignal, expected?: ExportRequest) {
    if (!validDomainID(domainId) || !validExportID(idempotencyKey)) throw new ApiError("invalid_input");
    return parseExportReceipt(await json(await fetchExport(`/receipt?${new URLSearchParams({ domainId, idempotencyKey })}`, actor, signal), signal), domainId, expected);
  },
  async detail(taskID: string, request: ExportRequest, actor: number, signal: AbortSignal): Promise<ExportDetail> {
    if (!validExportID(taskID) || !validExportRequest(request)) throw new ApiError("invalid_input");
    return parseExportReceipt(await json(await fetchExport(`/detail?${new URLSearchParams({ taskID })}`, actor, signal), signal), request.domainId, request, "detail", taskID) as ExportDetail;
  },
  async cancel(taskID: string, request: ExportRequest, proof: Proof, actor: number, csrf: string, signal: AbortSignal) {
    if (!validExportID(taskID) || !validExportRequest(request)) throw new ApiError("invalid_input");
    const value = await json(await fetchExport("/cancel", actor, signal, { taskID, ...proof }, csrf), signal);
    if (!object(value) || !exact(value, ["taskID", "task"]) || value.taskID !== taskID || !exportTask(value.task, request.domainId, taskID)) return invalid();
    return value.task;
  },
  async download(detail: ExportDetail, actor: number, signal: AbortSignal): Promise<Blob> {
    // A fresh detail read binds current session, kind, exact source and manifest.
    const current = await this.detail(detail.taskID, detail.request, actor, signal);
    const a = current.artifact;
    if (!current.downloadReady || !a) throw new ApiError("export_not_ready", 409);
    const response = await fetchExport(`/download?${new URLSearchParams({ taskID: detail.taskID })}`, actor, signal);
    if (!response.ok) { await json(response, signal); return invalid(); }
    const mime = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";
    if (response.headers.get("Content-Type") !== mime ||
      response.headers.get("Content-Disposition") !== `attachment; filename="${a.filename}"` ||
      response.headers.get("Content-Length") !== String(a.byteCount) ||
      response.headers.get("X-ADTR-Artifact-SHA256") !== a.sha256 ||
      response.headers.get("X-ADTR-Row-Count") !== String(a.rowCount)) {
      await response.body?.cancel(); return invalid();
    }
    const raw = await bytes(response, signal, 32 * 1024 * 1024);
    const digest = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", raw)), (v) => v.toString(16).padStart(2, "0")).join("");
    signal.throwIfAborted();
    if (raw.byteLength !== a.byteCount || digest !== a.sha256) return invalid();
    // Revalidate after the full body/digest to discard bytes if authority changed in flight.
    const verified = await this.detail(detail.taskID, detail.request, actor, signal);
    signal.throwIfAborted();
    if (!verified.downloadReady || !verified.artifact || verified.artifact.rowCount !== a.rowCount || verified.artifact.byteCount !== a.byteCount || verified.artifact.sha256 !== a.sha256 || verified.artifact.filename !== a.filename) return invalid();
    return new Blob([raw], { type: mime });
  },
};
export function userAssetExportError(error: unknown): string {
  const messages: Record<string, string> = {
    network: "网络中断，提交结果可能未知；请用原幂等键核对回执。",
    not_found: "未找到当前可见回执；这不能证明原提交未生效，请保留原幂等键继续核对。",
    invalid_response: "服务器响应未通过完整性校验，已停止应用或下载。",
    forbidden: "当前导出权限已失效，请重新核对授权。",
    unauthenticated: "登录会话已变化，请重新登录后核对原回执。",
    selection_changed: "固定观测或来源版本已变化，请重新选择来源。",
    directory_observation_unavailable: "固定观测不可用，不能将其视为空结果。",
    authorization_epoch_changed: "授权版本已变化，原导出已失效。",
    export_not_ready: "文件尚未就绪，请等待持久化任务完成。",
    task_terminal: "任务已结束，请刷新任务状态。",
    idempotency_conflict: "原幂等键与请求或授权版本冲突，请先核对原回执。",
  };
  return error instanceof ApiError ? messages[error.code] ?? `导出未完成（${error.code}），请核对后重试。` : "导出未完成，请核对原任务状态。";
}
