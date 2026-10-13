import { ApiError, type Profile } from "./api";
import { type PageInfo } from "./access-api";
import { validDomainID } from "./domain-api";
import { parseResolvedSource, type SourceChoice } from "./domain-selection-api";
import {
  validDirectoryV2Source,
  type DirectoryV2Source,
} from "./directory-v2-api";
import { readDirectoryV2JSON } from "./directory-v2-json";
import {
  validDirectoryV4Object,
  type DirectoryV4Object,
} from "./directory-v4-facts";
import {
  parseGroupMembersV4,
  validGroupMembersV4Query,
  type GroupMembersV4Query,
} from "./group-members-v4-api";
import { integer, object } from "./task-api";

export const groupMembersV4ReadOperations = [
  "GET /api/directory/v4/observation",
  "GET /api/group-assets/v4/members",
  "GET /api/domain-selection",
  "GET /api/domain-selection/resolve",
] as const;
export interface GroupMembersV4PageQuery {
  domainId: string;
  observationId?: string;
  pageIdx: number;
  pageSize: number;
}
export interface GroupMembersV4Page {
  dictionaryVersion: 4;
  available: boolean;
  observationId?: string;
  source?: DirectoryV2Source;
  list: DirectoryV4Object[];
  page: PageInfo;
}
const exact = (v: Record<string, unknown>, keys: string[]) =>
  Object.keys(v).length === keys.length &&
  Object.keys(v).every((k) => keys.includes(k));
