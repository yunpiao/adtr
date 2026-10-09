// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  installDirectoryV2ResponseObserver,
  observeDirectoryV2Responses,
  directoryV2ResponseJSON,
} from "../e2e/directory-v2-response-observer";
import { EventEmitter } from "node:events";
import type {
  BrowserContext,
  Response as BrowserResponse,
} from "@playwright/test";
import { directoryV2BodyLimit, readDirectoryV2JSON } from "./directory-v2-json";

const origin = "https://adtr.test";
const path = "/api/directory/v2/task?taskUUID=synthetic";
const identity = `GET ${origin}${path}`;
type Observation = {
  state: string;
  text: string;
  status?: number;
  actor?: string | null;
  cache?: string | null;
  failure?: string;
};
function observer() {
  return (
    window as unknown as {
      __adtrDirectoryV2ResponseObserver: {
        peek(id: string, ordinal: number): Observation | undefined;
        take(id: string, ordinal: number): Observation;
      };
    }
  ).__adtrDirectoryV2ResponseObserver;
}
function install(
  fetch: typeof globalThis.fetch,
  register: (epoch: string) => Promise<void> = async () => {},
) {
  vi.stubGlobal("window", {
    fetch,
    __adtrDirectoryV2Document: register,
  });
  Object.assign(window, { top: window });
  installDirectoryV2ResponseObserver();
}
function response(body: BodyInit) {
  return new Response(body, {
    headers: { "Cache-Control": "no-store", "X-ADTR-User-ID": "1" },
  });
}
const signal = () => new AbortController().signal;

beforeEach(() => vi.stubGlobal("location", new URL(origin)));
afterEach(() => vi.unstubAllGlobals());

