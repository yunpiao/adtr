import { ApiError } from "./api";
import { validDomainID, validRevision } from "./domain-api";
import {
  validDirectoryV2Source,
  type DirectoryV2Source,
} from "./directory-v2-api";
import { readDirectoryV2JSON } from "./directory-v2-json";
import {
  validDirectoryV4Object,
  type DirectoryV4Object,
} from "./directory-v4-facts";
import { object } from "./task-api";

export interface GroupMembersV4Query {
  domainId: string;
  expectedRevision: string;
  expectedCredentialRevision: string;
  observationId: string;
  objectGUID: string;
}
export interface GroupMembersV4 {
  dictionaryVersion: 4;
  selection: {
    domainId: string;
    revision: string;
    credentialRevision: string;
  };
  observationId: string;
  source: DirectoryV2Source;
  object: DirectoryV4Object & { kind: "group" };
}
const exact = (value: Record<string, unknown>, keys: string[]) =>
  Object.keys(value).length === keys.length &&
  Object.keys(value).every((key) => keys.includes(key));

export function validGroupMembersV4Query(
  value: unknown,
): value is GroupMembersV4Query {
  return (
    object(value) &&
    exact(value, [
      "domainId",
      "expectedRevision",
      "expectedCredentialRevision",
      "observationId",
      "objectGUID",
    ]) &&
    validDomainID(value.domainId) &&
    validRevision(value.expectedRevision) &&
    validRevision(value.expectedCredentialRevision) &&
    validDomainID(value.observationId) &&
    typeof value.objectGUID === "string" &&
    /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/u.test(value.objectGUID)
  );
}

// All five request pins are required; never use latest, upgrade a v3 object,
// combine observations, or infer empty membership from an unavailable response.
export function parseGroupMembersV4(
  value: unknown,
  query: GroupMembersV4Query,
): GroupMembersV4 {
  if (
    !validGroupMembersV4Query(query) ||
    !object(value) ||
    !exact(value, [
      "dictionaryVersion",
      "selection",
      "observationId",
      "source",
      "object",
    ]) ||
    value.dictionaryVersion !== 4 ||
    !object(value.selection) ||
    !exact(value.selection, ["domainId", "revision", "credentialRevision"]) ||
    value.selection.domainId !== query.domainId ||
    value.selection.revision !== query.expectedRevision ||
    value.selection.credentialRevision !== query.expectedCredentialRevision ||
    value.observationId !== query.observationId ||
    !validDirectoryV2Source(value.source) ||
    !validDirectoryV4Object(value.object) ||
    value.object.kind !== "group" ||
    value.object.objectGUID !== query.objectGUID
  )
    throw new ApiError("invalid_response");
  return value as unknown as GroupMembersV4;
}

// Preparatory parser only: the future transport must separately verify current
// actor/session and HTTP status before using this successful-response reader.
// Reuse fatal UTF-8 decoding, abort handling and the existing 8MiB byte budget.
export async function readGroupMembersV4(
  response: Response,
  query: GroupMembersV4Query,
  signal: AbortSignal,
): Promise<GroupMembersV4> {
  const value = await readDirectoryV2JSON(response, signal);
  signal.throwIfAborted();
  if (!response.ok) throw new ApiError("invalid_response", response.status);
  return parseGroupMembersV4(value, query);
}
