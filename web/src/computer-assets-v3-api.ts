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
  validUserAssetsV2Filters,
  type UserAssetsV2Filters,
} from "./user-assets-v2-filters";

export const computerAssetsV3Operations = [
  "GET /api/computer-assets/v3",
] as const;
export const computerAssetsV3PageSizes = [10, 20, 30, 40, 50] as const;
export interface ComputerAssetsV3Selection {
  domainId: string;
  revision: string;
  credentialRevision: string;
}
interface SourceRequest {
  domainId: string;
  expectedRevision: string;
  expectedCredentialRevision: string;
}
export interface ComputerAssetsV3Query
  extends SourceRequest,
    Omit<UserAssetsV2Filters, "sAMAccountName"> {
  observationId?: string;
  search: string;
  pageIdx: number;
  pageSize: number;
}
export type ComputerAssetV3Object = DirectoryV3Object & { kind: "computer" };
export interface ComputerAssetsV3List {
  dictionaryVersion: 3;
  selection: ComputerAssetsV3Selection;
  available: boolean;
  observedComputerCount: number | null;
  observationId?: string;
  source?: DirectoryV2Source;
  list: ComputerAssetV3Object[];
  page: PageInfo;
}
const exact = (value: Record<string, unknown>, keys: string[]) =>
  Object.keys(value).length === keys.length &&
  Object.keys(value).every((key) => keys.includes(key));
const invalid = (): never => {
  throw new ApiError("invalid_response");
};
export const validComputerAssetsV3Search = (value: unknown): value is string =>
  typeof value === "string" &&
  value.length <= 50 &&
  !/[\ud800-\udfff]/u.test(value) &&
  new TextEncoder().encode(value).length <= 200;
const validRequest = (q: SourceRequest) =>
  validDomainID(q.domainId) &&
  validRevision(q.expectedRevision) &&
  validRevision(q.expectedCredentialRevision);
const validListQuery = (q: ComputerAssetsV3Query) =>
  validRequest(q) &&
  validComputerAssetsV3Search(q.search) &&
  validUserAssetsV2Filters(q) &&
  integer(q.pageIdx, 1, 10000) &&
  computerAssetsV3PageSizes.some((size) => size === q.pageSize) &&
  (q.observationId === undefined || validDomainID(q.observationId)) &&
  (q.pageIdx === 1 || !!q.observationId);
const validSelection = (value: unknown, q: SourceRequest) =>
  object(value) &&
  exact(value, ["domainId", "revision", "credentialRevision"]) &&
  value.domainId === q.domainId &&
  value.revision === q.expectedRevision &&
  value.credentialRevision === q.expectedCredentialRevision;
const validComputer = (value: unknown): value is ComputerAssetV3Object =>
  validDirectoryV3Object(value) && value.kind === "computer";

function compareComputers(
  a: ComputerAssetV3Object,
  b: ComputerAssetV3Object,
  direction: ComputerAssetsV3Query["creatTm"],
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

export function parseComputerAssetsV3List(
  value: unknown,
  q: ComputerAssetsV3Query,
): ComputerAssetsV3List {
  if (
    !validListQuery(q) ||
    !object(value) ||
    value.dictionaryVersion !== 3 ||
    typeof value.available !== "boolean" ||
    !exact(value, [
      "dictionaryVersion",
      "selection",
      "available",
      "observedComputerCount",
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
    !value.list.every(validComputer) ||
    value.list.length !==
      Math.min(
        q.pageSize,
        Math.max(0, value.page.total - (q.pageIdx - 1) * q.pageSize),
      )
  )
    return invalid();
  if (value.available) {
    if (
      !integer(value.observedComputerCount, 0, 10000) ||
      value.page.total > value.observedComputerCount ||
      (q.search === "" &&
        q.startTm === undefined &&
        value.page.total !== value.observedComputerCount) ||
      !validDomainID(value.observationId) ||
      !validDirectoryV2Source(value.source) ||
      (q.observationId !== undefined && q.observationId !== value.observationId)
    )
      return invalid();
  } else if (
    value.observedComputerCount !== null ||
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
        [row.samAccountName, row.objectSid, row.distinguishedName],
        q.search,
      ) ||
      (start !== undefined &&
        end !== undefined &&
        (created === undefined || created < start || created >= end)) ||
      (index > 0 &&
        compareComputers(value.list[index - 1], row, q.creatTm) >= 0)
    )
      return invalid();
    seen.add(row.objectGUID);
  }
  return value as unknown as ComputerAssetsV3List;
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
      `/api/computer-assets/v3${path}?${new URLSearchParams(Object.entries(q).map(([key, value]) => [key, String(value)]))}`,
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
export const computerAssetsV3API = {
  async list(q: ComputerAssetsV3Query, actor: number, signal: AbortSignal) {
    if (!validListQuery(q)) throw new ApiError("invalid_input");
    return parseComputerAssetsV3List(
      await transport(
        "",
        {
          ...sourceParams(q),
          ...{ search: q.search, pageIdx: q.pageIdx, pageSize: q.pageSize },
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
};
const errors: Record<string, string> = {
  not_found: "当前无法读取此计算机，请重新选择数据源。",
  selection_changed: "数据源配置或凭据版本已改变，请重新选择数据源。",
  directory_observation_unavailable:
    "原计算机资产观测已不可用，请明确刷新观测。",
  directory_limit_exceeded: "计算机资产结果超过安全读取上限，未展示部分结果。",
  invalid_response: "服务器结果无法安全确认，未展示计算机资产。请重试原读取。",
  network: "连接中断，请重试原读取；已选观测不会自动更换。",
  invalid_input: "计算机资产查询参数不符合约定，请核对后重试。",
};
export function computerAssetsV3Error(error: unknown) {
  return error instanceof ApiError
    ? Object.hasOwn(errors, error.code)
      ? errors[error.code]
      : Object.hasOwn(accessMessages, error.code)
        ? accessMessages[error.code]
        : "计算机资产读取失败，请重试原读取。"
    : "计算机资产读取失败，请重试原读取。";
}
export const retryComputerAssetsV3Read = (error: unknown) =>
  error instanceof ApiError &&
  (error.status >= 500 || ["network", "invalid_response"].includes(error.code));
