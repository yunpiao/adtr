import { ApiError } from "./api";
import { validUTC } from "./audit-history";

export const messageSubTypes = [
  "succeeded",
  "failed",
  "partial_failed",
  "dead_letter",
  "cancelled",
] as const;
export type MessageSubType = (typeof messageSubTypes)[number];
export const messageSubTypeLabels: Record<MessageSubType, string> = {
  succeeded: "成功",
  failed: "失败",
  partial_failed: "部分失败",
  dead_letter: "死信",
  cancelled: "已取消",
};
export interface MessageQuery {
  keyword: string;
  subType: "" | MessageSubType;
  start: string;
  end: string;
  readFilter: "all" | "unread" | "read";
  sort: "asc" | "desc";
  limit: number;
}
export const defaultMessageQuery = (): MessageQuery => ({
  keyword: "",
  subType: "",
  start: "",
  end: "",
  readFilter: "all",
  sort: "desc",
  limit: 20,
});
// Compare without losing the RFC3339 fractional precision supplied by the user.
export const messageTimeKey = (value: string) =>
  value.slice(0, 19) + (value.split(".")[1]?.slice(0, -1) ?? "").padEnd(9, "0");
export function messageInputUTC(value: string): string {
  if (!value) return "";
  const utc = value.endsWith("Z")
    ? value
    : `${value.length === 16 ? `${value}:00` : value}Z`;
  if (!validUTC(utc)) throw new ApiError("invalid_message_time");
  return utc;
}
export function messageQueryBody(query: MessageQuery) {
  const start = messageInputUTC(query.start),
    end = messageInputUTC(query.end);
  if (
    Boolean(start) !== Boolean(end) ||
    (start && messageTimeKey(start) >= messageTimeKey(end))
  )
    throw new ApiError("invalid_message_time");
  if (
    ![20, 50, 100, 200].includes(query.limit) ||
    !["all", "unread", "read"].includes(query.readFilter) ||
    !["asc", "desc"].includes(query.sort) ||
    (query.subType !== "" && !messageSubTypes.includes(query.subType)) ||
    typeof query.keyword !== "string" ||
    new TextEncoder().encode(query.keyword).length > 16000
  )
    throw new ApiError("invalid_input");
  return {
    types: ["task_result"],
    subTypes: query.subType
      ? [{ type: "task_result", subType: query.subType }]
      : [],
    keyword: query.keyword,
    ...(start ? { start, end } : {}),
    readFilter: query.readFilter,
    sort: query.sort,
    offset: 0,
    limit: query.limit,
  };
}
