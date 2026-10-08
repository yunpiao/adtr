import { ApiError } from "./api";
import { accessMessages, type PageInfo, type Proof } from "./access-api";
import {
  integer,
  object,
  validTask,
  isDomainConnectionTask,
  terminal,
  type Task,
  type Submission,
} from "./task-api";

export const domainOperations = [
  "GET /api/domains",
  "GET /api/domains/detail",
  "GET /api/domains/creation",
  "POST /api/domains/create",
  "POST /api/domains/create-unconfigured",
  "GET /api/domains/credential-source",
  "GET /api/domains/credential-source/mutation",
  "POST /api/domains/credential-source/reference",
  "POST /api/domains/credential-source/custom",
  "POST /api/domains/credential-source/detach",
  "GET /api/operation-accounts",
  "GET /api/credential-use/effective",
  "POST /api/domains/update",
  "POST /api/domains/delete",
  "POST /api/domains/test",
  "GET /api/domains/test-result",
  "GET /api/domain-selection",
  "GET /api/domain-selection/resolve",
  "GET /api/tasks",
  "POST /api/tasks/cancel",
] as const;
export type DomainOperation = (typeof domainOperations)[number];
export const connectionStates = [
  "unverified",
  "testing",
  "verified",
  "error",
] as const;
export const connectionLabels = {
  unverified: "已保存，未验证",
  testing: "正在检测",
  verified: "上次检测通过",
  error: "上次检测未通过",
};
export interface Diagnostic {
  taskUUID: string;
  revision: string;
  credentialRevision: string;
  stage: string;
  code: string;
  observedAt: string;
  elapsedMilliseconds: number;
  dcHostName: string;
}
export interface Connection {
  domainId: string;
  domain: string;
  dcHostName: string;
  ldapAddr: string;
  port: "389" | "636";
  mode: "starttls" | "ldaps";
  revision: string;
  credentialRevision: string;
  credentialConfigured: boolean;
  credentialSource?: "custom" | "operation_account" | "unconfigured";
  connectionCredentialGeneration?: string;
  createdAt: string;
  updatedAt: string;
  connectionState: (typeof connectionStates)[number];
  lastDiagnostic: Diagnostic | null;
  latestTaskUUID: string;
}
export interface DomainList {
  page: PageInfo;
  List: Connection[];
  exhausted: boolean;
}
export interface Receipt {
  domainId: string;
  domain: string;
  revision: string;
  deleted: boolean;
  requiresResourceAssignment: true;
}
export interface Created {
  result: "SUCCESS";
  domainId: string;
  revision: string;
  requiresResourceAssignment: true;
  replayed: boolean;
}
export interface EndpointInput {
  dcHostName: string;
  ldapAddr: string;
  port: string;
}
export interface CreateInput extends EndpointInput {
  domain: string;
  username: string;
  password: string;
  idempotencyKey: string;
}
export interface UpdateInput extends EndpointInput {
  domainId: string;
  expectedRevision: string;
  username?: string;
  password?: string;
}
export interface TestInput {
  domainId: string;
  expectedRevision: string;
  idempotencyKey: string;
}
export interface TestResult {
  task: Task;
  diagnostic: Diagnostic | null;
}
export const validRevision = (v: unknown): v is string =>
  typeof v === "string" &&
  /^[1-9][0-9]{0,18}$/u.test(v) &&
  BigInt(v) <= 9223372036854775807n;
export const validDomainID = (v: unknown): v is string =>
  typeof v === "string" &&
  /^[A-Za-z0-9_-]{1,128}$/u.test(v) &&
  v !== "platform";
export const validDNS = (v: string) =>
  /^[\x00-\x7f]+$/u.test(v) &&
  !/^(?:[0-9]+\.){3}[0-9]+\.?$/u.test(v) &&
  v.length <= 254 &&
  /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+\.?$/iu.test(
    v,
  ) &&
  v.replace(/\.$/u, "").length <= 253;
const utc = (v: unknown): v is string =>
  typeof v === "string" &&
  /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z$/u.test(v) &&
  Number.isFinite(Date.parse(v));
const uuid = (v: unknown): v is string =>
  typeof v === "string" &&
  /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/iu.test(v);
