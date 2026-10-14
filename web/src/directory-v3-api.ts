import {
  validDirectoryV3Object,
  type DirectoryV3Object,
} from "./directory-v3-facts";
import { ApiError } from "./api";
import { readDirectoryV2JSON } from "./directory-v2-json";
import { accessMessages, type PageInfo, type Proof } from "./access-api";
import { validDNS, validDomainID, validRevision } from "./domain-api";
import {
  integer,
  object,
  validTask,
  type Task,
  type Submission,
} from "./task-api";

export const directoryV3Operations = [
  "GET /api/directory/v3/observation",
  "GET /api/directory/v3/receipt",
  "GET /api/directory/v3/task",
  "POST /api/directory/v3/sync",
  "POST /api/directory/v3/cancel",
] as const;
export type DirectoryV3Operation = (typeof directoryV3Operations)[number];
export type DirectoryV3Kind = "user" | "group" | "computer";
export type { DirectoryV3Object } from "./directory-v3-facts";
export interface DirectoryV3Source {
  server_name: string;
  dc_host_name: string;
  domain: string;
  naming_context: string;
  started_at: string;
  completed_at: string;
  elapsed_milliseconds: number;
  pages: number;
}
export interface DirectoryV3ObservedCounts {
  user: number;
  group: number;
  computer: number;
}
export interface DirectoryV3Observation {
  dictionaryVersion: 3;
  available: boolean;
  observedCounts: DirectoryV3ObservedCounts | null;
  observationId?: string;
  source?: DirectoryV3Source;
  list: DirectoryV3Object[];
  page: PageInfo;
}
export interface DirectoryV3Query {
  domainId: string;
  observationId?: string;
  kind: "" | DirectoryV3Kind;
  pageIdx: number;
  pageSize: number;
}
export interface DirectoryV3Input {
  domainId: string;
  expectedRevision: string;
  expectedCredentialGeneration: string;
  idempotencyKey: string;
}
const exact = (v: Record<string, unknown>, keys: string[]) =>
  Object.keys(v).length === keys.length &&
  Object.keys(v).every((k) => keys.includes(k));
const invalid = (): never => {
  throw new ApiError("invalid_response");
};
const text = (v: unknown, max: number): v is string =>
  typeof v === "string" &&
  v.length > 0 &&
  new TextEncoder().encode(v).length <= max &&
  !/[\p{Cc}\ud800-\udfff]/u.test(v);
const utc = (v: unknown): v is string =>
  typeof v === "string" &&
  /^(?!0000)\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/u.test(v) &&
  Number.isFinite(Date.parse(v)) &&
  new Date(v).toISOString().slice(0, 19) === v.slice(0, 19);
export const validDirectoryV3Input = (v: unknown): v is DirectoryV3Input =>
  object(v) &&
  validDomainID(v.domainId) &&
  validRevision(v.expectedRevision) &&
  validRevision(v.expectedCredentialGeneration) &&
  typeof v.idempotencyKey === "string" &&
  /^[A-Za-z0-9_-]{8,128}$/u.test(v.idempotencyKey);
