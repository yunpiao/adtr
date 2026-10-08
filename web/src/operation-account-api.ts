import { ApiError } from "./api";
import { accessMessages, type PageInfo, type Proof } from "./access-api";
import { integer, object } from "./task-api";
import { validDNS, validDomainID, validRevision } from "./domain-api";

export const operationAccountOperations = [
  "GET /api/operation-accounts",
  "GET /api/operation-accounts/domains",
  "GET /api/operation-accounts/detail",
  "GET /api/operation-accounts/mutation",
  "POST /api/operation-accounts/create",
  "POST /api/operation-accounts/update",
  "POST /api/operation-accounts/delete",
] as const;
export type OperationAccountOperation =
  (typeof operationAccountOperations)[number];
export type MutationKind = "create" | "update" | "delete";
export interface OperationAccount {
  accountId: string;
  domainId: string;
  domain: string;
  label: string;
  revision: string;
  credentialRevision: string;
  credentialConfigured: true;
  createdAt: string;
  updatedAt: string;
  storageState: "saved";
  verificationState: "unverified";
}
export interface AccountDomain {
  domainId: string;
  domain: string;
  revision: string;
}
export interface AccountList {
  page: PageInfo;
  List: OperationAccount[];
  exhausted: boolean;
}
export interface AccountDomainList {
  page: PageInfo;
  domains: AccountDomain[];
  exhausted: boolean;
}
export interface OperationReceipt {
  result: "SUCCESS";
  operation: MutationKind;
  accountId: string;
  domainId: string;
  revision: string;
  credentialRevision: string;
  replayed: boolean;
  verificationState: "unverified";
  deleted: boolean;
  currentRevision: string;
  currentCredentialRevision: string;
}
export interface CreateAccountInput {
  domainId: string;
  expectedDomainRevision: string;
  username: string;
  password: string;
  idempotencyKey: string;
  label?: string;
}
export interface UpdateAccountInput {
  accountId: string;
  expectedRevision: string;
  idempotencyKey: string;
  label?: string;
  username?: string;
  password?: string;
}
export interface DeleteAccountInput {
  accountId: string;
  expectedRevision: string;
  confirmAccountId: string;
  idempotencyKey: string;
}
const exact = (value: Record<string, unknown>, keys: string[]) =>
  Object.keys(value).length === keys.length &&
  Object.keys(value).every((key) => keys.includes(key));
const canonicalDNS = (value: unknown): value is string =>
  typeof value === "string" &&
  validDNS(value) &&
  value === value.toLowerCase().replace(/\.$/u, "");
const utc = (value: unknown): value is string =>
  typeof value === "string" &&
  /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z$/u.test(value) &&
  Number.isFinite(Date.parse(value));
export const validAccountID = (value: unknown): value is string =>
  typeof value === "string" && /^[A-Za-z0-9_-]{1,128}$/u.test(value);
export const validAccountLabel = (value: unknown): value is string =>
  typeof value === "string" &&
  value === value.trim() &&
  Array.from(value).length <= 50 &&
  new TextEncoder().encode(value).length <= 200 &&
  !/[\p{Cc}\ud800-\udfff]/u.test(value);
