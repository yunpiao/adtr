import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { messageAPI, validMessageSnapshot } from "./message-api";
import {
  defaultMessageQuery,
  messageInputUTC,
  messageQueryBody,
} from "./message-query";
import { sampleSnapshot } from "./message-test-fixtures";
const controller = () => new AbortController();
beforeEach(() => vi.stubGlobal("fetch", vi.fn()));
afterEach(() => vi.unstubAllGlobals());
describe("message transport contract", () => {
  it("sends the explicit default all/desc/20 exactly once with CSRF and no-store", async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify(sampleSnapshot())),
    );
    expect(
      await messageAPI.query(
        defaultMessageQuery(),
        "csrf",
        controller().signal,
      ),
    ).toEqual(sampleSnapshot());
    expect(fetch).toHaveBeenCalledTimes(1);
    const [url, init] = vi.mocked(fetch).mock.calls[0];
    expect(url).toBe("/api/messages/query");
    expect(init).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: { "X-CSRF-Token": "csrf" },
    });
    expect(JSON.parse(init!.body as string)).toEqual({
      types: ["task_result"],
      subTypes: [],
      keyword: "",
      readFilter: "all",
      sort: "desc",
      offset: 0,
      limit: 20,
    });
  });
  it("preserves nanosecond time boundaries and literal keywords", () => {
    const start = "2026-10-10T00:00:00.000000001Z",
      end = "2026-10-10T00:00:00.000000002Z";
    expect(
      messageQueryBody({
        ...defaultMessageQuery(),
        start,
        end,
        keyword: "[A].*",
      }),
    ).toMatchObject({ start, end, keyword: "[A].*" });
    expect(messageInputUTC("2026-10-10T00:00")).toBe("2026-10-10T00:00:00Z");
  });
  it.each([
    { start: "2026-10-10T00:00:00Z" },
    { start: "2026-10-10T00:00:00Z", end: "2026-10-10T00:00:00Z" },
    { start: "2026-02-30T00:00:00Z", end: "2026-03-01T00:00:00Z" },
    { limit: -1 },
    { limit: 1 },
    { keyword: "x".repeat(16001) },
  ])("rejects invalid input before transport: %j", async (patch) => {
    await expect(
      messageAPI.query(
        { ...defaultMessageQuery(), ...patch },
        "csrf",
        controller().signal,
      ),
    ).rejects.toThrow();
    expect(fetch).not.toHaveBeenCalled();
  });
  it("uses original result ID and metadata for page", async () => {
    const value = { ...sampleSnapshot(), offset: 20, items: [] };
    vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify(value)));
    await messageAPI.page(sampleSnapshot(), 20, controller().signal);
    expect(vi.mocked(fetch).mock.calls[0][0]).toBe(
      "/api/messages/page?resultId=result-1&offset=20&limit=20",
    );
  });
  it.each([
    "resultId",
    "confirmedAt",
    "expiresAt",
    "total",
    "limit",
    "offset",
    "counts",
  ])("rejects changed page %s", async (field) => {
    const value: any = sampleSnapshot();
    value[field] =
      field === "counts"
        ? { all: 2, unread: 2, read: 0 }
        : field === "total"
          ? 2
          : field === "limit"
            ? 50
            : field === "offset"
              ? 1
              : "changed";
    vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify(value)));
    await expect(
      messageAPI.page(sampleSnapshot(), 0, controller().signal),
    ).rejects.toThrow("invalid_response");
  });
  it.each([
    null,
    { ...sampleSnapshot(), counts: { all: 2, read: 0, unread: 1 } },
    { ...sampleSnapshot(), total: 2 },
    {
      ...sampleSnapshot(),
      items: [{ ...sampleSnapshot().items[0], scopeKind: "domain" }],
    },
    {
      ...sampleSnapshot(),
      items: [{ ...sampleSnapshot().items[0], read: true }],
    },
    {
      ...sampleSnapshot(),
      items: [{ ...sampleSnapshot().items[0], payload: "secret" }],
    },
  ])("rejects malformed snapshots", (value) =>
    expect(validMessageSnapshot(value)).toBe(false),
  );
  it("validates locate membership and one-based page", async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(
        JSON.stringify({
          resultId: "result-1",
          found: true,
          index: 0,
          page: 1,
        }),
      ),
    );
    expect(
      await messageAPI.locate(
        sampleSnapshot(),
        "health-1",
        controller().signal,
      ),
    ).toMatchObject({ found: true, page: 1 });
    expect(vi.mocked(fetch).mock.calls[0][0]).toBe(
      "/api/messages/locate?resultId=result-1&messageId=health-1&limit=20",
    );
  });
  it.each([
    { resultId: "other", found: true, index: 0, page: 1 },
    { resultId: "result-1", found: true, index: 1, page: 1 },
    { resultId: "result-1", found: false, index: 0, page: 0 },
    { resultId: "result-1", found: true, index: 0, page: 0 },
  ])("rejects invalid locate", async (value) => {
    vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify(value)));
    await expect(
      messageAPI.locate(sampleSnapshot(), "health-1", controller().signal),
    ).rejects.toThrow("invalid_response");
  });
  it("accepts explicit locate miss", async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(
        JSON.stringify({
          resultId: "result-1",
          found: false,
          index: -1,
          page: 0,
        }),
      ),
    );
    expect(
      (
        await messageAPI.locate(
          sampleSnapshot(),
          "health-2",
          controller().signal,
        )
      ).found,
    ).toBe(false);
  });
  it("does not retry a lost POST", async () => {
    vi.mocked(fetch).mockRejectedValue(new TypeError("network"));
    await expect(
      messageAPI.query(defaultMessageQuery(), "csrf", controller().signal),
    ).rejects.toThrow("network");
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it("drops a late JSON body after abort", async () => {
    let finish!: (value: unknown) => void;
    const ctrl = controller();
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      status: 200,
      json: () => new Promise((resolve) => (finish = resolve)),
    } as Response);
    const pending = messageAPI.query(
      defaultMessageQuery(),
      "csrf",
      ctrl.signal,
    );
    await Promise.resolve();
    ctrl.abort();
    finish(sampleSnapshot());
    await expect(pending).rejects.toThrow("Aborted");
  });
});
it("rejects page read counts contradicting fixed counts", async () => {
  vi.mocked(fetch).mockResolvedValue(
    new Response(
      JSON.stringify({
        ...sampleSnapshot(),
        items: [
          {
            ...sampleSnapshot().items[0],
            read: true,
            firstReadAt: sampleSnapshot().confirmedAt,
          },
        ],
      }),
    ),
  );
  await expect(
    messageAPI.page(sampleSnapshot(), 0, controller().signal),
  ).rejects.toThrow("invalid_response");
});
it("rejects changed same-page content even when aggregate counts allow it", async () => {
  const fixed = {
    ...sampleSnapshot(),
    total: 2,
    counts: { all: 2, read: 1, unread: 1 },
    limit: 1,
  };
  vi.mocked(fetch).mockResolvedValue(
    new Response(
      JSON.stringify({
        ...fixed,
        items: [
          { ...fixed.items[0], read: true, firstReadAt: fixed.confirmedAt },
        ],
      }),
    ),
  );
  await expect(messageAPI.page(fixed, 0, controller().signal)).rejects.toThrow(
    "invalid_response",
  );
});
it.each(["read", "unread"] as const)(
  "accepts %s filter with distinct base counts",
  async (readFilter) => {
    const value = {
      ...sampleSnapshot(),
      counts: {
        all: 4,
        read: readFilter === "read" ? 1 : 3,
        unread: readFilter === "unread" ? 1 : 3,
      },
      items: [
        {
          ...sampleSnapshot().items[0],
          read: readFilter === "read",
          firstReadAt:
            readFilter === "read" ? sampleSnapshot().confirmedAt : null,
        },
      ],
    };
    vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify(value)));
    expect(
      (
        await messageAPI.query(
          { ...defaultMessageQuery(), readFilter },
          "csrf",
          controller().signal,
        )
      ).total,
    ).toBe(1);
  },
);
it.each(["asc", "desc"] as const)(
  "keeps tied ID ascending under %s and rejects reversed ties",
  async (sort) => {
    const value = {
      ...sampleSnapshot(),
      total: 2,
      counts: { all: 2, read: 0, unread: 2 },
      items: [
        sampleSnapshot().items[0],
        { ...sampleSnapshot().items[0], messageId: "health-2" },
      ],
    };
    vi.mocked(fetch)
      .mockResolvedValueOnce(new Response(JSON.stringify(value)))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ ...value, items: [...value.items].reverse() }),
        ),
      );
    expect(
      (
        await messageAPI.query(
          { ...defaultMessageQuery(), sort },
          "csrf",
          controller().signal,
        )
      ).total,
    ).toBe(2);
    await expect(
      messageAPI.query(
        { ...defaultMessageQuery(), sort },
        "csrf",
        controller().signal,
      ),
    ).rejects.toThrow("invalid_response");
  },
);
it("rejects a row equal to the nanosecond exclusive end", async () => {
  vi.mocked(fetch).mockResolvedValue(
    new Response(JSON.stringify(sampleSnapshot())),
  );
  await expect(
    messageAPI.query(
      {
        ...defaultMessageQuery(),
        start: "2026-10-10T23:59:59.123456788Z",
        end: sampleSnapshot().items[0].occurredAt,
      },
      "csrf",
      controller().signal,
    ),
  ).rejects.toThrow("invalid_response");
});
