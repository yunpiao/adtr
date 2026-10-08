import { ApiError } from "./api";
import { accessMessages, type PageInfo, type Proof } from "./access-api";
import { validTask, type Task } from "./task-api";
import {
  exportFilename,
  historyQuery,
  parseHistory,
  validExportID,
  type ExportHistoryQuery,
} from "./audit-history";

export const auditColumns = [
  "userId",
  "loginUser",
  "sourceIp",
  "logTypeName",
  "event",
  "eventArgs",
  "eventResult",
  "CreateTm",
] as const;
export type AuditColumn = (typeof auditColumns)[number];
export interface AuditRow {
  ID: string;
  loginUser: string | null;
  sourceIp: string | null;
  event: string;
  eventArgs: string;
  eventResult: "NONE" | "SUCCESS" | "FAIL";
  CreateTm: string;
  logType: number;
  logTypeName: string;
  userId: number | null;
  source:
    | "auth"
    | "resource"
    | "task"
    | "audit"
    | "domain"
    | "operation_account"
    | "credential_use"
    | "operational_log";
  domainId: string | null;
  availability: Record<
    "loginUser" | "sourceIp" | "path" | "requestId" | "eventResult",
    boolean
  >;
  deleted: boolean;
  deletable: boolean;
  visibilityVersion: number;
}
export interface AuditFilter {
  startTm: string;
  endTm: string;
  keyword: string;
  filterEvent: string[];
  logTypeList: number[];
  createSort: number;
  visibility: "visible" | "hidden" | "all";
}
export const emptyFilter = (): AuditFilter => ({
  startTm: "",
  endTm: "",
  keyword: "",
  filterEvent: [],
  logTypeList: [],
  createSort: -1,
  visibility: "visible",
});
export interface AuditList {
  page: PageInfo;
  List: AuditRow[];
  exhausted: boolean;
}
export interface AuditTypes {
  events: string[];
  List: { logType: number; logTypeName: string; eventNameList: string[] }[];
}
export interface ColumnCatalogue {
  columns: { prop: AuditColumn; label: string }[];
  defaultColumns: AuditColumn[];
  maxColumns: number;
}
export interface ExportInput extends AuditFilter {
  selectColumn: AuditColumn[];
  idempotencyKey: string;
}
export interface ExportDetail {
  task: Task;
  downloadReady: boolean;
  rowCount?: number;
  snapshotAt?: string;
  downloadPath?: string;
}
export const auditOperations = [
  "GET /api/audit",
  "GET /api/audit/types",
  "GET /api/audit/columns",
  "POST /api/audit/delete",
  "POST /api/audit/restore",
  "POST /api/audit/exports",
  "GET /api/audit/exports/history",
  "GET /api/audit/exports/detail",
  "GET /api/audit/exports/download",
  "POST /api/tasks/submit",
] as const;
export type AuditOperation = (typeof auditOperations)[number];
const object = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const integer = (
  v: unknown,
  min = 0,
  max = Number.MAX_SAFE_INTEGER,
): v is number =>
  typeof v === "number" && Number.isSafeInteger(v) && v >= min && v <= max;
const strings = (v: unknown): v is string[] =>
  Array.isArray(v) && v.every((x) => typeof x === "string");
