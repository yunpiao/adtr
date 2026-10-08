import { ApiError } from "./api";
import type { PageInfo, Proof } from "./access-api";
import {
  integer,
  object,
  taskTransport,
  terminal,
  type TaskState,
} from "./task-api";

export const scheduleStates = [
  "paused",
  "enabled",
  "authorization_blocked",
] as const;
export type ScheduleState = (typeof scheduleStates)[number];
export const scheduleLabels: Record<ScheduleState, string> = {
  paused: "已暂停",
  enabled: "已启用",
  authorization_blocked: "授权失效，须新建计划",
};
export interface Schedule {
  scheduleUUID: string;
  label: string;
  taskName: string;
  domainId: string;
  payloadVersion: number;
  startAt: string;
  intervalSeconds: number;
  state: ScheduleState;
  controlVersion: number;
  nextAt?: string;
  lastTaskUUID: string;
  lastScheduledAt?: string;
  error: string;
  createdAt: string;
  updatedAt: string;
}
export interface ScheduleEvent {
  id: number;
  action: string;
  firstIndex?: number;
  lastIndex?: number;
  count?: number;
  firstAt?: string;
  lastAt?: string;
  taskUUID: string;
  controlVersion: number;
  createdAt: string;
}
export interface ScheduleDefinition {
  label: string;
  taskName: "infrastructure.health";
  domainId: "platform";
  payloadVersion: 1;
  payload: Record<string, never>;
  startAt: string;
  intervalSeconds: number;
}
export interface ScheduleControl {
  scheduleUUID: string;
  expectedControlVersion: number;
}
export interface ArchiveTarget {
  taskUUID: string;
  visibilityVersion: number;
}
export interface ArchiveInput {
  targets: ArchiveTarget[];
  before?: string;
  reason: string;
}
export interface ArchiveCandidate extends ArchiveTarget {
  taskName: string;
  domainId: string;
  state: TaskState;
  terminalAt: string;
}
export interface ArchiveReceipt {
  operationUUID: string;
  action: "archive" | "restore";
  before?: string;
  reason: string;
  targets: (ArchiveTarget & { archived: boolean })[];
  occurredAt: string;
}
export const validPage = (v: unknown): v is PageInfo =>
  object(v) &&
  integer(v.pageIdx, 1, 1_000_000) &&
  integer(v.pageSize, 1, 100) &&
  integer(v.total, 0) &&
  integer(v.totalPage, 0);
const strings = (v: Record<string, unknown>, keys: string[]) =>
  keys.every((k) => typeof v[k] === "string");
export const validSchedule = (v: unknown): v is Schedule =>
  object(v) &&
  strings(v, [
    "scheduleUUID",
    "label",
    "taskName",
    "domainId",
    "startAt",
    "lastTaskUUID",
    "error",
    "createdAt",
    "updatedAt",
  ]) &&
  !!v.scheduleUUID &&
  v.taskName === "infrastructure.health" &&
  v.domainId === "platform" &&
  v.payloadVersion === 1 &&
  integer(v.intervalSeconds, 60, 86400) &&
  integer(v.controlVersion, 0) &&
  scheduleStates.includes(v.state as ScheduleState) &&
  ["nextAt", "lastScheduledAt"].every(
    (k) => v[k] === undefined || typeof v[k] === "string",
  );
const validEvent = (v: unknown): v is ScheduleEvent =>
  object(v) &&
  strings(v, ["action", "taskUUID", "createdAt"]) &&
  integer(v.id, 1) &&
  integer(v.controlVersion, 0) &&
  ["firstIndex", "lastIndex", "count"].every(
    (k) => v[k] === undefined || integer(v[k], 0),
  ) &&
  ["firstAt", "lastAt"].every(
    (k) => v[k] === undefined || typeof v[k] === "string",
  );
