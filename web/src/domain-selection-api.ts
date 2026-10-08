import { ApiError } from "./api";
import { type PageInfo, queryString } from "./access-api";
import {
  connectionStates,
  diagnosticMessages,
  validDNS,
  validDomainID,
  validIP,
  validRevision,
  type Connection,
} from "./domain-api";
import { integer, object } from "./task-api";

export interface SourceChoice {
  domainId: string;
  domain: string;
  revision: string;
  credentialRevision: string;
  dcHostName: string;
  ldapAddr: string;
  port: Connection["port"];
  mode: Connection["mode"];
  credentialConfigured: boolean;
  connectionState: Connection["connectionState"];
  source: "configured_connection";
  lastTest: null | { observedAt: string; dcHostName: string; code: string };
}
export interface SourcePage {
  page: PageInfo;
  List: SourceChoice[];
  exhausted: boolean;
}
export interface SourceQuery {
  pageIdx: number;
  pageSize: number;
  keyword: string;
  observationState: "" | SourceChoice["connectionState"];
}
export interface ResolvedSource {
  selection: SourceChoice;
  checkedAt: string;
}
const exact = (v: Record<string, unknown>, keys: string[]) =>
  Object.keys(v).length === keys.length &&
  Object.keys(v).every((k) => keys.includes(k));
// RFC3339 UTC with Go's bounded nanosecond precision; reject normalized invalid
// dates such as February 30 rather than trusting Date.parse's rollover.
const utc = (v: unknown): v is string => {
  if (
    typeof v !== "string" ||
    !/^(?!0000)\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/u.test(v)
  )
    return false;
  const time = Date.parse(v);
  return (
    Number.isFinite(time) &&
    new Date(time).toISOString().slice(0, 19) === v.slice(0, 19)
  );
};
export const validSourceKeyword = (value: string) =>
  Array.from(value).length <= 50 &&
  !/[\u0000-\u001f\u007f-\u009f\ud800-\udfff]/u.test(value);
const pageSizes = [10, 20, 30, 40, 50];
export function validSourceChoice(v: unknown): v is SourceChoice {
  if (
    !object(v) ||
    !exact(v, [
      "domainId",
      "domain",
      "revision",
      "credentialRevision",
      "dcHostName",
      "ldapAddr",
      "port",
      "mode",
      "credentialConfigured",
      "connectionState",
      "source",
      "lastTest",
    ]) ||
    !validDomainID(v.domainId) ||
    typeof v.domain !== "string" ||
    !validDNS(v.domain) ||
    v.domain !== v.domain.toLowerCase().replace(/\.$/u, "") ||
    !validRevision(v.revision) ||
    !validRevision(v.credentialRevision) ||
    typeof v.dcHostName !== "string" ||
    !validDNS(v.dcHostName) ||
    typeof v.ldapAddr !== "string" ||
    v.ldapAddr.length > 45 ||
    !validIP(v.ldapAddr) ||
    !(
      (v.port === "389" && v.mode === "starttls") ||
      (v.port === "636" && v.mode === "ldaps")
    ) ||
    typeof v.credentialConfigured !== "boolean" ||
    !connectionStates.includes(
      v.connectionState as SourceChoice["connectionState"],
    ) ||
    v.source !== "configured_connection"
  )
    return false;
  if (v.lastTest !== null) {
    const t = v.lastTest;
    if (
      !object(t) ||
      !exact(t, ["observedAt", "dcHostName", "code"]) ||
      !utc(t.observedAt) ||
      typeof t.dcHostName !== "string" ||
      typeof t.code !== "string" ||
      !Object.hasOwn(diagnosticMessages, t.code) ||
      (t.code === "ok" ? !validDNS(t.dcHostName) : t.dcHostName !== "")
    )
      return false;
  }
  return v.connectionState === "verified"
    ? v.lastTest !== null &&
        (v.lastTest as SourceChoice["lastTest"])?.code === "ok"
    : (v.lastTest as SourceChoice["lastTest"])?.code !== "ok";
}
const invalid = (): never => {
  throw new ApiError("invalid_response");
};
export function parseSourcePage(
  value: unknown,
  query: SourceQuery,
): SourcePage {
  if (
    !object(value) ||
    !exact(value, ["page", "List", "exhausted"]) ||
    !object(value.page) ||
    !exact(value.page, ["pageIdx", "pageSize", "total", "totalPage"]) ||
    !integer(value.page.pageIdx, 1, 1000000) ||
    value.page.pageIdx !== query.pageIdx ||
    !pageSizes.includes(value.page.pageSize as number) ||
    value.page.pageSize !== query.pageSize ||
    !integer(value.page.total, 0) ||
    !integer(value.page.totalPage, 0) ||
    value.page.totalPage !== Math.ceil(value.page.total / query.pageSize) ||
    !Array.isArray(value.List) ||
    value.List.length > query.pageSize ||
    !value.List.every(validSourceChoice) ||
    new Set(value.List.map((c) => c.domainId)).size !== value.List.length ||
    typeof value.exhausted !== "boolean"
  )
    return invalid();
  const offset = (query.pageIdx - 1) * query.pageSize;
  if (
    value.List.length !==
      Math.min(query.pageSize, Math.max(0, value.page.total - offset)) ||
    value.exhausted !== offset + value.List.length >= value.page.total ||
    (query.observationState &&
      value.List.some((c) => c.connectionState !== query.observationState))
  )
    return invalid();
  return value as unknown as SourcePage;
}
export function parseResolvedSource(
  value: unknown,
  expected: SourceChoice,
): ResolvedSource {
  if (
    !object(value) ||
    !exact(value, ["selection", "checkedAt"]) ||
    !validSourceChoice(value.selection) ||
    !utc(value.checkedAt) ||
    value.selection.domainId !== expected.domainId ||
    value.selection.revision !== expected.revision ||
    value.selection.credentialRevision !== expected.credentialRevision
  )
    return invalid();
  return value as unknown as ResolvedSource;
}
const errorMessages: Record<string, string> = {
  invalid_input: "请检查数据源筛选条件。",
  unsupported_source_filter: "此数据源选择器尚不支持该筛选条件。",
  selection_changed:
    "配置或凭据已改变，已清除选择并刷新列表。请核对后重新选择。",
  unauthenticated: "登录已失效，请重新登录。",
  password_change_required: "请先修改密码，再选择数据源。",
  forbidden: "当前账户已无数据源访问权限，已清除选择。",
  not_found: "此数据源已不可用或不在当前授权范围内，已清除选择。",
  tenant_not_configured: "当前租户尚未配置，无法选择数据源。",
  tenant_expired: "当前租户已过期，无法选择数据源。",
  tenant_domain_limit_exceeded: "当前租户域数量超出限制，无法选择数据源。",
  schema_incompatible: "服务端数据版本不兼容，请联系管理员。",
  schema_unavailable: "服务端数据版本暂时无法确认，请稍后重试。",
  network: "无法读取数据源，请重试。",
  invalid_response: "数据源响应无法安全确认，请重新读取。",
  internal: "暂时无法读取数据源，请稍后重试。",
};
export const sourceError = (error: unknown) =>
  error instanceof ApiError && Object.hasOwn(errorMessages, error.code)
    ? errorMessages[error.code]
    : errorMessages.internal;
