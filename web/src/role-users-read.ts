import { ApiError, validUsername, type Profile } from "./api";
import {
  queryString,
  validText,
  type AccessUser,
  type Role,
  type UserList,
} from "./access-api";
import type { AccessContext } from "./access-common";

export const roleUsersPageSizes = [10, 20, 30, 40, 50] as const;
export interface RoleUsersQuery {
  roleID: string;
  pageIdx: number;
  pageSize: number;
  search: string;
}
const object = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value);
const integer = (
  value: unknown,
  minimum: number,
  maximum = Number.MAX_SAFE_INTEGER,
): value is number =>
  typeof value === "number" &&
  Number.isSafeInteger(value) &&
  value >= minimum &&
  value <= maximum;
const exact = (value: Record<string, unknown>, keys: string[]) =>
  Object.keys(value).length === keys.length &&
  Object.keys(value).every((key) => keys.includes(key));
const validRoleID = (value: unknown): value is string =>
  typeof value === "string" &&
  /^(?:viewer|platform_admin|[A-Za-z0-9_-]{24})$/.test(value);
export const validRoleUsersTarget = (role: Role) =>
  object(role) &&
  validRoleID(role.id) &&
  typeof role.name === "string" &&
  role.name.length > 0 &&
  validText(role.name, 50) &&
  integer(role.userNum, 0);
export const canReadRoleUsers = (context: AccessContext) =>
  context.menu.some(
    (node) =>
      object(node) &&
      node.mark === "users" &&
      object(node.auth) &&
      node.auth.readable === true,
  ) && context.can("GET /api/access/users");
// Kept only in component memory. A CSRF rotation is a new session even for the same actor.
export const roleUsersIdentity = (profile: Profile) =>
  JSON.stringify([
    profile.ID,
    profile.username,
    profile.role,
    profile.priv,
    profile.csrfToken,
    profile.needChangePwd,
    profile.isExpired,
  ]);
const validSession = (profile: Profile) =>
  integer(profile.ID, 1) &&
  validUsername(profile.username) &&
  typeof profile.csrfToken === "string" &&
  !!profile.csrfToken &&
  profile.needChangePwd === false &&
  profile.isExpired === false;
export const validRoleUsersSearch = (search: string) =>
  validText(search, 50) && !/[\p{Cc}\p{Cs}]/u.test(search);
const validQuery = (query: RoleUsersQuery) =>
  validRoleID(query.roleID) &&
  integer(query.pageIdx, 1, 1_000_000) &&
  roleUsersPageSizes.some((size) => size === query.pageSize) &&
  typeof query.search === "string" &&
  validRoleUsersSearch(query.search);
const stringFields = [
  "username",
  "passStrength",
  "role",
  "mobile",
  "email",
  "remark",
  "createTm",
  "avatar",
  "pwdUpdateTm",
  "address",
  "realName",
  "department",
  "post",
  "roleID",
  "roleName",
];
function validUser(value: unknown, roleID: string): value is AccessUser {
  if (
    !object(value) ||
    !exact(value, [...stringFields, "ID", "priv", "hasMfa", "disabled"]) ||
    !stringFields.every((key) => typeof value[key] === "string") ||
    !integer(value.ID, 1) ||
    !validUsername(value.username as string) ||
    value.roleID !== roleID ||
    typeof value.hasMfa !== "boolean" ||
    typeof value.disabled !== "boolean" ||
    !["high", "middle", "low"].includes(value.passStrength as string) ||
    !["viewer", "platform_admin"].includes(value.role as string) ||
    value.priv !==
      (roleID === "platform_admin" ? 1 : roleID === "viewer" ? 3 : 2) ||
    value.role !==
      (roleID === "platform_admin" ? "platform_admin" : "viewer") ||
    !value.roleName ||
    !validText(value.roleName as string, 50)
  )
    return false;
  return [value.createTm, value.pwdUpdateTm].every(
    (value) =>
      typeof value === "string" &&
      /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value) &&
      Number.isFinite(Date.parse(value)),
  );
}
export function parseRoleUsers(
  value: unknown,
  query: RoleUsersQuery,
): UserList {
  const invalid = (): never => {
    throw new ApiError("invalid_response");
  };
  if (
    !validQuery(query) ||
    !object(value) ||
    !exact(value, ["page", "List", "exhausted"]) ||
    !object(value.page) ||
    !exact(value.page, ["pageIdx", "pageSize", "total", "totalPage"]) ||
    value.page.pageIdx !== query.pageIdx ||
    value.page.pageSize !== query.pageSize ||
    !integer(value.page.total, 0) ||
    value.page.totalPage !== Math.ceil(value.page.total / query.pageSize) ||
    !Array.isArray(value.List) ||
    !value.List.every((row) => validUser(row, query.roleID)) ||
    typeof value.exhausted !== "boolean"
  )
    return invalid();
  const rows = value.List as AccessUser[],
    offset = (query.pageIdx - 1) * query.pageSize;
  if (
    rows.length !==
      Math.min(query.pageSize, Math.max(0, value.page.total - offset)) ||
    value.exhausted !== offset + rows.length >= value.page.total ||
    new Set(rows.map((row) => row.ID)).size !== rows.length ||
    new Set(rows.map((row) => row.username)).size !== rows.length
  )
    return invalid();
  return value as unknown as UserList;
}
async function json(path: string, signal: AbortSignal): Promise<unknown> {
  signal.throwIfAborted();
  let response: Response;
  try {
    response = await fetch(path, {
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
      signal,
    });
  } catch (error) {
    signal.throwIfAborted();
    throw error instanceof ApiError ? error : new ApiError("network");
  }
  signal.throwIfAborted();
  if (response.status === 401) {
    await response.body?.cancel().catch(() => undefined);
    throw new ApiError("unauthenticated", 401);
  }
  const value: unknown = await response.json().catch(() => {
    throw new ApiError(
      response.status === 403 ? "forbidden" : "invalid_response",
      response.status,
    );
  });
  signal.throwIfAborted();
  if (!response.ok) {
    const code =
      object(value) && typeof value.error === "string"
        ? value.error
        : "internal";
    throw new ApiError(
      response.status === 403 && code !== "password_change_required"
        ? "forbidden"
        : code,
      response.status,
    );
  }
  return value;
}
async function verifySession(profile: Profile, signal: AbortSignal) {
  const current = await json("/api/auth/me", signal);
  if (
    !validSession(profile) ||
    !object(current) ||
    [
      "ID",
      "username",
      "role",
      "priv",
      "csrfToken",
      "needChangePwd",
      "isExpired",
    ].some((key) => current[key] !== profile[key as keyof Profile])
  )
    throw new ApiError("unauthenticated", 401);
}
export async function readRoleUsers(
  query: RoleUsersQuery,
  profile: Profile,
  signal: AbortSignal,
): Promise<UserList> {
  if (!validQuery(query)) throw new ApiError("invalid_input");
  if (!validSession(profile)) throw new ApiError("unauthenticated", 401);
  // Snapshot caller-owned objects before awaiting. These checks guard presentation;
  // they cannot make browser cookie changes and the users GET atomic.
  const expected = { ...profile },
    selected = { ...query };
  await verifySession(expected, signal);
  const result = parseRoleUsers(
    await json(
      `/api/access/users?${queryString({ ...selected, sort: -1 })}`,
      signal,
    ),
    selected,
  );
  await verifySession(expected, signal);
  signal.throwIfAborted();
  return result;
}