const invalid = (): never => {
  throw new ApiError("invalid_response");
};
export function parseGroupMembersV4Page(
  v: unknown,
  q: GroupMembersV4PageQuery,
): GroupMembersV4Page {
  if (
    !object(v) ||
    v.dictionaryVersion !== 4 ||
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
    v.page.totalPage !== Math.ceil(v.page.total / q.pageSize) ||
    !Array.isArray(v.list) ||
    !v.list.every(validDirectoryV4Object) ||
    v.list.length !==
      Math.min(
        q.pageSize,
        Math.max(0, v.page.total - (q.pageIdx - 1) * q.pageSize),
      )
  )
    return invalid();
  if (
    v.available
      ? !validDomainID(v.observationId) ||
        !validDirectoryV2Source(v.source) ||
        !!(q.observationId && q.observationId !== v.observationId)
      : !!q.observationId || v.page.total !== 0
  )
    return invalid();
  const rows = v.list as DirectoryV4Object[];
  if (
    rows.some(
      (r, i) =>
        r.kind !== "group" || (i > 0 && rows[i - 1].objectGUID >= r.objectGUID),
    )
  )
    return invalid();
  return v as unknown as GroupMembersV4Page;
}
async function json(
  path: string,
  signal: AbortSignal,
  actor?: number,
  body?: unknown,
  csrf?: string,
): Promise<unknown> {
  signal.throwIfAborted();
  let response: Response;
  try {
    response = await fetch(path, {
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
  } catch (e) {
    signal.throwIfAborted();
    throw e instanceof ApiError ? e : new ApiError("network");
  }
  signal.throwIfAborted();
  // Status is authoritative even when an error body is malformed or oversized.
  if (!response.ok && [401, 403, 404, 409].includes(response.status)) {
    await response.body?.cancel().catch(() => undefined);
    throw new ApiError(
      (
        {
          401: "unauthenticated",
          403: "forbidden",
          404: "not_found",
          409: "selection_changed",
        } as Record<number, string>
      )[response.status],
      response.status,
    );
  }
  if (
    response.ok &&
    actor !== undefined &&
    response.headers.get("X-ADTR-User-ID") !== String(actor)
  ) {
    await response.body?.cancel().catch(() => undefined);
    throw new ApiError("unauthenticated", 401);
  }
  const value = await readDirectoryV2JSON(response, signal);
  signal.throwIfAborted();
  if (!response.ok) throw new ApiError("read_failed", response.status);
  return value;
}
async function session(profile: Profile, signal: AbortSignal) {
  const current = await json("/api/auth/me", signal);
  if (
    !object(current) ||
    !integer(profile.ID, 1) ||
    !profile.csrfToken ||
    [
      "ID",
      "username",
      "role",
      "priv",
      "csrfToken",
      "needChangePwd",
      "isExpired",
    ].some((k) => current[k] !== profile[k as keyof Profile]) ||
    current.needChangePwd !== false ||
    current.isExpired !== false
  )
    throw new ApiError("unauthenticated", 401);
}
async function resolveSource(
  choice: SourceChoice,
  profile: Profile,
  signal: AbortSignal,
) {
  const query = new URLSearchParams({
    domainId: choice.domainId,
    expectedRevision: choice.revision,
    expectedCredentialRevision: choice.credentialRevision,
  });
  return parseResolvedSource(
    await json(`/api/domain-selection/resolve?${query}`, signal, profile.ID),
    choice,
  );
}
export const groupMembersV4Read = {
  async authorize(profile: Profile, signal: AbortSignal) {
    await session(profile, signal);
    const menu = await json("/api/access/menu", signal);
    const checks = await json(
      "/api/access/check",
      signal,
      undefined,
      { paths: groupMembersV4ReadOperations },
      profile.csrfToken,
    );
    if (
      !object(menu) ||
      !Array.isArray(menu.menu) ||
      !object(checks) ||
      !Array.isArray(checks.results) ||
      checks.results.length !== 4 ||
      checks.results.some((v) => typeof v !== "boolean")
    )
      return invalid();
    if (
      !["domains", "directory_assets"].every((mark) =>
        (menu.menu as unknown[]).some(
          (p) =>
            object(p) &&
            p.mark === mark &&
            object(p.auth) &&
            p.auth.readable === true,
        ),
      ) ||
      checks.results.some((v) => !v)
    )
      throw new ApiError("forbidden", 403);
    await session(profile, signal);
  },
  async page(
    q: GroupMembersV4PageQuery,
    choice: SourceChoice,
    profile: Profile,
    signal: AbortSignal,
  ) {
    if (
      !validDomainID(q.domainId) ||
      q.domainId !== choice.domainId ||
      !integer(q.pageIdx, 1, 10000) ||
      ![25, 50, 100].includes(q.pageSize) ||
      (q.observationId !== undefined && !validDomainID(q.observationId)) ||
      (q.pageIdx > 1 && !q.observationId)
    )
      throw new ApiError("invalid_input");
    await session(profile, signal);
    await resolveSource(choice, profile, signal);
    const query = new URLSearchParams({
      domainId: q.domainId,
      kind: "group",
      pageIdx: String(q.pageIdx),
      pageSize: String(q.pageSize),
      ...(q.observationId ? { observationId: q.observationId } : {}),
    });
    const value = parseGroupMembersV4Page(
      await json(`/api/directory/v4/observation?${query}`, signal, profile.ID),
      q,
    );
    await resolveSource(choice, profile, signal);
    await session(profile, signal);
    return value;
  },
  async detail(q: GroupMembersV4Query, profile: Profile, signal: AbortSignal) {
    if (!validGroupMembersV4Query(q)) throw new ApiError("invalid_input");
    await session(profile, signal);
    const value = parseGroupMembersV4(
      await json(
        `/api/group-assets/v4/members?${new URLSearchParams({ ...q })}`,
        signal,
        profile.ID,
      ),
      q,
    );
    await session(profile, signal);
    return value;
  },
};
export function groupMembersV4ReadError(e: unknown) {
  if (e instanceof ApiError) {
    if (e.status === 409)
      return "原观测或配置版本已不可用，已清除数据。请重新核对并选择数据源。";
    if (e.status === 401)
      return "当前登录身份已失效或改变，已清除私有数据。请重新登录。";
    if (e.status === 403)
      return "当前读取权限已不可用，已清除私有数据。请重新核对授权。";
    if (e.status === 404)
      return "该对象或数据源在本次读取中不可用；这不能确定对象是否已从 AD 删除。";
  }
  return "读取结果无法安全确认，未展示部分数据。可重读原观测，或重新选择数据源。";
}
