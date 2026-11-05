import { ApiError } from "./api";
import type { PageInfo } from "./access-api";
import {
  validDirectoryV2Source,
  type DirectoryV2Source,
} from "./directory-v2-api";
import {
  validDirectoryV4Object,
  type DirectoryV4Object,
} from "./directory-v4-facts";
import {
  validGroupMembersV4Query,
  type GroupMembersV4Query,
} from "./group-members-v4-api";
import { integer, object } from "./task-api";

export interface RawDNReferencesV4Query extends GroupMembersV4Query {
  pageIdx: number;
  pageSize: number;
}
export interface RawDNReferencesV4 {
  dictionaryVersion: 4;
  selection: { domainId: string; revision: string; credentialRevision: string };
  observationId: string;
  source: DirectoryV2Source;
  object: DirectoryV4Object;
  list: DirectoryV4Object[];
  page: PageInfo;
  coverage: {
    groupCount: number;
    memberReturnedGroupCount: number;
    memberNotReturnedGroupCount: number;
  };
}
const exact = (v: Record<string, unknown>, keys: string[]) =>
  Object.keys(v).length === keys.length &&
  Object.keys(v).every((k) => keys.includes(k));
export function validRawDNReferencesV4Query(
  value: unknown,
): value is RawDNReferencesV4Query {
  if (!object(value)) return false;
  const { pageIdx, pageSize, ...pins } = value;
  return (
    validGroupMembersV4Query(pins) &&
    integer(pageIdx, 1, 10000) &&
    typeof pageSize === "number" &&
    [25, 50, 100].includes(pageSize)
  );
}
export function parseRawDNReferencesV4(
  v: unknown,
  q: RawDNReferencesV4Query,
): RawDNReferencesV4 {
  if (
    !validRawDNReferencesV4Query(q) ||
    !object(v) ||
    !exact(v, [
      "dictionaryVersion",
      "selection",
      "observationId",
      "source",
      "object",
      "list",
      "page",
      "coverage",
    ]) ||
    v.dictionaryVersion !== 4 ||
    !object(v.selection) ||
    !exact(v.selection, ["domainId", "revision", "credentialRevision"]) ||
    v.selection.domainId !== q.domainId ||
    v.selection.revision !== q.expectedRevision ||
    v.selection.credentialRevision !== q.expectedCredentialRevision ||
    v.observationId !== q.observationId ||
    !validDirectoryV2Source(v.source) ||
    !validDirectoryV4Object(v.object) ||
    v.object.objectGUID !== q.objectGUID ||
    !object(v.page) ||
    !exact(v.page, ["pageIdx", "pageSize", "total", "totalPage"]) ||
    v.page.pageIdx !== q.pageIdx ||
    v.page.pageSize !== q.pageSize ||
    !integer(v.page.total, 0, 10000) ||
    v.page.totalPage !== Math.ceil(v.page.total / q.pageSize) ||
    !object(v.coverage) ||
    !exact(v.coverage, [
      "groupCount",
      "memberReturnedGroupCount",
      "memberNotReturnedGroupCount",
    ]) ||
    !integer(v.coverage.groupCount, 0, 10000) ||
    !integer(v.coverage.memberReturnedGroupCount, 0, 10000) ||
    !integer(v.coverage.memberNotReturnedGroupCount, 0, 10000) ||
    v.coverage.groupCount !==
      v.coverage.memberReturnedGroupCount +
        v.coverage.memberNotReturnedGroupCount ||
    v.page.total > v.coverage.memberReturnedGroupCount ||
    !Array.isArray(v.list) ||
    v.list.length !==
      Math.min(
        q.pageSize,
        Math.max(0, v.page.total - (q.pageIdx - 1) * q.pageSize),
      )
  )
    throw new ApiError("invalid_response");
  const target = v.object;
  if (
    !v.list.every(validDirectoryV4Object) ||
    v.list.some(
      (r, i, rows) =>
        r.kind !== "group" ||
        !r.rawMember?.includes(target.distinguishedName) ||
        (i > 0 && rows[i - 1].objectGUID >= r.objectGUID),
    )
  )
    throw new ApiError("invalid_response");
  return v as unknown as RawDNReferencesV4;
}