describe("passive bounded directory browser response observer", () => {
  it("returns the original fetch/read promises, response, reader and read result", async () => {
    const native = response('{"ok":true}');
    const reader = native.body!.getReader();
    const originalRead = reader.read.bind(reader);
    let originalReadPromise: ReturnType<typeof reader.read>;
    let originalResult: ReadableStreamReadResult<Uint8Array>;
    reader.read = vi.fn(() => {
      originalReadPromise = originalRead();
      void originalReadPromise.then((value) => {
        originalResult = value;
      });
      return originalReadPromise;
    });
    native.body!.getReader = vi.fn(() => reader) as ReadableStream<
      Uint8Array<ArrayBuffer>
    >["getReader"];
    const originalFetchPromise = Promise.resolve(native);
    const fetch = vi.fn(() => originalFetchPromise);
    install(fetch);
    const pending = window.fetch(path);
    expect(pending).toBe(originalFetchPromise);
    expect(await pending).toBe(native);
    const actualReader = native.body!.getReader();
    expect(actualReader).toBe(reader);
    const read = actualReader.read();
    expect(read).toBe(originalReadPromise!);
    expect(await read).toBe(originalResult!);
    expect(observer().peek(identity, 0)?.state).toBe("pending");
    await actualReader.read();
    expect(observer().take(identity, 0)).toMatchObject({
      state: "complete",
      text: '{"ok":true}',
      status: 200,
      actor: "1",
      cache: "no-store",
    });
    expect(fetch).toHaveBeenCalledExactlyOnceWith(path);
  });

  it("observes only production-consumed chunks and strict EOF without extra reads or copies of the response", async () => {
    const text = JSON.stringify({
      description: ["Line\u0000😀"],
      task: "real-synthetic-result",
    });
    const bytes = new TextEncoder().encode(text);
    const emoji = bytes.indexOf(0xf0);
    const chunks = [bytes.slice(0, emoji + 1), bytes.slice(emoji + 1)];
    const pull = vi.fn(
      (controller: ReadableStreamDefaultController<Uint8Array>) => {
        const chunk = chunks.shift();
        if (chunk) controller.enqueue(chunk);
        else controller.close();
      },
    );
    const native = response(new ReadableStream({ pull }, { highWaterMark: 0 }));
    const clone = vi.spyOn(native, "clone");
    const tee = vi.spyOn(native.body!, "tee");
    install(vi.fn().mockResolvedValue(native));
    const received = await window.fetch(path);
    expect(received).toBe(native);
    expect(pull).not.toHaveBeenCalled();
    expect(observer().peek(identity, 0)?.state).toBe("pending");
    await expect(readDirectoryV2JSON(received, signal())).resolves.toEqual(
      JSON.parse(text),
    );
    expect(pull).toHaveBeenCalledTimes(3);
    expect(observer().take(identity, 0).text).toBe(text);
    expect(observer().peek(identity, 0)?.text).toBe("");
    expect(clone).not.toHaveBeenCalled();
    expect(tee).not.toHaveBeenCalled();
  });

  it("keeps repeated request identities separate even when responses resolve in reverse order", async () => {
    let resolveFirst!: (value: Response) => void;
    const first = new Promise<Response>((resolve) => {
      resolveFirst = resolve;
    });
    install(
      vi
        .fn()
        .mockReturnValueOnce(first)
        .mockResolvedValueOnce(response('{"sequence":2}')),
    );
    const pendingFirst = window.fetch(path);
    const second = await window.fetch(path);
    await readDirectoryV2JSON(second, signal());
    expect(observer().peek(identity, 0)?.state).toBe("pending");
    expect(observer().take(identity, 1).text).toBe('{"sequence":2}');
    resolveFirst(response('{"sequence":1}'));
    await readDirectoryV2JSON(await pendingFirst, signal());
    expect(observer().take(identity, 0).text).toBe('{"sequence":1}');
  });

  it("rejects a stream failure after complete JSON bytes without mistaking it for EOF", async () => {
    let calls = 0;
    const secretError = "synthetic-error-that-must-not-enter-observer-output";
    const native = response(
      new ReadableStream(
        {
          pull(controller) {
            if (calls++ === 0)
              controller.enqueue(new TextEncoder().encode('{"ok":true}'));
            else controller.error(new Error(secretError));
          },
        },
        { highWaterMark: 0 },
      ),
    );
    install(vi.fn().mockResolvedValue(native));
    await expect(
      readDirectoryV2JSON(await window.fetch(path), signal()),
    ).rejects.toMatchObject({ code: "invalid_response" });
    expect(observer().take(identity, 0)).toMatchObject({
      state: "failed",
      text: "",
    });
    expect(JSON.stringify(observer().peek(identity, 0))).not.toContain(
      secretError,
    );
  });

  it("does not treat cancellation of a stalled app read as successful EOF", async () => {
    const cancel = vi.fn();
    const native = response(new ReadableStream({ cancel }));
    install(vi.fn().mockResolvedValue(native));
    const controller = new AbortController();
    const reading = readDirectoryV2JSON(
      await window.fetch(path),
      controller.signal,
    );
    controller.abort();
    await expect(reading).rejects.toMatchObject({ name: "AbortError" });
    expect(cancel).toHaveBeenCalledOnce();
    expect(observer().take(identity, 0)).toMatchObject({
      state: "failed",
      text: "",
    });
  });

  it.each([new Uint8Array([0xc0, 0xaf]), "{", "null trailing"])(
    "fails invalid UTF-8/JSON instead of accepting partial output: %j",
    async (body) => {
      install(vi.fn().mockResolvedValue(response(body)));
      await expect(
        readDirectoryV2JSON(await window.fetch(path), signal()),
      ).rejects.toMatchObject({ code: "invalid_response" });
      expect(observer().take(identity, 0)).toMatchObject({
        state: "failed",
        text: "",
      });
    },
  );

  it("keeps the exact 8 MiB limit and fails oversize before retaining the extra chunk", async () => {
    const atLimit = "{}" + " ".repeat(directoryV2BodyLimit - 2);
    const native = response(
      new ReadableStream({
        start(controller) {
          controller.enqueue(new TextEncoder().encode(atLimit));
          controller.enqueue(new Uint8Array([32]));
          controller.close();
        },
      }),
    );
    install(
      vi
        .fn()
        .mockResolvedValueOnce(response(atLimit))
        .mockResolvedValueOnce(native),
    );
    await expect(
      readDirectoryV2JSON(await window.fetch(path), signal()),
    ).resolves.toEqual({});
    expect(observer().take(identity, 0).text).toHaveLength(
      directoryV2BodyLimit,
    );
    await expect(
      readDirectoryV2JSON(await window.fetch(path), signal()),
    ).rejects.toMatchObject({ code: "directory_limit_exceeded" });
    expect(observer().take(identity, 1)).toMatchObject({
      state: "failed",
      text: "",
    });
  });

  it("never observes authentication, unrelated or cross-origin responses or request proof", async () => {
    const native = response('{"task":"synthetic"}');
    const reader = native.body!.getReader;
    const fetch = vi.fn().mockResolvedValue(native);
    install(fetch);
    for (const url of [
      "/api/auth/login",
      "/api/profile",
      `https://other.test${path}`,
    ]) {
      await window.fetch(url, { method: "POST", body: "private-proof" });
      expect(native.body!.getReader).toBe(reader);
      expect(
        observer().peek(`POST ${new URL(url, origin)}`, 0),
      ).toBeUndefined();
    }
    const init = { method: "POST", body: "private-proof" };
    await window.fetch("/api/directory/v2/cancel", init);
    await readDirectoryV2JSON(native, signal());
    const observed = observer().take(
      `POST ${origin}/api/directory/v2/cancel`,
      0,
    );
    expect(observed.text).toBe('{"task":"synthetic"}');
    expect(JSON.stringify(observed)).not.toContain("private-proof");
    expect(fetch).toHaveBeenLastCalledWith("/api/directory/v2/cancel", init);
  });

  it("records fetch and pre-reader body cancellation failures without retaining their errors", async () => {
    const rejection = new Error("private fetch failure");
    install(
      vi
        .fn()
        .mockRejectedValueOnce(rejection)
        .mockResolvedValueOnce(response("{}")),
    );
    await expect(window.fetch(path)).rejects.toBe(rejection);
    expect(observer().take(identity, 0)).toEqual({
      state: "failed",
      text: "",
      failure: "fetch rejected",
    });
    const received = await window.fetch(path);
    await received.body!.cancel();
    expect(observer().take(identity, 1)).toMatchObject({
      state: "failed",
      text: "",
      failure: "cancelled before EOF",
    });
  });
});

