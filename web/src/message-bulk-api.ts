import { ApiError } from "./api";
import { validUTC } from "./audit-history";
import { messageTimeKey } from "./message-query";
import { messageID, messageRequest } from "./message-api";
import { integer, object } from "./task-api";

export type BulkInput = { resultId: string; operationId: string } & (
  | { selection: "all" }
  | { selection: "selected"; messageIds: string[] }
);
export type BulkOperation = {
  receipt: {
    operationId: string;
    resultId: string;
    selection: "all" | "selected";
    acceptedAt: string;
    target: number;
  };
  state:
    | "queued"
    | "running"
    | "completed"
    | "cancelled"
    | "authorization_blocked";
  updatedAt: string;
  progress: {
    target: number;
    processed: number;
    newlyRead: number;
    alreadyRead: number;
    failed: 0;
    remaining: number;
  };
};
const exact = (v: Record<string, unknown>, keys: string[]) =>
  Object.keys(v).length === keys.length &&
  keys.every((k) => Object.hasOwn(v, k));
export function validBulkInput(v: unknown): v is BulkInput {
  return (
    object(v) &&
    messageID(v.resultId) &&
    messageID(v.operationId) &&
    (v.selection === "all"
      ? exact(v, ["resultId", "operationId", "selection"])
      : v.selection === "selected" &&
        exact(v, ["resultId", "operationId", "selection", "messageIds"]) &&
        Array.isArray(v.messageIds) &&
        v.messageIds.length > 0 &&
        v.messageIds.length <= 1000 &&
        v.messageIds.every(messageID) &&
        new Set(v.messageIds).size === v.messageIds.length)
  );
}
export const bulkTerminal = (state: BulkOperation["state"]) =>
  ["completed", "cancelled", "authorization_blocked"].includes(state);
export function validBulkOperation(
  v: unknown,
  input: BulkInput,
  target: number,
): v is BulkOperation {
  if (
    !object(v) ||
    !exact(v, ["receipt", "state", "updatedAt", "progress"]) ||
    !object(v.receipt) ||
    !object(v.progress)
  )
    return false;
  const r = v.receipt,
    p = v.progress;
  return (
    exact(r, [
      "operationId",
      "resultId",
      "selection",
      "acceptedAt",
      "target",
    ]) &&
    r.operationId === input.operationId &&
    r.resultId === input.resultId &&
    r.selection === input.selection &&
    integer(target, 1, 1000) &&
    r.target === target &&
    (input.selection !== "selected" || target === input.messageIds.length) &&
    validUTC(r.acceptedAt) &&
    validUTC(v.updatedAt) &&
    messageTimeKey(v.updatedAt) >= messageTimeKey(r.acceptedAt) &&
    typeof v.state === "string" &&
    [
      "queued",
      "running",
      "completed",
      "cancelled",
      "authorization_blocked",
    ].includes(v.state) &&
    exact(p, [
      "target",
      "processed",
      "newlyRead",
      "alreadyRead",
      "failed",
      "remaining",
    ]) &&
    p.target === target &&
    [p.processed, p.newlyRead, p.alreadyRead, p.remaining].every((n) =>
      integer(n, 0, target),
    ) &&
    p.failed === 0 &&
    p.target === Number(p.processed) + Number(p.remaining) &&
    p.processed === Number(p.newlyRead) + Number(p.alreadyRead) &&
    (v.state !== "queued" || p.processed === 0) &&
    (v.state !== "running" ||
      (Number(p.processed) > 0 && Number(p.remaining) > 0)) &&
    (v.state !== "completed" || p.remaining === 0)
  );
}
export function checkBulkProgress(
  next: BulkOperation,
  previous?: BulkOperation,
) {
  if (!previous) return;
  const p = previous.progress,
    n = next.progress;
  if (
    (Object.keys(next.receipt) as (keyof BulkOperation["receipt"])[]).some(
      (k) => next.receipt[k] !== previous.receipt[k],
    ) ||
    messageTimeKey(next.updatedAt) < messageTimeKey(previous.updatedAt) ||
    n.processed < p.processed ||
    n.newlyRead < p.newlyRead ||
    n.alreadyRead < p.alreadyRead ||
    n.remaining > p.remaining ||
    (bulkTerminal(previous.state) &&
      (next.state !== previous.state ||
        next.updatedAt !== previous.updatedAt ||
        (Object.keys(n) as (keyof BulkOperation["progress"])[]).some(
          (k) => n[k] !== p[k],
        )))
  )
    throw new ApiError("invalid_response");
}
export const bulkAPI = {
  async run(
    input: BulkInput,
    target: number,
    mode: "start" | "lookup" | "cancel",
    csrf: string,
    signal: AbortSignal,
  ) {
    if (!validBulkInput(input) || !integer(target, 1, 1000))
      throw new ApiError("invalid_input");
    if (
      mode === "start" &&
      new TextEncoder().encode(JSON.stringify(input)).length > 32768
    )
      throw new ApiError("bulk_request_too_large");
    const value =
      mode === "lookup"
        ? await messageRequest(
            `/bulk-read-operation?${new URLSearchParams({ operationId: input.operationId })}`,
            signal,
          )
        : await messageRequest(
            mode === "start" ? "/bulk-read" : "/bulk-read-cancel",
            signal,
            mode === "start" ? input : { operationId: input.operationId },
            csrf,
          );
    if (!validBulkOperation(value, input, target))
      throw new ApiError("invalid_response");
    return value;
  },
};
