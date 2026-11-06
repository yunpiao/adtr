import { EventEmitter } from "node:events";
import { createHash } from "node:crypto";
import type { Page, Request, Response } from "@playwright/test";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  observeSystemCandidateRead,
  type CandidateKind,
} from "../e2e/system-candidate-observations";

const now = "2026-11-06T09:00:00Z";
const infoURL = "http://127.0.0.1:8080/api/system/info";
const storageURL =
  "http://127.0.0.1:8080/api/system/storage?instance=local-api&page=1&pageSize=20";
const safeError = /^System candidate read observation failed$/u;
const events = [
  "request",
  "response",
  "requestfinished",
  "requestfailed",
] as const;
const bytes = (value: unknown) => Buffer.from(JSON.stringify(value));
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};
const overview = () => ({
  current: {
    instance: "local-api",
    checkedAt: now,
    availability: "available",
    stale: false,
    snapshot: {
      instance: "local-api",
      observedAt: now,
      storage: [
        {
          id: "runtime-root",
          source: "statfs",
          scope: "runtime_filesystem",
          percent: 34.25,
          availability: "available",
          observedAt: now,
        },
      ],
    },
  },
});
const storage = () => ({
  instance: "local-api",
  checkedAt: now,
  availability: "available",
  stale: false,
  page: 1,
  pageSize: 20,
  total: 1,
  storage: [{ id: "runtime-root", revision: 0, alarmPercent: 85 }],
});
class FakePage extends EventEmitter {
  readonly frame = {};
  url = vi.fn(() => "http://127.0.0.1:8080/#system");
  mainFrame = vi.fn(() => this.frame);
  context = vi.fn(() => {
    throw new Error("No context requests allowed");
  });
  route = vi.fn(() => {
    throw new Error("No routes allowed");
  });
  evaluate = vi.fn(() => {
    throw new Error("No injected fetch allowed");
  });
  page() {
    return this as unknown as Page;
  }
}
class FakeRequest {
  constructor(
    readonly page: FakePage,
    readonly address = infoURL,
    readonly options: {
      method?: string;
      navigation?: boolean;
      frame?: object;
    } = {},
  ) {}
  url() {
    return this.address;
  }
  method() {
    return this.options.method ?? "GET";
  }
  frame() {
    return this.options.frame ?? this.page.frame;
  }
  isNavigationRequest() {
    return this.options.navigation ?? false;
  }
  request() {
    return this as unknown as Request;
  }
}
class FakeResponse {
  body = vi.fn<() => Promise<Buffer>>();
  json = vi.fn(() => {
    throw new Error("JSON read bypassed bounded decoder");
  });
  headers = vi.fn(() => {
    throw new Error("Headers must not be retained");
  });
  constructor(
    readonly owner: FakeRequest,
    value: Buffer = bytes(overview()),
    readonly code = 200,
  ) {
    this.body.mockResolvedValue(value);
  }
  request() {
    return this.owner.request();
  }
  status() {
    return this.code;
  }
  response() {
    return this as unknown as Response;
  }
}
function emit(
  page: FakePage,
  request: FakeRequest,
  response: FakeResponse,
  finish = true,
) {
  page.emit("request", request.request());
  page.emit("response", response.response());
  if (finish) page.emit("requestfinished", request.request());
}
function cleaned(page: FakePage) {
  for (const event of events) expect(page.listenerCount(event)).toBe(0);
  expect(vi.getTimerCount()).toBe(0);
  expect(page.context).not.toHaveBeenCalled();
  expect(page.route).not.toHaveBeenCalled();
  expect(page.evaluate).not.toHaveBeenCalled();
}
async function observeValue(
  value: unknown,
  kind: CandidateKind = "overview-disk",
  raw?: Buffer,
) {
  const page = new FakePage();
  const request = new FakeRequest(
    page,
    kind === "overview-disk" ? infoURL : storageURL,
  );
  const response = new FakeResponse(request, raw ?? bytes(value));
  const result = observeSystemCandidateRead(page.page(), kind, async () =>
    emit(page, request, response),
  );
  try {
    return await result;
  } finally {
    cleaned(page);
    expect(response.body).toHaveBeenCalledTimes(1);
  }
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(now));
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("native system candidate read collector", () => {
  it("retains only bounded fields and SHA256 of exact bytes after native finish", async () => {
    const page = new FakePage();
    const request = new FakeRequest(page);
    const raw = Buffer.from(
      JSON.stringify({ ...overview(), secret: "do-not-retain" }) + "\n",
    );
    const response = new FakeResponse(request, raw);
    const action = vi.fn(async () => emit(page, request, response, false));
    const result = observeSystemCandidateRead(
      page.page(),
      "overview-disk",
      action,
    );
    await vi.advanceTimersByTimeAsync(0);
    expect(action).toHaveBeenCalledTimes(1);
    expect(response.body).not.toHaveBeenCalled();
    page.emit("requestfinished", request.request());
    page.emit("requestfinished", request.request());
    const observation = await result;
    expect(observation).toEqual({
      kind: "overview-disk",
      responseSHA256: createHash("sha256").update(raw).digest("hex"),
      nativeFinished: true,
      current: { checkedAt: now, availability: "available", stale: false },
      disk: {
        id: "runtime-root",
        source: "statfs",
        scope: "runtime_filesystem",
        percent: 34.25,
        availability: "available",
        observedAt: now,
      },
    });
    expect(JSON.stringify(observation)).not.toContain("do-not-retain");
    expect(response.body).toHaveBeenCalledTimes(1);
    expect(response.json).not.toHaveBeenCalled();
    expect(response.headers).not.toHaveBeenCalled();
    cleaned(page);
  });

  it("cannot witness a response to an old request or a different Request identity", async () => {
    const page = new FakePage();
    const old = new FakeRequest(page);
    const oldResponse = new FakeResponse(old);
    page.emit("request", old.request());
    const current = new FakeRequest(page);
    const currentResponse = new FakeResponse(current);
    const action = vi.fn(async () => {
      page.emit("response", oldResponse.response());
      page.emit("requestfinished", old.request());
      page.emit("request", current.request());
      page.emit("response", oldResponse.response());
      page.emit("requestfinished", old.request());
      page.emit("requestfailed", old.request());
    });
    const result = observeSystemCandidateRead(
      page.page(),
      "overview-disk",
      action,
    );
    await vi.advanceTimersByTimeAsync(0);
    expect(oldResponse.body).not.toHaveBeenCalled();
    expect(currentResponse.body).not.toHaveBeenCalled();
    page.emit("response", currentResponse.response());
    page.emit("requestfinished", current.request());
    expect((await result).nativeFinished).toBe(true);
    expect(oldResponse.body).not.toHaveBeenCalled();
    expect(currentResponse.body).toHaveBeenCalledTimes(1);
    cleaned(page);
  });

  it("ignores requests emitted after listener installation but before the action starts", async () => {
    const page = new FakePage();
    const early = new FakeRequest(page);
    const earlyResponse = new FakeResponse(early);
    const request = new FakeRequest(page);
    const response = new FakeResponse(request);
    const result = observeSystemCandidateRead(
      page.page(),
      "overview-disk",
      async () => emit(page, request, response),
    );
    emit(page, early, earlyResponse);
    await result;
    expect(earlyResponse.body).not.toHaveBeenCalled();
    expect(response.body).toHaveBeenCalledTimes(1);
    cleaned(page);
  });

  it.each([
    ["foreign origin", "https://example.invalid/api/system/info", {}],
    ["foreign frame", infoURL, { frame: {} }],
    ["POST", infoURL, { method: "POST" }],
    ["navigation", infoURL, { navigation: true }],
    ["unknown path", "http://127.0.0.1:8080/api/system/health", {}],
    ["trailing path", infoURL + "/extra", {}],
    ["query", infoURL + "?instance=local-api", {}],
    ["duplicate query", infoURL + "?q=1&q=1", {}],
    ["fragment", infoURL + "#private", {}],
    ["userinfo", "http://private:secret@127.0.0.1:8080/api/system/info", {}],
    ["invalid URL", "not-a-url", {}],
  ])("ignores %s without reading its body", async (_name, url, options) => {
    const page = new FakePage();
    const wrong = new FakeRequest(page, url, options);
    const wrongResponse = new FakeResponse(wrong);
    const right = new FakeRequest(page);
    const rightResponse = new FakeResponse(right);
    await observeSystemCandidateRead(page.page(), "overview-disk", async () => {
      emit(page, wrong, wrongResponse);
      emit(page, right, rightResponse);
    });
    expect(wrongResponse.body).not.toHaveBeenCalled();
    expect(rightResponse.body).toHaveBeenCalledTimes(1);
    cleaned(page);
  });

  it.each([
    "",
    "?instance=local-api&page=1&pageSize=50",
    "?instance=local-api&page=2&pageSize=20",
    "?instance=other&page=1&pageSize=20",
    "?instance=local-api&page=1&pageSize=20&page=1",
    "?instance=local-api&instance=local-api&page=1&pageSize=20",
    "?instance=local-api&page=1&pageSize=20&private=value",
    "?pageSize=20&page=1&instance=local-api",
  ])("requires the exact native storage query contract: %s", async (query) => {
    const page = new FakePage();
    const wrong = new FakeRequest(
      page,
      "http://127.0.0.1:8080/api/system/storage" + query,
    );
    const wrongResponse = new FakeResponse(wrong, bytes(storage()));
    const right = new FakeRequest(page, storageURL);
    const response = new FakeResponse(right, bytes(storage()));
    const observation = await observeSystemCandidateRead(
      page.page(),
      "storage-ordinals",
      async () => {
        emit(page, wrong, wrongResponse);
        emit(page, right, response);
      },
    );
    expect(wrongResponse.body).not.toHaveBeenCalled();
    expect(observation.page).toEqual({ page: 1, pageSize: 20, total: 1 });
    expect(observation.rows).toEqual([
      { id: "runtime-root", ordinal: 1, revision: 0, alarmPercent: 85 },
    ]);
    expect(observation.disk).toBeUndefined();
    cleaned(page);
  });

  it("locks the first matching Request and first corresponding Response", async () => {
    const page = new FakePage();
    const first = new FakeRequest(page);
    const firstResponse = new FakeResponse(first);
    const duplicateResponse = new FakeResponse(
      first,
      bytes({ private: "invalid" }),
    );
    const later = new FakeRequest(page);
    const laterResponse = new FakeResponse(later);
    const result = observeSystemCandidateRead(
      page.page(),
      "overview-disk",
      async () => {
        page.emit("request", first.request());
        emit(page, later, laterResponse);
        page.emit("response", firstResponse.response());
        page.emit("response", duplicateResponse.response());
        page.emit("requestfinished", first.request());
      },
    );
    await result;
    expect(firstResponse.body).toHaveBeenCalledTimes(1);
    expect(laterResponse.body).not.toHaveBeenCalled();
    expect(duplicateResponse.body).not.toHaveBeenCalled();
    cleaned(page);
  });

  it.each([201, 204, 302, 401, 403, 500])(
    "refuses HTTP %s without reading the body",
    async (status) => {
      const page = new FakePage();
      const request = new FakeRequest(page);
      const response = new FakeResponse(request, bytes(overview()), status);
      await expect(
        observeSystemCandidateRead(page.page(), "overview-disk", async () =>
          emit(page, request, response),
        ),
      ).rejects.toThrow(safeError);
      expect(response.body).not.toHaveBeenCalled();
      cleaned(page);
    },
  );

  it.each(["failed", "missing-response"])(
    "refuses native %s without reading a body",
    async (terminal) => {
      const page = new FakePage();
      const request = new FakeRequest(page);
      const response = new FakeResponse(request);
      await expect(
        observeSystemCandidateRead(page.page(), "overview-disk", async () => {
          page.emit("request", request.request());
          if (terminal === "failed") page.emit("response", response.response());
          page.emit(
            terminal === "failed" ? "requestfailed" : "requestfinished",
            request.request(),
          );
        }),
      ).rejects.toThrow(safeError);
      expect(response.body).not.toHaveBeenCalled();
      cleaned(page);
    },
  );

  it("does not fall back to a later request when the first native request fails", async () => {
    const page = new FakePage();
    const first = new FakeRequest(page);
    const next = new FakeRequest(page);
    const response = new FakeResponse(next);
    await expect(
      observeSystemCandidateRead(page.page(), "overview-disk", async () => {
        page.emit("request", first.request());
        page.emit("requestfailed", first.request());
        emit(page, next, response);
      }),
    ).rejects.toThrow(safeError);
    expect(response.body).not.toHaveBeenCalled();
    cleaned(page);
  });

  it.each(["sync", "async"])(
    "sanitizes %s action failures and cleans listeners",
    async (mode) => {
      const page = new FakePage();
      const action = vi.fn(() => {
        if (mode === "sync") throw new Error("secret action call log");
        return Promise.reject(new Error("secret async call log"));
      });
      await expect(
        observeSystemCandidateRead(page.page(), "overview-disk", action),
      ).rejects.toThrow(safeError);
      expect(action).toHaveBeenCalledTimes(1);
      cleaned(page);
    },
  );

  it("rejects an unknown kind before action, listeners or timers", async () => {
    const page = new FakePage();
    const action = vi.fn(async () => {});
    await expect(
      observeSystemCandidateRead(
        page.page(),
        "private-kind" as CandidateKind,
        action,
      ),
    ).rejects.toThrow(safeError);
    expect(action).not.toHaveBeenCalled();
    cleaned(page);
  });

  it("sanitizes an invalid page URL before starting the action", async () => {
    const page = new FakePage();
    page.url.mockReturnValue("private-url");
    const action = vi.fn(async () => {});
    await expect(
      observeSystemCandidateRead(page.page(), "overview-disk", action),
    ).rejects.toThrow(safeError);
    expect(action).not.toHaveBeenCalled();
    cleaned(page);
  });

  it.each(["sync", "async"])(
    "sanitizes %s body retrieval errors and reads once",
    async (mode) => {
      const page = new FakePage();
      const request = new FakeRequest(page);
      const response = new FakeResponse(request);
      response.body.mockImplementation(() => {
        if (mode === "sync") throw new Error("private body call log");
        return Promise.reject(new Error("private body call log"));
      });
      await expect(
        observeSystemCandidateRead(page.page(), "overview-disk", async () =>
          emit(page, request, response),
        ),
      ).rejects.toThrow(safeError);
      expect(response.body).toHaveBeenCalledTimes(1);
      cleaned(page);
    },
  );

  it("cleans partially installed listeners if event subscription fails", async () => {
    const page = new FakePage();
    const on = page.on.bind(page);
    vi.spyOn(page, "on").mockImplementation((event, listener) => {
      if (event === "requestfinished")
        throw new Error("private subscription failure");
      return on(event, listener);
    });
    const action = vi.fn(async () => {});
    await expect(
      observeSystemCandidateRead(page.page(), "overview-disk", action),
    ).rejects.toThrow(safeError);
    expect(action).not.toHaveBeenCalled();
    cleaned(page);
  });

  it("ignores frameless browser requests instead of admitting another transport", async () => {
    const page = new FakePage();
    const frameless = new FakeRequest(page);
    vi.spyOn(frameless, "frame").mockImplementation(() => {
      throw new Error("private service worker URL");
    });
    const ignored = new FakeResponse(frameless);
    const request = new FakeRequest(page);
    const response = new FakeResponse(request);
    await observeSystemCandidateRead(page.page(), "overview-disk", async () => {
      emit(page, frameless, ignored);
      emit(page, request, response);
    });
    expect(ignored.body).not.toHaveBeenCalled();
    expect(response.body).toHaveBeenCalledTimes(1);
    cleaned(page);
  });

  it("cleans only its own listeners and never removes a caller's observer", async () => {
    const page = new FakePage();
    const listener = vi.fn();
    page.on("request", listener);
    const request = new FakeRequest(page);
    const response = new FakeResponse(request);
    await observeSystemCandidateRead(page.page(), "overview-disk", async () =>
      emit(page, request, response),
    );
    expect(listener).toHaveBeenCalledTimes(1);
    expect(page.listeners("request")).toEqual([listener]);
    page.off("request", listener);
    cleaned(page);
  });
});

