import { ApiError } from "./api";
import { readDirectoryV2JSON } from "./directory-v2-json";
import { accessMessages, type PageInfo, type Proof } from "./access-api";
import { integer, object } from "./task-api";
import { validDNS, validDomainID, validRevision } from "./domain-api";
import { validAccountID, validAccountLabel } from "./operation-account-api";

export const directoryV2CredentialPurpose = "domain.directory_read.v2" as const;
export const directoryV2CredentialUseOperations = [
  "GET /api/directory-credential-use/v2/accounts",
  "GET /api/directory-credential-use/v2/grants",
  "GET /api/directory-credential-use/v2/roles",
  "GET /api/directory-credential-use/v2/effective",
  "GET /api/directory-credential-use/v2/mutation",
  "POST /api/directory-credential-use/v2/grant",
  "POST /api/directory-credential-use/v2/revoke",
] as const;
export type DirectoryV2CredentialUseOperation =
  (typeof directoryV2CredentialUseOperations)[number];
export type DirectoryV2CredentialMutation = "grant" | "revoke";
export interface DirectoryV2CredentialAccount {
  accountId: string;
  domainId: string;
  domain: string;
  label: string;
  revision: string;
  credentialRevision: string;
}
export interface DirectoryV2CredentialRole {
  roleId: string;
  roleName: string;
  memberCount: number;
}
export interface DirectoryV2CredentialGrant {
  roleId: string;
  roleName: string;
  purpose: typeof directoryV2CredentialPurpose;
  allowed: boolean;
  grantRevision: string;
  accountCredentialRevision: string;
  updatedAt: string;
}
export interface DirectoryV2CredentialEffective {
  accountId: string;
  domainId: string;
  purpose: typeof directoryV2CredentialPurpose;
  explicitlyGranted: boolean;
  eligible: boolean;
  grantRevision: string;
  accountCredentialRevision: string;
  consumerEnabled: boolean;
}
export interface DirectoryV2CredentialReceipt {
  result: "SUCCESS";
  operation: DirectoryV2CredentialMutation;
  accountId: string;
  domainId: string;
  roleId: string;
  purpose: typeof directoryV2CredentialPurpose;
  grantRevision: string;
  accountCredentialRevision: string;
  allowed: boolean;
  replayed: boolean;
  accountDeleted: boolean;
  currentGrantRevision: string;
  currentAllowed: boolean;
}
export interface DirectoryV2CredentialInput {
  accountId: string;
  roleId: string;
  purpose: typeof directoryV2CredentialPurpose;
  expectedAccountRevision: string;
  expectedCredentialRevision: string;
  expectedGrantRevision: string;
  idempotencyKey: string;
}
const exact = (v: Record<string, unknown>, keys: readonly string[]) =>
  Object.keys(v).length === keys.length &&
  Object.keys(v).every((key) => keys.includes(key));