export const maintenanceAPI = {
  async schedules(query: string, signal: AbortSignal) {
    const v = await taskTransport(`/schedules?${query}`, signal);
    if (
      !object(v) ||
      !validPage(v.page) ||
      !Array.isArray(v.schedules) ||
      !v.schedules.every(validSchedule) ||
      typeof v.exhausted !== "boolean"
    )
      throw new ApiError("invalid_response");
    return v as unknown as {
      page: PageInfo;
      schedules: Schedule[];
      exhausted: boolean;
    };
  },
  async detail(id: string, pageIdx: number, signal: AbortSignal) {
    const v = await taskTransport(
      `/schedules/detail?${new URLSearchParams({ scheduleUUID: id, pageIdx: String(pageIdx), pageSize: "20" })}`,
      signal,
    );
    if (
      !object(v) ||
      !validSchedule(v.schedule) ||
      v.schedule.scheduleUUID !== id ||
      !validPage(v.page) ||
      !Array.isArray(v.events) ||
      !v.events.every(validEvent) ||
      typeof v.exhausted !== "boolean"
    )
      throw new ApiError("invalid_response");
    return v as unknown as {
      schedule: Schedule;
      page: PageInfo;
      events: ScheduleEvent[];
      exhausted: boolean;
    };
  },
  async create(
    input: ScheduleDefinition,
    key: string,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ) {
    const v = await taskTransport(
      "/schedules/create",
      signal,
      { ...input, idempotencyKey: key, ...proof },
      csrf,
    );
    if (
      !object(v) ||
      !validSchedule(v.schedule) ||
      typeof v.replayed !== "boolean"
    )
      throw new ApiError("invalid_response");
    return v as unknown as { schedule: Schedule; replayed: boolean };
  },
  async control(
    action: "enable" | "pause",
    input: ScheduleControl,
    key: string,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ) {
    const v = await taskTransport(
      `/schedules/${action}`,
      signal,
      { ...input, idempotencyKey: key, ...proof },
      csrf,
    );
    if (
      !object(v) ||
      !validSchedule(v.schedule) ||
      v.schedule.scheduleUUID !== input.scheduleUUID ||
      typeof v.replayed !== "boolean" ||
      !object(v.receipt) ||
      !strings(v.receipt, ["operationUUID", "createdAt"]) ||
      v.receipt.scheduleUUID !== input.scheduleUUID ||
      v.receipt.action !== action ||
      !scheduleStates.includes(v.receipt.state as ScheduleState) ||
      !integer(v.receipt.controlVersion, 0)
    )
      throw new ApiError("invalid_response");
    return v as unknown as {
      schedule: Schedule;
      replayed: boolean;
      receipt: {
        operationUUID: string;
        scheduleUUID: string;
        action: string;
        state: ScheduleState;
        controlVersion: number;
        createdAt: string;
      };
    };
  },
  async candidates(query: string, signal: AbortSignal) {
    const v = await taskTransport(`/archive-candidates?${query}`, signal);
    if (
      !object(v) ||
      typeof v.before !== "string" ||
      !validPage(v.page) ||
      typeof v.exhausted !== "boolean" ||
      !Array.isArray(v.tasks) ||
      !v.tasks.every(
        (t) =>
          object(t) &&
          strings(t, ["taskUUID", "taskName", "domainId", "terminalAt"]) &&
          t.taskName === "infrastructure.health" &&
          t.domainId === "platform" &&
          terminal(t.state as TaskState) &&
          integer(t.visibilityVersion, 0),
      )
    )
      throw new ApiError("invalid_response");
    return v as unknown as {
      before: string;
      page: PageInfo;
      tasks: ArchiveCandidate[];
      exhausted: boolean;
    };
  },
  async archive(
    action: "archive" | "restore",
    input: ArchiveInput,
    key: string,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ) {
    const v = await taskTransport(
      `/${action}`,
      signal,
      { ...input, idempotencyKey: key, ...proof },
      csrf,
    );
    if (
      !object(v) ||
      typeof v.replayed !== "boolean" ||
      !object(v.receipt) ||
      !strings(v.receipt, ["operationUUID", "reason", "occurredAt"]) ||
      v.receipt.action !== action ||
      !Array.isArray(v.receipt.targets) ||
      v.receipt.targets.length !== input.targets.length ||
      !v.receipt.targets.every(
        (t) =>
          object(t) &&
          typeof t.taskUUID === "string" &&
          input.targets.some((w) => w.taskUUID === t.taskUUID) &&
          typeof t.archived === "boolean" &&
          integer(t.visibilityVersion, 0),
      ) ||
      new Set(v.receipt.targets.map((t) => t.taskUUID)).size !==
        input.targets.length
    )
      throw new ApiError("invalid_response");
    return v as unknown as { receipt: ArchiveReceipt; replayed: boolean };
  },
};
export const utcSecond = (ms = Date.now()) =>
  new Date(Math.ceil(ms / 1000) * 1000).toISOString().replace(".000Z", "Z");
export const validUTC = (s: string) =>
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,6})?Z$/.test(s) &&
  Number.isFinite(Date.parse(s)) &&
  new Date(Date.parse(s)).toISOString().slice(0, 19) === s.slice(0, 19);
