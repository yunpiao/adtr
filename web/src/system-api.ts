import { ApiError } from "./api";
import { accessMessages, type Proof } from "./access-api";

export const systemOperations = [
  "GET /api/system/info",
  "GET /api/system/nodes",
  "GET /api/system/resources/current",
  "GET /api/system/resources/history",
  "GET /api/system/storage",
  "GET /api/system/services",
  "GET /api/system/health",
  "POST /api/system/storage/settings",
] as const;
export type SystemOperation = (typeof systemOperations)[number];
export type Availability =
  | "available"
  | "unavailable"
  | "warming_up"
  | "unsupported";
export type GraphType = "cpu_basic" | "ram_basic" | "disk_usage";
export interface Metadata {
  observedAt: string;
  source: string;
  scope: string;
  availability: Availability;
  reason?: string;
}
export interface PercentMetric extends Metadata {
  percent: number | null;
  intervalStart?: string;
}
export interface MemoryMetric extends PercentMetric {
  totalBytes: string | null;
  usedBytes: string | null;
  availableBytes: string | null;
}
export interface StorageMetric extends PercentMetric {
  id: string;
  mount: string;
  fs: string;
  totalBytes: string | null;
  usedBytes: string | null;
  freeBytes: string | null;
  reservedBytes: string | null;
}
export interface Snapshot {
  instance: string;
  observedAt: string;
  cpu: PercentMetric;
  memory: MemoryMetric;
  uptime: Metadata & { seconds: number | null };
  load: Metadata & {
    values: { one: number; five: number; fifteen: number } | null;
  };
  storage: StorageMetric[];
}
export interface Current {
  instance: string;
  snapshot: Snapshot | null;
  availability: Availability;
  reason?: string;
  stale: boolean;
  availableSince: string | null;
  checkedAt: string;
}
export interface StorageItem extends StorageMetric {
  alarmPercent: number;
  revision: number;
  alarmExceeded: boolean | null;
}
export interface StorageView {
  instance: string;
  storage: StorageItem[];
  total: number;
  page: number;
  pageSize: number;
  availability: Availability;
  reason?: string;
  stale: boolean;
  observedAt: string | null;
  checkedAt: string;
}
export interface HistoryInput {
  instance: string;
  graphType: GraphType;
  startTime: number;
  endTime: number;
  storageId?: string;
}
export interface History {
  instance: string;
  graphType: GraphType;
  info: {
    mode: string;
    data: {
      timestamp: number[];
      value: string[];
      dataStatistics: Record<"max" | "avg" | "min" | "current", number | null>;
    };
  }[];
  gaps: { startTime: number; endTime: number }[];
  availableSince: string | null;
  sampleIntervalSeconds: number;
}
export interface AlarmInput {
  instance: string;
  storageId: string;
  setType: "alarm";
  percent: number;
  expectedRevision: number;
}
export interface AlarmSetting {
  instance: string;
  storageId: string;
  alarmPercent: number;
  revision: number;
  updatedAt: string | null;
}

const object = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const integer = (
  v: unknown,
  min = 0,
  max = Number.MAX_SAFE_INTEGER,
): v is number =>
  typeof v === "number" && Number.isSafeInteger(v) && v >= min && v <= max;
const number = (v: unknown): v is number =>
  typeof v === "number" && Number.isFinite(v) && v >= 0;
const percent = (v: unknown) => v === null || (number(v) && v <= 100);
const bytes = (v: unknown) =>
  v === null || (typeof v === "string" && /^(0|[1-9][0-9]*)$/.test(v));
const date = (v: unknown): v is string =>
  typeof v === "string" && Number.isFinite(Date.parse(v));
const nullableDate = (v: unknown) => v === null || date(v);
const available = (v: unknown) =>
  ["available", "unavailable", "warming_up", "unsupported"].includes(
    v as string,
  );
const meta = (v: unknown): v is Metadata & Record<string, unknown> =>
  object(v) &&
  date(v.observedAt) &&
  typeof v.source === "string" &&
  typeof v.scope === "string" &&
  available(v.availability) &&
  (v.reason === undefined || typeof v.reason === "string");