export const validDirectoryV2CredentialRoleID = (v: unknown): v is string =>
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
export function validDirectoryV2CredentialAccount(
  v: unknown,
): v is DirectoryV2CredentialAccount {
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
export function validDirectoryV2CredentialGrant(
  v: unknown,
): v is DirectoryV2CredentialGrant {
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
    validDirectoryV2CredentialRoleID(v.roleId) &&
    v.roleId !== "viewer" &&
    roleName(v.roleName) &&
    v.purpose === directoryV2CredentialPurpose &&
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
export function validDirectoryV2CredentialReceipt(
  v: unknown,
): v is DirectoryV2CredentialReceipt {
  return (
    object(v) &&
    exact(v, receiptKeys) &&
    v.result === "SUCCESS" &&
    ["grant", "revoke"].includes(String(v.operation)) &&
    validAccountID(v.accountId) &&
    validDomainID(v.domainId) &&
    validDirectoryV2CredentialRoleID(v.roleId) &&
    v.roleId !== "viewer" &&
    v.purpose === directoryV2CredentialPurpose &&
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
    response = await fetch(`/api/directory-credential-use/v2${path}`, {
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
  ) {
    await response.body?.cancel().catch(() => undefined);
    throw new ApiError("credential_use_identity_changed", 401);
  }
  const data = await readDirectoryV2JSON(response, signal);
  signal.throwIfAborted();
  if (!response.ok)
    throw new ApiError(
      object(data) && exact(data, ["error"]) && typeof data.error === "string"
        ? data.error
        : "internal",
      response.status,
    );
  return data;
}
const accountQuery = (accountId: string) => new URLSearchParams({ accountId });
export const directoryV2CredentialUseAPI = {
  async accounts(
    query: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<{
    page: PageInfo;
    List: DirectoryV2CredentialAccount[];
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
      !v.List.every(validDirectoryV2CredentialAccount) ||
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
      List: DirectoryV2CredentialAccount[];
      exhausted: boolean;
      consumerEnabled: boolean;
    };
  },
  async grants(
    accountId: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<{
    account: DirectoryV2CredentialAccount;
    grants: DirectoryV2CredentialGrant[];
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
      !validDirectoryV2CredentialAccount(v.account) ||
      v.account.accountId !== accountId ||
      !Array.isArray(v.grants) ||
      v.grants.length > 1000 ||
      !v.grants.every(validDirectoryV2CredentialGrant) ||
      new Set(v.grants.map((g) => g.roleId)).size !== v.grants.length ||
      v.grants.some(
        (g) =>
          g.allowed &&
          g.accountCredentialRevision !==
            (v.account as DirectoryV2CredentialAccount).credentialRevision,
      )
    )
      return invalid();
    return v as unknown as {
      account: DirectoryV2CredentialAccount;
      grants: DirectoryV2CredentialGrant[];
      consumerEnabled: boolean;
    };
  },
  async roles(
    accountId: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<DirectoryV2CredentialRole[]> {
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
          validDirectoryV2CredentialRoleID(r.roleId) &&
          r.roleId !== "viewer" &&
          roleName(r.roleName) &&
          integer(r.memberCount, 0),
      ) ||
      new Set(v.roles.map((r) => r.roleId)).size !== v.roles.length
    )
      return invalid();
    return v.roles as DirectoryV2CredentialRole[];
  },
  async effective(
    accountId: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<DirectoryV2CredentialEffective> {
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
      v.purpose !== directoryV2CredentialPurpose ||
      typeof v.explicitlyGranted !== "boolean" ||
      typeof v.eligible !== "boolean" ||
      !revisionOrZero(v.grantRevision) ||
      !validRevision(v.accountCredentialRevision) ||
      typeof v.consumerEnabled !== "boolean" ||
      (v.explicitlyGranted && v.grantRevision === "0") ||
      (v.eligible && !v.explicitlyGranted)
    )
      return invalid();
    return v as unknown as DirectoryV2CredentialEffective;
  },
  async receipt(
    key: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<DirectoryV2CredentialReceipt> {
    const v = await transport(
      `/mutation?${new URLSearchParams({ idempotencyKey: key })}`,
      actor,
      signal,
    );
    if (
      !object(v) ||
      !exact(v, ["receipt", "consumerEnabled"]) ||
      typeof v.consumerEnabled !== "boolean" ||
      !validDirectoryV2CredentialReceipt(v.receipt) ||
      !v.receipt.replayed
    )
      return invalid();
    return v.receipt;
  },
  async mutate(
    kind: DirectoryV2CredentialMutation,
    input: DirectoryV2CredentialInput,
    proof: Proof,
    csrf: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<DirectoryV2CredentialReceipt> {
    if (
      !["grant", "revoke"].includes(kind) ||
      input.purpose !== directoryV2CredentialPurpose ||
      !validAccountID(input.accountId) ||
      !validDirectoryV2CredentialRoleID(input.roleId) ||
      input.roleId === "viewer" ||
      !validRevision(input.expectedAccountRevision) ||
      !validRevision(input.expectedCredentialRevision) ||
      !(
        validRevision(input.expectedGrantRevision) ||
        (kind === "grant" && input.expectedGrantRevision === "0")
      ) ||
      typeof input.idempotencyKey !== "string" ||
      !/^[A-Za-z0-9_-]{8,128}$/u.test(input.idempotencyKey)
    )
      throw new ApiError("invalid_input");
    const v = await transport(
      `/${kind}`,
      actor,
      signal,
      {
        accountId: input.accountId,
        roleId: input.roleId,
        purpose: directoryV2CredentialPurpose,
        expectedAccountRevision: input.expectedAccountRevision,
        expectedCredentialRevision: input.expectedCredentialRevision,
        expectedGrantRevision: input.expectedGrantRevision,
        idempotencyKey: input.idempotencyKey,
        actorPassword: proof.actorPassword,
        totpCode: proof.totpCode,
      },
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
      !validDirectoryV2CredentialReceipt(receipt) ||
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
export const uncertainDirectoryV2CredentialError = (error: unknown) =>
  !(error instanceof ApiError) ||
  error.status >= 500 ||
  (error.code === "directory_limit_exceeded" &&
    error.status >= 200 &&
    error.status < 300) ||
  ["network", "invalid_response", "idempotency_conflict"].includes(error.code);
export function directoryV2CredentialUseError(error: unknown): string {
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
