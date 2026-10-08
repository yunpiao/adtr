import type { Profile } from "./api";
import { auditColumns, type ExportInput } from "./audit-api";
import { object } from "./task-api";
interface Intent {
  input: ExportInput;
  attempted: boolean;
}
const storageKey = "adtr.pending-audit-export";
const pending = new Map<string, Intent>();
type Actor = Pick<Profile, "ID" | "username">;
let active: Actor | undefined;
const ownerKey = (p: Actor) =>
  `${storageKey}:${p.ID}:${encodeURIComponent(p.username)}`;
// Session changes forget bindings, not uncertain non-secret recovery records.
export function forgetAuditSession() {
  for (const [key, intent] of pending)
    if (!intent.attempted) pending.delete(key);
  active = undefined;
}
export function discardAuditIntent(profile: Actor | undefined = active) {
  if (!profile) return;
  pending.delete(ownerKey(profile));
  try {
    sessionStorage.removeItem(ownerKey(profile));
    const legacy = JSON.parse(sessionStorage.getItem(storageKey) ?? "null");
    if (legacy?.owner === profile.ID && legacy?.username === profile.username)
      sessionStorage.removeItem(storageKey);
  } catch {
    /* Storage may be disabled. */
  }
}
// Whitelist on both write and restore: proof, tokens, responses and arbitrary
// properties must never enter persisted recovery or a later request.
function project(input: unknown): ExportInput | undefined {
  if (
    !object(input) ||
    typeof input.idempotencyKey !== "string" ||
    !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(
      input.idempotencyKey,
    ) ||
    typeof input.startTm !== "string" ||
    input.startTm.length > 64 ||
    typeof input.endTm !== "string" ||
    input.endTm.length > 64 ||
    typeof input.keyword !== "string" ||
    Array.from(input.keyword).length > 50 ||
    !Array.isArray(input.filterEvent) ||
    input.filterEvent.length > 100 ||
    !input.filterEvent.every(
      (v: unknown) => typeof v === "string" && Array.from(v).length <= 128,
    ) ||
    !Array.isArray(input.logTypeList) ||
    input.logTypeList.length > 9 ||
    !input.logTypeList.every(
      (v: unknown) =>
        typeof v === "number" && Number.isInteger(v) && v >= 1 && v <= 9,
    ) ||
    ![1, -1].includes(input.createSort as number) ||
    !["visible", "hidden", "all"].includes(input.visibility as string) ||
    !Array.isArray(input.selectColumn) ||
    input.selectColumn.length < 1 ||
    input.selectColumn.length > 8 ||
    !input.selectColumn.every((v: unknown) =>
      auditColumns.includes(v as never),
    ) ||
    new Set(input.selectColumn).size !== input.selectColumn.length
  )
    return;
  const clean: ExportInput = {
    idempotencyKey: input.idempotencyKey,
    startTm: input.startTm,
    endTm: input.endTm,
    keyword: input.keyword,
    filterEvent: [...input.filterEvent],
    logTypeList: [...input.logTypeList],
    createSort: input.createSort as number,
    visibility: input.visibility as ExportInput["visibility"],
    selectColumn: [...input.selectColumn],
  };
  return JSON.stringify(clean).length <= 60000 ? clean : undefined;
}
export function auditIntent(profile: Profile): Intent | undefined {
  active = { ID: profile.ID, username: profile.username };
  const key = ownerKey(profile);
  if (!pending.has(key)) {
    try {
      const current = sessionStorage.getItem(key);
      const raw = current ?? sessionStorage.getItem(storageKey);
      const stored = raw && raw.length <= 65536 ? JSON.parse(raw) : null;
      const input =
        stored?.owner === profile.ID && stored?.username === profile.username
          ? project(stored.input)
          : undefined;
      if (input) {
        const intent = { attempted: true, input };
        pending.set(key, intent);
        if (current === null) {
          sessionStorage.setItem(
            key,
            JSON.stringify({
              owner: profile.ID,
              username: profile.username,
              input,
            }),
          );
          sessionStorage.removeItem(storageKey);
        }
      } else if (current !== null) sessionStorage.removeItem(key);
    } catch {
      /* Keep memory recovery when storage is unavailable. */
    }
  }
  return pending.get(key);
}
export function beginAuditIntent(
  profile: Profile,
  input: Omit<ExportInput, "idempotencyKey">,
): Intent {
  const current = auditIntent(profile);
  if (current?.attempted) return current;
  const clean = project({ ...input, idempotencyKey: crypto.randomUUID() });
  if (!clean) throw Error("Invalid audit intent");
  const intent = { input: clean, attempted: false };
  pending.set(ownerKey(profile), intent);
  return intent;
}
export function attemptedAuditIntent(profile: Profile, intent: Intent) {
  const input = project(intent.input);
  if (!input || pending.get(ownerKey(profile)) !== intent)
    throw Error("Invalid audit intent");
  intent.input = input;
  intent.attempted = true;
  try {
    sessionStorage.setItem(
      ownerKey(profile),
      JSON.stringify({ owner: profile.ID, username: profile.username, input }),
    );
  } catch {
    /* The original key remains available in memory. */
  }
}