export function validOperationAccount(
  value: unknown,
): value is OperationAccount {
  return (
    object(value) &&
    exact(value, [
      "accountId",
      "domainId",
      "domain",
      "label",
      "revision",
      "credentialRevision",
      "credentialConfigured",
      "createdAt",
      "updatedAt",
      "storageState",
      "verificationState",
    ]) &&
    validAccountID(value.accountId) &&
    validDomainID(value.domainId) &&
    canonicalDNS(value.domain) &&
    validAccountLabel(value.label) &&
    validRevision(value.revision) &&
    validRevision(value.credentialRevision) &&
    BigInt(value.credentialRevision) <= BigInt(value.revision) &&
    value.credentialConfigured === true &&
    utc(value.createdAt) &&
    utc(value.updatedAt) &&
    Date.parse(value.updatedAt) >= Date.parse(value.createdAt) &&
    value.storageState === "saved" &&
    value.verificationState === "unverified"
  );
}
export function validOperationReceipt(
  value: unknown,
): value is OperationReceipt {
  return (
    object(value) &&
    exact(value, [
      "result",
      "operation",
      "accountId",
      "domainId",
      "revision",
      "credentialRevision",
      "replayed",
      "verificationState",
      "deleted",
      "currentRevision",
      "currentCredentialRevision",
    ]) &&
    value.result === "SUCCESS" &&
    ["create", "update", "delete"].includes(String(value.operation)) &&
    validAccountID(value.accountId) &&
    validDomainID(value.domainId) &&
    validRevision(value.revision) &&
    validRevision(value.credentialRevision) &&
    validRevision(value.currentRevision) &&
    validRevision(value.currentCredentialRevision) &&
    BigInt(value.credentialRevision) <= BigInt(value.revision) &&
    BigInt(value.currentRevision) >= BigInt(value.revision) &&
    BigInt(value.currentCredentialRevision) >=
      BigInt(value.credentialRevision) &&
    BigInt(value.currentCredentialRevision) <= BigInt(value.currentRevision) &&
    typeof value.replayed === "boolean" &&
    typeof value.deleted === "boolean" &&
    (value.operation !== "delete" || value.deleted) &&
    value.verificationState === "unverified"
  );
}
function validPage(
  value: unknown,
  length: number,
  exhausted: unknown,
): value is PageInfo {
  if (
    !object(value) ||
    !exact(value, ["pageIdx", "pageSize", "total", "totalPage"]) ||
    !integer(value.pageIdx, 1, 1000000) ||
    !(integer(value.pageSize, 1, 100) || value.pageSize === -1) ||
    !integer(value.total, 0) ||
    !integer(value.totalPage, 0) ||
    typeof exhausted !== "boolean"
  )
    return false;
  const size =
    value.pageSize === -1 ? Math.max(value.total, 1) : value.pageSize;
  if (value.pageSize === -1 && (value.pageIdx !== 1 || value.total > 1000))
    return false;
  return (
    value.totalPage === Math.ceil(value.total / size) &&
    length ===
      Math.min(size, Math.max(0, value.total - (value.pageIdx - 1) * size)) &&
    exhausted === value.pageIdx >= value.totalPage
  );
}
async function transport(
  path: string,
  signal: AbortSignal,
  body?: unknown,
  csrf?: string,
  actor?: number,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(`/api/operation-accounts${path}`, {
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
const invalid = (): never => {
  throw new ApiError("invalid_response");
};
async function mutate(
  kind: MutationKind,
  input: CreateAccountInput | UpdateAccountInput | DeleteAccountInput,
  proof: Proof,
  csrf: string,
  signal: AbortSignal,
): Promise<OperationReceipt> {
  const data = await transport(
    `/${kind}`,
    signal,
    { ...input, ...proof },
    csrf,
  );
  if (
    !validOperationReceipt(data) ||
    data.operation !== kind ||
    ("accountId" in input && data.accountId !== input.accountId) ||
    ("domainId" in input && data.domainId !== input.domainId) ||
    ("expectedRevision" in input &&
      BigInt(data.revision) !== BigInt(input.expectedRevision) + 1n)
  )
    return invalid();
  return data;
}
export const operationAccountAPI = {
  async list(
    query: string,
    signal: AbortSignal,
    actor?: number,
  ): Promise<AccountList> {
    const data = await transport(
      `?${query}`,
      signal,
      undefined,
      undefined,
      actor,
    );
    if (
      !object(data) ||
      !exact(data, ["page", "List", "exhausted"]) ||
      !Array.isArray(data.List) ||
      !data.List.every(validOperationAccount) ||
      new Set(data.List.map((v) => v.accountId)).size !== data.List.length ||
      !validPage(data.page, data.List.length, data.exhausted)
    )
      return invalid();
    return data as unknown as AccountList;
  },
  async domains(
    query: string,
    signal: AbortSignal,
  ): Promise<AccountDomainList> {
    const data = await transport(`/domains?${query}`, signal);
    if (
      !object(data) ||
      !exact(data, ["page", "domains", "exhausted"]) ||
      !Array.isArray(data.domains) ||
      !data.domains.every(
        (v) =>
          object(v) &&
          exact(v, ["domainId", "domain", "revision"]) &&
          validDomainID(v.domainId) &&
          canonicalDNS(v.domain) &&
          validRevision(v.revision),
      ) ||
      new Set(data.domains.map((v) => v.domainId)).size !==
        data.domains.length ||
      !validPage(data.page, data.domains.length, data.exhausted)
    )
      return invalid();
    return data as unknown as AccountDomainList;
  },
  async detail(
    accountId: string,
    signal: AbortSignal,
    actor?: number,
  ): Promise<OperationAccount> {
    const data = await transport(
      `/detail?${new URLSearchParams({ accountId })}`,
      signal,
      undefined,
      undefined,
      actor,
    );
    if (
      !object(data) ||
      !exact(data, ["account"]) ||
      !validOperationAccount(data.account) ||
      data.account.accountId !== accountId
    )
      return invalid();
    return data.account;
  },
  async receipt(key: string, signal: AbortSignal): Promise<OperationReceipt> {
    const data = await transport(
      `/mutation?${new URLSearchParams({ idempotencyKey: key })}`,
      signal,
    );
    if (
      !object(data) ||
      !exact(data, ["receipt"]) ||
      !validOperationReceipt(data.receipt) ||
      !data.receipt.replayed
    )
      return invalid();
    return data.receipt;
  },
  create: (
    input: CreateAccountInput,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ) => mutate("create", input, proof, csrf, signal),
  update: (
    input: UpdateAccountInput,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ) => mutate("update", input, proof, csrf, signal),
  delete: (
    input: DeleteAccountInput,
    proof: Proof,
    csrf: string,
    signal: AbortSignal,
  ) => mutate("delete", input, proof, csrf, signal),
};
export const uncertainOperationError = (error: unknown) =>
  !(error instanceof ApiError) ||
  error.status >= 500 ||
  ["network", "invalid_response"].includes(error.code);
export function operationAccountError(error: unknown): string {
  const messages: Record<string, string> = {
    ...accessMessages,
    network: "连接中断，操作结果尚未确认。请查询原操作回执。",
    invalid_response: "服务器结果无法安全确认，请查询原操作回执或重新读取。",
    not_found: "记录不存在或当前无权访问；不能据此确认删除或提交失败。",
    revision_conflict: "记录或所属域已改变，请重新读取并核对最新版本后再提交。",
    idempotency_conflict: "此操作编号已对应其他内容，请核对原操作回执。",
    no_change: "未检测到修改，请调整标签或明确替换凭据后提交。",
    operation_account_in_use:
      "此账户仍被连接引用或有尚未确认停止的使用，暂不能删除或替换。终态任务也可能尚未释放使用，请调查未解决的使用记录。",
    account_in_use:
      "此账户仍被连接引用或有尚未确认停止的使用，暂不能删除或替换。终态任务也可能尚未释放使用，请调查未解决的使用记录。",
    domain_key_unavailable: "部署未配置可用的凭据密钥，请联系部署管理员。",
    credential_key_unavailable: "部署凭据密钥暂不可用，请联系部署管理员。",
    tenant_not_configured: "租户尚未配置，暂不能管理操作账户。",
    account_confirmation_mismatch: "输入的完整账户 ID 与当前登记不一致。",
    tenant_domain_limit_exceeded:
      "租户当前域容量策略不满足要求，暂不能管理操作账户。",
    revision_exhausted: "登记版本已达到上限，请联系部署管理员。",
    credential_unavailable: "保存的凭据暂不可用，请联系部署管理员。",
    schema_incompatible: "服务数据库版本尚未就绪，请稍后重新读取。",
    tenant_expired: "租户已过期，暂不能管理操作账户。",
    domain_capacity_exceeded: "租户域容量策略不满足要求，暂不能管理操作账户。",
    plaintext_unavailable: "当前不支持明文查看或导出凭据。",
    unsupported_status_filter: "当前未定义状态筛选，请移除该条件。",
    result_too_large: "结果超过一次读取上限，请缩小筛选范围或使用分页。",
  };
  return error instanceof ApiError
    ? (messages[error.code] ?? "操作账户请求失败，请重新读取服务器状态后核对。")
    : "操作结果尚未确认，请查询原操作回执。";
}