describe("single total deadline and late completion safety", () => {
  it.each(["action", "no-request", "no-finish"])(
    "times out %s within the original 15 second budget",
    async (phase) => {
      const page = new FakePage();
      const request = new FakeRequest(page);
      const response = new FakeResponse(request);
      const stalled = deferred<void>();
      const action = vi.fn(async () => {
        if (phase === "action") await stalled.promise;
        if (phase === "no-finish") emit(page, request, response, false);
      });
      const result = observeSystemCandidateRead(
        page.page(),
        "overview-disk",
        action,
      );
      const rejected = expect(result).rejects.toThrow(safeError);
      await vi.advanceTimersByTimeAsync(14_999);
      expect(page.listenerCount("requestfinished")).toBe(1);
      await vi.advanceTimersByTimeAsync(1);
      await rejected;
      expect(action).toHaveBeenCalledTimes(1);
      expect(response.body).not.toHaveBeenCalled();
      cleaned(page);
      if (phase === "action")
        stalled.reject(new Error("late private action failure"));
      else stalled.resolve();
      await vi.advanceTimersByTimeAsync(0);
      cleaned(page);
    },
  );

  it.each(["resolve", "reject"])(
    "ignores a late body %s after timeout without resetting the budget",
    async (outcome) => {
      const page = new FakePage();
      const request = new FakeRequest(page);
      const response = new FakeResponse(request);
      const pending = deferred<Buffer>();
      response.body.mockReturnValue(pending.promise);
      const result = observeSystemCandidateRead(
        page.page(),
        "overview-disk",
        async () => {
          await new Promise<void>((resolve) => setTimeout(resolve, 14_000));
          emit(page, request, response);
        },
      );
      const rejected = expect(result).rejects.toThrow(safeError);
      await vi.advanceTimersByTimeAsync(14_000);
      expect(response.body).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(1_000);
      await rejected;
      cleaned(page);
      if (outcome === "resolve") pending.resolve(bytes(overview()));
      else pending.reject(new Error("late private body failure"));
      await vi.advanceTimersByTimeAsync(0);
      expect(response.body).toHaveBeenCalledTimes(1);
      cleaned(page);
    },
  );

  it("requires successful action completion even after a complete valid body", async () => {
    const page = new FakePage();
    const request = new FakeRequest(page);
    const response = new FakeResponse(request);
    const pending = deferred<void>();
    const result = observeSystemCandidateRead(
      page.page(),
      "overview-disk",
      async () => {
        emit(page, request, response);
        await pending.promise;
      },
    );
    const rejected = expect(result).rejects.toThrow(safeError);
    await vi.advanceTimersByTimeAsync(0);
    expect(response.body).toHaveBeenCalledTimes(1);
    expect(page.listenerCount("requestfailed")).toBe(1);
    pending.reject(new Error("private action failure after response"));
    await rejected;
    cleaned(page);
  });

  it("does not let complete body bytes hide a hung action or extend its deadline", async () => {
    const page = new FakePage();
    const request = new FakeRequest(page);
    const response = new FakeResponse(request);
    const action = deferred<void>();
    const result = observeSystemCandidateRead(
      page.page(),
      "overview-disk",
      async () => {
        emit(page, request, response);
        await action.promise;
      },
    );
    const rejected = expect(result).rejects.toThrow(safeError);
    await vi.advanceTimersByTimeAsync(0);
    expect(response.body).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(15_000);
    await rejected;
    action.resolve();
    await vi.advanceTimersByTimeAsync(0);
    cleaned(page);
  });

  it("handles body rejection before a pending action resolves without an internal unhandled rejection", async () => {
    const page = new FakePage();
    const request = new FakeRequest(page);
    const response = new FakeResponse(request);
    response.body.mockRejectedValue(new Error("private body failure"));
    const action = deferred<void>();
    const result = observeSystemCandidateRead(
      page.page(),
      "overview-disk",
      async () => {
        emit(page, request, response);
        await action.promise;
      },
    );
    await expect(result).rejects.toThrow(safeError);
    cleaned(page);
    action.reject(new Error("private late action rejection"));
    await vi.advanceTimersByTimeAsync(0);
    cleaned(page);
  });

  it("ignores late body bytes after action failure", async () => {
    const page = new FakePage();
    const request = new FakeRequest(page);
    const response = new FakeResponse(request);
    const pending = deferred<Buffer>();
    const action = deferred<void>();
    response.body.mockReturnValue(pending.promise);
    const result = observeSystemCandidateRead(
      page.page(),
      "overview-disk",
      async () => {
        emit(page, request, response);
        await action.promise;
      },
    );
    const rejected = expect(result).rejects.toThrow(safeError);
    await vi.advanceTimersByTimeAsync(0);
    action.reject(new Error("private action"));
    await rejected;
    cleaned(page);
    pending.resolve(bytes(overview()));
    await vi.advanceTimersByTimeAsync(0);
    cleaned(page);
  });

  it("rejects a failed terminal arriving while the finished body is still pending", async () => {
    const page = new FakePage();
    const request = new FakeRequest(page);
    const response = new FakeResponse(request);
    const pending = deferred<Buffer>();
    response.body.mockReturnValue(pending.promise);
    const result = observeSystemCandidateRead(
      page.page(),
      "overview-disk",
      async () => emit(page, request, response),
    );
    const rejected = expect(result).rejects.toThrow(safeError);
    await vi.advanceTimersByTimeAsync(0);
    page.emit("requestfailed", request.request());
    await rejected;
    pending.resolve(bytes(overview()));
    await vi.advanceTimersByTimeAsync(0);
    cleaned(page);
  });
});

