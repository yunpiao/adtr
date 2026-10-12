import { directoryLiteralSearchMatches } from "./directory-search-lowercase";
import {
  validDirectoryV3Object,
  type DirectoryV3Object,
} from "./directory-v3-facts";
import { ApiError } from "./api";
import { accessMessages, type PageInfo } from "./access-api";
import { validDomainID, validRevision } from "./domain-api";
import {
  validDirectoryV2Source,
  type DirectoryV2Source,
} from "./directory-v2-api";
import { readDirectoryV2JSON } from "./directory-v2-json";
import { integer, object } from "./task-api";
import {
  userAssetsV2Time,
  validUserAssetsV2Filters,
  type UserAssetsV2Filters,
} from "./user-assets-v2-filters";

export const userAssetsV3Operations = [
  "GET /api/user-assets/v3",
  "GET /api/user-assets/v3/detail",
] as const;
export const userAssetsV3PageSizes = [10, 20, 30, 40, 50] as const;
export interface UserAssetsV3Selection {
  domainId: string;
  revision: string;
  credentialRevision: string;
}
interface SourceRequest {
  domainId: string;
  expectedRevision: string;
  expectedCredentialRevision: string;
}
export interface UserAssetsV3Query extends SourceRequest, UserAssetsV2Filters {
  observationId?: string;
  search: string;
  pageIdx: number;
  pageSize: number;
}
export interface UserAssetV3Query extends SourceRequest {
  observationId: string;
  objectGUID: string;
}
export type UserAssetV3Object = DirectoryV3Object & { kind: "user" };
export interface UserAssetsV3List {
  dictionaryVersion: 3;
  selection: UserAssetsV3Selection;
  available: boolean;
  observationId?: string;
  source?: DirectoryV2Source;
  list: UserAssetV3Object[];
  page: PageInfo;
}
export interface UserAssetV3Detail {
  dictionaryVersion: 3;
  selection: UserAssetsV3Selection;
  observationId: string;
  source: DirectoryV2Source;
  object: UserAssetV3Object;
}
const exact = (value: Record<string, unknown>, keys: string[]) =>
  Object.keys(value).length === keys.length &&
  Object.keys(value).every((key) => keys.includes(key));
const invalid = (): never => {
  throw new ApiError("invalid_response");
};
const validGUID = (value: unknown): value is string =>
  typeof value === "string" &&
  /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/u.test(value);
export const validUserAssetsV3Search = (value: unknown): value is string =>
  typeof value === "string" &&
  value.length <= 50 &&
  !/[\ud800-\udfff]/u.test(value) &&
  new TextEncoder().encode(value).length <= 200;
const validRequest = (q: SourceRequest) =>
  validDomainID(q.domainId) &&
  validRevision(q.expectedRevision) &&
  validRevision(q.expectedCredentialRevision);
const validListQuery = (q: UserAssetsV3Query) =>
  validRequest(q) &&
  validUserAssetsV3Search(q.search) &&
  validUserAssetsV2Filters(q) &&
  integer(q.pageIdx, 1, 10000) &&
  userAssetsV3PageSizes.some((size) => size === q.pageSize) &&
  (q.observationId === undefined || validDomainID(q.observationId)) &&
  (q.pageIdx === 1 || !!q.observationId);
const validDetailQuery = (q: UserAssetV3Query) =>
  validRequest(q) && validDomainID(q.observationId) && validGUID(q.objectGUID);
const validSelection = (value: unknown, q: SourceRequest) =>
  object(value) &&
  exact(value, ["domainId", "revision", "credentialRevision"]) &&
  value.domainId === q.domainId &&
  value.revision === q.expectedRevision &&
  value.credentialRevision === q.expectedCredentialRevision;
const validUser = (value: unknown): value is UserAssetV3Object =>
  validDirectoryV3Object(value) && value.kind === "user";

function compareUsers(
  a: UserAssetV3Object,
  b: UserAssetV3Object,
  direction: UserAssetsV3Query["creatTm"],
) {
  if (direction && a.whenCreated !== b.whenCreated) {
    if (a.whenCreated === null) return 1;
    if (b.whenCreated === null) return -1;
    const first = userAssetsV2Time(a.whenCreated)!;
    const second = userAssetsV2Time(b.whenCreated)!;
    if (first !== second)
      return (first < second ? -1 : 1) * (direction === "asc" ? 1 : -1);
  }
  return a.objectGUID < b.objectGUID
    ? -1
    : a.objectGUID === b.objectGUID
      ? 0
      : 1;
}

