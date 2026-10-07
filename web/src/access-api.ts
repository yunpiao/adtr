import { ApiError, messages } from "./api";

export type Mark = "users" | "roles" | "permissions";
export interface Grant {
  readable: boolean;
  writeable: boolean;
}
export interface PermissionInput {
  mark: Mark;
  auth: Grant;
}
export interface Permission extends PermissionInput {
  name: string;
  children: Permission[];
  paths: { name: string; url: string; auth: string }[];
  checked: boolean;
  allow_auth: Grant;
  icon: string;
}
export interface PageInfo {
  pageIdx: number;
  pageSize: number;
  total: number;
  totalPage: number;
}
export interface AccessUser {
  ID: number;
  username: string;
  passStrength: string;
  role: string;
  priv: number;
  mobile: string;
  email: string;
  remark: string;
  createTm: string;
  hasMfa: boolean;
  avatar: string;
  pwdUpdateTm: string;
  address: string;
  realName: string;
  department: string;
  post: string;
  roleID: string;
  roleName: string;
  disabled: boolean;
}
export interface Role {
  id: string;
  name: string;
  userNum: number;
  remark: string;
  allowDelete: boolean;
  allowEdit: boolean;
  created: string;
  dataSrc: Record<string, never>;
}
export interface UserList {
  page: PageInfo;
  List: AccessUser[];
  exhausted: boolean;
}
export interface RoleList {
  page: PageInfo;
  list: Role[];
  exhausted: boolean;
}
export interface Mutation {
  result: "SUCCESS";
  sessionRevoked: boolean;
  roleID?: string;
  ID?: number;
}
export interface Proof {
  actorPassword: string;
  totpCode: string;
}
export const marks: Mark[] = ["users", "roles", "permissions"];
export const labels: Record<Mark, string> = {
  users: "用户管理",
  roles: "角色管理",
  permissions: "功能权限",
};
export const operations = [
  "GET /api/access/users",
  "GET /api/access/users/exists",
  "POST /api/access/users/create",
  "POST /api/access/users/update",
  "POST /api/access/users/delete",
  "GET /api/access/roles",
  "GET /api/access/roles/detail",
  "GET /api/access/roles/exists",
  "POST /api/access/roles/save",
  "POST /api/access/roles/delete",
  "POST /api/access/assignments",
  "GET /api/access/permissions",
  "POST /api/access/permissions/save",
] as const;
export type Operation = (typeof operations)[number];
export type Can = (operation: Operation) => boolean;
export async function accessRequest<T>(
  path: string,
  signal: AbortSignal,
  body?: unknown,
  csrfToken?: string,
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api/access${path}`, {
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
export const accessMessages: Record<string, string> = {
  ...messages,
  mfa_required: "请先在账户安全中启用多因素认证，再执行管理操作。",
  invalid_credentials:
    "操作者密码或验证码不正确，或验证码已使用。请等待下一个未使用的验证码。",
  conflict: "数据已存在或已改变，请刷新列表后核对。",
  already_exists: "该用户名或角色名称已存在。",
  not_found: "目标不存在，可能已被删除。请刷新列表。",
  no_change: "未检测到修改，请调整内容后提交。",
  no_changes: "未检测到修改，请调整内容后提交。",
  role_in_use: "角色仍分配给用户，请先调整用户角色。",
  builtin_role: "内置角色不能修改或删除。",
  immutable_role_name: "角色名称创建后不能修改。",
  last_administrator: "不能移除最后一个启用的平台管理员。",
  self_protection: "不能删除或停用当前登录账户。",
  self_role_change_forbidden: "自定义角色账户不能修改自己所属角色。",
  immutable_role: "内置角色不能修改或删除。",
  result_too_large: "结果超过 1,000 条，请缩小筛选范围或改用分页。",
  unsupported_mfa_status: "不支持 MFA disable 策略；请使用已开启或已关闭筛选。",
  unsupported_data_source_scope: "本切片不支持数据源范围配置。",
  last_admin: "不能移除最后一个启用的平台管理员。",
  self_delete: "不能删除当前登录账户。",
  self_disable: "不能停用当前登录账户。",
};
export function errorText(error: unknown) {
  return error instanceof ApiError
    ? (accessMessages[error.code] ?? `${messages.internal}（${error.code}）`)
    : messages.internal;
}
export type QueryValue = string | number | boolean | string[] | undefined;
export function queryString(values: Record<string, QueryValue>) {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) {
    if (value === undefined || value === "") continue;
    for (const item of Array.isArray(value) ? value : [value])
      query.append(key, String(item));
  }
  return query.toString();
}
export function permissionInputs(
  permissions: PermissionInput[],
): PermissionInput[] {
  return permissions.map(({ mark, auth }) => ({
    mark,
    auth: { readable: auth.readable, writeable: auth.writeable },
  }));
}
export const validText = (value: string, max: number) =>
  Array.from(value).length <= max && !/[\u0000-\u001f\u007f]/u.test(value);
export function validUserFields(values: Record<string, string | undefined>) {
  return (
    ["address", "realName", "department", "post"].every((key) =>
      validText(values[key] ?? "", 50),
    ) &&
    validText(values.remark ?? "", 150) &&
    (!values.mobile || /^1[3-9][0-9]{9}$/.test(values.mobile)) &&
    (!values.email ||
      (new TextEncoder().encode(values.email).length <= 254 &&
        /^[^\s@<>]+@[^\s@<>]+$/.test(values.email)))
  );
}
