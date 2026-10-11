import { ApiError, type Profile } from "./api";
import { validBulkInput, type BulkInput } from "./message-bulk-api";
import { integer, object } from "./task-api";
type Actor = Pick<Profile, "ID" | "username">;
export type BulkIntent = { input: BulkInput; target: number };
const prefix = (p: Actor) =>
  `adtr.message-bulk.v1.${p.ID}.${encodeURIComponent(p.username)}.`;
const keyFor = (p: Actor, i: BulkIntent) => prefix(p) + i.input.operationId;
export function validBulkIntent(v: unknown): v is BulkIntent {
  return (
    object(v) &&
    Object.keys(v).length === 2 &&
    validBulkInput(v.input) &&
    integer(v.target, 1, 1000) &&
    (v.input.selection === "all" || v.target === v.input.messageIds.length)
  );
}
export function loadBulkIntents(p: Actor): BulkIntent[] {
  try {
    return Object.keys(localStorage)
      .sort()
      .filter((k) => k.startsWith(prefix(p)))
      .map((k) => {
        const raw = localStorage.getItem(k);
        if (!raw || raw.length > 150000) throw new Error();
        const v: unknown = JSON.parse(raw);
        if (
          !object(v) ||
          Object.keys(v).length !== 4 ||
          v.version !== 1 ||
          v.owner !== p.ID ||
          v.username !== p.username ||
          !validBulkIntent(v.intent) ||
          k !== keyFor(p, v.intent)
        )
          throw new Error();
        return v.intent;
      });
  } catch {
    throw new ApiError("storage_unavailable");
  }
}
export function persistBulkIntent(p: Actor, i: BulkIntent) {
  if (!validBulkIntent(i)) throw new ApiError("invalid_input");
  const key = keyFor(p, i),
    value = JSON.stringify({
      version: 1,
      owner: p.ID,
      username: p.username,
      intent: { input: i.input, target: i.target },
    });
  try {
    const old = localStorage.getItem(key);
    if (old !== null && old !== value) throw new Error();
    localStorage.setItem(key, value);
    if (localStorage.getItem(key) !== value) throw new Error();
  } catch {
    throw new ApiError("storage_unavailable");
  }
}
export function removeBulkIntent(p: Actor, i: BulkIntent) {
  try {
    const stored = loadBulkIntents(p).find(
      (v) => v.input.operationId === i.input.operationId,
    );
    if (
      stored &&
      (stored.target !== i.target ||
        JSON.stringify(stored.input) !== JSON.stringify(i.input))
    )
      return false;
    const key = keyFor(p, i);
    localStorage.removeItem(key);
    return localStorage.getItem(key) === null;
  } catch {
    return false;
  }
}
