import { ApiError } from "./api";
import { accessMessages, type PageInfo, type Proof } from "./access-api";
import { integer, object } from "./task-api";
import { validDNS, validDomainID, validRevision } from "./domain-api";
import { validAccountID, validAccountLabel } from "./operation-account-api";

export const directoryCredentialPurpose = "domain.directory_read" as const;
export const directoryCredentialUseOperations = [
  "GET /api/directory-credential-use/accounts",
  "GET /api/directory-credential-use/grants",
  "GET /api/directory-credential-use/roles",
  "GET /api/directory-credential-use/effective",
  "GET /api/directory-credential-use/mutation",
  "POST /api/directory-credential-use/grant",
  "POST /api/directory-credential-use/revoke",
] as const;
export type DirectoryCredentialUseOperation =
  (typeof directoryCredentialUseOperations)[number];
export type DirectoryCredentialMutation = "grant" | "revoke";
export interface DirectoryCredentialAccount {
  accountId: string;
  domainId: string;
  domain: string;
  label: string;
  revision: string;
  credentialRevision: string;
}
export interface DirectoryCredentialRole {
  roleId: string;
  roleName: string;
  memberCount: number;
}
export interface DirectoryCredentialGrant {
  roleId: string;
  roleName: string;
  purpose: typeof directoryCredentialPurpose;
  allowed: boolean;
  grantRevision: string;
  accountCredentialRevision: string;
  updatedAt: string;
}
export interface DirectoryCredentialEffective {
  accountId: string;
  domainId: string;
  purpose: typeof directoryCredentialPurpose;
  explicitlyGranted: boolean;
  eligible: boolean;
  grantRevision: string;
  accountCredentialRevision: string;
  consumerEnabled: boolean;
}
export interface DirectoryCredentialReceipt {
  result: "SUCCESS";
  operation: DirectoryCredentialMutation;
  accountId: string;
  domainId: string;
  roleId: string;
  purpose: typeof directoryCredentialPurpose;
  grantRevision: string;
  accountCredentialRevision: string;
  allowed: boolean;
  replayed: boolean;
  accountDeleted: boolean;
  currentGrantRevision: string;
  currentAllowed: boolean;
}
export interface DirectoryCredentialInput {
  accountId: string;
  roleId: string;
  purpose: typeof directoryCredentialPurpose;
  expectedAccountRevision: string;
  expectedCredentialRevision: string;
  expectedGrantRevision: string;
  idempotencyKey: string;
}
const exact = (v: Record<string, unknown>, keys: readonly string[]) =>
  Object.keys(v).length === keys.length &&
  Object.keys(v).every((key) => keys.includes(key));
export const validDirectoryCredentialRoleID = (v: unknown): v is string =>
  typeof v === "string" &&
  (v === "platform_admin" || v === "viewer" || /^[A-Za-z0-9_-]{24}$/u.test(v));
const roleName = (v: unknown): v is string =>
  typeof v === "string" &&
  v.length > 0 &&
  Array.from(v).length <= 128 &&
  !/[\p{Cc}\ud800-\udfff]/u.test(v);
const revisionOrZero = (v: unknown): v is string =>
  v === "0" || validRevision(v);
const utc = (v: unknown): v is string =>
  typeof v === "string" &&
  /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z$/u.test(v) &&
  Number.isFinite(Date.parse(v));
