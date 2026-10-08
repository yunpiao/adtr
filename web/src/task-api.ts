import { ApiError } from "./api";
import { accessMessages, type PageInfo, type Proof } from "./access-api";

export const taskStates = [
  "queued",
  "running",
  "retry_wait",
  "cancel_requested",
  "succeeded",
  "failed",
  "partial_failed",
  "dead_letter",
  "cancelled",
] as const;
export type TaskState = (typeof taskStates)[number];
export const stateLabels: Record<TaskState, string> = {
  queued: "等待执行",
  running: "执行中",
  retry_wait: "等待业务重试",
  cancel_requested: "已请求取消，待确认",
  succeeded: "已成功",
  failed: "已失败",
  partial_failed: "部分失败",
  dead_letter: "死信，自动重试已停止",
  cancelled: "已取消",
};
export const isDomainConnectionTask = (name: unknown) =>
  name === "domain.connection_test" ||
  name === "domain.account_connection_test";
export const terminal = (state: TaskState) =>
  [
    "succeeded",
    "failed",
    "partial_failed",
    "dead_letter",
    "cancelled",
  ].includes(state);
export interface Task {
  taskUUID: string;
  taskName: string;
  domainId: string;
  payloadVersion: number;
  state: TaskState;
  sourceState: string;
  error: string;
  createdAt: string;
  updatedAt: string;
  attempt: number;
  maxAttempts: number;
  progress: number;
  resultVersion: number;
  result: unknown;
  cursor: unknown;
  parentTaskUUID: string;
  nextAttemptAt?: string;
  terminalAt: string | null;
  archived: boolean;
  visibilityVersion: number;
}
export interface TaskEvent {
  id: number;
  action: string;
  state: TaskState;
  attempt: number;
  resultVersion: number;
  createdAt: string;
}
export interface TaskDetail {
  task: Task;
  events: TaskEvent[];
}
export interface TaskList {
  page: PageInfo;
  tasks: Task[];
  exhausted: boolean;
}
export interface TaskKind {
  taskName: string;
  payloadVersion: number;
  scope: string;
  maxAttempts: number;
  timeoutSeconds: number;
}
export interface Submission {
  task: Task;
  replayed: boolean;
}
export interface SubmitInput {
  taskName: string;
  domainId: string;
  payloadVersion: number;
  payload: Record<string, never>;
  idempotencyKey: string;
}
export const taskOperations = [
  "GET /api/tasks",
  "GET /api/tasks/kinds",
  "GET /api/tasks/detail",
  "POST /api/tasks/submit",
  "POST /api/tasks/cancel",
  "POST /api/tasks/recover",
  "GET /api/tasks/schedules",
  "GET /api/tasks/schedules/detail",
  "POST /api/tasks/schedules/create",
  "POST /api/tasks/schedules/enable",
  "POST /api/tasks/schedules/pause",
  "GET /api/tasks/archive-candidates",
  "POST /api/tasks/archive",
  "POST /api/tasks/restore",
] as const;
export type TaskOperation = (typeof taskOperations)[number];

// Only the frozen, safe production kind has an input editor. A future registry
// entry is not permission to invent its payload schema or expose an AD executor.
export const supportedKind = (kind: TaskKind) =>
  kind.taskName === "infrastructure.health" &&
  kind.payloadVersion === 1 &&
  kind.scope === "platform";
