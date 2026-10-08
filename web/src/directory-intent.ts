import type { Profile } from "./api";
import { validDomainID } from "./domain-api";
import { object } from "./task-api";
import { validDirectoryInput, type DirectoryInput } from "./directory-api";
export interface DirectoryIntent extends DirectoryInput {
  taskUUID?: string;
}
const prefix = "adtr.directory-sync-intent.v1:";
const memory = new Map<string, DirectoryIntent>();
const owner = (p: Profile) => `${p.ID}:${encodeURIComponent(p.username)}`;
function project(v: unknown): DirectoryIntent | null {
  if (
    !validDirectoryInput(v) ||
    (object(v) && v.taskUUID !== undefined && !validDomainID(v.taskUUID))
  )
    return null;
  return {
    domainId: v.domainId,
    expectedRevision: v.expectedRevision,
    expectedCredentialGeneration: v.expectedCredentialGeneration,
    idempotencyKey: v.idempotencyKey,
    ...(object(v) && typeof v.taskUUID === "string"
      ? { taskUUID: v.taskUUID }
      : {}),
  };
}
export function saveDirectoryIntent(profile: Profile, value: DirectoryIntent) {
  const intent = project(value);
  if (!intent) throw new Error("Invalid directory intent");
  memory.set(owner(profile), intent);
  try {
    sessionStorage.setItem(
      prefix + owner(profile),
      JSON.stringify({ owner: owner(profile), intent }),
    );
  } catch {
    /* Keep nonsecret recovery in memory. */
  }
}
export function readDirectoryIntent(profile: Profile): DirectoryIntent | null {
  const key = owner(profile);
  if (memory.has(key)) return memory.get(key)!;
  try {
    const raw = sessionStorage.getItem(prefix + key);
    const value: unknown = raw && raw.length < 8192 ? JSON.parse(raw) : null;
    const intent =
      object(value) && value.owner === key ? project(value.intent) : null;
    if (intent) {
      memory.set(key, intent);
      return intent;
    }
    sessionStorage.removeItem(prefix + key);
  } catch {
    /* Ignore unavailable or malformed storage. */
  }
  return null;
}
export function discardDirectoryIntent(profile: Profile) {
  memory.delete(owner(profile));
  try {
    sessionStorage.removeItem(prefix + owner(profile));
  } catch {
    /* Memory is clear. */
  }
}
