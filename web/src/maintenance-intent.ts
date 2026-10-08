import type { Profile } from "./api";
import { integer, object } from "./task-api";
import {
  validUTC,
  type ArchiveInput,
  type ScheduleControl,
  type ScheduleDefinition,
} from "./maintenance-api";

export type MaintenanceIntent = { key: string; attempted: boolean } & (
  | { action: "create"; input: ScheduleDefinition }
  | { action: "enable"; input: ScheduleControl }
  | { action: "pause"; input: ScheduleControl }
  | { action: "archive"; input: ArchiveInput }
  | { action: "restore"; input: ArchiveInput }
);
const storage = "adtr.pending-task-maintenance";
const pending = new Map<string, MaintenanceIntent>();
type Actor = Pick<Profile, "ID" | "username">;
let active: Actor | undefined;
const ownerKey = (p: Actor) =>
  `${storage}:${p.ID}:${encodeURIComponent(p.username)}`;
export function forgetMaintenanceSession() {
  for (const [key, intent] of pending)
    if (!intent.attempted) pending.delete(key);
  active = undefined;
}
export function discardMaintenanceIntent(profile: Actor | undefined = active) {
  if (!profile) return;
  pending.delete(ownerKey(profile));
  try {
    sessionStorage.removeItem(ownerKey(profile));
    const legacy = JSON.parse(sessionStorage.getItem(storage) ?? "null");
    if (legacy?.owner === profile.ID && legacy?.username === profile.username)
      sessionStorage.removeItem(storage);
  } catch {
    /* Storage disabled. */
  }
}
const uuid = (v: unknown): v is string =>
  typeof v === "string" &&
  /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(v);
// Whitelist and reconstruct every persisted value. Proof, CSRF/session tokens,
// server results and injected storage properties never reach a replay request.
function restored(v: unknown): MaintenanceIntent | undefined {
  if (!object(v) || !uuid(v.key) || !object(v.input)) return;
  const i = v.input,
    base = { key: v.key, attempted: true };
  if (
    v.action === "create" &&
    typeof i.label === "string" &&
    Array.from(i.label).length <= 80 &&
    i.taskName === "infrastructure.health" &&
    i.domainId === "platform" &&
    i.payloadVersion === 1 &&
    typeof i.startAt === "string" &&
    validUTC(i.startAt) &&
    integer(i.intervalSeconds, 60, 86400)
  )
    return {
      ...base,
      action: "create",
      input: {
        label: i.label,
        taskName: i.taskName,
        domainId: i.domainId,
        payloadVersion: 1,
        payload: {},
        startAt: i.startAt,
        intervalSeconds: i.intervalSeconds,
      },
    };
  if (
    (v.action === "enable" || v.action === "pause") &&
    uuid(i.scheduleUUID) &&
    integer(i.expectedControlVersion, 0)
  )
    return {
      ...base,
      action: v.action,
      input: {
        scheduleUUID: i.scheduleUUID,
        expectedControlVersion: i.expectedControlVersion,
      },
    };
  if (
    (v.action === "archive" || v.action === "restore") &&
    typeof i.reason === "string" &&
    i.reason.length <= 1000 &&
    Array.isArray(i.targets) &&
    i.targets.length >= 1 &&
    i.targets.length <= 100 &&
    i.targets.every(
      (t) => object(t) && uuid(t.taskUUID) && integer(t.visibilityVersion, 0),
    ) &&
    (v.action === "restore" ||
      (typeof i.before === "string" && validUTC(i.before)))
  )
    return {
      ...base,
      action: v.action,
      input: {
        reason: i.reason,
        targets: i.targets.map((t) => ({
          taskUUID: t.taskUUID as string,
          visibilityVersion: t.visibilityVersion as number,
        })),
        ...(v.action === "archive" ? { before: i.before as string } : {}),
      },
    };
}
export function maintenanceIntent(profile: Profile) {
  active = { ID: profile.ID, username: profile.username };
  const key = ownerKey(profile);
  if (!pending.has(key)) {
    try {
      const current = sessionStorage.getItem(key);
      const raw = current ?? sessionStorage.getItem(storage);
      const v = raw && raw.length <= 65536 ? JSON.parse(raw) : null;
      const intent =
        v?.owner === profile.ID && v?.username === profile.username
          ? restored(v)
          : undefined;
      if (intent) {
        pending.set(key, intent);
        if (current === null) {
          sessionStorage.setItem(
            key,
            JSON.stringify({
              owner: profile.ID,
              username: profile.username,
              ...intent,
            }),
          );
          sessionStorage.removeItem(storage);
        }
      } else if (current !== null) sessionStorage.removeItem(key);
    } catch {
      /* Preserve in-memory key when storage is unavailable. */
    }
  }
  return pending.get(key);
}
export function beginMaintenanceIntent(
  profile: Profile,
  next: Omit<MaintenanceIntent, "key" | "attempted">,
): MaintenanceIntent {
  const old = maintenanceIntent(profile);
  if (old?.attempted) return old;
  const clean = restored({ ...next, key: crypto.randomUUID() });
  if (!clean) throw Error("Invalid maintenance intent");
  const intent = { ...clean, attempted: false };
  pending.set(ownerKey(profile), intent);
  return intent;
}
export function attemptMaintenanceIntent(
  profile: Profile,
  intent: MaintenanceIntent,
) {
  const clean = restored(intent);
  if (!clean || pending.get(ownerKey(profile)) !== intent)
    throw Error("Invalid maintenance intent");
  intent.input = clean.input;
  intent.attempted = true;
  try {
    sessionStorage.setItem(
      ownerKey(profile),
      JSON.stringify({
        owner: profile.ID,
        username: profile.username,
        key: clean.key,
        action: clean.action,
        input: clean.input,
      }),
    );
  } catch {
    /* The in-memory intent remains. */
  }
}