export function object(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value);
}
export function integer(
  value: unknown,
  min: number,
  max = Number.MAX_SAFE_INTEGER,
): value is number {
  return (
    typeof value === "number" &&
    Number.isSafeInteger(value) &&
    value >= min &&
    value <= max
  );
}
export function validTask(value: unknown): value is Task {
  if (!object(value)) return false;
  return (
    [
      "taskUUID",
      "taskName",
      "domainId",
      "sourceState",
      "error",
      "createdAt",
      "updatedAt",
      "parentTaskUUID",
    ].every((key) => typeof value[key] === "string") &&
    !!value.taskUUID &&
    taskStates.includes(value.state as TaskState) &&
    integer(value.payloadVersion, 1) &&
    integer(value.attempt, 0) &&
    integer(value.maxAttempts, 1) &&
    integer(value.progress, 0, 100) &&
    integer(value.resultVersion, 0) &&
    typeof value.archived === "boolean" &&
    integer(value.visibilityVersion, 0) &&
    (value.terminalAt === null || typeof value.terminalAt === "string") &&
    "result" in value &&
    "cursor" in value &&
    (value.nextAttemptAt === undefined ||
      typeof value.nextAttemptAt === "string")
  );
}
function assertTask(value: unknown): asserts value is Task {
  if (!validTask(value)) throw new ApiError("invalid_response");
}
export async function taskTransport(
  path: string,
  signal: AbortSignal,
  body?: unknown,
  csrf?: string,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(`/api/tasks${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
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
export const taskAPI = {
  async list(query: string, signal: AbortSignal): Promise<TaskList> {
    const data = await taskTransport(`?${query}`, signal);
    if (
      !object(data) ||
      !object(data.page) ||
      !Array.isArray(data.tasks) ||
      !data.tasks.every(validTask) ||
      typeof data.exhausted !== "boolean" ||
      !integer(data.page.pageIdx, 1) ||
      !integer(data.page.pageSize, 1, 100) ||
      !integer(data.page.total, 0) ||
      !integer(data.page.totalPage, 0)
    )
      throw new ApiError("invalid_response");
    return data as unknown as TaskList;
  },
  async kinds(signal: AbortSignal): Promise<TaskKind[]> {
    const data = await taskTransport("/kinds", signal);
    if (
      !object(data) ||
      !Array.isArray(data.kinds) ||
      !data.kinds.every(
        (kind) =>
          object(kind) &&
          typeof kind.taskName === "string" &&
          typeof kind.scope === "string" &&
          integer(kind.payloadVersion, 1) &&
          integer(kind.maxAttempts, 1) &&
          integer(kind.timeoutSeconds, 1),
      )
    )
      throw new ApiError("invalid_response");
    return data.kinds as TaskKind[];
  },
  async detail(
    id: string,
    signal: AbortSignal,
    visibility = "active",
  ): Promise<TaskDetail> {
    const data = await taskTransport(
      `/detail?${new URLSearchParams({ taskUUID: id, ...(visibility !== "active" ? { visibility } : {}) })}`,
      signal,
    );
    if (
      !object(data) ||
      !Array.isArray(data.events) ||
      !data.events.every(
        (event) =>
          object(event) &&
          integer(event.id, 1) &&
          typeof event.action === "string" &&
          typeof event.createdAt === "string" &&
          taskStates.includes(event.state as TaskState) &&
          integer(event.attempt, 0) &&
          integer(event.resultVersion, 0),
      )
    )
      throw new ApiError("invalid_response");
    assertTask(data.task);
    if (data.task.taskUUID !== id) throw new ApiError("invalid_response");
    return data as unknown as TaskDetail;
  },
  async submit(
    input: SubmitInput,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ): Promise<Submission> {
    const data = await taskTransport(
      "/submit",
      signal,
      { ...input, ...proof },
      csrf,
    );
    if (!object(data) || typeof data.replayed !== "boolean")
      throw new ApiError("invalid_response");
    assertTask(data.task);
    return data as unknown as Submission;
  },
  async cancel(
    id: string,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ): Promise<Task> {
    const data = await taskTransport(
      "/cancel",
      signal,
      { taskUUID: id, ...proof },
      csrf,
    );
    if (!object(data)) throw new ApiError("invalid_response");
    assertTask(data.task);
    if (data.task.taskUUID !== id) throw new ApiError("invalid_response");
    return data.task;
  },
  async recover(
    id: string,
    key: string,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ): Promise<Submission> {
    const data = await taskTransport(
      "/recover",
      signal,
      { taskUUID: id, idempotencyKey: key, ...proof },
      csrf,
    );
    if (!object(data) || typeof data.replayed !== "boolean")
      throw new ApiError("invalid_response");
    assertTask(data.task);
    if (data.task.parentTaskUUID !== id || data.task.taskUUID === id)
      throw new ApiError("invalid_response");
    return data as unknown as Submission;
  },
};
export function taskError(error: unknown): string {
  const specific: Record<string, string> = {
    network:
      "连接中断，操作结果尚未确认。请核对重新读取的服务器状态；重试提交必须使用同一个幂等键及新的验证码。",
    invalid_response:
      "服务器结果无法确认。请刷新任务状态；不要换用新的幂等键重复提交。",
    idempotency_conflict:
      "幂等键已用于不同的任务内容。请核对原任务，不要自动重试。",
    invalid_state: "任务状态已经改变，当前操作不可用。请核对最新服务器状态。",
    task_terminal: "任务已进入终态，不能再取消。请核对服务器返回的实际结果。",
    task_not_recoverable:
      "此任务不能恢复；只有服务器允许的失败或死信任务可创建新的恢复任务。",
    unknown_task_kind: "服务器未登记此任务种类，不能提交。",
    task_archived: "任务已归档，请先恢复可见性，再创建恢复任务。",
    visibility_conflict: "任务可见性已改变，请重新预览并核对版本。",
    task_not_archivable: "仅可归档已结束的平台健康任务；请重新预览。",
    invalid_before: "截止时间必须是有效 UTC 时间，且不能晚于服务器当前时间。",
    control_version_conflict: "计划控制版本已改变，请重新读取后核对。",
    authorization_epoch_changed:
      "计划授权已失效，不能重新启用；请创建新的计划。",
    invalid_start_at: "首次时间须为 UTC 整秒，且至少比服务器当前时间晚 60 秒。",
    unsupported_schedule_kind: "仅支持已登记的平台健康检查周期计划。",
    domain_route_required:
      "域连接检测须通过域连接页面明确提交，不支持通用恢复。",
    audit_route_required:
      "审计导出须通过操作审计页面创建，请在那里重新提交导出条件。",
    operational_log_route_required:
      "运行日志诊断包须通过「运行日志与诊断包」页面提交或取消，并重新核验当前权限。",
  };
  return error instanceof ApiError
    ? (specific[error.code] ??
        accessMessages[error.code] ??
        `任务操作失败（${error.code}），请刷新后核对。`)
    : "任务请求失败，请刷新服务器状态。";
}
