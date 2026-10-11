import { ApiError, type Profile } from "./api";
import { validMessageReadInput, type MessageReadInput } from "./message-api";
import { object } from "./task-api";

type Actor = Pick<Profile, "ID" | "username">;
const prefix = "adtr.message-read.v1.";
const ownerPrefix = (p: Actor) =>
  `${prefix}${p.ID}.${encodeURIComponent(p.username)}.`;
const keyFor = (p: Actor, input: MessageReadInput) =>
  ownerPrefix(p) + input.operationId;
// Only original non-secret identifiers persist, not session bindings, messages,
// receipts or private query rows. Per-operation keys cannot overwrite another tab.
export function loadMessageReadIntents(p: Actor): MessageReadInput[] {
  const records: MessageReadInput[] = [];
  try {
    for (const key of Object.keys(localStorage).sort()) {
      if (!key.startsWith(ownerPrefix(p))) continue;
      const raw = localStorage.getItem(key);
      if (!raw || raw.length > 2048) throw new Error();
      const v: unknown = JSON.parse(raw);
      if (
        !object(v) ||
        Object.keys(v).length !== 4 ||
        v.version !== 1 ||
        v.owner !== p.ID ||
        v.username !== p.username ||
        !validMessageReadInput(v.input) ||
        key !== keyFor(p, v.input)
      )
        throw new Error();
      records.push({ ...v.input });
    }
  } catch {
    // Never replace an unreadable uncertain intent with a new operation.
    throw new ApiError("storage_unavailable");
  }
  return records;
}
export function persistMessageReadIntent(p: Actor, input: MessageReadInput) {
  if (!validMessageReadInput(input)) throw new ApiError("invalid_input");
  const key = keyFor(p, input);
  const saved = JSON.stringify({
    version: 1,
    owner: p.ID,
    username: p.username,
    input,
  });
  try {
    const existing = localStorage.getItem(key);
    if (existing !== null && existing !== saved) throw new Error();
    localStorage.setItem(key, saved);
    if (localStorage.getItem(key) !== saved) throw new Error();
  } catch {
    throw new ApiError("storage_unavailable");
  }
}
export function completeMessageReadIntent(p: Actor, input: MessageReadInput) {
  const key = keyFor(p, input);
  try {
    const raw = localStorage.getItem(key);
    if (raw === null) return true;
    const stored = JSON.parse(raw);
    if (
      stored.owner !== p.ID ||
      stored.username !== p.username ||
      !validMessageReadInput(stored.input) ||
      Object.keys(input).some(
        (k) => input[k as keyof MessageReadInput] !== stored.input[k],
      )
    )
      return false;
    localStorage.removeItem(key);
    return localStorage.getItem(key) === null;
  } catch {
    return false;
  }
}
