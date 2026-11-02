import { ApiError, type Profile } from "./api";
import { object, integer, validTask, type Task } from "./task-api";
import {
  detectionMapping,
  parseDetectionResponse,
} from "./simulation-detection-api";
export const scheduleContract = "adtr.simulation-detection-schedules.v1";
export type Definition = {
  label: string;
  cycle: 0 | 1 | 2;
  templateId: string;
  templateVersion: number;
  datasetId: string;
};
export type Schedule = Definition & {
  id: string;
  domainId: string;
  revision: number;
  controlVersion: number;
  state:
    | "paused"
    | "enabled"
    | "archived"
    | "authorization_blocked"
    | "configuration_blocked";
  error: string;
  pinned: {
    templateId: string;
    templateVersion: number;
    templateHash: string;
    datasetId: string;
    fixtureHash: string;
    mappingVersion: string;
  };
  anchor: string;
  nextIndex: number;
  nextAt: string;
  createdAt: string;
  updatedAt: string;
  simulation: true;
};
export type ScheduleEvent = {
  id: number;
  revision: number;
  action: string;
  firstIndex?: number;
  lastIndex?: number;
  taskId?: string;
  createdAt: string;
};
export type Detail = {
  schedule: Schedule;
  events: ScheduleEvent[];
  hasMore: boolean;
};
export type List = {
  schedules: Schedule[];
  page: { pageIdx: number; pageSize: number; total: number; totalPage: number };
};
export type Change = {
  receipt: {
    operationId: string;
    action: string;
    scheduleId: string;
    controlVersion: number;
    revision: number;
    state: string;
  };
  schedule: Schedule;
  replayed: boolean;
};
export type WriteKind = "create" | "update" | "enable" | "pause" | "archive";
export type Write = {
  domainId: string;
  operationId: string;
  id?: string;
  expectedControlVersion?: number;
} & Partial<Definition>;
const text = (v: unknown): v is string => typeof v === "string" && v.length > 0;
const date = (v: unknown) => text(v) && Number.isFinite(Date.parse(v));
const hash = (v: unknown) => typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
const states = [
  "paused",
  "enabled",
  "archived",
  "authorization_blocked",
  "configuration_blocked",
];
export function validSchedule(v: unknown): v is Schedule {
  return (
    object(v) &&
    v.simulation === true &&
    text(v.id) &&
    text(v.domainId) &&
    text(v.label) &&
    [...v.label].length <= 80 &&
    integer(v.cycle, 0, 2) &&
    integer(v.revision, 1) &&
    integer(v.controlVersion, 1) &&
    states.includes(String(v.state)) &&
    typeof v.error === "string" &&
    text(v.templateId) &&
    integer(v.templateVersion, 1) &&
    text(v.datasetId) &&
    object(v.pinned) &&
    v.pinned.templateId === v.templateId &&
    v.pinned.templateVersion === v.templateVersion &&
    v.pinned.datasetId === v.datasetId &&
    hash(v.pinned.templateHash) &&
    hash(v.pinned.fixtureHash) &&
    v.pinned.mappingVersion === detectionMapping &&
    date(v.anchor) &&
    date(v.nextAt) &&
    date(v.createdAt) &&
    date(v.updatedAt) &&
    integer(v.nextIndex, 1)
  );
}
export function parseScheduleResponse(
  kind: "list" | "detail" | "change" | "task",
  v: unknown,
): unknown {
  let ok = false;
  if (object(v)) {
    if (kind === "list")
      ok =
        Array.isArray(v.schedules) &&
        v.schedules.length <= 50 &&
        v.schedules.every(validSchedule) &&
        object(v.page) &&
        integer(v.page.pageIdx, 1, 10000) &&
        integer(v.page.pageSize, 1, 50) &&
        integer(v.page.total, 0) &&
        integer(v.page.totalPage, 0);
    if (kind === "detail")
      ok =
        validSchedule(v.schedule) &&
        typeof v.hasMore === "boolean" &&
        Array.isArray(v.events) &&
        v.events.length <= 50 &&
        v.events.every(
          (e) =>
            object(e) &&
            integer(e.id, 1) &&
            integer(e.revision, 1) &&
            text(e.action) &&
            date(e.createdAt) &&
            (e.taskId === undefined || text(e.taskId)) &&
            (e.firstIndex === undefined || integer(e.firstIndex, 1)) &&
            (e.lastIndex === undefined || integer(e.lastIndex, 1)),
        );
    if (kind === "change")
      ok =
        validSchedule(v.schedule) &&
        typeof v.replayed === "boolean" &&
        object(v.receipt) &&
        text(v.receipt.operationId) &&
        ["created", "edited", "enabled", "paused", "archived"].includes(
          String(v.receipt.action),
        ) &&
        v.receipt.scheduleId === v.schedule.id &&
        integer(v.receipt.revision, 1) &&
        integer(v.receipt.controlVersion, 1) &&
        states.includes(String(v.receipt.state));
    if (
      kind === "task" &&
      v.simulation === true &&
      validTask(v.task) &&
      v.task.taskName === "simulation.detection.scheduled"
    ) {
      ok = true;
      if (v.task.state === "succeeded") {
        try {
          parseDetectionResponse("task", {
            simulation: true,
            task: { ...v.task, taskName: "simulation.detection" },
          });
        } catch {
          ok = false;
        }
        ok =
          ok &&
          object(v.task.result) &&
          text(v.task.result.scheduleId) &&
          integer(v.task.result.revision, 1) &&
          date(v.task.result.scheduledAt);
      }
    }
  }
  if (!ok) throw new ApiError("invalid_response");
  return v;
}
async function request<T>(
  path: string,
  kind: Parameters<typeof parseScheduleResponse>[0],
  domainId: string,
  p: Profile,
  s: AbortSignal,
  body?: Write,
): Promise<T> {
  let r: Response;
  try {
    r = await fetch("/api/simulation/detection-schedules" + path, {
      method: body ? "POST" : "GET",
      credentials: "same-origin",
      cache: "no-store",
      signal: s,
      headers: body
        ? { "Content-Type": "application/json", "X-CSRF-Token": p.csrfToken }
        : {},
      ...(body ? { body: JSON.stringify(body) } : {}),
    });
  } catch (e) {
    if (s.aborted) throw e;
    throw new ApiError("network");
  }
  if (r.ok && r.headers.get("X-ADTR-User-ID") !== String(p.ID))
    throw new ApiError("unauthenticated", 401);
  const v: unknown = await r.json().catch(() => {
    throw new ApiError("invalid_response", r.status);
  });
  if (!r.ok)
    throw new ApiError(
      object(v) && text(v.error) ? v.error : "internal",
      r.status,
    );
  const parsed = parseScheduleResponse(kind, v);
  if (object(parsed)) {
    const rows =
      kind === "list" ? parsed.schedules : [parsed.schedule ?? parsed.task];
    if (
      !Array.isArray(rows) ||
      rows.some((row) => !object(row) || row.domainId !== domainId)
    )
      throw new ApiError("invalid_response");
  }
  const requested = new URLSearchParams(path.split("?")[1] ?? "");
  if (object(parsed)) {
    if (
      kind === "detail" &&
      object(parsed.schedule) &&
      parsed.schedule.id !== requested.get("id")
    )
      throw new ApiError("invalid_response");
    if (
      kind === "task" &&
      object(parsed.task) &&
      parsed.task.taskUUID !== requested.get("taskId")
    )
      throw new ApiError("invalid_response");
    if (
      kind === "change" &&
      object(parsed.receipt) &&
      (parsed.receipt.operationId !==
        (body?.operationId ?? requested.get("operationId")) ||
        (body?.id && parsed.receipt.scheduleId !== body.id))
    )
      throw new ApiError("invalid_response");
  }
  return parsed as T;
}
const query = (p: Record<string, string | number>) =>
  new URLSearchParams(
    Object.entries(p).map(([k, v]) => [k, String(v)]),
  ).toString();
export const simulationDetectionSchedulesAPI = {
  list: (domainId: string, pageIdx: number, p: Profile, s: AbortSignal) =>
    request<List>(
      `/list?${query({ domainId, pageIdx, pageSize: 20 })}`,
      "list",
      domainId,
      p,
      s,
    ),
  detail: (
    domainId: string,
    id: string,
    pageIdx: number,
    p: Profile,
    s: AbortSignal,
  ) =>
    request<Detail>(
      `/detail?${query({ domainId, id, pageIdx, pageSize: 20 })}`,
      "detail",
      domainId,
      p,
      s,
    ),
  receipt: (
    domainId: string,
    operationId: string,
    p: Profile,
    s: AbortSignal,
  ) =>
    request<Change>(
      `/receipt?${query({ domainId, operationId })}`,
      "change",
      domainId,
      p,
      s,
    ),
  write: (kind: WriteKind, input: Write, p: Profile, s: AbortSignal) =>
    request<Change>("/" + kind, "change", input.domainId, p, s, input),
  task: (domainId: string, taskId: string, p: Profile, s: AbortSignal) =>
    request<{ simulation: true; task: Task }>(
      `/task?${query({ domainId, taskId })}`,
      "task",
      domainId,
      p,
      s,
    ),
};