const measured = (v: unknown): v is PercentMetric & Record<string, unknown> =>
  meta(v) &&
  percent(v.percent) &&
  (v.availability !== "available" ? v.percent === null : v.percent !== null);
const storage = (v: unknown): v is StorageMetric =>
  measured(v) &&
  ["id", "mount", "fs"].every((k) => typeof v[k] === "string") &&
  ["totalBytes", "usedBytes", "freeBytes", "reservedBytes"].every((k) =>
    bytes(v[k]),
  );
const validSnapshot = (v: unknown): v is Snapshot =>
  object(v) &&
  v.instance === "local-api" &&
  date(v.observedAt) &&
  measured(v.cpu) &&
  (v.cpu.intervalStart === undefined || date(v.cpu.intervalStart)) &&
  measured(v.memory) &&
  ["totalBytes", "usedBytes", "availableBytes"].every((k) =>
    bytes((v.memory as unknown as Record<string, unknown>)[k]),
  ) &&
  meta(v.uptime) &&
  (v.uptime.seconds === null || integer(v.uptime.seconds)) &&
  meta(v.load) &&
  (v.load.values === null ||
    (object(v.load.values) &&
      ["one", "five", "fifteen"].every((k) =>
        number(
          (v.load as Record<string, unknown>).values &&
            (
              (v.load as Record<string, unknown>).values as Record<
                string,
                unknown
              >
            )[k],
        ),
      ))) &&
  Array.isArray(v.storage) &&
  v.storage.every(storage);
export const validCurrent = (v: unknown): v is Current =>
  object(v) &&
  v.instance === "local-api" &&
  (v.snapshot === null || validSnapshot(v.snapshot)) &&
  available(v.availability) &&
  typeof v.stale === "boolean" &&
  nullableDate(v.availableSince) &&
  date(v.checkedAt);
export const validStorage = (v: unknown): v is StorageView =>
  object(v) &&
  v.instance === "local-api" &&
  Array.isArray(v.storage) &&
  v.storage.length <= 50 &&
  v.storage.every(
    (x) =>
      storage(x) &&
      object(x) &&
      integer(x.alarmPercent, 85, 90) &&
      integer(x.revision) &&
      (x.alarmExceeded === null || typeof x.alarmExceeded === "boolean"),
  ) &&
  integer(v.total, 0, 32) &&
  integer(v.page, 1) &&
  integer(v.pageSize, 1, 50) &&
  typeof v.stale === "boolean" &&
  available(v.availability) &&
  nullableDate(v.observedAt) &&
  date(v.checkedAt);
