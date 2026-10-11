import { ApiError } from "./api";
import { validUTC } from "./audit-history";
import { object, integer } from "./task-api";
import {
  messageQueryBody,
  messageSubTypes,
  messageTimeKey,
  type MessageQuery,
  type MessageSubType,
} from "./message-query";

export const messageOperations = [
  "POST /api/messages/query",
  "GET /api/messages/page",
  "GET /api/messages/locate",
  "POST /api/messages/read",
  "GET /api/messages/read-operation",
  "POST /api/messages/bulk-read",
  "GET /api/messages/bulk-read-operation",
  "POST /api/messages/bulk-read-cancel",
] as const;
export interface MessageEntry {
  messageId: string;
  taskUUID: string;
  type: "task_result";
  subType: MessageSubType;
  title: string;
  description: string;
  scopeKind: "platform";
  domainId: "";
  occurredAt: string;
  ingestedAt: string;
  read: boolean;
  firstReadAt: string | null;
}
export interface MessageSnapshot {
  resultId: string;
  confirmedAt: string;
  expiresAt: string;
  items: MessageEntry[];
  total: number;
  counts: { all: number; unread: number; read: number };
  offset: number;
  limit: number;
}
export interface MessageLocation {
  resultId: string;
  found: boolean;
  index: number;
  page: number;
}
export const messageID = (v: unknown): v is string =>
  typeof v === "string" && /^[A-Za-z0-9_-]{1,128}$/.test(v);
const exact = (v: Record<string, unknown>, keys: string[]) =>
  Object.keys(v).length === keys.length &&
  keys.every((k) => Object.hasOwn(v, k));