export const diagnosticMessages: Record<string, string> = {
  ok: "TLS、凭据绑定及域命名上下文检测通过。",
  invalid_config: "已保存的连接配置无效。",
  dns_failed: "域控名称解析失败。",
  destination_denied: "目标不在部署允许范围内。",
  connection_failed: "连接检测无法完成。",
  credential_unavailable: "保存的凭据暂时不可用。",
  checkpoint_failed: "检测结果未能提交，不能确认成功。",
  connect_failed: "无法建立网络连接。",
  connection_timeout: "连接检测超时。",
  tls_untrusted: "服务器证书不受信任，请检查部署 CA。",
  tls_hostname: "证书与配置的域控 DNS 名称不匹配。",
  tls_expired: "服务器证书已过期或尚未生效。",
  tls_failed: "TLS 验证失败，未降级为明文连接。",
  starttls_required: "服务器未成功启用 StartTLS。",
  credentials_rejected: "AD 凭据被拒绝，请修正凭据后手动检测。",
  ldap_access_denied: "目录拒绝此次读取。",
  ldap_timeout: "目录操作超时。",
  referral_rejected: "服务器返回转介；未访问转介目标。",
  invalid_response: "服务器响应无法安全确认。",
  directory_mismatch: "目录命名上下文与已配置域不匹配。",
  cancelled: "执行器已确认取消。",
  authorization_revoked: "执行授权已撤销。",
  connection_revision_changed: "配置或凭据版本已改变，旧检测结果不再有效。",
  policy_revision_changed: "部署信任或目标策略已改变，需重新检测。",
  credential_key_unavailable: "凭据密钥不可用，请联系部署管理员。",
  credential_invalid: "保存的凭据无法读取。",
  executor_lost: "执行器失联，外部检测结果不确定；不会自动重试。",
  internal: "检测服务暂时不可用。",
};
const stages = [
  "authorization",
  "resolve",
  "connect",
  "tls",
  "bind",
  "rootdse",
  "identity",
  "publish",
  "complete",
];
const exact = (v: Record<string, unknown>, keys: string[]) =>
  Object.keys(v).length === keys.length &&
  Object.keys(v).every((k) => keys.includes(k));
