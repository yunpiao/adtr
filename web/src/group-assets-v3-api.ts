import {
  validDirectoryV3Object,
  type DirectoryV3Object,
} from "./directory-v3-facts";
import { ApiError } from "./api";
import { directoryLiteralSearchMatches } from "./directory-search-lowercase";
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
  type UserAssetsV2Filters,
} from "./user-assets-v2-filters";

export const groupAssetsV3Operations = ["GET /api/group-assets/v3"] as const;
export const groupAssetsV3PageSizes = [10, 20, 30, 40, 50] as const;
export interface GroupAssetsV3Selection {
  domainId: string;
  revision: string;
  credentialRevision: string;
}
interface SourceRequest {
  domainId: string;
  expectedRevision: string;
  expectedCredentialRevision: string;
}
export interface GroupAssetsV3Query
  extends SourceRequest,
    Pick<UserAssetsV2Filters, "creatTm"> {
  observationId?: string;
  search: string;
  pageIdx: number;
  pageSize: number;
}
export type GroupAssetV3Object = DirectoryV3Object & { kind: "group" };
export interface GroupAssetsV3List {
  dictionaryVersion: 3;
  selection: GroupAssetsV3Selection;
  available: boolean;
  observedGroupCount: number | null;
  observationId?: string;
  source?: DirectoryV2Source;
  list: GroupAssetV3Object[];
  page: PageInfo;
}
const exact = (value: Record<string, unknown>, keys: string[]) =>
  Object.keys(value).length === keys.length &&
  Object.keys(value).every((key) => keys.includes(key));
const invalid = (): never => {
  throw new ApiError("invalid_response");
};
export const validGroupAssetsV3Search = (value: unknown): value is string =>
  typeof value === "string" &&
  value.length <= 20 &&
  !/[\ud800-\udfff]/u.test(value) &&
  new TextEncoder().encode(value).length <= 80;
const validRequest = (q: SourceRequest) =>
  validDomainID(q.domainId) &&
  validRevision(q.expectedRevision) &&
  validRevision(q.expectedCredentialRevision);
const validListQuery = (q: GroupAssetsV3Query) =>
  validRequest(q) &&
  validGroupAssetsV3Search(q.search) &&
  (q.creatTm === undefined || q.creatTm === "asc" || q.creatTm === "desc") &&
  integer(q.pageIdx, 1, 10000) &&
  groupAssetsV3PageSizes.some((size) => size === q.pageSize) &&
  (q.observationId === undefined || validDomainID(q.observationId)) &&
  (q.pageIdx === 1 || !!q.observationId);
const validSelection = (value: unknown, q: SourceRequest) =>
  object(value) &&
  exact(value, ["domainId", "revision", "credentialRevision"]) &&
  value.domainId === q.domainId &&
  value.revision === q.expectedRevision &&
  value.credentialRevision === q.expectedCredentialRevision;
const validGroup = (value: unknown): value is GroupAssetV3Object =>
  validDirectoryV3Object(value) && value.kind === "group";

function compareGroups(
  a: GroupAssetV3Object,
  b: GroupAssetV3Object,
  direction: GroupAssetsV3Query["creatTm"],
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

export function parseGroupAssetsV3List(
  value: unknown,
  q: GroupAssetsV3Query,
): GroupAssetsV3List {
  if (
    !validListQuery(q) ||
    !object(value) ||
    value.dictionaryVersion !== 3 ||
    typeof value.available !== "boolean" ||
    !exact(value, [
      "dictionaryVersion",
      "selection",
      "available",
      "observedGroupCount",
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
    !value.list.every(validGroup) ||
    value.list.length !==
      Math.min(
        q.pageSize,
        Math.max(0, value.page.total - (q.pageIdx - 1) * q.pageSize),
      )
  )
    return invalid();
  if (value.available) {
    if (
      !integer(value.observedGroupCount, 0, 10000) ||
      value.page.total > value.observedGroupCount ||
      (q.search === "" && value.page.total !== value.observedGroupCount) ||
      !validDomainID(value.observationId) ||
      !validDirectoryV2Source(value.source) ||
      (q.observationId !== undefined && q.observationId !== value.observationId)
    )
      return invalid();
  } else if (
    value.observedGroupCount !== null ||
    q.observationId !== undefined ||
    value.list.length ||
    value.page.total !== 0
  ) {
    return invalid();
  }
  const seen = new Set<string>();
  for (const [index, row] of value.list.entries()) {
    if (
      seen.has(row.objectGUID) ||
      !directoryLiteralSearchMatches(
        [row.samAccountName, row.objectSid, row.distinguishedName],
        q.search,
      ) ||
      (index > 0 && compareGroups(value.list[index - 1], row, q.creatTm) >= 0)
    )
      return invalid();
    seen.add(row.objectGUID);
  }
  return value as unknown as GroupAssetsV3List;
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
      `/api/group-assets/v3${path}?${new URLSearchParams(Object.entries(q).map(([key, value]) => [key, String(value)]))}`,
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
export const groupAssetsV3API = {
  async list(q: GroupAssetsV3Query, actor: number, signal: AbortSignal) {
    if (!validListQuery(q)) throw new ApiError("invalid_input");
    return parseGroupAssetsV3List(
      await transport(
        "",
        {
          ...sourceParams(q),
          ...{ search: q.search, pageIdx: q.pageIdx, pageSize: q.pageSize },
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
};
const errors: Record<string, string> = {
  not_found: "当前无法读取此组，请重新选择数据源。",
  selection_changed: "数据源配置或凭据版本已改变，请重新选择数据源。",
  directory_observation_unavailable: "原组资产观测已不可用，请明确刷新观测。",
  directory_limit_exceeded: "组资产结果超过安全读取上限，未展示部分结果。",
  invalid_response: "服务器结果无法安全确认，未展示组资产。请重试原读取。",
  network: "连接中断，请重试原读取；已选观测不会自动更换。",
  invalid_input: "组资产查询参数不符合约定，请核对后重试。",
};
export function groupAssetsV3Error(error: unknown) {
  return error instanceof ApiError
    ? Object.hasOwn(errors, error.code)
      ? errors[error.code]
      : Object.hasOwn(accessMessages, error.code)
        ? accessMessages[error.code]
        : "组资产读取失败，请重试原读取。"
    : "组资产读取失败，请重试原读取。";
}
export const retryGroupAssetsV3Read = (error: unknown) =>
  error instanceof ApiError &&
  (error.status >= 500 || ["network", "invalid_response"].includes(error.code));