const fail = (): never => {
  throw new ApiError("invalid_response");
};
export function validMessageEntry(v: unknown): v is MessageEntry {
  return (
    object(v) &&
    exact(v, [
      "messageId",
      "taskUUID",
      "type",
      "subType",
      "title",
      "description",
      "scopeKind",
      "domainId",
      "occurredAt",
      "ingestedAt",
      "read",
      "firstReadAt",
    ]) &&
    messageID(v.messageId) &&
    messageID(v.taskUUID) &&
    v.type === "task_result" &&
    messageSubTypes.includes(v.subType as MessageSubType) &&
    typeof v.title === "string" &&
    typeof v.description === "string" &&
    v.scopeKind === "platform" &&
    v.domainId === "" &&
    validUTC(v.occurredAt) &&
    validUTC(v.ingestedAt) &&
    typeof v.read === "boolean" &&
    (v.read ? validUTC(v.firstReadAt) : v.firstReadAt === null)
  );
}
export function validMessageSnapshot(v: unknown): v is MessageSnapshot {
  return (
    object(v) &&
    exact(v, [
      "resultId",
      "confirmedAt",
      "expiresAt",
      "items",
      "total",
      "counts",
      "offset",
      "limit",
    ]) &&
    messageID(v.resultId) &&
    validUTC(v.confirmedAt) &&
    validUTC(v.expiresAt) &&
    messageTimeKey(v.expiresAt) > messageTimeKey(v.confirmedAt) &&
    integer(v.total, 0, 1000) &&
    integer(v.offset, 0, Number.MAX_SAFE_INTEGER) &&
    integer(v.limit, 1, 200) &&
    object(v.counts) &&
    exact(v.counts, ["all", "unread", "read"]) &&
    integer(v.counts.all, 0, 1000) &&
    integer(v.counts.unread, 0, 1000) &&
    integer(v.counts.read, 0, 1000) &&
    v.counts.all === v.counts.unread + v.counts.read &&
    v.total <= v.counts.all &&
    Array.isArray(v.items) &&
    v.items.every(validMessageEntry) &&
    v.items.length === Math.max(0, Math.min(v.limit, v.total - v.offset)) &&
    v.items.filter((row) => row.read).length <= v.counts.read &&
    v.items.filter((row) => !row.read).length <= v.counts.unread &&
    new Set(v.items.map((row) => row.messageId)).size === v.items.length
  );
}
export async function messageRequest(
  path: string,
  signal: AbortSignal,
  body?: unknown,
  csrfToken?: string,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(`/api/messages${path}`, {
      method: body === undefined ? "GET" : "POST",
      signal,
      credentials: "same-origin",
      cache: "no-store",
      headers:
        body === undefined
          ? {}
          : {
              "Content-Type": "application/json",
              "X-CSRF-Token": csrfToken ?? "",
            },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  } catch (error) {
    if (signal.aborted) throw error;
    throw new ApiError("network");
  }
  const value = await response.json().catch(() => {
    throw new ApiError("invalid_response", response.status);
  });
  if (signal.aborted) throw new DOMException("Aborted", "AbortError");
  if (!response.ok)
    throw new ApiError(
      object(value) && typeof value.error === "string"
        ? value.error
        : "internal",
      response.status,
    );
  return value;
}
export function matchesMessageQuery(
  value: MessageSnapshot,
  query: MessageQuery,
) {
  const body = messageQueryBody(query);
  return (
    value.total === value.counts[query.readFilter] &&
    value.items.every((row, index) => {
      if (
        (query.readFilter !== "all" &&
          row.read !== (query.readFilter === "read")) ||
        (query.subType && row.subType !== query.subType) ||
        !row.title.includes(query.keyword)
      )
        return false;
      const time = messageTimeKey(row.occurredAt);
      if (
        body.start &&
        body.end &&
        (time < messageTimeKey(body.start) || time >= messageTimeKey(body.end))
      )
        return false;
      const previous = value.items[index - 1];
      if (!previous) return true;
      const prior = messageTimeKey(previous.occurredAt);
      return prior === time
        ? previous.messageId < row.messageId
        : query.sort === "asc"
          ? prior < time
          : prior > time;
    })
  );
}
export interface MessageReadInput {
  resultId: string;
  messageId: string;
  operationId: string;
}
export interface MessageReadReceipt extends MessageReadInput {
  firstReadAt: string;
  committedAt: string;
  progress: {
    target: 1;
    processed: 1;
    newlyRead: number;
    alreadyRead: number;
    failed: 0;
    remaining: 0;
  };
}
export function validMessageReadInput(v: unknown): v is MessageReadInput {
  return (
    object(v) &&
    exact(v, ["resultId", "messageId", "operationId"]) &&
    messageID(v.resultId) &&
    messageID(v.messageId) &&
    messageID(v.operationId)
  );
}
export function validMessageReadReceipt(
  v: unknown,
  input: MessageReadInput,
): v is MessageReadReceipt {
  return (
    object(v) &&
    exact(v, [
      "resultId",
      "messageId",
      "operationId",
      "firstReadAt",
      "committedAt",
      "progress",
    ]) &&
    v.resultId === input.resultId &&
    v.messageId === input.messageId &&
    v.operationId === input.operationId &&
    validUTC(v.firstReadAt) &&
    validUTC(v.committedAt) &&
    object(v.progress) &&
    exact(v.progress, [
      "target",
      "processed",
      "newlyRead",
      "alreadyRead",
      "failed",
      "remaining",
    ]) &&
    v.progress.target === 1 &&
    v.progress.processed === 1 &&
    v.progress.failed === 0 &&
    v.progress.remaining === 0 &&
    integer(v.progress.newlyRead, 0, 1) &&
    integer(v.progress.alreadyRead, 0, 1) &&
    v.progress.newlyRead + v.progress.alreadyRead === 1
  );
}
export const messageAPI = {
  async markRead(input: MessageReadInput, csrf: string, signal: AbortSignal) {
    if (!validMessageReadInput(input)) throw new ApiError("invalid_input");
    const value = await messageRequest("/read", signal, input, csrf);
    if (!validMessageReadReceipt(value, input)) return fail();
    return value;
  },
  async readOperation(input: MessageReadInput, signal: AbortSignal) {
    if (!validMessageReadInput(input)) throw new ApiError("invalid_input");
    const value = await messageRequest(
      `/read-operation?${new URLSearchParams({ operationId: input.operationId })}`,
      signal,
    );
    if (!validMessageReadReceipt(value, input)) return fail();
    return value;
  },
  async query(query: MessageQuery, csrf: string, signal: AbortSignal) {
    const body = messageQueryBody(query),
      value = await messageRequest("/query", signal, body, csrf);
    if (
      !validMessageSnapshot(value) ||
      value.offset !== 0 ||
      value.limit !== query.limit ||
      !matchesMessageQuery(value, query)
    )
      return fail();
    return value;
  },
  async page(
    snapshot: MessageSnapshot,
    offset: number,
    signal: AbortSignal,
    query?: MessageQuery,
  ) {
    if (
      !messageID(snapshot.resultId) ||
      !integer(offset, 0, Number.MAX_SAFE_INTEGER)
    )
      throw new ApiError("invalid_input");
    const params = new URLSearchParams({
      resultId: snapshot.resultId,
      offset: String(offset),
      limit: String(snapshot.limit),
    });
    const value = await messageRequest(`/page?${params}`, signal);
    if (
      !validMessageSnapshot(value) ||
      value.resultId !== snapshot.resultId ||
      value.offset !== offset ||
      value.limit !== snapshot.limit ||
      value.total !== snapshot.total ||
      value.confirmedAt !== snapshot.confirmedAt ||
      value.expiresAt !== snapshot.expiresAt ||
      value.counts.all !== snapshot.counts.all ||
      value.counts.unread !== snapshot.counts.unread ||
      value.counts.read !== snapshot.counts.read ||
      (query !== undefined && !matchesMessageQuery(value, query)) ||
      (offset === snapshot.offset &&
        value.items.some((row, index) =>
          Object.entries(row).some(
            ([key, item]) =>
              item !== snapshot.items[index]?.[key as keyof MessageEntry],
          ),
        ))
    )
      return fail();
    return value;
  },
  async locate(
    snapshot: MessageSnapshot,
    id: string,
    signal: AbortSignal,
  ): Promise<MessageLocation> {
    if (!messageID(snapshot.resultId) || !messageID(id))
      throw new ApiError("invalid_message_id");
    const params = new URLSearchParams({
      resultId: snapshot.resultId,
      messageId: id,
      limit: String(snapshot.limit),
    });
    const value = await messageRequest(`/locate?${params}`, signal);
    if (
      !object(value) ||
      !exact(value, ["resultId", "found", "index", "page"]) ||
      value.resultId !== snapshot.resultId ||
      typeof value.found !== "boolean" ||
      (value.found
        ? !integer(value.index, 0, snapshot.total - 1) ||
          value.page !==
            Math.floor((value.index as number) / snapshot.limit) + 1
        : value.index !== -1 || value.page !== 0)
    )
      return fail();
    return value as unknown as MessageLocation;
  },
};
