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

export const directoryV2Operations = [
  "GET /api/directory/v2/observation",
  "GET /api/directory/v2/receipt",
  "GET /api/directory/v2/task",
  "POST /api/directory/v2/sync",
  "POST /api/directory/v2/cancel",
] as const;
export type DirectoryV2Operation = (typeof directoryV2Operations)[number];
export type DirectoryV2Kind = "user" | "group" | "computer";
export interface DirectoryV2Object {
  objectGUID: string;
  distinguishedName: string;
  kind: DirectoryV2Kind;
  objectClass: string[];
  samAccountName: string | null;
  userAccountControl: number | null;
  objectSid: string | null;
  mail: string | null;
  description: [string] | null;
  whenCreated: string | null;
}
export interface DirectoryV2Source {
  server_name: string;
  dc_host_name: string;
  domain: string;
  naming_context: string;
  started_at: string;
  completed_at: string;
  elapsed_milliseconds: number;
  pages: number;
}
export interface DirectoryV2Observation {
  dictionaryVersion: 2;
  available: boolean;
  observationId?: string;
  source?: DirectoryV2Source;
  list: DirectoryV2Object[];
  page: PageInfo;
}
export interface DirectoryV2Query {
  domainId: string;
  observationId?: string;
  kind: "" | DirectoryV2Kind;
  pageIdx: number;
  pageSize: number;
}
export interface DirectoryV2Input {
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
export const validDirectoryV2Input = (v: unknown): v is DirectoryV2Input =>
  object(v) &&
  validDomainID(v.domainId) &&
  validRevision(v.expectedRevision) &&
  validRevision(v.expectedCredentialGeneration) &&
  typeof v.idempotencyKey === "string" &&
  /^[A-Za-z0-9_-]{8,128}$/u.test(v.idempotencyKey);
// Supplemental text preserves valid UTF-8, whitespace, NUL and non-BMP text.
// JavaScript length counts the same UTF-16 units as the fixed LDAP profile.
const rawText = (
  value: unknown,
  bytes: number,
  units: number,
): value is string =>
  typeof value === "string" &&
  value.length > 0 &&
  value.length <= units &&
  !/[\ud800-\udfff]/u.test(value) &&
  new TextEncoder().encode(value).length <= bytes;
function validSID(value: unknown): value is string {
  if (
    typeof value !== "string" ||
    value.length > 73 ||
    !/^S-1-(?:0|[1-9][0-9]{0,9}|0x[0-9a-f]{12})(?:-(?:0|[1-9][0-9]{0,9})){1,5}$/u.test(
      value,
    )
  )
    return false;
  const [, , authority, ...parts] = value.split("-");
  if (
    authority.startsWith("0x")
      ? BigInt(authority) < 4294967296n
      : BigInt(authority) > 4294967295n
  )
    return false;
  return parts.every((part) => BigInt(part) <= 4294967295n);
}
function validObject(v: unknown): v is DirectoryV2Object {
  return (
    object(v) &&
    exact(v, [
      "objectGUID",
      "distinguishedName",
      "kind",
      "objectClass",
      "samAccountName",
      "userAccountControl",
      "objectSid",
      "mail",
      "description",
      "whenCreated",
    ]) &&
    typeof v.objectGUID === "string" &&
    /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/u.test(v.objectGUID) &&
    text(v.distinguishedName, 4096) &&
    ["user", "group", "computer"].includes(String(v.kind)) &&
    Array.isArray(v.objectClass) &&
    v.objectClass.length > 0 &&
    v.objectClass.length <= 32 &&
    v.objectClass.every(
      (c, i, all) =>
        typeof c === "string" &&
        /^[a-z0-9.-]{1,64}$/u.test(c) &&
        (i === 0 || all[i - 1] < c),
    ) &&
    !(
      v.objectClass.includes("group") &&
      (v.objectClass.includes("user") || v.objectClass.includes("computer"))
    ) &&
    v.kind ===
      (v.objectClass.includes("computer")
        ? "computer"
        : v.objectClass.includes("group")
          ? "group"
          : v.objectClass.includes("user")
            ? "user"
            : "") &&
    (v.samAccountName === null ||
      (text(v.samAccountName, 1024) &&
        Array.from(v.samAccountName).length <= 256)) &&
    (v.userAccountControl === null ||
      integer(v.userAccountControl, 0, 4294967295)) &&
    (v.objectSid === null || validSID(v.objectSid)) &&
    (v.mail === null || rawText(v.mail, 1024, 256)) &&
    (v.description === null ||
      (Array.isArray(v.description) &&
        v.description.length === 1 &&
        rawText(v.description[0], 4096, 1024))) &&
    (v.whenCreated === null ||
      (typeof v.whenCreated === "string" &&
        /^(?!0000)\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/u.test(v.whenCreated) &&
        utc(v.whenCreated)))
  );
}
function validSource(v: unknown): v is DirectoryV2Source {
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
export function parseDirectoryV2Observation(
  v: unknown,
  q: DirectoryV2Query,
): DirectoryV2Observation {
  if (object(v) && integer(v.dictionaryVersion, 1) && v.dictionaryVersion !== 2)
    throw new ApiError("unsupported_directory_profile");
  if (
    !object(v) ||
    v.dictionaryVersion !== 2 ||
    typeof v.available !== "boolean" ||
    !exact(
      v,
      v.available
        ? [
            "dictionaryVersion",
            "available",
            "observationId",
            "source",
            "list",
            "page",
          ]
        : ["dictionaryVersion", "available", "list", "page"],
    ) ||
    !object(v.page) ||
    !exact(v.page, ["pageIdx", "pageSize", "total", "totalPage"]) ||
    v.page.pageIdx !== q.pageIdx ||
    v.page.pageSize !== q.pageSize ||
    !integer(v.page.total, 0, 10000) ||
    !integer(v.page.totalPage, 0) ||
    v.page.totalPage !== Math.ceil(v.page.total / q.pageSize) ||
    !Array.isArray(v.list) ||
    !v.list.every(validObject) ||
    v.list.length !==
      Math.min(
        q.pageSize,
        Math.max(0, v.page.total - (q.pageIdx - 1) * q.pageSize),
      )
  )
    return invalid();
  if (!v.available) {
    if (q.observationId || v.list.length || v.page.total !== 0)
      return invalid();
  } else {
    if (
      !validDomainID(v.observationId) ||
      !validSource(v.source) ||
      (q.observationId && q.observationId !== v.observationId)
    )
      return invalid();
  }
  const rows = v.list as DirectoryV2Object[];
  if (
    rows.some(
      (r, i) =>
        (q.kind && r.kind !== q.kind) ||
        (i > 0 && rows[i - 1].objectGUID >= r.objectGUID),
    )
  )
    return invalid();
  return v as unknown as DirectoryV2Observation;
}
export function parseDirectoryV2Task(
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
    v.taskName !== "domain.directory_read.v2" ||
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
    response = await fetch(`/api/directory/v2${path}`, {
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
  return parseDirectoryV2Task(v.task, domain, id);
};
export const directoryV2API = {
  async observation(q: DirectoryV2Query, actor: number, signal: AbortSignal) {
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
    return parseDirectoryV2Observation(
      await transport(`/observation?${query}`, actor, signal),
      q,
    );
  },
  async sync(
    input: DirectoryV2Input,
    proof: Proof,
    csrf: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<Submission> {
    if (!validDirectoryV2Input(input)) throw new ApiError("invalid_input");
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
      task: parseDirectoryV2Task(value.task, input.domainId),
      replayed: value.replayed,
    };
  },
  async receipt(input: DirectoryV2Input, actor: number, signal: AbortSignal) {
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
export const uncertainDirectoryV2Error = (e: unknown) =>
  !(e instanceof ApiError) ||
  ["network", "invalid_response"].includes(e.code) ||
  (e.code === "directory_limit_exceeded" &&
    e.status >= 200 &&
    e.status < 300) ||
  (e.status >= 500 && e.code !== "directory_read_disabled");
const errors: Record<string, string> = {
  directory_limit_exceeded: "目录结果超过安全读取上限，未展示部分结果。",
  unsupported_directory_profile: "此目录字段或版本不受当前补充目录配置支持。",
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
    "尚未获得 domain.directory_read.v2 用途的显式凭据使用授权。",
  revision_conflict: "配置或凭据版本已改变，请重新选择并核对数据源。",
  idempotency_conflict:
    "原操作编号与提交内容冲突，请核对原回执，不能更换编号重复提交。",
  network: "连接中断，结果尚未确认。请查询原同步回执后再决定是否重试。",
  invalid_response: "服务器结果无法安全确认，请重新查询原回执或状态。",
  not_found: "记录不存在或当前无权访问。未找到回执不能证明提交失败。",
};
export function directoryV2Error(e: unknown) {
  return e instanceof ApiError
    ? Object.hasOwn(errors, e.code)
      ? errors[e.code]
      : Object.hasOwn(accessMessages, e.code)
        ? accessMessages[e.code]
        : "目录请求失败，请重新核对服务器状态。"
    : "目录请求失败，请重试读取。";
}

// Shared factual validators; existing directory parsers retain their contracts.
export {
  validObject as validDirectoryV2Object,
  validSource as validDirectoryV2Source,
};