function validSource(v: unknown): v is DirectoryV3Source {
  return (
    object(v) &&
    exact(v, [
      "server_name",
      "dc_host_name",
      "domain",
      "naming_context",
      "started_at",
      "completed_at",
      "elapsed_milliseconds",
      "pages",
    ]) &&
    text(v.server_name, 4096) &&
    typeof v.dc_host_name === "string" &&
    validDNS(v.dc_host_name) &&
    typeof v.domain === "string" &&
    validDNS(v.domain) &&
    text(v.naming_context, 4096) &&
    utc(v.started_at) &&
    utc(v.completed_at) &&
    Date.parse(v.completed_at) >= Date.parse(v.started_at) &&
    integer(v.elapsed_milliseconds, 0) &&
    integer(v.pages, 1, 100)
  );
}
export function parseDirectoryV3Observation(
  v: unknown,
  q: DirectoryV3Query,
): DirectoryV3Observation {
  if (object(v) && integer(v.dictionaryVersion, 1) && v.dictionaryVersion !== 3)
    throw new ApiError("unsupported_directory_profile");
  if (
    !object(v) ||
    v.dictionaryVersion !== 3 ||
    typeof v.available !== "boolean" ||
    !exact(
      v,
      v.available
        ? [
            "dictionaryVersion",
            "available",
            "observationId",
            "source",
            "observedCounts",
            "list",
            "page",
          ]
        : ["dictionaryVersion", "available", "observedCounts", "list", "page"],
    ) ||
    !object(v.page) ||
    !exact(v.page, ["pageIdx", "pageSize", "total", "totalPage"]) ||
    v.page.pageIdx !== q.pageIdx ||
    v.page.pageSize !== q.pageSize ||
    !integer(v.page.total, 0, 10000) ||
    !integer(v.page.totalPage, 0) ||
    v.page.totalPage !== Math.ceil(v.page.total / q.pageSize) ||
    !Array.isArray(v.list) ||
    !v.list.every(validDirectoryV3Object) ||
    v.list.length !==
      Math.min(
        q.pageSize,
        Math.max(0, v.page.total - (q.pageIdx - 1) * q.pageSize),
      )
  )
    return invalid();
  if (!v.available) {
    if (
      q.observationId ||
      v.observedCounts !== null ||
      v.list.length ||
      v.page.total !== 0
    )
      return invalid();
  } else {
    if (
      !validDomainID(v.observationId) ||
      !validSource(v.source) ||
      !object(v.observedCounts) ||
      !exact(v.observedCounts, ["user", "group", "computer"]) ||
      !integer(v.observedCounts.user, 0, 10000) ||
      !integer(v.observedCounts.group, 0, 10000) ||
      !integer(v.observedCounts.computer, 0, 10000) ||
      v.observedCounts.user + v.observedCounts.group + v.observedCounts.computer >
        10000 ||
      v.page.total !==
        (q.kind
          ? v.observedCounts[q.kind]
          : v.observedCounts.user +
            v.observedCounts.group +
            v.observedCounts.computer) ||
      (q.observationId && q.observationId !== v.observationId)
    )
      return invalid();
  }
  const rows = v.list as DirectoryV3Object[];
  if (
    rows.some(
      (r, i) =>
        (q.kind && r.kind !== q.kind) ||
        (i > 0 && rows[i - 1].objectGUID >= r.objectGUID),
    )
  )
    return invalid();
  if (v.available) {
    const counts = v.observedCounts as unknown as DirectoryV3ObservedCounts;
    // Even an all-kind partial page cannot contain more of a kind than the
    // validated full observation reports.
    for (const kind of ["user", "group", "computer"] as const)
      if (rows.filter((r) => r.kind === kind).length > counts[kind])
        return invalid();
  }
  return v as unknown as DirectoryV3Observation;
}
export function parseDirectoryV3Task(
  v: unknown,
  domainId: string,
  taskUUID?: string,
): Task {
  if (
    !object(v) ||
    !validTask(v) ||
    !exact(v, [
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
      ...(v.nextAttemptAt === undefined ? [] : ["nextAttemptAt"]),
    ]) ||
    !utc(v.createdAt) ||
    !utc(v.updatedAt) ||
    (v.terminalAt !== null && !utc(v.terminalAt)) ||
    (v.nextAttemptAt !== undefined && !utc(v.nextAttemptAt)) ||
    v.taskName !== "domain.directory_read.v3" ||
    v.domainId !== domainId ||
    !validDomainID(v.taskUUID) ||
    (taskUUID && v.taskUUID !== taskUUID) ||
    v.payloadVersion !== 1 ||
    v.maxAttempts !== 1 ||
    v.parentTaskUUID !== "" ||
    !object(v.result) ||
    Object.keys(v.result).length ||
    !object(v.cursor) ||
    Object.keys(v.cursor).length
  )
    return invalid();
  return v;
}
async function transport(
  path: string,
  actor: number,
  signal: AbortSignal,
  body?: unknown,
  csrf?: string,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(`/api/directory/v3${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
      signal,
      ...(body === undefined
        ? {}
        : {
            headers: {
              "Content-Type": "application/json",
              "X-CSRF-Token": csrf ?? "",
            },
            body: JSON.stringify(body),
          }),
    });
  } catch (error) {
    if (signal.aborted) throw error;
    throw new ApiError("network");
  }
  if (
    response.ok &&
    (!integer(actor, 1) ||
      response.headers.get("X-ADTR-User-ID") !== String(actor))
  ) {
    await response.body?.cancel().catch(() => undefined);
    throw new ApiError("unauthenticated", 401);
  }
  const value = await readDirectoryV2JSON(response, signal);
  signal.throwIfAborted();
  if (!response.ok)
    throw new ApiError(
      object(value) &&
      exact(value, ["error"]) &&
      typeof value.error === "string"
        ? value.error
        : "internal",
      response.status,
    );
  return value;
}
const wrapTask = (v: unknown, domain: string, id?: string) => {
  if (!object(v) || !exact(v, ["task"])) return invalid();
  return parseDirectoryV3Task(v.task, domain, id);
};
export const directoryV3API = {
  async observation(q: DirectoryV3Query, actor: number, signal: AbortSignal) {
    if (
      !validDomainID(q.domainId) ||
      !integer(q.pageIdx, 1, 10000) ||
      ![25, 50, 100].includes(q.pageSize) ||
      !["", "user", "group", "computer"].includes(q.kind) ||
      (q.observationId !== undefined && !validDomainID(q.observationId)) ||
      (q.pageIdx > 1 && !q.observationId)
    )
      throw new ApiError("invalid_input");
    const query = new URLSearchParams({
      domainId: q.domainId,
      pageIdx: String(q.pageIdx),
      pageSize: String(q.pageSize),
      ...(q.kind ? { kind: q.kind } : {}),
      ...(q.observationId ? { observationId: q.observationId } : {}),
    });
    return parseDirectoryV3Observation(
      await transport(`/observation?${query}`, actor, signal),
      q,
    );
  },
  async sync(
    input: DirectoryV3Input,
    proof: Proof,
    csrf: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<Submission> {
    if (!validDirectoryV3Input(input)) throw new ApiError("invalid_input");
    const value = await transport(
      "/sync",
      actor,
      signal,
      {
        domainId: input.domainId,
        expectedRevision: input.expectedRevision,
        expectedCredentialGeneration: input.expectedCredentialGeneration,
        idempotencyKey: input.idempotencyKey,
        actorPassword: proof.actorPassword,
        totpCode: proof.totpCode,
      },
      csrf,
    );
    if (
      !object(value) ||
      !exact(value, ["task", "replayed"]) ||
      typeof value.replayed !== "boolean"
    )
      return invalid();
    return {
      task: parseDirectoryV3Task(value.task, input.domainId),
      replayed: value.replayed,
    };
  },
  async receipt(input: DirectoryV3Input, actor: number, signal: AbortSignal) {
    return wrapTask(
      await transport(
        `/receipt?${new URLSearchParams({ domainId: input.domainId, idempotencyKey: input.idempotencyKey })}`,
        actor,
        signal,
      ),
      input.domainId,
    );
  },
  async task(domain: string, id: string, actor: number, signal: AbortSignal) {
    return wrapTask(
      await transport(
        `/task?${new URLSearchParams({ taskUUID: id })}`,
        actor,
        signal,
      ),
      domain,
      id,
    );
  },
  async cancel(
    domain: string,
    id: string,
    proof: Proof,
    csrf: string,
    actor: number,
    signal: AbortSignal,
  ) {
    return wrapTask(
      await transport(
        "/cancel",
        actor,
        signal,
        {
          taskUUID: id,
          actorPassword: proof.actorPassword,
          totpCode: proof.totpCode,
        },
        csrf,
      ),
      domain,
      id,
    );
  },
};
export const uncertainDirectoryV3Error = (e: unknown) =>
  !(e instanceof ApiError) ||
  ["network", "invalid_response"].includes(e.code) ||
  (e.code === "directory_limit_exceeded" &&
    e.status >= 200 &&
    e.status < 300) ||
  (e.status >= 500 && e.code !== "directory_read_disabled");
const errors: Record<string, string> = {
  directory_limit_exceeded: "目录结果超过安全读取上限，未展示部分结果。",
  unsupported_directory_profile:
    "此目录字段或版本不受当前字典 3 目录配置支持。",
  invalid_directory_response:
    "目录响应格式不符合当前字段约定，未展示部分结果。",
  directory_read_disabled:
    "当前部署未开启目录读取。已编译消费者不代表部署开关已启用。",
  directory_account_required:
    "同步要求域已绑定操作账户，请先在域连接中配置账户来源。",
  directory_observation_unavailable:
    "原目录观察已不可用。请明确刷新目录观察，从第一页重新读取。",
  directory_use_changed: "目录读取授权或账户绑定已改变，请重新核对配置与授权。",
  credential_use_denied:
    "尚未获得 domain.directory_read.v3 用途的显式凭据使用授权。",
  revision_conflict: "配置或凭据版本已改变，请重新选择并核对数据源。",
  idempotency_conflict:
    "原操作编号与提交内容冲突，请核对原回执，不能更换编号重复提交。",
  network: "连接中断，结果尚未确认。请查询原同步回执后再决定是否重试。",
  invalid_response: "服务器结果无法安全确认，请重新查询原回执或状态。",
  not_found: "记录不存在或当前无权访问。未找到回执不能证明提交失败。",
};
export function directoryV3Error(e: unknown) {
  return e instanceof ApiError
    ? Object.hasOwn(errors, e.code)
      ? errors[e.code]
      : Object.hasOwn(accessMessages, e.code)
        ? accessMessages[e.code]
        : "目录请求失败，请重新核对服务器状态。"
    : "目录请求失败，请重试读取。";
}