async function read(
  path: string,
  signal: AbortSignal,
  userID: number,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(`/api/domain-selection${path}`, {
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
      signal,
    });
  } catch (error) {
    if (signal.aborted) throw error;
    throw new ApiError("network");
  }
  // The session cookie can change in another tab before React sees a new
  // profile. Successful metadata must belong to the same authenticated actor.
  if (response.ok && response.headers.get("X-ADTR-User-ID") !== String(userID))
    throw new ApiError("unauthenticated", 401);
  const value: unknown = await response.json().catch(() => invalid());
  if (!response.ok) {
    const code =
      object(value) &&
      exact(value, ["error"]) &&
      typeof value.error === "string" &&
      Object.hasOwn(errorMessages, value.error)
        ? value.error
        : "internal";
    throw new ApiError(code, response.status);
  }
  return value;
}
export const sourceAPI = {
  async list(
    query: SourceQuery,
    signal: AbortSignal,
    userID: number,
  ): Promise<SourcePage> {
    if (
      !integer(query.pageIdx, 1, 1000000) ||
      !integer(userID, 1) ||
      !pageSizes.includes(query.pageSize) ||
      !validSourceKeyword(query.keyword) ||
      (query.observationState !== "" &&
        !connectionStates.includes(query.observationState))
    )
      throw new ApiError("invalid_input");
    return parseSourcePage(
      await read(`?${queryString({ ...query })}`, signal, userID),
      query,
    );
  },
  async resolve(
    choice: SourceChoice,
    signal: AbortSignal,
    userID: number,
  ): Promise<ResolvedSource> {
    if (!validSourceChoice(choice) || !integer(userID, 1))
      throw new ApiError("invalid_input");
    const query = new URLSearchParams({
      domainId: choice.domainId,
      expectedRevision: choice.revision,
      expectedCredentialRevision: choice.credentialRevision,
    });
    return parseResolvedSource(
      await read(`/resolve?${query}`, signal, userID),
      choice,
    );
  },
};
