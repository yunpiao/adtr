import { ApiError } from "./api";
import { integer, object, taskStates, type TaskState } from "./task-api";
import { type PageInfo } from "./access-api";

export interface ExportHistoryQuery {
  pageIdx: number;
  pageSize: number;
  status: TaskState[];
  startTm: string;
  endTm: string;
  sortTm: -1 | 1;
}
export const emptyHistoryQuery = (): ExportHistoryQuery => ({
  pageIdx: 1,
  pageSize: 20,
  status: [],
  startTm: "",
  endTm: "",
  sortTm: -1,
});
export type DownloadStatus =
  | "eligible"
  | "not_ready"
  | "snapshot_changed"
  | "artifact_invalid";
export interface ExportHistoryRow {
  taskUUID: string;
  fileName: string;
  modelType: "Audit";
  fileType: "xlsx";
  state: TaskState;
  progress: number;
  error: string;
  createdAt: string;
  updatedAt: string;
  attempt: number;
  maxAttempts: number;
  nextAttemptAt?: string;
  downloadReady: boolean;
  downloadStatus: DownloadStatus;
  rowCount?: number;
  snapshotAt?: string;
  downloadPath?: string;
}
export interface ExportHistory {
  page: PageInfo;
  list: ExportHistoryRow[];
  exhausted: boolean;
}
export const validExportID = (value: unknown): value is string =>
  typeof value === "string" &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value);
export const exportFilename = (id: string) => `audit-${id}.xlsx`;
export const exportDownloadPath = (id: string) =>
  `/api/audit/exports/download?taskUUID=${id}`;
export function validUTC(value: unknown): value is string {
  if (
    typeof value !== "string" ||
    !/^(?!0000)\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(value)
  )
    return false;
  const time = Date.parse(value);
  return (
    Number.isFinite(time) &&
    new Date(time).toISOString().slice(0, 19) === value.slice(0, 19)
  );
}
export function historyInputUTC(value: string) {
  if (!value) return "";
  // These controls explicitly use UTC wall-clock time, independently of the
  // browser's timezone. datetime-local may omit seconds when they are zero.
  const utc = `${value.length === 16 ? `${value}:00` : value}Z`;
  if (!validUTC(utc) || /\.\d{7,}Z$/.test(utc))
    throw new ApiError("invalid_input");
  return /\.\d{4,6}Z$/.test(utc) ? utc : new Date(utc).toISOString();
}
// Compare UTC values without rounding PostgreSQL's microsecond boundaries to
// JavaScript milliseconds. Both have already passed strict UTC validation.
const comparableUTC = (value: string) =>
  value.slice(0, 19) + (value.split(".")[1]?.slice(0, -1) ?? "").padEnd(9, "0");
export function historyQuery(query: ExportHistoryQuery) {
  if (
    !integer(query.pageIdx, 1, 1000000) ||
    !(
      integer(query.pageSize, 1, 100) ||
      (query.pageSize === -1 && query.pageIdx === 1)
    ) ||
    ![-1, 1].includes(query.sortTm) ||
    !Array.isArray(query.status) ||
    query.status.some((s) => !taskStates.includes(s)) ||
    new Set(query.status).size !== query.status.length ||
    [query.startTm, query.endTm].some(
      (t) => t !== "" && (!validUTC(t) || /\.\d{7,}Z$/.test(t)),
    ) ||
    (query.startTm &&
      query.endTm &&
      comparableUTC(query.startTm) >= comparableUTC(query.endTm))
  )
    throw new ApiError("invalid_input");
  const values = new URLSearchParams({
    pageIdx: String(query.pageIdx),
    pageSize: String(query.pageSize),
    sortTm: String(query.sortTm),
  });
  for (const status of query.status) values.append("status", status);
  if (query.startTm) values.set("startTm", query.startTm);
  if (query.endTm) values.set("endTm", query.endTm);
  return values.toString();
}
const exact = (
  v: Record<string, unknown>,
  required: string[],
  optional: string[] = [],
) =>
  required.every((k) => Object.hasOwn(v, k)) &&
  Object.keys(v).every((k) => required.includes(k) || optional.includes(k));
export function validHistoryRow(v: unknown): v is ExportHistoryRow {
  if (
    !object(v) ||
    !exact(
      v,
      [
        "taskUUID",
        "fileName",
        "modelType",
        "fileType",
        "state",
        "progress",
        "error",
        "createdAt",
        "updatedAt",
        "attempt",
        "maxAttempts",
        "downloadReady",
        "downloadStatus",
      ],
      ["nextAttemptAt", "rowCount", "snapshotAt", "downloadPath"],
    ) ||
    !validExportID(v.taskUUID) ||
    v.fileName !== exportFilename(v.taskUUID) ||
    v.modelType !== "Audit" ||
    v.fileType !== "xlsx" ||
    !taskStates.includes(v.state as TaskState) ||
    !integer(v.progress, 0, 100) ||
    typeof v.error !== "string" ||
    !/^(?:[a-z][a-z0-9_]{0,63})?$/.test(v.error) ||
    !validUTC(v.createdAt) ||
    !validUTC(v.updatedAt) ||
    !integer(v.attempt, 0) ||
    !integer(v.maxAttempts, 1) ||
    (v.nextAttemptAt !== undefined && !validUTC(v.nextAttemptAt)) ||
    typeof v.downloadReady !== "boolean"
  )
    return false;
  if (v.state !== "succeeded" && v.downloadStatus !== "not_ready") return false;
  if (
    v.state === "succeeded" &&
    !["eligible", "snapshot_changed", "artifact_invalid"].includes(
      v.downloadStatus as string,
    )
  )
    return false;
  if (v.downloadStatus === "eligible")
    return (
      v.downloadReady &&
      integer(v.rowCount, 0, 100000) &&
      validUTC(v.snapshotAt) &&
      v.downloadPath === exportDownloadPath(v.taskUUID)
    );
  return (
    !v.downloadReady &&
    !["rowCount", "snapshotAt", "downloadPath"].some((key) =>
      Object.hasOwn(v, key),
    )
  );
}
export function parseHistory(
  value: unknown,
  query: ExportHistoryQuery,
): ExportHistory {
  const invalid = (): never => {
    throw new ApiError("invalid_response");
  };
  const size = query.pageSize === -1 ? 1000 : query.pageSize;
  if (
    !object(value) ||
    !exact(value, ["page", "list", "exhausted"]) ||
    !object(value.page) ||
    !exact(value.page, ["pageIdx", "pageSize", "total", "totalPage"]) ||
    value.page.pageIdx !== query.pageIdx ||
    value.page.pageSize !== query.pageSize ||
    !integer(value.page.total, 0) ||
    !integer(value.page.totalPage, 0) ||
    value.page.totalPage !==
      (query.pageSize === -1
        ? Number(value.page.total > 0)
        : Math.ceil(value.page.total / size)) ||
    (query.pageSize === -1 && value.page.total > 1000) ||
    !Array.isArray(value.list) ||
    value.list.length > size ||
    !value.list.every(validHistoryRow) ||
    new Set(value.list.map((row) => row.taskUUID)).size !== value.list.length ||
    typeof value.exhausted !== "boolean"
  )
    return invalid();
  const offset = (query.pageIdx - 1) * size;
  if (
    value.list.length !==
      Math.min(size, Math.max(0, value.page.total - offset)) ||
    value.exhausted !== offset + value.list.length >= value.page.total ||
    (query.status.length > 0 &&
      value.list.some((row) => !query.status.includes(row.state)))
  )
    return invalid();
  return value as unknown as ExportHistory;
}
