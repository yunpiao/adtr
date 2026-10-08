import type { Profile } from "./api";
import { object } from "./task-api";
import {
  validDomainID,
  validRevision,
  validDNS,
  type TestInput,
} from "./domain-api";
export type DomainIntent =
  | { kind: "create"; key: string; domain: string }
  | ({ kind: "test"; key: string; taskUUID?: string } & TestInput);
const storage = "adtr.domain-intent.v1";
const memory = new Map<string, DomainIntent>();
type Actor = Pick<Profile, "ID" | "username">;
let active: Actor | undefined;
const owner = (p: Actor) => `${p.ID}:${p.username}`;
const ownerKey = (p: Actor) =>
  `${storage}:${p.ID}:${encodeURIComponent(p.username)}`;
export function forgetDomainSession() {
  // Non-secret recovery stays isolated by actor even if storage is disabled.
  active = undefined;
}
export function discardDomainIntent(profile: Actor | undefined = active) {
  if (!profile) return;
  memory.delete(ownerKey(profile));
  try {
    sessionStorage.removeItem(ownerKey(profile));
    const legacy = JSON.parse(sessionStorage.getItem(storage) ?? "null");
    if (legacy?.owner === owner(profile)) sessionStorage.removeItem(storage);
  } catch {
    /* In-memory intent is cleared. */
  }
}
function project(v: unknown): DomainIntent | null {
  if (
    !object(v) ||
    typeof v.key !== "string" ||
    !/^[A-Za-z0-9_-]{8,128}$/u.test(v.key)
  )
    return null;
  if (v.kind === "create" && typeof v.domain === "string" && validDNS(v.domain))
    return { kind: "create", key: v.key, domain: v.domain };
  if (
    v.kind === "test" &&
    validDomainID(v.domainId) &&
    validRevision(v.expectedRevision) &&
    v.idempotencyKey === v.key &&
    (v.taskUUID === undefined ||
      (typeof v.taskUUID === "string" &&
        /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(v.taskUUID)))
  )
    return {
      kind: "test",
      key: v.key,
      domainId: v.domainId,
      expectedRevision: v.expectedRevision,
      idempotencyKey: v.key,
      ...(typeof v.taskUUID === "string" ? { taskUUID: v.taskUUID } : {}),
    };
  return null;
}
export function readDomainIntent(p: Profile): DomainIntent | null {
  active = { ID: p.ID, username: p.username };
  const key = ownerKey(p),
    saved = memory.get(key);
  if (saved) return saved;
  try {
    const current = sessionStorage.getItem(key);
    const raw = current ?? sessionStorage.getItem(storage);
    const d: unknown = raw && raw.length <= 65536 ? JSON.parse(raw) : null;
    const intent = object(d) && d.owner === owner(p) ? project(d.intent) : null;
    if (intent) {
      memory.set(key, intent);
      if (current === null) {
        sessionStorage.setItem(
          key,
          JSON.stringify({ owner: owner(p), intent }),
        );
        sessionStorage.removeItem(storage);
      }
      return intent;
    }
    if (current !== null) sessionStorage.removeItem(key);
  } catch {
    /* Invalid or unavailable storage is ignored. */
  }
  return memory.get(key) ?? null;
}
export function saveDomainIntent(p: Profile, value: DomainIntent) {
  const intent = project(value);
  if (!intent) throw Error("Invalid domain intent");
  active = { ID: p.ID, username: p.username };
  memory.set(ownerKey(p), intent);
  try {
    sessionStorage.setItem(
      ownerKey(p),
      JSON.stringify({ owner: owner(p), intent }),
    );
  } catch {
    /* Navigation retains the same key in memory. */
  }
}
