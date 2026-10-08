import { ApiError, messages } from "./api";
import { accessMessages, type PageInfo } from "./access-api";

export interface ResourceData {
  appName: "ad";
  resources: string[];
}
export interface ResourceInput {
  name: string;
  mark: string;
  datas: ResourceData[];
}
export interface ResourceMeta extends ResourceInput {
  id: string;
  applyRoleCount: number;
  createTime: string;
}
export interface ResourceList {
  page: PageInfo;
  metas: ResourceMeta[];
  exhausted: boolean;
}
export interface AssociatedRole {
  id: string;
  name: string;
  mark: string;
}
export interface AssociatedRoles {
  page: PageInfo;
  details: AssociatedRole[];
  exhausted: boolean;
}
export interface TenantConfig {
  maxAdCount: number;
  expireTime: number;
  uid: string;
  name: string;
}
export interface ResourceGrants {
  list: { application: "ad"; resources: string[] }[];
  authorizationScopeOnly: true;
}
export interface ResourceCheck {
  resourceType: 2;
  resources: { application: "ad"; dataResource: string[] }[];
}
export interface ResourceChecks {
  results: boolean[];
  authorizationScopeOnly: true;
}
export interface ResourceMutation {
  result: "SUCCESS";
  sessionRevoked: boolean;
  id?: string;
}
export const resourceOperations = [
  "GET /api/resources/groups",
  "GET /api/resources/groups/detail",
  "GET /api/resources/groups/exists",
  "GET /api/resources/groups/roles",
  "POST /api/resources/groups/create",
  "POST /api/resources/groups/update",
  "POST /api/resources/groups/delete",
  "POST /api/resources/groups/assign",
  "GET /api/resources/tenant",
  "POST /api/resources/tenant/save",
  "GET /api/resources/grants",
  "POST /api/resources/check",
] as const;
export type ResourceOperation = (typeof resourceOperations)[number];
export async function resourceRequest<T>(
  path: string,
  signal: AbortSignal,
  body?: unknown,
  csrfToken?: string,
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api/resources${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      cache: "no-store",
      signal,
      headers:
        body === undefined
          ? {}
          : {
              "Content-Type": "application/json",
              ...(csrfToken ? { "X-CSRF-Token": csrfToken } : {}),
            },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  } catch (error) {
    if (signal.aborted) throw error;
    throw new ApiError("network");
  }
  const data = await response.json().catch(() => {
    throw new ApiError("invalid_response", response.status);
  });
  if (!response.ok)
    throw new ApiError(
      typeof data?.error === "string" ? data.error : "internal",
      response.status,
    );
  return data as T;
}
export const resourceMessages: Record<string, string> = {
  ...accessMessages,
  resource_delegation_forbidden:
    "不能管理或委派超出当前角色已有范围的域，或更改平台管理员关联。请重新核对服务器授权。",
  own_resource_scope_forbidden:
    "不能修改当前角色自身的资源范围或关联；请由其他获授权管理员处理。",
  tenant_not_configured:
    "当前租户尚未配置，数据访问默认拒绝。请由平台管理员完成配置。",
  tenant_expired:
    "当前租户配置已过期，数据访问已停用。平台管理员仍可修改配置。",
  tenant_domain_limit_exceeded:
    "活动域数量超过租户配置上限，数据访问已停用。请由平台管理员核对配置。",
  domain_limit_below_existing:
    "域数量上限不能低于当前活动域数量；本操作不会删除域。",
  unavailable_resource:
    "包含不可用的域 ID。只能关联当前租户已存在且活动的域，不能在此创建域。",
  unsupported_resource_type:
    "仅支持数据资源类型 2；类型 0 和 1 没有安全的已实现语义。",
  name_conflict: "资源组名称已存在（不区分大小写），请使用其他名称。",
  forbidden: "服务器拒绝此操作。请核对功能权限、当前角色和可委派的域范围。",
};
export const resourceErrorText = (error: unknown) =>
  error instanceof ApiError
    ? (resourceMessages[error.code] ?? `${messages.internal}（${error.code}）`)
    : messages.internal;
const control = /\p{Cc}/u;
export const validResourceID = (id: string) =>
  /^[A-Za-z0-9_.-]{1,128}$/.test(id);
export const validGroupName = (name: string) =>
  name.length > 0 &&
  new TextEncoder().encode(name).length <= 256 &&
  !/[\p{White_Space}\p{Cc}]/u.test(name);
export const validResourceMark = (mark: string) =>
  Array.from(mark).length <= 500 && !control.test(mark);
export function parseResourceIDs(value: string, max = 1000): string[] | null {
  const ids = value.trim() ? value.trim().split(/[\s,]+/u) : [];
  return ids.length <= max &&
    ids.every(validResourceID) &&
    new Set(ids).size === ids.length
    ? ids
    : null;
}
export function parseCustomRoleIDs(value: string): string[] | null {
  const ids = value.trim() ? value.trim().split(/[\s,]+/u) : [];
  return ids.length <= 100 &&
    ids.every((id) => /^[A-Za-z0-9_-]{24}$/.test(id)) &&
    new Set(ids).size === ids.length
    ? ids
    : null;
}
export function parseChecks(value: string): ResourceCheck | null {
  const lines = value.trim().split(/\r?\n/u);
  if (!value.trim() || lines.length > 100) return null;
  const groups = lines.map((line) => parseResourceIDs(line));
  if (groups.some((ids) => !ids || !ids.length)) return null;
  return {
    resourceType: 2,
    resources: groups.map((ids) => ({ application: "ad", dataResource: ids! })),
  };
}
export function parseTenant(
  values: Record<string, string>,
): TenantConfig | null {
  const maxAdCount = Number(values.maxAdCount),
    expireTime = Number(values.expireTime);
  const text = (value: string, max: number) =>
    !!value &&
    value === value.trim() &&
    Array.from(value).length <= max &&
    !control.test(value);
  if (
    !/^\d+$/u.test(values.maxAdCount) ||
    !/^\d+$/u.test(values.expireTime) ||
    !Number.isSafeInteger(maxAdCount) ||
    maxAdCount > 100000 ||
    !Number.isSafeInteger(expireTime) ||
    expireTime > 253402300799 ||
    !text(values.uid, 256) ||
    !text(values.name, 32)
  )
    return null;
  return { maxAdCount, expireTime, uid: values.uid, name: values.name };
}
export const groupDomainIDs = (group: ResourceInput) =>
  group.datas.flatMap((data) => data.resources);