export function validHistory(v: unknown, query: HistoryInput): v is History {
  if (
    !object(v) ||
    v.instance !== query.instance ||
    v.graphType !== query.graphType ||
    !nullableDate(v.availableSince) ||
    !integer(v.sampleIntervalSeconds, 1) ||
    !Array.isArray(v.gaps) ||
    !v.gaps.every(
      (g) =>
        object(g) &&
        integer(g.startTime, query.startTime, query.endTime) &&
        integer(g.endTime, g.startTime as number, query.endTime),
    ) ||
    !Array.isArray(v.info) ||
    v.info.length !== 1
  )
    return false;
  return v.info.every((s) => {
    if (!object(s) || s.mode !== query.graphType || !object(s.data))
      return false;
    const { timestamp, value, dataStatistics } = s.data;
    if (
      !Array.isArray(timestamp) ||
      timestamp.length > 5760 ||
      !timestamp.every(
        (t, i, a) =>
          integer(t, query.startTime, query.endTime - 1) &&
          (i === 0 || t >= a[i - 1]),
      ) ||
      !Array.isArray(value) ||
      value.length !== timestamp.length ||
      !value.every(
        (n) =>
          typeof n === "string" &&
          /^(0|[1-9][0-9]*)(\.[0-9]+)?$/.test(n) &&
          percent(Number(n)),
      ) ||
      !object(dataStatistics)
    )
      return false;
    return ["max", "min", "avg", "current"].every((k) =>
      value.length
        ? dataStatistics[k] !== null && percent(dataStatistics[k])
        : dataStatistics[k] === null,
    );
  });
}
export interface SystemInfo {
  status: {
    memoryUsagePercent: number | null;
    cpuUsagePercent: number | null;
    diskUsagePercent: number | null;
    bootTime: number | null;
    LoadAverage: { one: number; five: number; fifteen: number } | null;
  };
  version: {
    engineVersion: string | null;
    majorVersion: string | null;
    engineVersionTimestamp: number | null;
    majorVersionTimestamp: number | null;
    OSPlatform: string;
    OSVersion: string | null;
    goVersion: string;
    revision: string | null;
    modified: boolean | null;
  };
  basic: {
    ip: string | null;
    systemName: string;
    companyName: string | null;
    officialWebsite: string | null;
  };
  upgrade: {
    upgradeEngineVersion: string | null;
    upgradeMajorVersion: string | null;
  };
  systemCurrentTime: string;
  current: Current;
  capabilities: Record<string, string>;
}
export interface SystemNode {
  instance: string;
  hostName: string | null;
  ip: string | null;
  scope: string;
  availability: Availability;
}
export interface Dependency {
  id: string;
  name: string;
  type: string;
  status: "healthy" | "unhealthy" | "unknown" | "not_configured";
  reason: string;
  checkedAt: string;
  observedAt: string | null;
  source: string;
  scope: string;
}
export interface WorkerCycle {
  workerId: string;
  cycle: "queue" | "recovery" | "scheduler";
  status: "success" | "failure" | "progress";
  evidence: string;
  lastActivityAt: string | null;
  lastSuccessAt: string | null;
  availability: Availability;
  reason?: string;
}
export interface SystemHealth {
  result: "healthy" | "degraded";
  checkedAt: string;
  dependencies: Dependency[];
  worker: {
    cycles: WorkerCycle[];
    availability: Availability;
    reason?: string;
    checkedAt: string;
    staleAfterSeconds: number;
  };
}
const nullableString = (v: unknown) => v === null || typeof v === "string";
const validInfo = (v: unknown): v is SystemInfo =>
  object(v) &&
  object(v.status) &&
  ["memoryUsagePercent", "cpuUsagePercent", "diskUsagePercent"].every((k) =>
    percent((v.status as Record<string, unknown>)[k]),
  ) &&
  (v.status.bootTime === null || integer(v.status.bootTime)) &&
  object(v.version) &&
  ["engineVersion", "majorVersion", "OSVersion", "revision"].every((k) =>
    nullableString((v.version as Record<string, unknown>)[k]),
  ) &&
  typeof v.version.OSPlatform === "string" &&
  typeof v.version.goVersion === "string" &&
  object(v.basic) &&
  typeof v.basic.systemName === "string" &&
  ["ip", "companyName", "officialWebsite"].every((k) =>
    nullableString((v.basic as Record<string, unknown>)[k]),
  ) &&
  object(v.upgrade) &&
  nullableString(v.upgrade.upgradeEngineVersion) &&
  nullableString(v.upgrade.upgradeMajorVersion) &&
  date(v.systemCurrentTime) &&
  validCurrent(v.current) &&
  object(v.capabilities) &&
  Object.values(v.capabilities).every((x) => typeof x === "string");
const validNodes = (v: unknown): v is { nodeList: SystemNode[] } =>
  object(v) &&
  Array.isArray(v.nodeList) &&
  v.nodeList.length === 1 &&
  v.nodeList.every(
    (n) =>
      object(n) &&
      n.instance === "local-api" &&
      nullableString(n.hostName) &&
      nullableString(n.ip) &&
      n.scope === "kernel_visible" &&
      available(n.availability),
  );
