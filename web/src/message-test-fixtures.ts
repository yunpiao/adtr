import type { MessageSnapshot } from "./message-api";
export const sampleSnapshot = (): MessageSnapshot => ({
  resultId: "result-1",
  confirmedAt: "2026-10-11T00:00:00Z",
  expiresAt: "2026-10-11T00:10:00Z",
  offset: 0,
  limit: 20,
  total: 1,
  counts: { all: 1, unread: 1, read: 0 },
  items: [
    {
      messageId: "health-1",
      taskUUID: "task-1",
      type: "task_result",
      subType: "succeeded",
      title: "Health succeeded",
      description: "Real projection template",
      scopeKind: "platform",
      domainId: "",
      occurredAt: "2026-10-10T23:59:59.123456789Z",
      ingestedAt: "2026-10-11T00:00:00Z",
      read: false,
      firstReadAt: null,
    },
  ],
});
