// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  installDirectoryV2ResponseObserver,
  observeDirectoryV2Responses,
  directoryV2ResponseJSON,
  directoryV2RequestDiagnostic,
  directoryV2ResponseWitness,
  directoryV2RequestIdentity,
} from "../e2e/directory-v2-response-observer";
import { createHash, webcrypto } from "node:crypto";
import { EventEmitter } from "node:events";
import type {
  BrowserContext,
  Response as BrowserResponse,
} from "@playwright/test";
import { userAssetsV2API } from "./user-assets-v2-api";
import { ApiError } from "./api";
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
  byteLength: number;
  sha256: string | null;
  eof: boolean;
  signalAborted: boolean;
  readers: number;
  released: number;
  cancelCalls: number;
  taken: boolean;
  identity: { epoch: string; method: string; url: string; ordinal: number };
};
function observer() {
  return (
    window as unknown as {
      __adtrDirectoryV2ResponseObserver: {
        epoch: string;
        count(id: string): number;
        peek(id: string, ordinal: number): Observation | undefined;
        take(id: string, ordinal: number): Observation;
        check(id: string, ordinal: number): Observation;
        buffers(): {
          chunks: number;
          hashing: number;
          text: number;
          records: number;
        };
      };
    }
  ).__adtrDirectoryV2ResponseObserver;
}
async function settle(id: string, ordinal: number) {
  await vi.waitFor(
    () =>
      expect(["complete", "failed"]).toContain(
        observer().peek(id, ordinal)?.state,
      ),
    { interval: 1 },
  );
}
async function takeRecord(id: string, ordinal: number) {
  await settle(id, ordinal);
  return observer().take(id, ordinal);
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
    actualReader.releaseLock();
    expect(await takeRecord(identity, 0)).toMatchObject({
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
    expect((await takeRecord(identity, 0)).text).toBe(text);
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
    expect((await takeRecord(identity, 1)).text).toBe('{"sequence":2}');
    resolveFirst(response('{"sequence":1}'));
    await readDirectoryV2JSON(await pendingFirst, signal());
    expect((await takeRecord(identity, 0)).text).toBe('{"sequence":1}');
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
    expect(await takeRecord(identity, 0)).toMatchObject({
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
    expect(await takeRecord(identity, 0)).toMatchObject({
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
      expect(await takeRecord(identity, 0)).toMatchObject({
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
    expect((await takeRecord(identity, 0)).text).toHaveLength(
      directoryV2BodyLimit,
    );
    await expect(
      readDirectoryV2JSON(await window.fetch(path), signal()),
    ).rejects.toMatchObject({ code: "directory_limit_exceeded" });
    expect(await takeRecord(identity, 1)).toMatchObject({
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
    const observed = await takeRecord(
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
    expect(await takeRecord(identity, 0)).toMatchObject({
      state: "failed",
      text: "",
      failure: "fetch rejected",
    });
    const received = await window.fetch(path);
    await received.body!.cancel();
    expect(await takeRecord(identity, 1)).toMatchObject({
      state: "failed",
      text: "",
      failure: "cancelled before EOF",
    });
  });
});

describe("raw-byte witness lifecycle and document budgets", () => {
  const digest = (body: Uint8Array | string) =>
    createHash("sha256").update(body).digest("hex");
  async function drain(reader: ReadableStreamDefaultReader<Uint8Array>) {
    while (!(await reader.read()).done) {
      /* Application-owned reads only. */
    }
    reader.releaseLock();
  }
  it("hashes the exact native bytes including whitespace and split Unicode, not re-encoded JSON", async () => {
    const raw = new TextEncoder().encode(' {"value":"😀"} \n');
    const split = raw.indexOf(0xf0) + 2;
    const native = response(
      new ReadableStream({
        start(controller) {
          controller.enqueue(raw.slice(0, split));
          controller.enqueue(raw.slice(split));
          controller.close();
        },
      }),
    );
    install(vi.fn().mockResolvedValue(native));
    const parsed = await readDirectoryV2JSON(
      await window.fetch(path),
      signal(),
    );
    const record = await takeRecord(identity, 0);
    expect(record).toMatchObject({
      byteLength: raw.byteLength,
      sha256: digest(raw),
      eof: true,
      signalAborted: false,
      cancelCalls: 0,
      readers: 1,
      released: 1,
    });
    expect(record.sha256).not.toBe(digest(JSON.stringify(parsed)));
    expect(observer().buffers()).toMatchObject({
      chunks: 0,
      hashing: 0,
      text: 0,
    });
  });

  it("distinguishes different raw representations of the same JSON value", async () => {
    install(
      vi
        .fn()
        .mockResolvedValueOnce(response('{"a":1}'))
        .mockResolvedValueOnce(response(' { "a": 1 } ')),
    );
    for (let ordinal = 0; ordinal < 2; ordinal++)
      await readDirectoryV2JSON(await window.fetch(path), signal());
    const first = await takeRecord(identity, 0),
      second = await takeRecord(identity, 1);
    expect(JSON.parse(first.text)).toEqual(JSON.parse(second.text));
    expect(first.sha256).not.toBe(second.sha256);
    expect(first.byteLength).not.toBe(second.byteLength);
  });

  it("never accepts complete JSON bytes until the original reader actually reaches EOF", async () => {
    const native = response(
      new ReadableStream(
        {
          start(controller) {
            controller.enqueue(new TextEncoder().encode("{}"));
          },
        },
        { highWaterMark: 0 },
      ),
    );
    install(vi.fn().mockResolvedValue(native));
    const reader = (await window.fetch(path)).body!.getReader();
    await reader.read();
    expect(observer().peek(identity, 0)).toMatchObject({
      state: "pending",
      text: "{}",
      eof: false,
      sha256: null,
    });
    expect(() => observer().take(identity, 0)).toThrow("Missing completed");
    await reader.cancel();
    reader.releaseLock();
    expect(await takeRecord(identity, 0)).toMatchObject({
      state: "failed",
      eof: false,
      sha256: null,
    });
  });

  it("ignores empty chunks without inventing bytes or an EOF", async () => {
    const native = response(
      new ReadableStream({
        start(controller) {
          for (let index = 0; index < 100; index++)
            controller.enqueue(new Uint8Array());
          controller.enqueue(new TextEncoder().encode("{}"));
          controller.close();
        },
      }),
    );
    install(vi.fn().mockResolvedValue(native));
    await readDirectoryV2JSON(await window.fetch(path), signal());
    expect(await takeRecord(identity, 0)).toMatchObject({
      state: "complete",
      byteLength: 2,
      sha256: digest("{}"),
      eof: true,
    });
    expect(observer().buffers()).toMatchObject({
      chunks: 0,
      hashing: 0,
      text: 0,
    });
  });

  it("does not mistake done:true caused by cancel for native EOF", async () => {
    install(vi.fn().mockResolvedValue(response(new ReadableStream())));
    const reader = (await window.fetch(path)).body!.getReader();
    const pending = reader.read();
    await reader.cancel("private reason");
    expect(await pending).toEqual({ done: true, value: undefined });
    reader.releaseLock();
    expect(await takeRecord(identity, 0)).toMatchObject({
      state: "failed",
      eof: false,
      cancelCalls: 1,
      text: "",
      sha256: null,
    });
  });

  it.each(["pending", "after EOF"])(
    "makes the original request signal abort sticky %s",
    async (when) => {
      const controller = new AbortController();
      install(
        vi
          .fn()
          .mockResolvedValue(
            response(when === "pending" ? new ReadableStream() : "{}"),
          ),
      );
      const native = await window.fetch(path, { signal: controller.signal });
      if (when === "after EOF") {
        await readDirectoryV2JSON(native, controller.signal);
        await settle(identity, 0);
      }
      controller.abort(new Error("private abort reason"));
      const record = await takeRecord(identity, 0);
      expect(record).toMatchObject({
        state: "failed",
        signalAborted: true,
        failure: "signal aborted",
        text: "",
        sha256: null,
      });
      expect(JSON.stringify(record)).not.toContain("private abort reason");
      if (when === "pending") await native.body!.cancel();
    },
  );

  it.each(["reader", "body"])(
    "rejects %s cancellation after native EOF, including after a prior take",
    async (target) => {
      install(vi.fn().mockResolvedValue(response("{}")));
      const native = await window.fetch(path);
      const reader = native.body!.getReader();
      await drain(reader);
      expect((await takeRecord(identity, 0)).state).toBe("complete");
      if (target === "reader")
        await expect(reader.cancel()).rejects.toBeInstanceOf(TypeError);
      else await native.body!.cancel();
      expect(observer().check(identity, 0)).toMatchObject({
        state: "failed",
        cancelCalls: 1,
        failure: "cancelled after EOF",
        sha256: null,
      });
    },
  );

  it("does not become eligible before native release and hash completion, and refuses duplicate takes", async () => {
    install(vi.fn().mockResolvedValue(response("{}")));
    const reader = (await window.fetch(path)).body!.getReader();
    await reader.read();
    await reader.read();
    await vi.waitFor(() => expect(observer().buffers().hashing).toBe(0), {
      interval: 1,
    });
    expect(observer().peek(identity, 0)).toMatchObject({
      state: "hashing",
      eof: true,
      released: 0,
    });
    expect(() => observer().take(identity, 0)).toThrow("Missing completed");
    reader.releaseLock();
    expect((await takeRecord(identity, 0)).state).toBe("complete");
    expect(() => observer().take(identity, 0)).toThrow("already taken");
  });

  it.each([
    "early release",
    "second reader",
    "second release",
    "read after EOF",
  ])("fails closed for %s", async (action) => {
    install(vi.fn().mockResolvedValue(response("{}")));
    const native = await window.fetch(path);
    const reader = native.body!.getReader();
    if (action === "early release") reader.releaseLock();
    else {
      await drain(reader);
      await settle(identity, 0);
      if (action === "second reader") native.body!.getReader().releaseLock();
      if (action === "second release") reader.releaseLock();
      if (action === "read after EOF")
        await expect(reader.read()).rejects.toBeInstanceOf(TypeError);
    }
    expect(await takeRecord(identity, 0)).toMatchObject({
      state: "failed",
      text: "",
      sha256: null,
    });
    expect(observer().buffers()).toMatchObject({
      chunks: 0,
      hashing: 0,
      text: 0,
    });
  });

  it("preserves native promise and rejection identity for malformed signal setup", async () => {
    const rejection = new TypeError("private native invalid signal error");
    const pending = Promise.reject(rejection);
    const nativeFetch = vi.fn(() => pending);
    install(nativeFetch);
    const init = { signal: {} as AbortSignal };
    const actual = window.fetch(path, init);
    expect(actual).toBe(pending);
    await expect(actual).rejects.toBe(rejection);
    expect(nativeFetch).toHaveBeenCalledExactlyOnceWith(path, init);
  });

  it("preserves the original rejected read promise and exact rejection identity", async () => {
    const native = response("{}");
    const reader = native.body!.getReader();
    const rejection = new Error("private read failure");
    const pending = Promise.reject(rejection);
    reader.read = vi.fn(() => pending);
    native.body!.getReader = vi.fn(() => reader) as ReadableStream<
      Uint8Array<ArrayBuffer>
    >["getReader"];
    install(vi.fn().mockResolvedValue(native));
    await window.fetch(path);
    const actual = native.body!.getReader().read();
    expect(actual).toBe(pending);
    await expect(actual).rejects.toBe(rejection);
    const record = await takeRecord(identity, 0);
    expect(record).toMatchObject({
      state: "failed",
      failure: "body read rejected",
    });
    expect(JSON.stringify(record)).not.toContain(rejection.message);
    reader.releaseLock();
  });

  it("does not attribute another body's bytes to a borrowed getReader receiver", async () => {
    const original = response('{"original":true}');
    const other = response('{"foreign":true}');
    const nativeGetReader = original.body!.getReader;
    let actualReader: ReadableStreamDefaultReader<Uint8Array>;
    const getReader = vi.fn(function (
      this: ReadableStream<Uint8Array>,
      ...args: any[]
    ) {
      actualReader = Reflect.apply(nativeGetReader, this, args);
      return actualReader;
    });
    original.body!.getReader = getReader as typeof nativeGetReader;
    install(vi.fn().mockResolvedValue(original));
    await window.fetch(path);
    const reader = original.body!.getReader.call(
      other.body!,
    ) as ReadableStreamDefaultReader<Uint8Array>;
    expect(reader).toBe(actualReader!);
    expect(getReader.mock.contexts[0]).toBe(other.body);
    await drain(reader);
    expect(original.bodyUsed).toBe(false);
    expect(other.bodyUsed).toBe(true);
    expect(await takeRecord(identity, 0)).toMatchObject({
      state: "failed",
      failure: "foreign body receiver",
      text: "",
      sha256: null,
      eof: false,
    });
  });

  it.each(["read", "cancel", "releaseLock"])(
    "preserves borrowed reader.%s semantics but invalidates original-body proof",
    async (method) => {
      const original = response('{"original":true}');
      const originalReader = original.body!.getReader();
      const foreign = response('{"foreign":true}');
      const foreignReader = foreign.body!.getReader();
      const native = originalReader[method as "read"];
      let nativeResult: unknown;
      const spy = vi.fn(function (
        this: ReadableStreamDefaultReader<Uint8Array>,
        ...args: any[]
      ) {
        nativeResult = Reflect.apply(native, this, args);
        return nativeResult;
      });
      (originalReader as any)[method] = spy;
      original.body!.getReader = vi.fn(() => originalReader) as ReadableStream<
        Uint8Array<ArrayBuffer>
      >["getReader"];
      install(vi.fn().mockResolvedValue(original));
      await window.fetch(path);
      const wrapped = original.body!.getReader();
      const argumentsForNative =
        method === "cancel" ? ["private cancel reason"] : [];
      const result = Reflect.apply(
        (wrapped as any)[method],
        foreignReader,
        argumentsForNative,
      );
      expect(result).toBe(nativeResult);
      expect(spy.mock.contexts[0]).toBe(foreignReader);
      expect(spy).toHaveBeenCalledExactlyOnceWith(...argumentsForNative);
      await result;
      expect(original.bodyUsed).toBe(false);
      const record = await takeRecord(identity, 0);
      expect(record).toMatchObject({
        state: "failed",
        failure: "foreign reader receiver",
        text: "",
        sha256: null,
        eof: false,
      });
      expect(JSON.stringify(record)).not.toContain("private cancel reason");
      originalReader.releaseLock();
      foreignReader.releaseLock();
    },
  );

  it("preserves borrowed body.cancel result and arguments without proving original-body consumption", async () => {
    const original = response("{}");
    const foreign = response('{"foreign":true}');
    const native = original.body!.cancel;
    let nativeResult: Promise<void>;
    const cancel = vi.fn(function (
      this: ReadableStream<Uint8Array>,
      ...args: [unknown?]
    ) {
      nativeResult = native.apply(this, args);
      return nativeResult;
    });
    original.body!.cancel = cancel;
    install(vi.fn().mockResolvedValue(original));
    await window.fetch(path);
    const pending = original.body!.cancel.call(foreign.body!, "private reason");
    expect(pending).toBe(nativeResult!);
    expect(cancel.mock.contexts[0]).toBe(foreign.body);
    expect(cancel).toHaveBeenCalledExactlyOnceWith("private reason");
    await pending;
    expect(original.bodyUsed).toBe(false);
    expect(await takeRecord(identity, 0)).toMatchObject({
      state: "failed",
      failure: "foreign body receiver",
      text: "",
      sha256: null,
    });
  });

  it("bounds retained chunks across concurrent responses and releases failed/taken storage", async () => {
    const raw = new TextEncoder().encode(
      '"' + "x".repeat(5 * 1024 * 1024 - 2) + '"',
    );
    const make = () =>
      response(
        new ReadableStream({
          start(controller) {
            controller.enqueue(raw);
            controller.close();
          },
        }),
      );
    install(
      vi.fn().mockResolvedValueOnce(make()).mockResolvedValueOnce(make()),
    );
    const first = (await window.fetch(path)).body!.getReader();
    const second = (await window.fetch(path)).body!.getReader();
    await first.read();
    expect(observer().buffers().chunks).toBe(raw.byteLength);
    await second.read();
    expect(observer().peek(identity, 1)).toMatchObject({
      state: "failed",
      failure: "observer byte quota exceeded",
    });
    expect(observer().buffers()).toMatchObject({
      chunks: raw.byteLength,
      text: raw.byteLength,
    });
    await drain(first);
    await drain(second);
    expect((await takeRecord(identity, 0)).sha256).toBe(digest(raw));
    expect((await takeRecord(identity, 1)).state).toBe("failed");
    expect(observer().buffers()).toMatchObject({
      chunks: 0,
      hashing: 0,
      text: 0,
    });
  });

  it("charges aborted hash staging until settlement and rejects concurrent hash overflow", async () => {
    let finish!: (value: ArrayBuffer) => void;
    const digestMock = vi.fn(
      () =>
        new Promise<ArrayBuffer>((resolve) => {
          finish = resolve;
        }),
    );
    vi.stubGlobal("crypto", {
      randomUUID: () => webcrypto.randomUUID(),
      subtle: { digest: digestMock },
    });
    const raw = '"' + "x".repeat(5 * 1024 * 1024 - 2) + '"';
    install(
      vi
        .fn()
        .mockResolvedValueOnce(response(raw))
        .mockResolvedValueOnce(response(raw)),
    );
    const controller = new AbortController();
    await readDirectoryV2JSON(
      await window.fetch(path, { signal: controller.signal }),
      controller.signal,
    );
    expect(observer().buffers()).toMatchObject({
      chunks: 0,
      hashing: raw.length,
      text: raw.length,
    });
    controller.abort();
    expect(observer().buffers()).toMatchObject({
      chunks: 0,
      hashing: raw.length,
      text: 0,
    });
    await readDirectoryV2JSON(await window.fetch(path), signal());
    expect(await takeRecord(identity, 1)).toMatchObject({
      state: "failed",
      failure: "hash quota exceeded",
    });
    expect(digestMock).toHaveBeenCalledTimes(1);
    finish(new ArrayBuffer(32));
    await vi.waitFor(() => expect(observer().buffers().hashing).toBe(0), {
      interval: 1,
    });
    expect(await takeRecord(identity, 0)).toMatchObject({
      state: "failed",
      sha256: null,
    });
    expect(observer().buffers()).toMatchObject({
      chunks: 0,
      hashing: 0,
      text: 0,
    });
  });

  it.each(["reject", "throw"])(
    "releases all buffers and censors a native hash %s",
    async (mode) => {
      const secret = new Error("private digest failure");
      vi.stubGlobal("crypto", {
        randomUUID: () => webcrypto.randomUUID(),
        subtle: {
          digest: () => {
            if (mode === "throw") throw secret;
            return Promise.reject(secret);
          },
        },
      });
      install(vi.fn().mockResolvedValue(response("{}")));
      await readDirectoryV2JSON(await window.fetch(path), signal());
      const record = await takeRecord(identity, 0);
      expect(record).toMatchObject({
        state: "failed",
        failure: "hash failed",
        text: "",
        sha256: null,
      });
      expect(JSON.stringify(record)).not.toContain(secret.message);
      expect(observer().buffers()).toMatchObject({
        chunks: 0,
        hashing: 0,
        text: 0,
      });
    },
  );

  it("bounds document tombstones without shifting later ordinals or evicting records", async () => {
    install(vi.fn(() => new Promise<Response>(() => {})));
    for (let index = 0; index < 2049; index++) void window.fetch(path);
    expect(observer().buffers().records).toBe(2048);
    expect(observer().count(identity)).toBe(2048);
    expect(observer().peek(identity, 0)).toMatchObject({
      state: "failed",
      failure: "observer record limit exceeded",
      identity: { ordinal: 0 },
    });
    expect(observer().peek(identity, 2047)).toMatchObject({
      state: "failed",
      identity: { ordinal: 2047 },
    });
    expect(observer().peek(identity, 2048)).toBeUndefined();
  });
});

describe("host directory response identity", () => {
  async function harness(options: { status?: number; body?: BodyInit } = {}) {
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
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        origin,
      ).href;
      const request = {
        url: () => requestURL,
        method: () => "GET",
        frame: () => frame,
        resourceType: () => "fetch",
      };
      context.emit("request", request);
      const native = new Response(
        options.body ?? JSON.stringify({ sequence: ++count }),
        {
          status: options.status ?? 200,
          headers: { "Cache-Control": "no-store", "X-ADTR-User-ID": "1" },
        },
      );
      last = {
        url: request.url,
        request: () => request,
        status: () => options.status ?? 200,
        headers: () => ({ "cache-control": "no-store", "x-adtr-user-id": "1" }),
      } as unknown as BrowserResponse;
      return Promise.resolve(native);
    });
    const consume = async (controller = new AbortController()) => {
      const ordinal = observer().count(identity);
      await readDirectoryV2JSON(
        await window.fetch(path, { signal: controller.signal }),
        controller.signal,
      );
      await settle(identity, ordinal);
      return last;
    };
    newDocument();
    return {
      context,
      page,
      fetch,
      consume,
      latest: () => last,
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

  const errorBody = '{"error":"not_found"}';
  const detailQuery = {
    domainId: "synthetic-domain",
    expectedRevision: "1",
    expectedCredentialRevision: "2",
    observationId: "synthetic-observation",
    objectGUID: "01010101-0101-0101-0101-010101010101",
  };
  it.each(["JSON then strict", "strict then JSON"])(
    "retains the exact production 404 body after API rejection and UI cleanup abort: %s",
    async (order) => {
      let releaseHash!: () => void;
      vi.stubGlobal("crypto", {
        randomUUID: () => webcrypto.randomUUID(),
        subtle: {
          digest: (algorithm: string, bytes: Uint8Array<ArrayBuffer>) =>
            new Promise<ArrayBuffer>((resolve, reject) => {
              releaseHash = () => {
                void webcrypto.subtle
                  .digest(algorithm, bytes)
                  .then(resolve, reject);
              };
            }),
        },
      });
      const { latest } = await harness({ status: 404, body: errorBody });
      vi.stubGlobal("fetch", (...args: Parameters<typeof fetch>) =>
        window.fetch(...args),
      );
      const controller = new AbortController();
      await userAssetsV2API.detail(detailQuery, 1, controller.signal).then(
        () => {
          throw new Error("Expected the actual production API error");
        },
        (error: unknown) => {
          expect(error).toBeInstanceOf(ApiError);
          expect(error).toMatchObject({ code: "not_found", status: 404 });
          // Same ordering as Workspace.handleFailure: the production parser has
          // returned/released, then the fatal API error clears and aborts the UI.
          controller.abort();
        },
      );
      const browserResponse = latest();
      const id = `GET ${browserResponse.url()}`;
      expect(observer().peek(id, 0)).toMatchObject({
        state: "hashing",
        eof: true,
        readers: 1,
        released: 1,
        signalAborted: true,
        cancelCalls: 0,
        completedErrorRead: true,
        text: errorBody,
      });
      expect(observer().buffers()).toMatchObject({
        chunks: 0,
        hashing: errorBody.length,
        text: errorBody.length,
      });
      releaseHash();
      await settle(id, 0);
      const take = vi.spyOn(observer(), "take");
      if (order === "strict then JSON")
        await expect(
          directoryV2ResponseWitness(browserResponse),
        ).rejects.toThrow("body observation failed");
      const ordinary = directoryV2ResponseJSON(browserResponse);
      expect(directoryV2ResponseJSON(browserResponse)).toBe(ordinary);
      await expect(ordinary).resolves.toEqual({ error: "not_found" });
      await expect(directoryV2ResponseWitness(browserResponse)).rejects.toThrow(
        "body observation failed",
      );
      await expect(directoryV2ResponseJSON(browserResponse)).resolves.toEqual({
        error: "not_found",
      });
      expect(take).toHaveBeenCalledOnce();
      expect(observer().buffers()).toMatchObject({
        chunks: 0,
        hashing: 0,
        text: 0,
      });
    },
  );

  it("rechecks a previously returned strict error witness after cleanup abort", async () => {
    const { latest } = await harness({ status: 404, body: errorBody });
    const controller = new AbortController();
    await readDirectoryV2JSON(
      await window.fetch(path, { signal: controller.signal }),
      controller.signal,
    );
    await settle(identity, 0);
    const browserResponse = latest();
    await expect(
      directoryV2ResponseWitness(browserResponse),
    ).resolves.toMatchObject({ status: 404, signalAborted: false });
    controller.abort();
    await expect(directoryV2ResponseWitness(browserResponse)).rejects.toThrow(
      "body observation failed",
    );
    await expect(directoryV2ResponseJSON(browserResponse)).resolves.toEqual({
      error: "not_found",
    });
  });

  it("rejects an error signal abort between EOF and native reader release in both APIs", async () => {
    const { latest } = await harness({ status: 404, body: errorBody });
    const controller = new AbortController();
    const reader = (
      await window.fetch(path, { signal: controller.signal })
    ).body!.getReader();
    await reader.read();
    await reader.read();
    expect(observer().peek(identity, 0)).toMatchObject({
      eof: true,
      released: 0,
    });
    controller.abort();
    reader.releaseLock();
    await expect(directoryV2ResponseJSON(latest())).rejects.toThrow(
      "body observation failed",
    );
    await expect(directoryV2ResponseWitness(latest())).rejects.toThrow(
      "body observation failed",
    );
    expect(observer().peek(identity, 0)).toMatchObject({
      state: "failed",
      signalAborted: true,
      text: "",
      sha256: null,
    });
  });

  it.each(["cancel", "read rejection", "malformed", "wrong receiver"])(
    "does not preserve an ordinary error artifact for %s",
    async (failure) => {
      let native: Response;
      if (failure === "read rejection") {
        let reads = 0;
        const stream = new ReadableStream(
          {
            pull(controller) {
              if (reads++ === 0)
                controller.enqueue(new TextEncoder().encode(errorBody));
              else controller.error(new Error("private stream failure"));
            },
          },
          { highWaterMark: 0 },
        );
        const { latest } = await harness({ status: 404, body: stream });
        await expect(
          readDirectoryV2JSON(await window.fetch(path), signal()),
        ).rejects.toMatchObject({ code: "invalid_response" });
        await expect(directoryV2ResponseJSON(latest())).rejects.toThrow(
          "body observation failed",
        );
        await expect(directoryV2ResponseWitness(latest())).rejects.toThrow(
          "body observation failed",
        );
        return;
      }
      const { latest } = await harness({
        status: 404,
        body: failure === "malformed" ? "{" : errorBody,
      });
      native = await window.fetch(path);
      if (failure === "malformed")
        await expect(
          readDirectoryV2JSON(native, signal()),
        ).rejects.toMatchObject({ code: "invalid_response" });
      else if (failure === "cancel") await native.body!.cancel();
      else {
        const foreign = response(errorBody);
        const reader = native.body!.getReader.call(
          foreign.body!,
        ) as ReadableStreamDefaultReader<Uint8Array>;
        while (!(await reader.read()).done) {
          /* A foreign reader cannot prove the original error body. */
        }
        reader.releaseLock();
        expect(native.bodyUsed).toBe(false);
      }
      await expect(directoryV2ResponseJSON(latest())).rejects.toThrow(
        "body observation failed",
      );
      await expect(directoryV2ResponseWitness(latest())).rejects.toThrow(
        "body observation failed",
      );
    },
  );

  it("rejects a 404 abort during a pending read even after the complete JSON prefix arrived", async () => {
    const stream = new ReadableStream(
      {
        start(controller) {
          controller.enqueue(new TextEncoder().encode(errorBody));
        },
      },
      { highWaterMark: 0 },
    );
    const { latest } = await harness({ status: 404, body: stream });
    const controller = new AbortController();
    const reading = readDirectoryV2JSON(
      await window.fetch(path, { signal: controller.signal }),
      controller.signal,
    );
    const rejected = expect(reading).rejects.toMatchObject({
      name: "AbortError",
    });
    await vi.waitFor(
      () => expect(observer().peek(identity, 0)?.text).toBe(errorBody),
      { interval: 1 },
    );
    controller.abort();
    await rejected;
    await expect(directoryV2ResponseJSON(latest())).rejects.toThrow(
      "body observation failed",
    );
    await expect(directoryV2ResponseWitness(latest())).rejects.toThrow(
      "body observation failed",
    );
    expect(observer().peek(identity, 0)).toMatchObject({
      state: "failed",
      eof: false,
      text: "",
      sha256: null,
    });
  });

  it.each(["body cancel", "second reader"])(
    "invalidates a completed historical error candidate after later %s",
    async (failure) => {
      const { latest } = await harness({ status: 404, body: errorBody });
      const controller = new AbortController();
      const native = await window.fetch(path, { signal: controller.signal });
      await readDirectoryV2JSON(native, controller.signal);
      controller.abort();
      if (failure === "body cancel") await native.body!.cancel();
      else native.body!.getReader().releaseLock();
      await expect(directoryV2ResponseJSON(latest())).rejects.toThrow(
        "body observation failed",
      );
      await expect(directoryV2ResponseWitness(latest())).rejects.toThrow(
        "body observation failed",
      );
      expect(observer().peek(identity, 0)).toMatchObject({
        state: "failed",
        text: "",
        sha256: null,
      });
    },
  );

  it.each([400, 401, 403, 500, 599])(
    "allows only historical JSON, never live proof, after HTTP %i cleanup",
    async (status) => {
      const { latest } = await harness({ status, body: errorBody });
      const controller = new AbortController();
      await readDirectoryV2JSON(
        await window.fetch(path, { signal: controller.signal }),
        controller.signal,
      );
      controller.abort();
      await settle(identity, 0);
      await expect(directoryV2ResponseJSON(latest())).resolves.toEqual({
        error: "not_found",
      });
      await expect(directoryV2ResponseWitness(latest())).rejects.toThrow(
        "body observation failed",
      );
    },
  );

  it.each([200, 302])(
    "does not retain a post-completion abort artifact for HTTP %i",
    async (status) => {
      const { latest } = await harness({ status, body: errorBody });
      const controller = new AbortController();
      await readDirectoryV2JSON(
        await window.fetch(path, { signal: controller.signal }),
        controller.signal,
      );
      controller.abort();
      await expect(directoryV2ResponseJSON(latest())).rejects.toThrow(
        "body observation failed",
      );
      await expect(directoryV2ResponseWitness(latest())).rejects.toThrow(
        "body observation failed",
      );
    },
  );

  it.each(["status", "actor", "cache", "identity"])(
    "retains exact %s validation for historical error artifacts",
    async (field) => {
      const { latest } = await harness({ status: 404, body: errorBody });
      const controller = new AbortController();
      await readDirectoryV2JSON(
        await window.fetch(path, { signal: controller.signal }),
        controller.signal,
      );
      await settle(identity, 0);
      controller.abort();
      const record = observer().peek(identity, 0)!;
      if (field === "identity") record.identity.ordinal++;
      else
        Object.assign(record, {
          [field]: field === "status" ? 500 : "mismatch",
        });
      await expect(directoryV2ResponseJSON(latest())).rejects.toThrow(
        field === "identity"
          ? "request identity did not match"
          : "response metadata did not match",
      );
      await expect(directoryV2ResponseWitness(latest())).rejects.toThrow();
    },
  );

  it("returns an exact raw-byte witness bound to the original request and metadata", async () => {
    const { consume } = await harness();
    const response = await consume();
    const witness = await directoryV2ResponseWitness(response);
    expect(witness).toEqual({
      value: { sequence: 1 },
      byteLength: 14,
      sha256: createHash("sha256").update('{"sequence":1}').digest("hex"),
      state: "complete",
      uncanceledEOF: true,
      eof: true,
      signalAborted: false,
      cancelCalls: 0,
      readers: 1,
      released: 1,
      status: 200,
      actor: "1",
      cache: "no-store",
      identity: directoryV2RequestIdentity(response.request()),
    });
    expect(await directoryV2ResponseWitness(response)).toBe(witness);
    expect(observer().peek(identity, 0)?.text).toBe("");
  });

  it("rejects a signal abort between the completion peek and atomic take", async () => {
    const { consume, page } = await harness();
    const controller = new AbortController();
    const response = await consume(controller);
    expect(observer().peek(identity, 0)?.state).toBe("complete");
    page.evaluate.mockImplementationOnce(async (fn, argument) => {
      controller.abort();
      return fn(argument);
    });
    const take = vi.spyOn(observer(), "take");
    await expect(directoryV2ResponseWitness(response)).rejects.toThrow(
      "body observation failed",
    );
    expect(take).toHaveBeenCalledOnce();
    expect(take.mock.results[0]?.value).toMatchObject({
      state: "failed",
      signalAborted: true,
      text: "",
      sha256: null,
    });
  });

  it.each(["JSON", "witness"])(
    "rechecks live abort after a previously cached %s read",
    async (initial) => {
      const { consume } = await harness();
      const controller = new AbortController();
      const response = await consume(controller);
      if (initial === "JSON") await directoryV2ResponseJSON(response);
      else await directoryV2ResponseWitness(response);
      controller.abort();
      await expect(directoryV2ResponseWitness(response)).rejects.toThrow(
        "body observation failed",
      );
      if (initial === "JSON")
        await expect(directoryV2ResponseJSON(response)).resolves.toEqual({
          sequence: 1,
        });
    },
  );

  it("revalidates a cached witness against the current document", async () => {
    const { consume, newDocument } = await harness();
    const response = await consume();
    await directoryV2ResponseWitness(response);
    newDocument();
    await consume();
    await expect(directoryV2ResponseWitness(response)).rejects.toThrow(
      "document changed",
    );
  });

  it.each([
    ["eof", undefined],
    ["eof", null],
    ["signalAborted", undefined],
    ["signalAborted", null],
    ["cancelCalls", undefined],
    ["readers", 2],
    ["released", 0],
    ["byteLength", -1],
    ["sha256", null],
  ])(
    "fails closed for missing or invalid final evidence %s=%s",
    async (field, value) => {
      const { consume, page } = await harness();
      const response = await consume();
      page.evaluate.mockImplementationOnce(async (fn, argument) => {
        const result = fn(argument);
        result.record[field as string] = value;
        return result;
      });
      await expect(directoryV2ResponseWitness(response)).rejects.toThrow(
        "body observation failed",
      );
    },
  );

  it.each(["epoch", "ordinal", "method", "url"])(
    "rejects a mismatched final request %s",
    async (field) => {
      const { consume, page } = await harness();
      const response = await consume();
      page.evaluate.mockImplementationOnce(async (fn, argument) => {
        const result = fn(argument);
        result.record.identity[field] = field === "ordinal" ? 999 : "mismatch";
        return result;
      });
      await expect(directoryV2ResponseWitness(response)).rejects.toThrow(
        "request identity did not match",
      );
    },
  );

  it.each(["status", "actor", "cache"])(
    "rejects changed response metadata %s",
    async (field) => {
      const { consume } = await harness();
      const response = await consume();
      Object.assign(observer().peek(identity, 0)!, {
        [field]: field === "status" ? 201 : "changed",
      });
      await expect(directoryV2ResponseWitness(response)).rejects.toThrow(
        "response metadata did not match",
      );
    },
  );

  it("rejects a request identity never registered by the real host event", async () => {
    const { consume } = await harness();
    const response = await consume();
    const fabricated = {
      ...response,
      request: () => ({ ...response.request() }),
    } as BrowserResponse;
    await expect(directoryV2ResponseWitness(fabricated)).rejects.toThrow(
      "was not observed",
    );
  });

  it("rejects changes to finalized hash evidence after cached JSON consumption", async () => {
    const { consume } = await harness();
    const response = await consume();
    await directoryV2ResponseJSON(response);
    observer().peek(identity, 0)!.sha256 = "0".repeat(64);
    await expect(directoryV2ResponseWitness(response)).rejects.toThrow(
      "witness changed after final take",
    );
  });

  it("rejects a host identity advancing during an otherwise balanced final take", async () => {
    const { consume, page } = await harness();
    const response = await consume();
    page.evaluate.mockImplementationOnce(async (fn, argument) => {
      const result = fn(argument);
      await consume();
      return result;
    });
    await expect(directoryV2ResponseWitness(response)).rejects.toThrow(
      "identity changed during final take",
    );
  });

  it("diagnoses exact repeated-URL request ordinals without mutating or consuming observations", async () => {
    const { consume, fetch } = await harness();
    const first = await consume();
    const second = await consume();
    const before = [0, 1].map((ordinal) => ({
      ...observer().peek(identity, ordinal),
    }));
    const take = vi.spyOn(observer(), "take");
    for (const [ordinal, response] of [first, second].entries()) {
      await expect(
        directoryV2RequestDiagnostic(response.request()),
      ).resolves.toEqual({
        registered: true,
        sameDocument: true,
        ordinal,
        hostCount: 2,
        browserCount: 2,
        countsMatch: true,
        state: "complete",
        failure: null,
      });
    }
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(take).not.toHaveBeenCalled();
    expect([0, 1].map((ordinal) => observer().peek(identity, ordinal))).toEqual(
      before,
    );
    await expect(directoryV2ResponseJSON(second)).resolves.toEqual({
      sequence: 2,
    });
    await expect(directoryV2ResponseJSON(first)).resolves.toEqual({
      sequence: 1,
    });
  });

  it("diagnoses missing/document-changed identities and censors arbitrary failure text", async () => {
    const { consume, newDocument } = await harness();
    const retained = await consume();
    const entry = observer().peek(identity, 0)!;
    entry.state = "failed";
    entry.failure = "non-allowlisted private diagnostic";
    entry.text = "private response text";
    const diagnostic = await directoryV2RequestDiagnostic(retained.request());
    expect(diagnostic).toMatchObject({ state: "failed", failure: "other" });
    expect(JSON.stringify(diagnostic)).not.toContain("private");
    expect(entry.text).toBe("private response text");
    await expect(
      directoryV2RequestDiagnostic(
        {} as ReturnType<BrowserResponse["request"]>,
      ),
    ).resolves.toMatchObject({
      registered: false,
      sameDocument: false,
      state: "missing",
    });
    newDocument();
    await consume();
    await expect(
      directoryV2RequestDiagnostic(retained.request()),
    ).resolves.toMatchObject({
      registered: true,
      sameDocument: false,
      ordinal: 0,
      hostCount: null,
      browserCount: null,
      countsMatch: false,
      state: "missing",
    });
  });

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
    expect(page.evaluate).toHaveBeenCalledTimes(2);
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
    expect((await takeRecord(identity, 0)).text).toBe('{"sequence":1}');
    const second = await window.fetch(request, { signal: signal() });
    await readDirectoryV2JSON(second, signal());
    expect((await takeRecord(identity, 1)).text).toBe('{"sequence":2}');
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