const validHealth = (v: unknown): v is SystemHealth =>
  object(v) &&
  ["healthy", "degraded"].includes(v.result as string) &&
  date(v.checkedAt) &&
  Array.isArray(v.dependencies) &&
  v.dependencies.every(
    (d) =>
      object(d) &&
      ["id", "name", "type", "reason", "source", "scope"].every(
        (k) => typeof d[k] === "string",
      ) &&
      ["healthy", "unhealthy", "unknown", "not_configured"].includes(
        d.status as string,
      ) &&
      date(d.checkedAt) &&
      nullableDate(d.observedAt),
  ) &&
  object(v.worker) &&
  available(v.worker.availability) &&
  date(v.worker.checkedAt) &&
  integer(v.worker.staleAfterSeconds, 1) &&
  Array.isArray(v.worker.cycles) &&
  v.worker.cycles.every(
    (c) =>
      object(c) &&
      typeof c.workerId === "string" &&
      ["queue", "recovery", "scheduler"].includes(c.cycle as string) &&
      ["success", "failure", "progress"].includes(c.status as string) &&
      typeof c.evidence === "string" &&
      available(c.availability) &&
      nullableDate(c.lastActivityAt) &&
      nullableDate(c.lastSuccessAt),
  );

async function transport(
  path: string,
  signal: AbortSignal,
  body?: unknown,
  csrf?: string,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(`/api/system${path}`, {
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
function checked<T>(
  value: unknown,
  validate: (value: unknown) => value is T,
): T {
  if (!validate(value)) throw new ApiError("invalid_response");
  return value;
}
export const systemAPI = {
  async info(signal: AbortSignal) {
    return checked(await transport("/info", signal), validInfo);
  },
  async nodes(signal: AbortSignal) {
    return checked(await transport("/nodes", signal), validNodes);
  },
  async services(
    type: "all" | "service" | "port" | "engine",
    signal: AbortSignal,
  ) {
    return checked(
      await transport(`/services?type=${type}`, signal),
      validHealth,
    );
  },
  async health(signal: AbortSignal) {
    return checked(await transport("/health", signal), validHealth);
  },
  async current(signal: AbortSignal) {
    return checked(
      await transport("/resources/current?instance=local-api", signal),
      validCurrent,
    );
  },
  async storage(page: number, pageSize: number, signal: AbortSignal) {
    return checked(
      await transport(
        `/storage?${new URLSearchParams({ instance: "local-api", page: String(page), pageSize: String(pageSize) })}`,
        signal,
      ),
      validStorage,
    );
  },
  async history(query: HistoryInput, signal: AbortSignal) {
    const params = new URLSearchParams({
      instance: query.instance,
      graphType: query.graphType,
      startTime: String(query.startTime),
      endTime: String(query.endTime),
      ...(query.storageId ? { storageId: query.storageId } : {}),
    });
    return checked(
      await transport(`/resources/history?${params}`, signal),
      (v): v is History => validHistory(v, query),
    );
  },
  async settings(
    input: AlarmInput,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ) {
    const value = await transport(
      "/storage/settings",
      signal,
      { ...input, ...proof },
      csrf,
    );
    if (!object(value) || value.result !== 1)
      throw new ApiError("invalid_response");
    return checked(
      value.setting,
      (v): v is AlarmSetting =>
        object(v) &&
        v.instance === input.instance &&
        v.storageId === input.storageId &&
        v.alarmPercent === input.percent &&
        integer(v.revision, 1) &&
        v.revision === input.expectedRevision + 1 &&
        date(v.updatedAt),
    );
  },
};
export function systemError(error: unknown) {
  const messages: Record<string, string> = {
    network:
      "连接中断，结果尚未确认。已清除旧数据显示；修改阈值前请重新读取服务器版本。",
    invalid_response: "服务器结果无法确认。修改阈值前请重新读取服务器版本。",
    revision_conflict:
      "存储设置已被其他操作修改。请重新读取最新阈值与版本，再决定是否提交。",
    forbidden: "当前账户没有此系统健康操作权限，或权限已被撤销。",
    database_unavailable:
      "数据库暂不可用，无法确认会话与当前健康状态。旧数据已清除，请恢复连接后重新读取。",
    service_unavailable: "服务暂不可用，旧数据已清除，请稍后重新读取。",
  };
  return error instanceof ApiError
    ? (messages[error.code] ??
        (error.status >= 500
          ? messages.service_unavailable
          : (accessMessages[error.code] ??
            `系统健康请求失败（${error.code}），请重新读取。`)))
    : "系统健康请求失败，请重新读取服务器状态。";
}