describe("bounded safe candidate projections", () => {
  it("preserves null, stale and clock-skew availability without manufacturing a healthy sample", async () => {
    for (const variant of [
      "missing",
      "stale",
      "clock-skew",
      "metric-unavailable",
    ] as const) {
      const value: Record<string, unknown> = overview();
      const current = value.current as Record<string, unknown>;
      const snapshot = current.snapshot as ReturnType<
        typeof overview
      >["current"]["snapshot"];
      if (variant === "missing") {
        current.snapshot = null;
        current.availability = "unavailable";
      } else if (variant === "metric-unavailable") {
        Object.assign(snapshot.storage[0], {
          availability: "unavailable",
          percent: null,
        });
      } else {
        current.availability = "unavailable";
        current.stale = variant === "stale";
        const observedAt =
          variant === "stale" ? "2026-11-06T08:55:00Z" : "2026-11-06T09:05:00Z";
        snapshot.observedAt = observedAt;
        snapshot.storage[0].observedAt = observedAt;
      }
      const observation = await observeValue(value);
      expect(observation.current).toEqual({
        checkedAt: now,
        availability: current.availability,
        stale: current.stale,
      });
      if (variant === "missing") expect(observation.disk).toBeUndefined();
      else
        expect(observation.disk?.percent).toBe(
          variant === "metric-unavailable" ? null : 34.25,
        );
    }
  });

  it("excludes all arbitrary body fields, including nested secrets and raw error values", async () => {
    const value = overview();
    Object.assign(value, {
      headers: "PRIVATE-HEADERS",
      proof: "PRIVATE-PROOF",
      nativeFinished: false,
    });
    Object.assign(value.current, {
      reason: "PRIVATE-REASON",
      token: "PRIVATE-TOKEN",
    });
    Object.assign(value.current.snapshot.storage[0], {
      mount: "PRIVATE-MOUNT",
      fs: "PRIVATE-FS",
      secret: "PRIVATE-SECRET",
    });
    const observation = await observeValue(value);
    expect(JSON.stringify(observation)).not.toContain("PRIVATE-");
    expect(observation.nativeFinished).toBe(true);
    const storageValue = storage();
    Object.assign(storageValue.storage[0], {
      ordinal: 900,
      proof: "PRIVATE-PROOF",
    });
    const row = (await observeValue(storageValue, "storage-ordinals")).rows![0];
    expect(row).toEqual({
      id: "runtime-root",
      ordinal: 1,
      revision: 0,
      alarmPercent: 85,
    });
  });

  it.each(["available", "unavailable", "warming_up", "unsupported"])(
    "keeps known %s availability explicit",
    async (availability) => {
      const value = overview();
      Object.assign(value.current.snapshot.storage[0], {
        availability,
        percent: availability === "available" ? 0 : null,
      });
      const observation = await observeValue(value);
      expect(observation.disk?.availability).toBe(availability);
      expect(observation.disk?.percent).toBe(
        availability === "available" ? 0 : null,
      );
    },
  );

  it("accepts exact 64 KiB body and percent/revision/threshold boundaries", async () => {
    const value = overview();
    value.current.snapshot.storage[0].percent = 100;
    const text = JSON.stringify(value);
    const raw = Buffer.from(
      text + " ".repeat(64 * 1024 - Buffer.byteLength(text)),
    );
    expect(
      (await observeValue(value, "overview-disk", raw)).disk?.percent,
    ).toBe(100);
    for (const alarmPercent of [85, 90]) {
      const value = storage();
      Object.assign(value.storage[0], {
        revision: Number.MAX_SAFE_INTEGER,
        alarmPercent,
      });
      const row = (await observeValue(value, "storage-ordinals")).rows![0];
      expect(row.revision).toBe(Number.MAX_SAFE_INTEGER);
      expect(row.alarmPercent).toBe(alarmPercent);
    }
  });

  it.each([
    ["oversize", Buffer.alloc(64 * 1024 + 1, 32)],
    ["invalid UTF8", Buffer.from([0xc3, 0x28])],
    ["malformed JSON", Buffer.from('{"private-value":')],
    ["empty", Buffer.alloc(0)],
    ["array", Buffer.from("[]")],
    ["null", Buffer.from("null")],
    ["scalar", Buffer.from('"private-value"')],
  ])("sanitizes %s bytes", async (_label, raw) => {
    await expect(observeValue(null, "overview-disk", raw)).rejects.toThrow(
      safeError,
    );
  });

  it.each([
    ["instance", "foreign"],
    ["checkedAt", "private-date"],
    ["checkedAt", "2026-11-06"],
    ["availability", "private-value"],
    ["stale", "false"],
  ])("rejects invalid current %s", async (field, value) => {
    const input = overview();
    Object.assign(input.current, { [field]: value });
    await expect(observeValue(input)).rejects.toThrow(safeError);
    const storageInput = storage();
    Object.assign(storageInput, { [field]: value });
    await expect(
      observeValue(storageInput, "storage-ordinals"),
    ).rejects.toThrow(safeError);
  });

  it.each([
    ["id", "private-id"],
    ["source", "linux_statfs"],
    ["scope", "other"],
    ["observedAt", "private-time"],
    ["observedAt", "2026-11-06T08:59:00Z"],
    ["percent", -1],
    ["percent", 101],
    ["percent", null],
    ["percent", "34"],
    ["availability", "unavailable"],
    ["availability", "unknown"],
  ])("rejects invalid disk %s", async (field, value) => {
    const input = overview();
    Object.assign(input.current.snapshot.storage[0], { [field]: value });
    await expect(observeValue(input)).rejects.toThrow(safeError);
  });

  it.each([
    "missing-current",
    "missing-snapshot",
    "null-available",
    "stale-available",
    "old-available",
    "future-available",
    "foreign-snapshot",
    "empty-disks",
    "duplicate-disks",
    "extra-disk",
  ])("rejects inconsistent overview %s", async (variant) => {
    const input = overview();
    const dynamic = input as unknown as Record<string, unknown>;
    const current = input.current as unknown as Record<string, unknown>;
    const snapshot = input.current.snapshot;
    if (variant === "missing-current") delete dynamic.current;
    if (variant === "missing-snapshot") delete current.snapshot;
    if (variant === "null-available") current.snapshot = null;
    if (variant === "stale-available") current.stale = true;
    if (variant === "foreign-snapshot") snapshot.instance = "foreign";
    if (variant === "empty-disks") snapshot.storage = [];
    if (variant === "duplicate-disks")
      snapshot.storage.push({ ...snapshot.storage[0] });
    if (variant === "extra-disk")
      snapshot.storage.push({ ...snapshot.storage[0], id: "extra" });
    if (variant === "old-available" || variant === "future-available") {
      const observedAt =
        variant === "old-available"
          ? "2026-11-06T08:58:00Z"
          : "2026-11-06T09:02:00Z";
      snapshot.observedAt = observedAt;
      snapshot.storage[0].observedAt = observedAt;
    }
    await expect(observeValue(input)).rejects.toThrow(safeError);
  });

  it.each([
    ["page", 2],
    ["page", 0],
    ["pageSize", 50],
    ["total", 0],
    ["total", 2],
    ["storage", []],
    [
      "storage",
      [
        { id: "runtime-root", revision: 0, alarmPercent: 85 },
        { id: "runtime-root", revision: 0, alarmPercent: 85 },
      ],
    ],
  ])("rejects out-of-fixture storage %s", async (field, value) => {
    const input = storage();
    Object.assign(input, { [field]: value });
    await expect(observeValue(input, "storage-ordinals")).rejects.toThrow(
      safeError,
    );
  });

  it.each([
    ["id", "other"],
    ["revision", -1],
    ["revision", 0.5],
    ["revision", Number.MAX_SAFE_INTEGER + 1],
    ["revision", "1"],
    ["alarmPercent", 84],
    ["alarmPercent", 91],
    ["alarmPercent", 85.5],
    ["alarmPercent", "85"],
  ])("rejects invalid storage row %s", async (field, value) => {
    const input = storage();
    Object.assign(input.storage[0], { [field]: value });
    await expect(observeValue(input, "storage-ordinals")).rejects.toThrow(
      safeError,
    );
  });
});
