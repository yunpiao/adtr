import { ApiError } from "./api";
import { accessMessages, type PageInfo, type Proof } from "./access-api";
import { validUTC, validExportID } from "./audit-history";
import {
  integer,
  object,
  taskStates,
  validTask,
  type Task,
  type TaskState,
} from "./task-api";

export const logOperations = [
  "GET /api/system/logs",
  "GET /api/system/logs/sources",
  "POST /api/system/logs/bundles",
  "GET /api/system/logs/bundles/history",
  "GET /api/system/logs/bundles/detail",
  "GET /api/system/logs/bundles/download",
  "POST /api/system/logs/bundles/cancel",
] as const;
export type LogOperation = (typeof logOperations)[number];
export type LogModule = "api" | "worker";
export interface Selection {
  startTm: string;
  endTm: string;
  systemType: LogModule[];
}
export interface LogQuery extends Selection {
  pageIdx: number;
  pageSize: number;
}
export const emptyLogQuery = (): LogQuery => ({
  pageIdx: 1,
  pageSize: 20,
  startTm: "",
  endTm: "",
  systemType: ["api", "worker"],
});
export interface Coverage {
  mode: "best_effort";
  gapsPossible: true;
  journalCreatedAt: string;
  firstRecordedAt: string | null;
}
export interface LogEvent {
  schemaVersion: 1;
  eventId: string;
  processId: string;
  module: LogModule;
  code:
    | "service_start_requested"
    | "service_stopped"
    | "service_failed"
    | "queue_cycle"
    | "recovery_cycle"
    | "scheduler_cycle"
    | "queue_progress";
  outcome: "attempted" | "completed" | "failed" | "progress";
  severity: "info" | "error";
  reason: "" | "serve_failed" | "worker_failed" | "cycle_failed";
  observedAt: string;
  recordedAt: string;
}
export interface LogList {
  page: PageInfo;
  List: { event: LogEvent; log: string }[];
  exhausted: boolean;
  selection: Selection;
  coverage: Coverage;
}
export interface RecorderReport {
  processId: string;
  module: LogModule;
  startedAt: string;
  observedAt: string;
  accepted: number;
  acknowledged: number;
  rejected: number;
  queueFull: number;
  writeFailures: number;
  unacknowledged: number;
  abandoned: number;
  acceptanceStopped: boolean;
  reportedAt: string;
  evidence: "incomplete_process_observation";
}
export interface LogSources {
  modules: {
    module: LogModule;
    registered: true;
    firstRecordedAt: string | null;
    lastRecordedAt: string | null;
    recordedCount: number;
  }[];
  coverage: Coverage;
  reports: RecorderReport[];
  reportsTruncated: boolean;
}
export interface BundleInput extends Selection {
  idempotencyKey: string;
}
export interface BundleDetail {
  task: Task;
  downloadReady: boolean;
  artifactStatus: "eligible" | "not_ready" | "artifact_invalid";
  rowCount?: number;
  snapshotAt?: string;
  downloadPath?: string;
  coverage?: Coverage;
}
export interface BundleQuery {
  pageIdx: number;
  pageSize: number;
  status: TaskState[];
}
export const emptyBundleQuery = (): BundleQuery => ({
  pageIdx: 1,
  pageSize: 20,
  status: [],
});
export interface BundleHistory {
  page: PageInfo;
  list: BundleDetail[];
  exhausted: boolean;
}
export const bundleFilename = (id: string) => `system-logs-${id}.zip`;
export const bundleDownloadPath = (id: string) =>
  `/api/system/logs/bundles/download?taskUUID=${id}`;
const fail = (): never => {
  throw new ApiError("invalid_response");
};
const exact = (
  v: Record<string, unknown>,
  required: string[],
  optional: string[] = [],
) =>
  required.every((key) => Object.hasOwn(v, key)) &&
  Object.keys(v).every(
    (key) => required.includes(key) || optional.includes(key),
  );