export function parseUserAssetsV3List(
  value: unknown,
  q: UserAssetsV3Query,
): UserAssetsV3List {
  if (
    !validListQuery(q) ||
    !object(value) ||
    value.dictionaryVersion !== 3 ||
    typeof value.available !== "boolean" ||
    !exact(value, [
      "dictionaryVersion",
      "selection",
      "available",
      "list",
      "page",
      ...(value.available ? ["observationId", "source"] : []),
    ]) ||
    !validSelection(value.selection, q) ||
    !object(value.page) ||
    !exact(value.page, ["pageIdx", "pageSize", "total", "totalPage"]) ||
    value.page.pageIdx !== q.pageIdx ||
    value.page.pageSize !== q.pageSize ||
    !integer(value.page.total, 0, 10000) ||
    value.page.totalPage !== Math.ceil(value.page.total / q.pageSize) ||
    !Array.isArray(value.list) ||
    !value.list.every(validUser) ||
    value.list.length !==
      Math.min(
        q.pageSize,
        Math.max(0, value.page.total - (q.pageIdx - 1) * q.pageSize),
      )
  )
    return invalid();
  if (value.available) {
    if (
      !validDomainID(value.observationId) ||
      !validDirectoryV2Source(value.source) ||
      (q.observationId !== undefined && q.observationId !== value.observationId)
    )
      return invalid();
  } else if (
    q.observationId !== undefined ||
    value.list.length ||
    value.page.total !== 0
  ) {
    return invalid();
  }
  const start = userAssetsV2Time(q.startTm),
    end = userAssetsV2Time(q.endTm);
  const seen = new Set<string>();
  for (const [index, row] of value.list.entries()) {
    const created = userAssetsV2Time(row.whenCreated);
    if (
      seen.has(row.objectGUID) ||
      !directoryLiteralSearchMatches(
        [row.samAccountName, row.objectSid, row.mail, row.distinguishedName],
        q.search,
      ) ||
      (q.sAMAccountName !== undefined &&
        q.sAMAccountName !== "" &&
        row.samAccountName !== q.sAMAccountName) ||
      (start !== undefined &&
        end !== undefined &&
        (created === undefined || created < start || created >= end)) ||
      (index > 0 && compareUsers(value.list[index - 1], row, q.creatTm) >= 0)
    )
      return invalid();
    seen.add(row.objectGUID);
  }
  return value as unknown as UserAssetsV3List;
}
export function parseUserAssetV3Detail(
  value: unknown,
  q: UserAssetV3Query,
): UserAssetV3Detail {
  if (
    !validDetailQuery(q) ||
    !object(value) ||
    !exact(value, [
      "dictionaryVersion",
      "selection",
      "observationId",
      "source",
      "object",
    ]) ||
    value.dictionaryVersion !== 3 ||
    !validSelection(value.selection, q) ||
    value.observationId !== q.observationId ||
    !validDirectoryV2Source(value.source) ||
    !validUser(value.object) ||
    value.object.objectGUID !== q.objectGUID
  )
    return invalid();
  return value as unknown as UserAssetV3Detail;
}
async function transport(
  path: string,
  q: SourceRequest,
  actor: number,
  signal: AbortSignal,
) {
  let response: Response;
  try {
    response = await fetch(
      `/api/user-assets/v3${path}?${new URLSearchParams(Object.entries(q).map(([key, value]) => [key, String(value)]))}`,
      {
        method: "GET",
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
        signal,
      },
    );
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
const sourceParams = (q: SourceRequest) => ({
  domainId: q.domainId,
  expectedRevision: q.expectedRevision,
  expectedCredentialRevision: q.expectedCredentialRevision,
});
export const userAssetsV3API = {
  async list(q: UserAssetsV3Query, actor: number, signal: AbortSignal) {
    if (!validListQuery(q)) throw new ApiError("invalid_input");
    return parseUserAssetsV3List(
      await transport(
        "",
        {
          ...sourceParams(q),
          ...{ search: q.search, pageIdx: q.pageIdx, pageSize: q.pageSize },
          ...(q.sAMAccountName ? { sAMAccountName: q.sAMAccountName } : {}),
          ...(q.startTm === undefined
            ? {}
            : { startTm: q.startTm, endTm: q.endTm }),
          ...(q.creatTm === undefined ? {} : { creatTm: q.creatTm }),
          ...(q.observationId === undefined
            ? {}
            : { observationId: q.observationId }),
        },
        actor,
        signal,
      ),
      q,
    );
  },
  async detail(q: UserAssetV3Query, actor: number, signal: AbortSignal) {
    if (!validDetailQuery(q)) throw new ApiError("invalid_input");
    return parseUserAssetV3Detail(
      await transport(
        "/detail",
        {
          ...sourceParams(q),
          ...{ observationId: q.observationId, objectGUID: q.objectGUID },
        },
        actor,
        signal,
      ),
      q,
    );
  },
};
const errors: Record<string, string> = {
  not_found: "当前无法读取此用户，请重新选择数据源。",
  selection_changed: "数据源配置或凭据版本已改变，请重新选择数据源。",
  directory_observation_unavailable: "原用户资产观测已不可用，请明确刷新观测。",
  directory_limit_exceeded: "用户资产结果超过安全读取上限，未展示部分结果。",
  invalid_response: "服务器结果无法安全确认，未展示用户资产。请重试原读取。",
  network: "连接中断，请重试原读取；已选观测不会自动更换。",
  invalid_input: "用户资产查询参数不符合约定，请核对后重试。",
};
export function userAssetsV3Error(error: unknown) {
  return error instanceof ApiError
    ? Object.hasOwn(errors, error.code)
      ? errors[error.code]
      : Object.hasOwn(accessMessages, error.code)
        ? accessMessages[error.code]
        : "用户资产读取失败，请重试原读取。"
    : "用户资产读取失败，请重试原读取。";
}
export const retryUserAssetsV3Read = (error: unknown) =>
  error instanceof ApiError &&
  (error.status >= 500 || ["network", "invalid_response"].includes(error.code));
