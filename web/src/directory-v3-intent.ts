import type { Profile } from "./api";
import { validDomainID } from "./domain-api";
import { object } from "./task-api";
import {
  validDirectoryV3Input,
  type DirectoryV3Input,
} from "./directory-v3-api";
export interface DirectoryV3Intent extends DirectoryV3Input {
  taskUUID?: string;
  receiptOnly?: boolean;
}
const prefix = "adtr.directory-sync-intent.v3:";
const memory = new Map<string, DirectoryV3Intent>();
const owner = (p: Profile) => `${p.ID}:${encodeURIComponent(p.username)}`;
function project(v: unknown): DirectoryV3Intent | null {
  if (
    !validDirectoryV3Input(v) ||
    (object(v) && v.taskUUID !== undefined && !validDomainID(v.taskUUID)) ||
    (object(v) &&
      v.receiptOnly !== undefined &&
      typeof v.receiptOnly !== "boolean")
  )
    return null;
  return {
    domainId: v.domainId,
    expectedRevision: v.expectedRevision,
    expectedCredentialGeneration: v.expectedCredentialGeneration,
    idempotencyKey: v.idempotencyKey,
    ...(object(v) && v.receiptOnly === true ? { receiptOnly: true } : {}),
    ...(object(v) && typeof v.taskUUID === "string"
      ? { taskUUID: v.taskUUID }
      : {}),
  };
}
export function saveDirectoryV3Intent(
  profile: Profile,
  value: DirectoryV3Intent,
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
export function readDirectoryV3Intent(
  profile: Profile,
): DirectoryV3Intent | null {
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
export function discardDirectoryV3Intent(profile: Profile) {
  memory.delete(owner(profile));
  try {
    sessionStorage.removeItem(prefix + owner(profile));
  } catch {
    /* Memory is clear. */
  }
}