const utc = (v: unknown): v is string => validUTC(v) && !/\.\d{7,}Z$/.test(v);
const time = (v: string) =>
  BigInt(Date.parse(v.slice(0, 19) + "Z")) * 1000n +
  BigInt((v.split(".")[1]?.slice(0, -1) ?? "").padEnd(6, "0"));
const moduleValue = (v: unknown): v is LogModule =>
  v === "api" || v === "worker";
const modules = (v: unknown): v is LogModule[] =>
  Array.isArray(v) &&
  v.length > 0 &&
  v.length <= 2 &&
  v.every(moduleValue) &&
  new Set(v).size === v.length;
export function validSelection(v: unknown): v is Selection {
  return (
    object(v) &&
    exact(v, ["startTm", "endTm", "systemType"]) &&
    utc(v.startTm) &&
    utc(v.endTm) &&
    time(v.startTm) < time(v.endTm) &&
    time(v.endTm) - time(v.startTm) <= 86400000000n &&
    modules(v.systemType)
  );
}
export function logQuery(query: LogQuery) {
  const selection = {
    startTm: query.startTm,
    endTm: query.endTm,
    systemType: query.systemType,
  };
  if (
    !validPageInput(query) ||
    !modules(query.systemType) ||
    (!(query.startTm === "" && query.endTm === "") &&
      !validSelection(selection))
  )
    throw new ApiError("invalid_input");
  const params = new URLSearchParams({
    pageIdx: String(query.pageIdx),
    pageSize: String(query.pageSize),
  });
  if (query.startTm) {
    params.set("startTm", query.startTm);
    params.set("endTm", query.endTm);
  }
  query.systemType.forEach((m) => params.append("systemType", m));
  return params.toString();
}
function validPageInput(q: { pageIdx: number; pageSize: number }) {
  return (
    integer(q.pageIdx, 1, 1000000) &&
    (integer(q.pageSize, 1, 100) || (q.pageSize === -1 && q.pageIdx === 1))
  );
}
function pageValid(
  v: unknown,
  q: { pageIdx: number; pageSize: number },
  length: number,
  exhausted: unknown,
): v is PageInfo {
  if (
    !object(v) ||
    !exact(v, ["pageIdx", "pageSize", "total", "totalPage"]) ||
    v.pageIdx !== q.pageIdx ||
    v.pageSize !== q.pageSize ||
    !integer(v.total, 0) ||
    !integer(v.totalPage, 0)
  )
    return false;
  const size = q.pageSize === -1 ? 1000 : q.pageSize,
    offset = (q.pageIdx - 1) * size;
  return (
    (q.pageSize !== -1 || v.total <= 1000) &&
    v.totalPage ===
      (q.pageSize === -1 ? Number(v.total > 0) : Math.ceil(v.total / size)) &&
    length === Math.min(size, Math.max(0, v.total - offset)) &&
    exhausted === offset + length >= v.total
  );
}
export function validCoverage(v: unknown): v is Coverage {
  return (
    object(v) &&
    exact(v, ["mode", "gapsPossible", "journalCreatedAt", "firstRecordedAt"]) &&
    v.mode === "best_effort" &&
    v.gapsPossible === true &&
    utc(v.journalCreatedAt) &&
    (v.firstRecordedAt === null || utc(v.firstRecordedAt))
  );
}
export function validLogEvent(v: unknown): v is LogEvent {
  if (
    !object(v) ||
    !exact(v, [
      "schemaVersion",
      "eventId",
      "processId",
      "module",
      "code",
      "outcome",
      "severity",
      "reason",
      "observedAt",
      "recordedAt",
    ]) ||
    v.schemaVersion !== 1 ||
    !validExportID(v.eventId) ||
    !validExportID(v.processId) ||
    !moduleValue(v.module) ||
    !utc(v.observedAt) ||
    !utc(v.recordedAt) ||
    v.severity !== (v.outcome === "failed" ? "error" : "info")
  )
    return false;
  switch (v.code) {
    case "service_start_requested":
      return v.outcome === "attempted" && v.reason === "";
    case "service_stopped":
      return v.outcome === "completed" && v.reason === "";
    case "service_failed":
      return (
        v.outcome === "failed" &&
        v.reason === (v.module === "api" ? "serve_failed" : "worker_failed")
      );
    case "queue_cycle":
    case "recovery_cycle":
    case "scheduler_cycle":
      return (
        v.module === "worker" &&
        ((v.outcome === "completed" && v.reason === "") ||
          (v.outcome === "failed" && v.reason === "cycle_failed"))
      );
    case "queue_progress":
      return (
        v.module === "worker" && v.outcome === "progress" && v.reason === ""
      );
    default:
      return false;
  }
}
export function parseLogList(v: unknown, query: LogQuery): LogList {
  if (
    !object(v) ||
    !exact(v, ["page", "List", "exhausted", "selection", "coverage"]) ||
    !validSelection(v.selection) ||
    !validCoverage(v.coverage) ||
    !Array.isArray(v.List) ||
    !pageValid(v.page, query, v.List.length, v.exhausted)
  )
    return fail();
  const selection = v.selection;
  if (
    [...query.systemType].sort().join() !==
      [...selection.systemType].sort().join() ||
    (query.startTm &&
      (time(query.startTm) !== time(selection.startTm) ||
        time(query.endTm) !== time(selection.endTm)))
  )
    return fail();
  const seen = new Set<string>();
  let previous: LogEvent | undefined;
  for (const row of v.List) {
    if (
      !object(row) ||
      !exact(row, ["event", "log"]) ||
      !validLogEvent(row.event) ||
      typeof row.log !== "string" ||
      row.log.length > 4096
    )
      return fail();
    let raw: unknown;
    try {
      raw = JSON.parse(row.log);
    } catch {
      return fail();
    }
    const event = row.event;
    if (
      !validLogEvent(raw) ||
      // The server serializes the same fixed DTO twice. Requiring its canonical
      // form also rejects duplicate keys with concealed earlier values before
      // the raw alias can ever be displayed.
      row.log !== JSON.stringify(event) ||
      Object.keys(event).some(
        (key) => raw[key as keyof LogEvent] !== event[key as keyof LogEvent],
      ) ||
      seen.has(event.eventId) ||
      !selection.systemType.includes(event.module) ||
      time(event.recordedAt) < time(selection.startTm) ||
      time(event.recordedAt) >= time(selection.endTm) ||
      (previous &&
        (time(previous.recordedAt) < time(event.recordedAt) ||
          (time(previous.recordedAt) === time(event.recordedAt) &&
            previous.eventId <= event.eventId)))
    )
      return fail();
    seen.add(event.eventId);
    previous = event;
  }
  return v as unknown as LogList;
}
export function parseSources(v: unknown): LogSources {
  const counts = [
    "accepted",
    "acknowledged",
    "rejected",
    "queueFull",
    "writeFailures",
    "unacknowledged",
    "abandoned",
  ];
  if (
    !object(v) ||
    !exact(v, ["modules", "coverage", "reports", "reportsTruncated"]) ||
    !validCoverage(v.coverage) ||
    typeof v.reportsTruncated !== "boolean" ||
    !Array.isArray(v.modules) ||
    v.modules.length !== 2 ||
    new Set(v.modules.map((m) => m?.module)).size !== 2 ||
    !Array.isArray(v.reports) ||
    v.reports.length > 100
  )
    return fail();
  for (const m of v.modules)
    if (
      !object(m) ||
      !exact(m, [
        "module",
        "registered",
        "firstRecordedAt",
        "lastRecordedAt",
        "recordedCount",
      ]) ||
      !moduleValue(m.module) ||
      m.registered !== true ||
      !integer(m.recordedCount, 0) ||
      (m.recordedCount === 0
        ? m.firstRecordedAt !== null || m.lastRecordedAt !== null
        : !utc(m.firstRecordedAt) ||
          !utc(m.lastRecordedAt) ||
          time(m.firstRecordedAt) > time(m.lastRecordedAt))
    )
      return fail();
  for (const report of v.reports)
    if (
      !object(report) ||
      !exact(report, [
        "processId",
        "module",
        "startedAt",
        "observedAt",
        ...counts,
        "acceptanceStopped",
        "reportedAt",
        "evidence",
      ]) ||
      !validExportID(report.processId) ||
      !moduleValue(report.module) ||
      !utc(report.startedAt) ||
      !utc(report.observedAt) ||
      !utc(report.reportedAt) ||
      counts.some((key) => !integer(report[key], 0)) ||
      typeof report.acceptanceStopped !== "boolean" ||
      report.evidence !== "incomplete_process_observation"
    )
      return fail();
  if (new Set(v.reports.map((r) => r.processId)).size !== v.reports.length)
    return fail();
  return v as unknown as LogSources;
}
export function validBundleTask(v: unknown): v is Task {
  return (
    validTask(v) &&
    exact(
      v as unknown as Record<string, unknown>,
      [
        "taskUUID",
        "taskName",
        "domainId",
        "payloadVersion",
        "state",
        "sourceState",
        "error",
        "createdAt",
        "updatedAt",
        "attempt",
        "maxAttempts",
        "progress",
        "resultVersion",
        "result",
        "cursor",
        "parentTaskUUID",
        "terminalAt",
        "archived",
        "visibilityVersion",
      ],
      ["nextAttemptAt"],
    ) &&
    validExportID(v.taskUUID) &&
    v.taskName === "system.logs_bundle" &&
    v.domainId === "platform" &&
    v.payloadVersion === 1 &&
    v.maxAttempts === 1 &&
    v.attempt <= 1 &&
    !v.archived &&
    v.parentTaskUUID === "" &&
    /^[A-Z_]*$/.test(v.sourceState) &&
    /^(?:[a-z][a-z0-9_]{0,63})?$/.test(v.error) &&
    utc(v.createdAt) &&
    utc(v.updatedAt) &&
    (v.terminalAt === null || utc(v.terminalAt)) &&
    (v.nextAttemptAt === undefined || utc(v.nextAttemptAt)) &&
    object(v.result) &&
    Object.keys(v.result).length === 0 &&
    object(v.cursor) &&
    Object.keys(v.cursor).length === 0
  );
}
export function parseBundleDetail(v: unknown, id?: string): BundleDetail {
  if (
    !object(v) ||
    !exact(
      v,
      ["task", "downloadReady", "artifactStatus"],
      ["rowCount", "snapshotAt", "downloadPath", "coverage"],
    ) ||
    !validBundleTask(v.task) ||
    (id !== undefined && v.task.taskUUID !== id) ||
    typeof v.downloadReady !== "boolean"
  )
    return fail();
  if (v.artifactStatus === "eligible") {
    if (
      !v.downloadReady ||
      v.task.state !== "succeeded" ||
      !integer(v.rowCount, 0, 10000) ||
      !utc(v.snapshotAt) ||
      v.downloadPath !== bundleDownloadPath(v.task.taskUUID) ||
      !validCoverage(v.coverage)
    )
      return fail();
  } else if (
    !["not_ready", "artifact_invalid"].includes(v.artifactStatus as string) ||
    v.downloadReady ||
    (v.artifactStatus === "artifact_invalid" && v.task.state !== "succeeded") ||
    ["rowCount", "snapshotAt", "downloadPath", "coverage"].some((key) =>
      Object.hasOwn(v, key),
    )
  )
    return fail();
  return v as unknown as BundleDetail;
}
export function bundleQuery(q: BundleQuery) {
  if (
    !validPageInput(q) ||
    !Array.isArray(q.status) ||
    q.status.some((s) => !taskStates.includes(s)) ||
    new Set(q.status).size !== q.status.length
  )
    throw new ApiError("invalid_input");
  const params = new URLSearchParams({
    pageIdx: String(q.pageIdx),
    pageSize: String(q.pageSize),
  });
  q.status.forEach((s) => params.append("status", s));
  return params.toString();
}
export function parseBundleHistory(v: unknown, q: BundleQuery): BundleHistory {
  if (
    !object(v) ||
    !exact(v, ["page", "list", "exhausted"]) ||
    !Array.isArray(v.list) ||
    !pageValid(v.page, q, v.list.length, v.exhausted)
  )
    return fail();
  const list = v.list.map((row) => parseBundleDetail(row));
  if (
    new Set(list.map((row) => row.task.taskUUID)).size !== list.length ||
    list.some(
      (row) => q.status.length > 0 && !q.status.includes(row.task.state),
    )
  )
    return fail();
  return { ...v, list } as unknown as BundleHistory;
}
async function response(
  path: string,
  signal: AbortSignal,
  actor: number,
  body?: unknown,
  csrf?: string,
) {
  if (!integer(actor, 1)) throw new ApiError("invalid_input");
  let result: Response;
  try {
    result = await fetch(`/api/system/logs${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      redirect: "error",
      cache: "no-store",
      signal,
      headers:
        body === undefined
          ? {}
          : { "Content-Type": "application/json", "X-CSRF-Token": csrf ?? "" },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  } catch (error) {
    if (signal.aborted) throw error;
    throw new ApiError("network");
  }
  // Verify the identity before consuming either JSON or artifact bytes.
  if (result.ok && result.headers.get("X-ADTR-User-ID") !== String(actor))
    throw new ApiError("unauthenticated", 401);
  return result;
}
async function json(response: Response) {
  const v: unknown = await response.json().catch(() => fail());
  if (!response.ok)
    throw new ApiError(
      object(v) &&
      typeof v.error === "string" &&
      /^[a-z][a-z0-9_]{0,63}$/.test(v.error)
        ? v.error
        : "internal",
      response.status,
    );
  return v;
}
export const logAPI = {
  async list(q: LogQuery, signal: AbortSignal, actor: number) {
    return parseLogList(
      await json(await response(`?${logQuery(q)}`, signal, actor)),
      q,
    );
  },
  async sources(signal: AbortSignal, actor: number) {
    return parseSources(await json(await response("/sources", signal, actor)));
  },
  async history(q: BundleQuery, signal: AbortSignal, actor: number) {
    return parseBundleHistory(
      await json(
        await response(`/bundles/history?${bundleQuery(q)}`, signal, actor),
      ),
      q,
    );
  },
  async detail(id: string, signal: AbortSignal, actor: number) {
    if (!validExportID(id)) throw new ApiError("invalid_input");
    return parseBundleDetail(
      await json(
        await response(`/bundles/detail?taskUUID=${id}`, signal, actor),
      ),
      id,
    );
  },
  async submit(
    input: BundleInput,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
    actor: number,
  ) {
    const { startTm, endTm, systemType, idempotencyKey } = input;
    if (
      !validSelection({ startTm, endTm, systemType }) ||
      !validExportID(idempotencyKey)
    )
      throw new ApiError("invalid_input");
    const v = await json(
      await response(
        "/bundles",
        signal,
        actor,
        {
          startTm,
          endTm,
          systemType,
          idempotencyKey,
          actorPassword: proof.actorPassword,
          totpCode: proof.totpCode,
        },
        csrf,
      ),
    );
    if (
      !object(v) ||
      !exact(v, ["taskUUID", "task", "replayed"]) ||
      !validBundleTask(v.task) ||
      v.taskUUID !== v.task.taskUUID ||
      typeof v.replayed !== "boolean"
    )
      return fail();
    return { task: v.task, replayed: v.replayed };
  },
  async cancel(
    id: string,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
    actor: number,
  ) {
    if (!validExportID(id)) throw new ApiError("invalid_input");
    const v = await json(
      await response(
        "/bundles/cancel",
        signal,
        actor,
        {
          taskUUID: id,
          actorPassword: proof.actorPassword,
          totpCode: proof.totpCode,
        },
        csrf,
      ),
    );
    if (
      !object(v) ||
      !exact(v, ["task"]) ||
      !validBundleTask(v.task) ||
      v.task.taskUUID !== id
    )
      return fail();
    return v.task;
  },
  async download(
    id: string,
    signal: AbortSignal,
    actor: number,
  ): Promise<Blob> {
    if (!validExportID(id)) throw new ApiError("invalid_input");
    const r = await response(`/bundles/download?taskUUID=${id}`, signal, actor);
    if (!r.ok) {
      await json(r);
      return fail();
    }
    const max = 16 * 1024 * 1024,
      length = r.headers.get("Content-Length");
    if (
      r.headers.get("Content-Type")?.split(";")[0] !== "application/zip" ||
      r.headers.get("Content-Disposition") !==
        `attachment; filename="${bundleFilename(id)}"` ||
      (length !== null &&
        (!/^\d+$/.test(length) || Number(length) > max || Number(length) < 1))
    )
      return fail();
    if (!r.body) return fail();
    const reader = r.body.getReader(),
      parts: Uint8Array<ArrayBuffer>[] = [];
    let size = 0;
    try {
      for (;;) {
        const next = await reader.read();
        if (next.done) break;
        size += next.value.byteLength;
        if (size > max || signal.aborted) {
          await reader.cancel();
          return fail();
        }
        parts.push(new Uint8Array(next.value));
      }
    } finally {
      reader.releaseLock();
    }
    if (!size || (length !== null && Number(length) !== size) || signal.aborted)
      return fail();
    const blob = new Blob(parts, { type: "application/zip" }),
      signature = new Uint8Array(await blob.slice(0, 4).arrayBuffer());
    if (signature.join() !== "80,75,3,4" || signal.aborted) return fail();
    return blob;
  },
};
export function logError(error: unknown) {
  const messages: Record<string, string> = {
    network:
      "连接中断，结果尚未确认。请核对打包历史；重试保留原幂等键并使用新的验证码。",
    invalid_response:
      "服务器结果无法确认，旧数据已清除。请重新读取；不要换用新幂等键重复打包。",
    forbidden:
      "当前账户没有此运行日志操作权限，或权限已被撤销。仅安装所有者租户可以使用此功能。",
    artifact_invalid: "ZIP 文件校验失败，未下载文件。请重新读取打包详情。",
    bundle_not_ready: "ZIP 尚未发布，请重新读取任务和文件状态。",
    bundle_too_large:
      "所选记录超过 10,000 条或 ZIP 超过 16 MiB；任务不会截断记录。请缩小范围。",
    artifact_too_large:
      "ZIP 超过 16 MiB 上限，未发布文件；请缩小范围后重新提交。",
    result_too_large:
      "结果超过此操作的允许上限。查询请使用分页；诊断包最多 10,000 条事件且不会截断。",
    authorization_revoked: "打包授权已失效。请重新登录并核对当前权限。",
    storage_unavailable:
      "无法保存本次打包意图，尚未发送请求。请启用此页面的会话存储后重试。",
    schema_unavailable:
      "运行日志数据库或版本暂不可用，无法确认数据。请稍后重新读取。",
    idempotency_conflict:
      "此幂等键已有不同请求，不能替换原条件。请核对原提交。",
  };
  return error instanceof ApiError
    ? (messages[error.code] ??
        accessMessages[error.code] ??
        "运行日志请求失败，请重新读取服务器状态。")
    : "运行日志请求失败，请重新读取服务器状态。";
}