function validDiagnostic(v: unknown): v is Diagnostic {
  return (
    object(v) &&
    exact(v, [
      "taskUUID",
      "revision",
      "credentialRevision",
      "stage",
      "code",
      "observedAt",
      "elapsedMilliseconds",
      "dcHostName",
    ]) &&
    uuid(v.taskUUID) &&
    validRevision(v.revision) &&
    validRevision(v.credentialRevision) &&
    typeof v.stage === "string" &&
    stages.includes(v.stage) &&
    typeof v.code === "string" &&
    Object.hasOwn(diagnosticMessages, v.code) &&
    utc(v.observedAt) &&
    integer(v.elapsedMilliseconds, 0, 60000) &&
    typeof v.dcHostName === "string" &&
    (v.code === "ok"
      ? v.stage === "complete" && validDNS(v.dcHostName)
      : v.dcHostName === "" && v.stage !== "complete")
  );
}
export function validConnection(v: unknown): v is Connection {
  if (
    !object(v) ||
    !exact(v, [
      "domainId",
      "domain",
      "dcHostName",
      "ldapAddr",
      "port",
      "mode",
      "revision",
      "credentialRevision",
      "credentialConfigured",
      "createdAt",
      "updatedAt",
      "connectionState",
      "lastDiagnostic",
      "latestTaskUUID",
      ...(Object.hasOwn(v, "credentialSource") ||
      Object.hasOwn(v, "connectionCredentialGeneration")
        ? ["credentialSource", "connectionCredentialGeneration"]
        : []),
    ])
  )
    return false;
  if (
    !validDomainID(v.domainId) ||
    typeof v.domain !== "string" ||
    !validDNS(v.domain) ||
    v.domain !== v.domain.toLowerCase().replace(/\.$/u, "") ||
    typeof v.dcHostName !== "string" ||
    !validDNS(v.dcHostName) ||
    typeof v.ldapAddr !== "string" ||
    !validIP(v.ldapAddr) ||
    !(
      (v.port === "389" && v.mode === "starttls") ||
      (v.port === "636" && v.mode === "ldaps")
    ) ||
    !validRevision(v.revision) ||
    !validRevision(v.credentialRevision) ||
    typeof v.credentialConfigured !== "boolean" ||
    (Object.hasOwn(v, "credentialSource") &&
      (!["custom", "operation_account", "unconfigured"].includes(
        String(v.credentialSource),
      ) ||
        v.connectionCredentialGeneration !== v.credentialRevision ||
        (v.credentialSource === "unconfigured" && v.credentialConfigured))) ||
    !utc(v.createdAt) ||
    !utc(v.updatedAt) ||
    !connectionStates.includes(
      v.connectionState as Connection["connectionState"],
    )
  )
    return false;
  if (
    typeof v.latestTaskUUID !== "string" ||
    (v.latestTaskUUID !== "" && !uuid(v.latestTaskUUID))
  )
    return false;
  if (
    v.lastDiagnostic !== null &&
    (!validDiagnostic(v.lastDiagnostic) ||
      v.lastDiagnostic.revision !== v.revision ||
      v.lastDiagnostic.credentialRevision !== v.credentialRevision ||
      v.lastDiagnostic.taskUUID !== v.latestTaskUUID)
  )
    return false;
  return v.connectionState === "verified"
    ? v.lastDiagnostic !== null &&
        (v.lastDiagnostic as Diagnostic).code === "ok"
    : !(v.lastDiagnostic as Diagnostic | null)?.code ||
        (v.lastDiagnostic as Diagnostic).code !== "ok";
}
export function validIP(value: string): boolean {
  if (value === "") return true;
  if (/^(?:0|[1-9][0-9]{0,2})(?:\.(?:0|[1-9][0-9]{0,2})){3}$/u.test(value))
    return value.split(".").every((v) => Number(v) <= 255);
  if (!value.includes(":") || /[^a-f0-9:.]/iu.test(value)) return false;
  try {
    return new URL(`https://[${value}]/`).hostname.startsWith("[");
  } catch {
    return false;
  }
}
export const validADPair = (username: string, password: string) => {
  const bounded = (v: string) =>
    Array.from(v).length >= 1 &&
    Array.from(v).length <= 50 &&
    new TextEncoder().encode(v).length <= 200 &&
    !v.includes("\0") &&
    !/[\ud800-\udfff]/u.test(v);
  return (
    bounded(username) &&
    bounded(password) &&
    username === username.trim() &&
    !/[\u0000-\u001f\u007f]/u.test(username) &&
    (/^[^\\@\s]+\\[^\\@\s]+$/u.test(username) ||
      /^[^\\@\s]+@[^\\@\s]+$/u.test(username))
  );
};
async function transport(
  path: string,
  signal: AbortSignal,
  body?: unknown,
  csrf?: string,
  actor?: number,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(`/api/domains${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
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
  if (
    response.ok &&
    actor !== undefined &&
    response.headers.get("X-ADTR-User-ID") !== String(actor)
  )
    throw new ApiError("domain_source_identity_changed", 401);
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
const invalid = () => {
  throw new ApiError("invalid_response");
};
export const domainAPI = {
  async list(query: string, signal: AbortSignal): Promise<DomainList> {
    const d = await transport(`?${query}`, signal);
    if (
      !object(d) ||
      !object(d.page) ||
      !Array.isArray(d.List) ||
      !d.List.every(validConnection) ||
      typeof d.exhausted !== "boolean" ||
      !integer(d.page.pageIdx, 1, 1000000) ||
      !integer(d.page.pageSize, 1, 100) ||
      !integer(d.page.total, 0) ||
      !integer(d.page.totalPage, 0)
    )
      return invalid();
    return d as unknown as DomainList;
  },
  async detail(id: string, signal: AbortSignal): Promise<Connection> {
    const d = await transport(
      `/detail?${new URLSearchParams({ domainId: id })}`,
      signal,
    );
    if (
      !object(d) ||
      !validConnection(d.connection) ||
      d.connection.domainId !== id
    )
      return invalid();
    return d.connection;
  },
  async receipt(
    key: string,
    signal: AbortSignal,
    actor?: number,
  ): Promise<Receipt> {
    const d = await transport(
      `/creation?${new URLSearchParams({ idempotencyKey: key })}`,
      signal,
      undefined,
      undefined,
      actor,
    );
    if (!object(d) || !object(d.receipt)) return invalid();
    const r = d.receipt;
    if (
      !exact(r, [
        "domainId",
        "domain",
        "revision",
        "deleted",
        "requiresResourceAssignment",
      ]) ||
      !validDomainID(r.domainId) ||
      typeof r.domain !== "string" ||
      !validDNS(r.domain) ||
      !validRevision(r.revision) ||
      typeof r.deleted !== "boolean" ||
      r.requiresResourceAssignment !== true
    )
      return invalid();
    return r as unknown as Receipt;
  },
  async create(
    input: CreateInput,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ): Promise<Created> {
    const d = await transport("/create", signal, { ...input, ...proof }, csrf);
    if (
      !object(d) ||
      d.result !== "SUCCESS" ||
      !validDomainID(d.domainId) ||
      !validRevision(d.revision) ||
      d.requiresResourceAssignment !== true ||
      typeof d.replayed !== "boolean"
    )
      return invalid();
    return d as unknown as Created;
  },
  async update(
    input: UpdateInput,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ): Promise<void> {
    const d = await transport("/update", signal, { ...input, ...proof }, csrf);
    if (
      !object(d) ||
      d.result !== "SUCCESS" ||
      d.domainId !== input.domainId ||
      !validRevision(d.revision) ||
      BigInt(d.revision) <= BigInt(input.expectedRevision)
    )
      return invalid();
  },
  async delete(
    input: {
      domainId: string;
      expectedRevision: string;
      confirmDomain: string;
    },
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ): Promise<void> {
    const d = await transport("/delete", signal, { ...input, ...proof }, csrf);
    if (!object(d) || d.result !== "SUCCESS" || d.domainId !== input.domainId)
      return invalid();
  },
  async test(
    input: TestInput,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ): Promise<Submission> {
    const d = await transport("/test", signal, { ...input, ...proof }, csrf);
    if (
      !object(d) ||
      typeof d.replayed !== "boolean" ||
      !validDomainTask(d.task) ||
      d.task.domainId !== input.domainId
    )
      return invalid();
    return d as unknown as Submission;
  },
  async result(
    id: string,
    domainId: string,
    signal: AbortSignal,
  ): Promise<TestResult> {
    const d = await transport(
      `/test-result?${new URLSearchParams({ taskUUID: id })}`,
      signal,
    );
    if (
      !object(d) ||
      !validDomainTask(d.task) ||
      d.task.taskUUID !== id ||
      d.task.domainId !== domainId
    )
      return invalid();
    if (
      d.diagnostic !== null &&
      (!validDiagnostic(d.diagnostic) ||
        d.diagnostic.taskUUID !== id ||
        !terminal(d.task.state) ||
        (d.diagnostic.code === "ok") !== (d.task.state === "succeeded"))
    )
      return invalid();
    return d as unknown as TestResult;
  },
};
export function validDomainTask(v: unknown): v is Task {
  return (
    validTask(v) &&
    isDomainConnectionTask(v.taskName) &&
    v.payloadVersion === 1 &&
    v.maxAttempts === 1 &&
    v.attempt <= 1 &&
    v.state !== "retry_wait" &&
    (v.result === null ||
      (object(v.result) && Object.keys(v.result).length === 0)) &&
    (v.cursor === null ||
      (object(v.cursor) && Object.keys(v.cursor).length === 0))
  );
}
export const uncertainDomainError = (e: unknown) =>
  !(e instanceof ApiError) ||
  e.status >= 500 ||
  ["network", "invalid_response"].includes(e.code);
export function domainError(error: unknown): string {
  const codes: Record<string, string> = {
    ...accessMessages,
    ...diagnosticMessages,
    network:
      "连接中断，操作结果尚未确认。请核对服务器状态，不要换用新幂等键重复提交。",
    invalid_response: "服务器结果无法确认；请重新读取，不要重复创建。",
    domain_conflict: "该域已有本地连接配置，请核对原配置或联系管理员。",
    revision_conflict:
      "配置已被其他操作修改，请重新读取并检查最新版本后再提交。",
    idempotency_conflict: "此幂等键已对应其他内容，请核对原提交。",
    tenant_not_configured: "当前租户尚未配置，需先完成租户配置。",
    domain_capacity_exceeded: "已达到租户域容量上限。",
    domain_probe_disabled: "部署尚未启用连接检测。",
    domain_key_unavailable: "部署尚未配置可用凭据密钥。",
    domain_in_use: "其他模块仍使用此域，暂不能删除。",
    not_found: "此连接或记录不存在，或当前账户已无访问权限。",
    domain_route_required: "请在域连接页面使用新的明确检测操作。",
    credential_unconfigured: "尚未配置连接凭据，请先明确选择凭据来源。",
    credential_source_changed:
      "凭据来源已改变，请重新读取，并在凭据来源页面明确切换。",
    no_change: "未检测到修改，请调整后提交。",
  };
  return error instanceof ApiError
    ? (codes[error.code] ?? "域连接操作失败，请刷新服务器状态后核对。")
    : "操作结果尚未确认，请重新读取服务器状态。";
}
