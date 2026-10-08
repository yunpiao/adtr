import type { Profile } from "./api";
import { validDomainID } from "./domain-api";
import { object } from "./task-api";
import {
  validDirectoryV2Input,
  type DirectoryV2Input,
} from "./directory-v2-api";
export interface DirectoryV2Intent extends DirectoryV2Input {
  taskUUID?: string;
}
const prefix = "adtr.directory-sync-intent.v2:";
const memory = new Map<string, DirectoryV2Intent>();
const owner = (p: Profile) => `${p.ID}:${encodeURIComponent(p.username)}`;
function project(v: unknown): DirectoryV2Intent | null {
  if (
    !validDirectoryV2Input(v) ||
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
export function saveDirectoryV2Intent(
  profile: Profile,
  value: DirectoryV2Intent,
) {
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
export function readDirectoryV2Intent(
  profile: Profile,
): DirectoryV2Intent | null {
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
export function discardDirectoryV2Intent(profile: Profile) {
  memory.delete(owner(profile));
  try {
    sessionStorage.removeItem(prefix + owner(profile));
  } catch {
    /* Memory is clear. */
  }
}
