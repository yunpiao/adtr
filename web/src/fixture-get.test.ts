// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createServer, type Server } from "node:http";
import type { Socket } from "node:net";
import {
  request as playwrightRequest,
  type APIResponse,
} from "@playwright/test";
import { fixtureGET, fixtureGetDeadlineMilliseconds } from "../e2e/fixture-get";

const path = "/api/directory-credential-use/v2/effective?accountId=synthetic";
const secret = "synthetic-session-and-body-value";
const resetError = () =>
  new Error(
    `apiRequestContext.get: read ECONNRESET\nCall log:\n - cookie: adtr_session=${secret}\n - body: ${secret}`,
  );
const response = (status = 200) => ({ status: () => status }) as APIResponse;

describe("bounded fixture GET retry", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] });
    vi.spyOn(console, "log").mockImplementation(() => {});
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("returns the exact response without reading, replacing or retrying it", async () => {
    const result = response();
    const get = vi.fn().mockResolvedValue(result);
    expect(await fixtureGET({ get }, path)).toBe(result);
    expect(get).toHaveBeenCalledExactlyOnceWith(path, {
      maxRetries: 0,
      timeout: fixtureGetDeadlineMilliseconds,
    });
    expect(console.log).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("retries one exact reset with the same receiver/path and a fixed safe receipt", async () => {
    const result = response();
    const get = vi
      .fn()
      .mockRejectedValueOnce(resetError())
      .mockResolvedValue(result);
    const request = { get };
    const pending = fixtureGET(request, path);
    await vi.advanceTimersByTimeAsync(249);
    expect(get).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(await pending).toBe(result);
    expect(get.mock.contexts).toEqual([request, request]);
    expect(get.mock.calls).toEqual([
      [path, { maxRetries: 0, timeout: 30_000 }],
      [path, { maxRetries: 0, timeout: 29_750 }],
    ]);
    expect(console.log).toHaveBeenCalledExactlyOnceWith(
      "User-assets fixture GET retry: ECONNRESET; attempt=2/2",
    );
    expect(JSON.stringify(vi.mocked(console.log).mock.calls)).not.toContain(
      secret,
    );
    expect(vi.getTimerCount()).toBe(0);
  });

  it("fails after a second reset without leaking either raw error", async () => {
    const get = vi.fn().mockRejectedValue(resetError());
    const observed = fixtureGET({ get }, path).catch((error: Error) => error);
    await vi.advanceTimersByTimeAsync(250);
    const error = await observed;
    expect(error).toBeInstanceOf(Error);
    expect((error as Error).message).toBe(
      "User-assets fixture GET failed: ECONNRESET_after_retry",
    );
    expect((error as Error).cause).toBeUndefined();
    expect(String((error as Error).stack)).not.toContain(secret);
    expect(get).toHaveBeenCalledTimes(2);
    expect(console.log).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it.each([
    new Error(`apiRequestContext.get: connect ECONNREFUSED\n${secret}`),
    new Error(`apiRequestContext.get: socket hang up\n${secret}`),
    new Error(`apiRequestContext.get: read EPIPE\n${secret}`),
    new Error(`apiRequestContext.get: Target closed\n${secret}`),
    new Error(`apiRequestContext.get: failed\nCookie: ECONNRESET ${secret}`),
    new Error(`apiRequestContext.get: read ECONNRESET ${secret}`),
    new Error(`some-other-operation: read ECONNRESET\n${secret}`),
    { code: "ECONNRESET", message: secret },
    secret,
  ])("never retries an unrecognized/non-reset error %#", async (raw) => {
    const get = vi.fn().mockRejectedValue(raw);
    const error = await fixtureGET({ get }, path).catch(
      (value: Error) => value,
    );
    expect(error).toBeInstanceOf(Error);
    expect((error as Error).message).toBe(
      "User-assets fixture GET failed: transport_error",
    );
    expect((error as Error).cause).toBeUndefined();
    expect(String((error as Error).stack)).not.toContain(secret);
    expect(get).toHaveBeenCalledTimes(1);
    expect(console.log).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it.each([200, 401, 403, 404, 429, 500, 503])(
    "returns HTTP %i unchanged without retries",
    async (status) => {
      const result = response(status);
      const get = vi.fn().mockResolvedValue(result);
      expect(await fixtureGET({ get }, path)).toBe(result);
      expect(get).toHaveBeenCalledTimes(1);
      expect(console.log).not.toHaveBeenCalled();
    },
  );

  it("keeps elapsed first-attempt time inside the shared deadline", async () => {
    const result = response();
    const get = vi
      .fn()
      .mockImplementationOnce(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10_000));
        throw resetError();
      })
      .mockResolvedValueOnce(result);
    const pending = fixtureGET({ get }, path);
    await vi.advanceTimersByTimeAsync(10_250);
    expect(await pending).toBe(result);
    expect(get.mock.calls[1]).toEqual([
      path,
      { maxRetries: 0, timeout: 19_750 },
    ]);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("does not start a retry after its total deadline expires during backoff", async () => {
    const get = vi.fn().mockImplementation(async () => {
      await new Promise((resolve) => setTimeout(resolve, 29_900));
      throw resetError();
    });
    const observed = fixtureGET({ get }, path).catch((error: Error) => error);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(((await observed) as Error).message).toBe(
      "User-assets fixture GET failed: deadline_exceeded",
    );
    expect(get).toHaveBeenCalledTimes(1);
    expect(console.log).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("bounds a stalled second attempt by the original total deadline", async () => {
    const get = vi
      .fn()
      .mockRejectedValueOnce(resetError())
      .mockImplementationOnce(() => new Promise(() => {}));
    const observed = fixtureGET({ get }, path).catch((error: Error) => error);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(((await observed) as Error).message).toBe(
      "User-assets fixture GET failed: deadline_exceeded",
    );
    expect(get).toHaveBeenCalledTimes(2);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("sanitizes a native timeout without retrying", async () => {
    const get = vi
      .fn()
      .mockRejectedValue(
        new Error(
          `apiRequestContext.get: Timeout 30000ms exceeded.\nCookie: ${secret}`,
        ),
      );
    const error = await fixtureGET({ get }, path).catch(
      (value: Error) => value,
    );
    expect((error as Error).message).toBe(
      "User-assets fixture GET failed: deadline_exceeded",
    );
    expect((error as Error).cause).toBeUndefined();
    expect(String((error as Error).stack)).not.toContain(secret);
    expect(get).toHaveBeenCalledTimes(1);
    expect(console.log).not.toHaveBeenCalled();
  });
});

// No browser, Docker, real account or external endpoint. The reset is an
// intentional server-side fault on a proved reused connection. This validates
// retry behavior; it does not prove the original CI failure was an idle-close race.
describe("real Playwright APIRequestContext on owned loopback", () => {
  afterEach(() => vi.restoreAllMocks());

  async function fixture(mode: "once" | "always" | "http503" | "none") {
    let seedSocket: Socket;
    const requests: {
      method: string;
      reused: boolean;
      cookieMatches: boolean;
    }[] = [];
    const server = createServer((request, response) => {
      if (request.url === "/seed") {
        seedSocket = request.socket;
        response.setHeader("Set-Cookie", `fixture=${secret}; Path=/`);
      } else {
        requests.push({
          method: request.method!,
          reused: request.socket === seedSocket,
          cookieMatches: request.headers.cookie === `fixture=${secret}`,
        });
        if (mode === "always" || (mode === "once" && requests.length === 1)) {
          request.socket.resetAndDestroy();
          return;
        }
      }
      response.writeHead(
        request.url !== "/seed" && mode === "http503" ? 503 : 200,
        {
          "Content-Type": "application/json",
          "Cache-Control": "no-store",
          "X-ADTR-User-ID": "7",
        },
      );
      response.end('{"ok":true}');
    });
    server.on("connection", (socket) => socket.on("error", () => {}));
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", resolve);
    });
    const address = server.address();
    if (!address || typeof address === "string")
      throw new Error("Missing owned loopback listener");
    return { server, base: `http://127.0.0.1:${address.port}`, requests };
  }

  async function close(server: Server) {
    server.closeAllConnections();
    await new Promise<void>((resolve, reject) =>
      server.close((error) => (error ? reject(error) : resolve())),
    );
  }

  it("reproduces a reused-socket reset without any retry", async () => {
    const owned = await fixture("once");
    const context = await playwrightRequest.newContext();
    try {
      await (await context.get(`${owned.base}/seed`)).body();
      const error = await context
        .get(`${owned.base}/probe`, { maxRetries: 0, timeout: 3000 })
        .catch((value: Error) => value);
      expect(error).toBeInstanceOf(Error);
      // Only assert a boolean; never send the raw secret-bearing call log to a reporter.
      expect(
        (error as Error).message.startsWith(
          "apiRequestContext.get: read ECONNRESET\n",
        ),
      ).toBe(true);
      expect(owned.requests).toEqual([
        { method: "GET", reused: true, cookieMatches: true },
      ]);
    } finally {
      await context.dispose();
      await close(owned.server);
    }
  });

  it("retries exactly once on a fresh socket with the same cookie jar and response", async () => {
    const owned = await fixture("once");
    const context = await playwrightRequest.newContext();
    const log = vi.spyOn(console, "log").mockImplementation(() => {});
    try {
      await (await context.get(`${owned.base}/seed`)).body();
      const result = await fixtureGET(context, `${owned.base}/probe`);
      expect(result.status()).toBe(200);
      expect(result.headers()).toMatchObject({
        "cache-control": "no-store",
        "x-adtr-user-id": "7",
      });
      expect(await result.json()).toEqual({ ok: true });
      expect(owned.requests).toEqual([
        { method: "GET", reused: true, cookieMatches: true },
        { method: "GET", reused: false, cookieMatches: true },
      ]);
      expect(log).toHaveBeenCalledExactlyOnceWith(
        "User-assets fixture GET retry: ECONNRESET; attempt=2/2",
      );
    } finally {
      await context.dispose();
      await close(owned.server);
    }
  });

  it("still fails repeated real resets with a fixed safe error", async () => {
    const owned = await fixture("always");
    const context = await playwrightRequest.newContext();
    const log = vi.spyOn(console, "log").mockImplementation(() => {});
    try {
      await (await context.get(`${owned.base}/seed`)).body();
      const error = await fixtureGET(context, `${owned.base}/probe`).catch(
        (value: Error) => value,
      );
      expect((error as Error).message).toBe(
        "User-assets fixture GET failed: ECONNRESET_after_retry",
      );
      expect(String((error as Error).stack)).not.toContain(secret);
      expect((error as Error).cause).toBeUndefined();
      expect(owned.requests).toHaveLength(2);
      expect(log).toHaveBeenCalledExactlyOnceWith(
        "User-assets fixture GET retry: ECONNRESET; attempt=2/2",
      );
    } finally {
      await context.dispose();
      await close(owned.server);
    }
  });

  it("does not retry a real HTTP error", async () => {
    const owned = await fixture("http503");
    const context = await playwrightRequest.newContext();
    const log = vi.spyOn(console, "log").mockImplementation(() => {});
    try {
      await (await context.get(`${owned.base}/seed`)).body();
      const result = await fixtureGET(context, `${owned.base}/probe`);
      expect(result.status()).toBe(503);
      expect(await result.json()).toEqual({ ok: true });
      expect(owned.requests).toHaveLength(1);
      expect(log).not.toHaveBeenCalled();
    } finally {
      await context.dispose();
      await close(owned.server);
    }
  });

  it("proves creating a new API context does not isolate the socket pool", async () => {
    const owned = await fixture("none");
    const first = await playwrightRequest.newContext();
    let second:
      | Awaited<ReturnType<typeof playwrightRequest.newContext>>
      | undefined;
    try {
      await (await first.get(`${owned.base}/seed`)).body();
      second = await playwrightRequest.newContext({
        storageState: await first.storageState(),
      });
      expect((await second.get(`${owned.base}/probe`)).status()).toBe(200);
      expect(owned.requests).toEqual([
        { method: "GET", reused: true, cookieMatches: true },
      ]);
    } finally {
      await second?.dispose();
      await first.dispose();
      await close(owned.server);
    }
  });
});