const nullableString = (v: unknown) => v === null || typeof v === "string";
export function validAuditRow(v: unknown): v is AuditRow {
  return (
    object(v) &&
    typeof v.ID === "string" &&
    /^(auth|resource|task|audit|domain|operation_account|credential_use|operational_log)\.[1-9][0-9]{0,18}$/.test(
      v.ID,
    ) &&
    typeof v.source === "string" &&
    v.ID.startsWith(`${v.source}.`) &&
    nullableString(v.loginUser) &&
    nullableString(v.sourceIp) &&
    nullableString(v.domainId) &&
    (v.userId === null || integer(v.userId, 1)) &&
    ["event", "eventArgs", "CreateTm", "logTypeName"].every(
      (k) => typeof v[k] === "string",
    ) &&
    ["NONE", "SUCCESS", "FAIL"].includes(v.eventResult as string) &&
    integer(v.logType, 1, 9) &&
    typeof v.deleted === "boolean" &&
    typeof v.deletable === "boolean" &&
    integer(v.visibilityVersion) &&
    object(v.availability) &&
    ["loginUser", "sourceIp", "path", "requestId", "eventResult"].every(
      (k) =>
        typeof (v.availability as Record<string, unknown>)[k] === "boolean",
    )
  );
}
export function auditQuery(filter: AuditFilter, pageIdx = 1, pageSize = 20) {
  const query = new URLSearchParams({
    pageIdx: String(pageIdx),
    pageSize: String(pageSize),
    createSort: String(filter.createSort),
    visibility: filter.visibility,
  });
  for (const key of ["startTm", "endTm", "keyword"] as const)
    if (filter[key]) query.set(key, filter[key]);
  for (const event of filter.filterEvent) query.append("filterEvent", event);
  for (const type of filter.logTypeList)
    query.append("logTypeList", String(type));
  return query.toString();
}
async function fetchResponse(
  path: string,
  signal: AbortSignal,
  body?: unknown,
  csrf?: string,
) {
  try {
    return await fetch(`/api/audit${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      redirect: "error",
      cache: "no-store",
      signal,
      headers:
        body === undefined
          ? {}
          : {
              "Content-Type": "application/json",
              ...(csrf ? { "X-CSRF-Token": csrf } : {}),
            },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  } catch (error) {
    if (signal.aborted) throw error;
    throw new ApiError("network");
  }
}
async function json(response: Response) {
  const data: unknown = await response.json().catch(() => {
    throw new ApiError("invalid_response", response.status);
  });
  if (!response.ok)
    throw new ApiError(
      object(data) && typeof data.error === "string" ? data.error : "internal",
      response.status,
    );
  return data;
}
async function transport(
  path: string,
  signal: AbortSignal,
  body?: unknown,
  csrf?: string,
) {
  return json(await fetchResponse(path, signal, body, csrf));
}
function verifyExportActor(response: Response, userID: number) {
  if (response.ok && response.headers.get("X-ADTR-User-ID") !== String(userID))
    throw new ApiError("unauthenticated", 401);
}
async function exportResponse(
  path: string,
  signal: AbortSignal,
  userID: number,
) {
  if (!integer(userID, 1)) throw new ApiError("invalid_input");
  const response = await fetchResponse(path, signal);
  verifyExportActor(response, userID);
  return response;
}
export const auditAPI = {
  async history(
    query: ExportHistoryQuery,
    signal: AbortSignal,
    userID: number,
  ) {
    return parseHistory(
      await json(
        await exportResponse(
          `/exports/history?${historyQuery(query)}`,
          signal,
          userID,
        ),
      ),
      query,
    );
  },
  async list(query: string, signal: AbortSignal): Promise<AuditList> {
    const v = await transport(`?${query}`, signal);
    if (
      !object(v) ||
      !object(v.page) ||
      !integer(v.page.pageIdx, 1) ||
      !(v.page.pageSize === -1 || integer(v.page.pageSize, 1, 100)) ||
      !integer(v.page.total) ||
      !integer(v.page.totalPage) ||
      !Array.isArray(v.List) ||
      !v.List.every(validAuditRow) ||
      typeof v.exhausted !== "boolean"
    )
      throw new ApiError("invalid_response");
    return v as unknown as AuditList;
  },
  async types(signal: AbortSignal): Promise<AuditTypes> {
    const v = await transport("/types", signal);
    if (
      !object(v) ||
      !strings(v.events) ||
      !Array.isArray(v.List) ||
      !v.List.every(
        (x) =>
          object(x) &&
          integer(x.logType, 1, 9) &&
          typeof x.logTypeName === "string" &&
          strings(x.eventNameList),
      )
    )
      throw new ApiError("invalid_response");
    return v as unknown as AuditTypes;
  },
  async columns(signal: AbortSignal): Promise<ColumnCatalogue> {
    const v = await transport("/columns", signal);
    const validSelection = (s: unknown) =>
      strings(s) &&
      s.length > 0 &&
      s.length <= 8 &&
      new Set(s).size === s.length &&
      s.every((x) => auditColumns.includes(x as AuditColumn));
    if (
      !object(v) ||
      v.maxColumns !== 8 ||
      !Array.isArray(v.columns) ||
      v.columns.length !== 8 ||
      !v.columns.every(
        (x) =>
          object(x) &&
          auditColumns.includes(x.prop as AuditColumn) &&
          typeof x.label === "string",
      ) ||
      new Set(v.columns.map((x) => x.prop)).size !== 8 ||
      !validSelection(v.defaultColumns)
    )
      throw new ApiError("invalid_response");
    return v as unknown as ColumnCatalogue;
  },
  async visibility(
    action: "delete" | "restore",
    id: string[],
    reason: string,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ) {
    const v = await transport(
      `/${action}`,
      signal,
      { id, reason, ...proof },
      csrf,
    );
    if (
      !object(v) ||
      v.result !== "success" ||
      !integer(v.changed, 1, id.length) ||
      !integer(v.visibilityRevision, 1)
    )
      throw new ApiError("invalid_response");
    return { changed: v.changed, visibilityRevision: v.visibilityRevision };
  },
  async submit(
    input: ExportInput,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ) {
    if (input.visibility !== "visible")
      throw new ApiError("invalid_export_visibility");
    const {
      startTm,
      endTm,
      keyword,
      filterEvent,
      createSort,
      logTypeList,
      selectColumn,
      idempotencyKey,
    } = input;
    const v = await transport(
      "/exports",
      signal,
      {
        startTm,
        endTm,
        keyword,
        filterEvent,
        createSort,
        logTypeList,
        selectColumn,
        idempotencyKey,
        ...proof,
      },
      csrf,
    );
    if (
      !object(v) ||
      !validTask(v.task) ||
      v.task.taskName !== "audit.export" ||
      v.taskUUID !== v.task.taskUUID ||
      typeof v.replayed !== "boolean"
    )
      throw new ApiError("invalid_response");
    return { task: v.task, replayed: v.replayed };
  },
  async detail(
    taskUUID: string,
    signal: AbortSignal,
    userID: number,
  ): Promise<ExportDetail> {
    if (!validExportID(taskUUID)) throw new ApiError("invalid_input");
    const v = await json(
      await exportResponse(
        `/exports/detail?${new URLSearchParams({ taskUUID })}`,
        signal,
        userID,
      ),
    );
    if (
      !object(v) ||
      !validTask(v.task) ||
      v.task.taskUUID !== taskUUID ||
      v.task.taskName !== "audit.export" ||
      typeof v.downloadReady !== "boolean" ||
      (v.rowCount !== undefined && !integer(v.rowCount, 0, 100000)) ||
      (v.snapshotAt !== undefined && typeof v.snapshotAt !== "string") ||
      (v.downloadPath !== undefined && typeof v.downloadPath !== "string") ||
      (v.downloadReady &&
        (v.task.state !== "succeeded" || !integer(v.rowCount, 0, 100000)))
    )
      throw new ApiError("invalid_response");
    return v as unknown as ExportDetail;
  },
  async download(
    taskUUID: string,
    signal: AbortSignal,
    userID: number,
  ): Promise<Blob> {
    if (!validExportID(taskUUID)) throw new ApiError("invalid_input");
    const response = await exportResponse(
      `/exports/download?${new URLSearchParams({ taskUUID })}`,
      signal,
      userID,
    );
    if (!response.ok) {
      await json(response);
      throw new ApiError("invalid_response");
    }
    if (
      response.headers.get("Content-Type")?.split(";")[0] !==
      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
    )
      throw new ApiError("invalid_response");
    if (
      response.headers.get("Content-Disposition") !==
      `attachment; filename="${exportFilename(taskUUID)}"`
    )
      throw new ApiError("invalid_response");
    if (Number(response.headers.get("Content-Length")) > 128 * 1024 * 1024)
      throw new ApiError("invalid_response");
    const blob = await response.blob();
    if (!blob.size || blob.size > 128 * 1024 * 1024)
      throw new ApiError("invalid_response");
    return blob;
  },
};
export function auditError(error: unknown) {
  const specific: Record<string, string> = {
    network:
      "连接中断，操作结果尚未确认。请重新读取服务器状态；导出重试必须保留原幂等键并使用新的验证码。",
    invalid_response:
      "服务器结果无法确认，请刷新核对。导出不要换用新幂等键重复提交。",
    invalid_export_visibility:
      "当前仅支持导出未隐藏记录，请将可见性切换为未隐藏记录并应用筛选。",
    authorization_revoked:
      "当前导出授权已失效，请重新登录并核对权限，再创建新的导出。",
    invalid_columns:
      "请选择 1 至 8 个已登记的导出列；不能留空、重复或使用未知列。",
    export_too_large: "导出超过 100,000 条上限，请缩小筛选范围后重新创建导出。",
    result_too_large: "结果超过允许上限，请缩小筛选范围或使用分页。",
    protected_audit_event: "隐藏和恢复的控制审计记录受到保护，不能隐藏。",
    visibility_unchanged: "记录可见性已经改变，请刷新列表核对；没有重复执行。",
    export_snapshot_changed:
      "导出快照已因隐藏或恢复操作失效，不能下载。请返回列表重新创建导出。",
    export_not_ready: "导出文件尚未就绪，请等待服务器完成任务。",
    idempotency_conflict:
      "此幂等键已用于其他导出条件，请核对原提交，不要自动重试。",
    forbidden: "当前账户没有此审计操作权限，或权限已被撤销。",
    artifact_invalid: "导出文件校验失败，未下载文件。请重新创建导出。",
    unsupported_model_type: "目前仅支持 Audit 审计导出，其他模型尚未提供。",
    unsupported_app_type: "当前导出历史尚不支持应用类型筛选。",
  };
  return error instanceof ApiError
    ? (specific[error.code] ??
        accessMessages[error.code] ??
        `审计操作失败（${error.code}），请刷新核对。`)
    : "审计请求失败，请刷新服务器状态。";
}