describe("host directory response identity", () => {
  async function harness() {
    const context = new EventEmitter() as EventEmitter & {
      exposeBinding: ReturnType<typeof vi.fn>;
      addInitScript: ReturnType<typeof vi.fn>;
    };
    let register!: (source: unknown, epoch: string) => void;
    const frame = { page: () => page, url: () => origin };
    const page = {
      mainFrame: () => frame,
      waitForFunction: vi.fn(async (fn, argument) => {
        if (!fn(argument)) throw new Error("Observer is still pending");
      }),
      evaluate: vi.fn(async (fn, argument) => fn(argument)),
    };
    context.exposeBinding = vi.fn(async (_name, callback) => {
      register = callback;
    });
    context.addInitScript = vi.fn(async () => {});
    await observeDirectoryV2Responses(context as unknown as BrowserContext);
    const newDocument = () =>
      install(fetch, async (epoch) => register({ page, frame }, epoch));
    let last!: BrowserResponse;
    let count = 0;
    let omitNextRequest = false;
    let pendingWithoutEvent: Promise<Response> | undefined;
    const fetch = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const effectiveSignal =
        init?.signal !== undefined
          ? init.signal
          : input instanceof Request
            ? input.signal
            : undefined;
      if (effectiveSignal?.aborted)
        return Promise.reject(
          new DOMException("Aborted before dispatch", "AbortError"),
        );
      if (omitNextRequest) {
        omitNextRequest = false;
        return Promise.reject(new TypeError("Fetch rejected before dispatch"));
      }
      if (pendingWithoutEvent) {
        const pending = pendingWithoutEvent;
        pendingWithoutEvent = undefined;
        return pending;
      }
      const request = {
        url: () => `${origin}${path}`,
        method: () => "GET",
        frame: () => frame,
        resourceType: () => "fetch",
      };
      context.emit("request", request);
      const native = response(JSON.stringify({ sequence: ++count }));
      last = {
        url: request.url,
        request: () => request,
        status: () => 200,
        headers: () => ({ "cache-control": "no-store", "x-adtr-user-id": "1" }),
      } as unknown as BrowserResponse;
      return Promise.resolve(native);
    });
    const consume = async () => {
      await readDirectoryV2JSON(await window.fetch(path), signal());
      return last;
    };
    newDocument();
    return {
      context,
      page,
      consume,
      newDocument,
      omitNextRequest: () => {
        omitNextRequest = true;
      },
      prepareUndispatchedAbort: () => {
        let reject!: (reason: unknown) => void;
        pendingWithoutEvent = new Promise<Response>(
          (_resolve, rejectPromise) => {
            reject = rejectPromise;
          },
        );
        return () =>
          reject(new DOMException("Aborted before dispatch", "AbortError"));
      },
    };
  }

  it("keeps same-document ordinals and caches duplicate reads of the exact Response", async () => {
    const { context, page, consume } = await harness();
    const first = await consume();
    const reading = directoryV2ResponseJSON(first);
    expect(directoryV2ResponseJSON(first)).toBe(reading);
    await expect(reading).resolves.toEqual({ sequence: 1 });
    context.emit("framenavigated", page.mainFrame()); // history.pushState/hash
    await expect(directoryV2ResponseJSON(await consume())).resolves.toEqual({
      sequence: 2,
    });
    await expect(directoryV2ResponseJSON(first)).resolves.toEqual({
      sequence: 1,
    });
    expect(page.evaluate).toHaveBeenCalledTimes(4);
  });

  it("resets for a new document but rejects an unconsumed old Response instead of borrowing new bytes", async () => {
    const { consume, newDocument } = await harness();
    const retained = await consume();
    newDocument();
    const current = await consume();
    await expect(directoryV2ResponseJSON(retained)).rejects.toThrow(
      "Directory response document changed",
    );
    await expect(directoryV2ResponseJSON(current)).resolves.toEqual({
      sequence: 2,
    });
  });

  it("does not shift network ordinals for a pre-aborted Fetch without a request event", async () => {
    const { consume } = await harness();
    const controller = new AbortController();
    controller.abort();
    await expect(
      window.fetch(path, { signal: controller.signal }),
    ).rejects.toMatchObject({ name: "AbortError" });
    const first = await consume();
    const second = await consume();
    await expect(directoryV2ResponseJSON(second)).resolves.toEqual({
      sequence: 2,
    });
    await expect(directoryV2ResponseJSON(first)).resolves.toEqual({
      sequence: 1,
    });
  });

  it("honors Request signals and explicit null or live signal overrides", async () => {
    await harness();
    const controller = new AbortController();
    controller.abort();
    const request = new Request(`${origin}${path}`, {
      signal: controller.signal,
    });
    await expect(window.fetch(request)).rejects.toMatchObject({
      name: "AbortError",
    });
    await expect(
      window.fetch(request, { signal: undefined }),
    ).rejects.toMatchObject({ name: "AbortError" });
    const first = await window.fetch(request, { signal: null });
    await readDirectoryV2JSON(first, signal());
    expect(observer().take(identity, 0).text).toBe('{"sequence":1}');
    const second = await window.fetch(request, { signal: signal() });
    await readDirectoryV2JSON(second, signal());
    expect(observer().take(identity, 1).text).toBe('{"sequence":2}');
  });

  it("ignores host-only XHR, iframe and cross-origin request events", async () => {
    const { context, page, consume } = await harness();
    const request = {
      url: () => `${origin}${path}`,
      method: () => "GET",
      frame: () => page.mainFrame(),
      resourceType: () => "fetch",
    };
    context.emit("request", { ...request, resourceType: () => "xhr" });
    context.emit("request", {
      ...request,
      frame: () => ({ page: () => page, url: () => origin }),
    });
    context.emit("request", {
      ...request,
      url: () => `https://other.test${path}`,
    });
    await expect(directoryV2ResponseJSON(await consume())).resolves.toEqual({
      sequence: 1,
    });
  });

  it("fails closed when a non-signal pre-dispatch failure would shift response ordinals", async () => {
    const { consume, omitNextRequest } = await harness();
    omitNextRequest();
    await expect(window.fetch(path)).rejects.toThrow(
      "Fetch rejected before dispatch",
    );
    await consume(); // A is intentionally not inspected.
    const second = await consume();
    const now = vi
      .spyOn(Date, "now")
      .mockReturnValueOnce(0)
      .mockReturnValue(15_000);
    try {
      await expect(directoryV2ResponseJSON(second)).rejects.toThrow(
        "invocation/request counts did not match",
      );
    } finally {
      now.mockRestore();
    }
  });

  it("fails closed on a post-invocation abort without a host event, including a later in-flight request", async () => {
    const { consume, page, prepareUndispatchedAbort } = await harness();
    const abort = prepareUndispatchedAbort();
    const pending = window.fetch(path);
    abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    await consume();
    const second = await consume();
    page.evaluate.mockImplementationOnce(async (fn, argument) => {
      const snapshot = fn(argument);
      await consume(); // Host advances after the page snapshot; this cannot balance a missing event.
      return snapshot;
    });
    const now = vi
      .spyOn(Date, "now")
      .mockReturnValueOnce(0)
      .mockReturnValue(15_000);
    try {
      await expect(directoryV2ResponseJSON(second)).rejects.toThrow(
        "invocation/request counts did not match",
      );
    } finally {
      now.mockRestore();
    }
  });
});
