// Synthetic contract fixtures only; never production data or real-API evidence.
import type { Profile } from "./api";
import type { Task } from "./task-api";
import { exportColumns, exportFilename, exportKind, type ExportRequest, type ExportDetail } from "./group-asset-export-v3-api";
export const exportProfile: Profile = {
  ID: 1, username: "synthetic-exporter", role: "custom", priv: 0, mobile: "", email: "", remark: "",
  passStrength: "high", hasMfa: true, needChangePwd: false, isExpired: false,
  passwordNotUpdatedDays: 0, pwdUpdateTm: "2026-10-01T00:00:00Z", csrfToken: "synthetic-session",
};
export const exportRequest: ExportRequest = {
  domainId: "synthetic-domain", expectedRevision: "9007199254740993", expectedCredentialRevision: "9007199254740995",
  observationId: "observation-one", search: "", creatTm: "", screenColumns: [...exportColumns],
};
export const exportTask: Task = {
  taskUUID: "00000000-0000-0000-0000-000000000001", taskName: exportKind, domainId: exportRequest.domainId,
  payloadVersion: 1, state: "queued", sourceState: "", error: "", createdAt: "2026-10-13T00:00:00Z", updatedAt: "2026-10-13T00:00:00Z",
  attempt: 0, maxAttempts: 3, progress: 0, resultVersion: 0, result: {}, cursor: {}, parentTaskUUID: "", terminalAt: null, archived: false, visibilityVersion: 0,
};
export const exportReceipt = { taskID: exportTask.taskUUID, task: exportTask, request: exportRequest };
export const readyExport: ExportDetail = {
  ...exportReceipt, task: { ...exportTask, state: "succeeded", progress: 100, terminalAt: "2026-10-13T00:00:01Z" }, downloadReady: true, downloadStatus: "ready",
  artifact: { rowCount: 0, byteCount: 3, sha256: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", filename: exportFilename(exportTask.taskUUID) },
};
export function exportJSON(value: unknown, status = 200, actor = 1) {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-ADTR-User-ID": String(actor) } });
}
