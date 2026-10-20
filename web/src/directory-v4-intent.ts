import { ApiError, validUsername } from "./api";
import { validDomainID, validRevision } from "./domain-api";
import { integer, object } from "./task-api";

export interface DirectoryV4Input {
  domainId: string;
  expectedRevision: string;
  expectedCredentialGeneration: string;
  idempotencyKey: string;
}
export interface DirectoryV4Owner { ID: number; username: string }
export interface DirectoryV4Intent extends DirectoryV4Input {
  storageVersion: 1;
  dictionaryVersion: 4;
  kind: "domain.directory_read.v4";
  payloadVersion: 1;
  owner: DirectoryV4Owner;
  receiptOnly: boolean;
  taskUUID?: string;
  cancelState?: "unknown" | "accepted" | "terminal";
}
export type DirectoryV4IntentRead =
  | { state: "empty" }
  | { state: "valid"; intent: DirectoryV4Intent }
  | { state: "unusable" | "storage_unavailable" };
export interface DirectoryV4Storage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}
export const directoryV4IntentPrefix = "adtr.directory-sync-intent.v4:";
const exact = (v: Record<string, unknown>, keys: string[]) =>
  Object.keys(v).length === keys.length && Object.keys(v).every(k => keys.includes(k));
export function validDirectoryV4Input(v: unknown): v is DirectoryV4Input {
  return object(v) && exact(v, ["domainId", "expectedRevision", "expectedCredentialGeneration", "idempotencyKey"]) &&
    validDomainID(v.domainId) && validRevision(v.expectedRevision) &&
    validRevision(v.expectedCredentialGeneration) && typeof v.idempotencyKey === "string" &&
    /^[A-Za-z0-9_-]{8,128}$/u.test(v.idempotencyKey);
}
export const directoryV4Input = (v: DirectoryV4Input): DirectoryV4Input => ({
  domainId: v.domainId, expectedRevision: v.expectedRevision,
  expectedCredentialGeneration: v.expectedCredentialGeneration, idempotencyKey: v.idempotencyKey,
});
const sameOwner = (a: DirectoryV4Owner, b: DirectoryV4Owner) => a.ID === b.ID && a.username === b.username;
export function directoryV4OwnerKey(owner: DirectoryV4Owner): string {
  if (!integer(owner.ID, 1) || !validUsername(owner.username)) throw new ApiError("invalid_input");
  return `${owner.ID}:${encodeURIComponent(owner.username)}`;
}
export function parseDirectoryV4Intent(v: unknown, owner: DirectoryV4Owner): DirectoryV4Intent {
  if (!object(v) || !exact(v, ["storageVersion", "dictionaryVersion", "kind", "payloadVersion", "owner",
    "domainId", "expectedRevision", "expectedCredentialGeneration", "idempotencyKey", "receiptOnly",
    ...(v.taskUUID === undefined ? [] : ["taskUUID"]), ...(v.cancelState === undefined ? [] : ["cancelState"])]) ||
    v.storageVersion !== 1 || v.dictionaryVersion !== 4 || v.kind !== "domain.directory_read.v4" ||
    v.payloadVersion !== 1 || !object(v.owner) || !exact(v.owner, ["ID", "username"]) ||
    !integer(v.owner.ID, 1) || typeof v.owner.username !== "string" || !validUsername(v.owner.username) ||
    !sameOwner(v.owner as unknown as DirectoryV4Owner, owner) || typeof v.receiptOnly !== "boolean" ||
    !validDirectoryV4Input(directoryV4Input(v as unknown as DirectoryV4Input)) ||
    (v.taskUUID !== undefined && !validDomainID(v.taskUUID)) ||
    (v.cancelState !== undefined && (!v.taskUUID || !["unknown", "accepted", "terminal"].includes(String(v.cancelState)))))
    throw new ApiError("unusable_recovery_record");
  // Project only the closed nonsecret fields, never caller extras or Profile.
  return {
    storageVersion: 1, dictionaryVersion: 4, kind: "domain.directory_read.v4", payloadVersion: 1,
    owner: { ID: owner.ID, username: owner.username },
    ...directoryV4Input(v as unknown as DirectoryV4Input), receiptOnly: v.receiptOnly,
    ...(v.taskUUID === undefined ? {} : { taskUUID: v.taskUUID as string }),
    ...(v.cancelState === undefined ? {} : { cancelState: v.cancelState as DirectoryV4Intent["cancelState"] }),
  };
}
export const sameDirectoryV4Input = (a: DirectoryV4Input, b: DirectoryV4Input) =>
  a.domainId === b.domainId && a.expectedRevision === b.expectedRevision &&
  a.expectedCredentialGeneration === b.expectedCredentialGeneration && a.idempotencyKey === b.idempotencyKey;
