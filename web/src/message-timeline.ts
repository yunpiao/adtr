import type { MessageEntry } from "./message-api";

// Input is the accepted current page: its occurredAt values are validated UTC.
// Emit only adjacent date boundaries; never sort, merge, or modify message rows.
export function messageTimelineDates(
  items: readonly Pick<MessageEntry, "occurredAt">[],
): (string | null)[] {
  return items.map((row, index) => {
    const date = row.occurredAt.slice(0, 10);
    return index === 0 || date !== items[index - 1].occurredAt.slice(0, 10)
      ? date
      : null;
  });
}