export function validDirectoryCredentialAccount(
  v: unknown,
): v is DirectoryCredentialAccount {
  return (
    object(v) &&
    exact(v, [
      "accountId",
      "domainId",
      "domain",
      "label",
      "revision",
      "credentialRevision",
    ]) &&
    validAccountID(v.accountId) &&
    validDomainID(v.domainId) &&
    typeof v.domain === "string" &&
    validDNS(v.domain) &&
    v.domain === v.domain.toLowerCase().replace(/\.$/u, "") &&
    validAccountLabel(v.label) &&
    validRevision(v.revision) &&
    validRevision(v.credentialRevision) &&
    BigInt(v.credentialRevision) <= BigInt(v.revision)
  );
}
export function validDirectoryCredentialGrant(
  v: unknown,
): v is DirectoryCredentialGrant {
  return (
    object(v) &&
    exact(v, [
      "roleId",
      "roleName",
      "purpose",
      "allowed",
      "grantRevision",
      "accountCredentialRevision",
      "updatedAt",
    ]) &&
    validDirectoryCredentialRoleID(v.roleId) &&
    v.roleId !== "viewer" &&
    roleName(v.roleName) &&
    v.purpose === directoryCredentialPurpose &&
    typeof v.allowed === "boolean" &&
    validRevision(v.grantRevision) &&
    validRevision(v.accountCredentialRevision) &&
    utc(v.updatedAt)
  );
}
const receiptKeys = [
  "result",
  "operation",
  "accountId",
  "domainId",
  "roleId",
  "purpose",
  "grantRevision",
  "accountCredentialRevision",
  "allowed",
  "replayed",
  "accountDeleted",
  "currentGrantRevision",
  "currentAllowed",
];
export function validDirectoryCredentialReceipt(
  v: unknown,
): v is DirectoryCredentialReceipt {
  return (
    object(v) &&
    exact(v, receiptKeys) &&
    v.result === "SUCCESS" &&
    ["grant", "revoke"].includes(String(v.operation)) &&
    validAccountID(v.accountId) &&
    validDomainID(v.domainId) &&
    validDirectoryCredentialRoleID(v.roleId) &&
    v.roleId !== "viewer" &&
    v.purpose === directoryCredentialPurpose &&
    validRevision(v.grantRevision) &&
    validRevision(v.accountCredentialRevision) &&
    typeof v.allowed === "boolean" &&
    v.allowed === (v.operation === "grant") &&
    typeof v.replayed === "boolean" &&
    typeof v.accountDeleted === "boolean" &&
    revisionOrZero(v.currentGrantRevision) &&
    typeof v.currentAllowed === "boolean" &&
    (v.currentGrantRevision === "0"
      ? !v.currentAllowed
      : BigInt(v.currentGrantRevision) >= BigInt(v.grantRevision)) &&
    (!v.accountDeleted || !v.currentAllowed)
  );
}
const invalid = (): never => {
  throw new ApiError("invalid_response");
};
async function transport(
  path: string,
  actor: number,
  signal: AbortSignal,
  body?: unknown,
  csrf?: string,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(`/api/directory-credential-use${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      redirect: "error",
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
  // A cookie can change in another tab while a request is in flight. Never put a
  // successful response belonging to a different actor into the current view.
  if (
    response.ok &&
    (!integer(actor, 1) ||
      response.headers.get("X-ADTR-User-ID") !== String(actor))
  )
    throw new ApiError("credential_use_identity_changed", 401);
  const data: unknown = await response.json().catch(() => {
    throw new ApiError("invalid_response", response.status);
  });
  signal.throwIfAborted();
  if (!response.ok)
    throw new ApiError(
      object(data) && typeof data.error === "string" ? data.error : "internal",
      response.status,
    );
  return data;
}
const accountQuery = (accountId: string) => new URLSearchParams({ accountId });
export const directoryCredentialUseAPI = {
  async accounts(
    query: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<{
    page: PageInfo;
    List: DirectoryCredentialAccount[];
    exhausted: boolean;
    consumerEnabled: boolean;
  }> {
    const v = await transport(`/accounts?${query}`, actor, signal);
    if (
      !object(v) ||
      !exact(v, ["page", "List", "exhausted", "consumerEnabled"]) ||
      typeof v.consumerEnabled !== "boolean" ||
      !object(v.page) ||
      !exact(v.page, ["pageIdx", "pageSize", "total", "totalPage"]) ||
      !integer(v.page.pageIdx, 1, 1000000) ||
      ![10, 20, 30, 40, 50].includes(v.page.pageSize as number) ||
      !integer(v.page.total, 0) ||
      !integer(v.page.totalPage, 0) ||
      !Array.isArray(v.List) ||
      !v.List.every(validDirectoryCredentialAccount) ||
      new Set(v.List.map((a) => a.accountId)).size !== v.List.length ||
      typeof v.exhausted !== "boolean"
    )
      return invalid();
    const size = v.page.pageSize as number;
    if (
      v.page.totalPage !== Math.ceil(v.page.total / size) ||
      v.List.length !==
        Math.min(
          size,
          Math.max(0, v.page.total - (v.page.pageIdx - 1) * size),
        ) ||
      v.exhausted !== v.page.pageIdx >= v.page.totalPage
    )
      return invalid();
    return v as unknown as {
      page: PageInfo;
      List: DirectoryCredentialAccount[];
      exhausted: boolean;
      consumerEnabled: boolean;
    };
  },
  async grants(
    accountId: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<{
    account: DirectoryCredentialAccount;
    grants: DirectoryCredentialGrant[];
    consumerEnabled: boolean;
  }> {
    const v = await transport(
      `/grants?${accountQuery(accountId)}`,
      actor,
      signal,
    );
    if (
      !object(v) ||
      !exact(v, ["account", "grants", "consumerEnabled"]) ||
      typeof v.consumerEnabled !== "boolean" ||
      !validDirectoryCredentialAccount(v.account) ||
      v.account.accountId !== accountId ||
      !Array.isArray(v.grants) ||
      v.grants.length > 1000 ||
      !v.grants.every(validDirectoryCredentialGrant) ||
      new Set(v.grants.map((g) => g.roleId)).size !== v.grants.length ||
      v.grants.some(
        (g) =>
          g.allowed &&
          g.accountCredentialRevision !==
            (v.account as DirectoryCredentialAccount).credentialRevision,
      )
    )
      return invalid();
    return v as unknown as {
      account: DirectoryCredentialAccount;
      grants: DirectoryCredentialGrant[];
      consumerEnabled: boolean;
    };
  },
  async roles(
    accountId: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<DirectoryCredentialRole[]> {
    const v = await transport(
      `/roles?${accountQuery(accountId)}`,
      actor,
      signal,
    );
    if (
      !object(v) ||
      !exact(v, ["roles", "consumerEnabled"]) ||
      typeof v.consumerEnabled !== "boolean" ||
      !Array.isArray(v.roles) ||
      v.roles.length > 1000 ||
      !v.roles.every(
        (r) =>
          object(r) &&
          exact(r, ["roleId", "roleName", "memberCount"]) &&
          validDirectoryCredentialRoleID(r.roleId) &&
          r.roleId !== "viewer" &&
          roleName(r.roleName) &&
          integer(r.memberCount, 0),
      ) ||
      new Set(v.roles.map((r) => r.roleId)).size !== v.roles.length
    )
      return invalid();
    return v.roles as DirectoryCredentialRole[];
  },
  async effective(
    accountId: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<DirectoryCredentialEffective> {
    const v = await transport(
      `/effective?${accountQuery(accountId)}`,
      actor,
      signal,
    );
    if (
      !object(v) ||
      !exact(v, [
        "accountId",
        "domainId",
        "purpose",
        "explicitlyGranted",
        "eligible",
        "grantRevision",
        "accountCredentialRevision",
        "consumerEnabled",
      ]) ||
      v.accountId !== accountId ||
      !validAccountID(v.accountId) ||
      !validDomainID(v.domainId) ||
      v.purpose !== directoryCredentialPurpose ||
      typeof v.explicitlyGranted !== "boolean" ||
      typeof v.eligible !== "boolean" ||
      !revisionOrZero(v.grantRevision) ||
      !validRevision(v.accountCredentialRevision) ||
      typeof v.consumerEnabled !== "boolean" ||
      (v.explicitlyGranted && v.grantRevision === "0") ||
      (v.eligible && !v.explicitlyGranted)
    )
      return invalid();
    return v as unknown as DirectoryCredentialEffective;
  },
  async receipt(
    key: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<DirectoryCredentialReceipt> {
    const v = await transport(
      `/mutation?${new URLSearchParams({ idempotencyKey: key })}`,
      actor,
      signal,
    );
    if (
      !object(v) ||
      !exact(v, ["receipt", "consumerEnabled"]) ||
      typeof v.consumerEnabled !== "boolean" ||
      !validDirectoryCredentialReceipt(v.receipt) ||
      !v.receipt.replayed
    )
      return invalid();
    return v.receipt;
  },
  async mutate(
    kind: DirectoryCredentialMutation,
    input: DirectoryCredentialInput,
    proof: Proof,
    csrf: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<DirectoryCredentialReceipt> {
    const v = await transport(
      `/${kind}`,
      actor,
      signal,
      { ...input, ...proof },
      csrf,
    );
    if (
      !object(v) ||
      !exact(v, [...receiptKeys, "consumerEnabled"]) ||
      typeof v.consumerEnabled !== "boolean"
    )
      return invalid();
    const { consumerEnabled: _, ...receipt } = v;
    if (
      !validDirectoryCredentialReceipt(receipt) ||
      receipt.operation !== kind ||
      receipt.accountId !== input.accountId ||
      receipt.roleId !== input.roleId ||
      receipt.accountCredentialRevision !== input.expectedCredentialRevision ||
      receipt.purpose !== input.purpose
    )
      return invalid();
    return receipt;
  },
};
export const uncertainDirectoryCredentialError = (error: unknown) =>
  !(error instanceof ApiError) ||
  error.status >= 500 ||
  ["network", "invalid_response", "idempotency_conflict"].includes(error.code);
export function directoryCredentialUseError(error: unknown): string {
  const messages: Record<string, string> = {
    ...accessMessages,
    network: "连接中断，授权操作结果尚未确认。请查询原授权回执。",
    invalid_response: "授权结果无法安全确认，请查询原授权回执或重新读取。",
    credential_use_identity_changed: "登录身份已改变，请重新确认登录状态。",
    not_found: "授权记录不存在或当前无权访问；这不能证明原操作未提交。",
    revision_conflict: "账户、凭据或授权版本已改变，请重新读取并核对后操作。",
    idempotency_conflict: "原操作编号已对应其他内容，请查询原授权回执。",
    credential_use_target_unavailable:
      "此角色目前不满足使用条件，不能授予权限。已有授权仍可撤销。",
    unsupported_credential_purpose: "当前只支持登记目录读取用途的使用权限。",
    credential_use_governance_required:
      "此变更涉及凭据使用授权，需要具有该域管理范围的平台管理员重新验证。",
    tenant_expired: "租户已过期，不能新增授权；仍可查看和撤销已有授权。",
    tenant_not_configured: "租户尚未配置，不能新增授权；请核对已有授权。",
    tenant_domain_limit_exceeded:
      "租户域容量超限，不能新增授权；仍可撤销已有授权。",
    domain_capacity_exceeded:
      "租户域容量超限，不能新增授权；仍可撤销已有授权。",
    result_too_large:
      "授权或角色超过读取上限，请联系管理员核对；未展示部分授权。",
    forbidden: "当前账户没有此凭据授权管理权限，请核对登录身份与域范围。",
    schema_incompatible: "服务数据库版本尚未就绪，请稍后重新读取。",
  };
  return error instanceof ApiError
    ? Object.hasOwn(messages, error.code)
      ? messages[error.code]
      : "凭据授权请求失败，请重新读取服务器状态后核对。"
    : "授权操作结果尚未确认，请查询原授权回执。";
}
