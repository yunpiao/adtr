// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  installDirectoryV2ResponseObserver,
  observeDirectoryV2Responses,
  directoryV2ResponseJSON,
  directoryV2RequestDiagnostic,
  directoryV2ResponseWitness,
  directoryV2RequestIdentity,
  directoryV4ControlsResponseJSON,
  directoryUACResponseBytes,
  directoryUACResponseFailureCheck,
} from "../e2e/directory-v2-response-observer";
import { createHash, webcrypto } from "node:crypto";
import { parseDirectoryV4UniqueJSON } from "./directory-v4-controls-json";
import {
  safeResponse,
  uacFailureMessage,
} from "../e2e/user-asset-export-v3-uac-helpers";
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
  mode: "legacy" | "v4-controls" = "legacy",
) {
  vi.stubGlobal("window", {
    fetch,
    __adtrDirectoryV2Document: register,
  });
  Object.assign(window, { top: window });
  installDirectoryV2ResponseObserver(mode);
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
  it.each([
    "/api/user-assets/v2",
    "/api/user-assets/v2/detail",
    "/api/user-assets/v3",
    "/api/user-assets/v3/detail",
    "/api/directory/v3/observation",
    "/api/directory/v3/receipt",
    "/api/directory/v3/task",
    "/api/directory/v3/sync",
    "/api/directory/v3/cancel",
    "/api/directory-credential-use/v3/accounts",
    "/api/directory-credential-use/v3/grants",
    "/api/directory-credential-use/v3/roles",
    "/api/directory-credential-use/v3/effective",
    "/api/directory-credential-use/v3/mutation",
    "/api/directory-credential-use/v3/grant",
    "/api/directory-credential-use/v3/revoke",
  ])("observes original bounded-reader bytes for %s", async (route) => {
    const text = JSON.stringify({
      dictionaryVersion: route.includes("/v3") ? 3 : 2,
    });
    const native = response(text);
    const fetch = vi.fn(() => Promise.resolve(native));
    install(fetch);
    const result = await window.fetch(route);
    expect(result).toBe(native);
    expect(await readDirectoryV2JSON(result, signal())).toEqual(
      JSON.parse(text),
    );
    expect(await takeRecord(`GET ${origin}${route}`, 0)).toMatchObject({
      state: "complete",
      text,
      readers: 1,
      released: 1,
      cancelCalls: 0,
      signalAborted: false,
    });
    expect(fetch).toHaveBeenCalledExactlyOnceWith(route);
  });
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
      expect(record.signalAborted).toBe(true);
      if (when === "pending")
        expect(record).toMatchObject({
          state: "failed",
          failure: "signal aborted",
          text: "",
          sha256: null,
        });
      else
        expect(record).toMatchObject({
          state: "complete",
          completedRead: true,
          text: "{}",
          sha256: digest("{}"),
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

  it("charges cancelled hash staging until settlement and rejects concurrent hash overflow", async () => {
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
    const native = await window.fetch(path, { signal: controller.signal });
    await readDirectoryV2JSON(native, controller.signal);
    expect(observer().buffers()).toMatchObject({
      chunks: 0,
      hashing: raw.length,
      text: raw.length,
    });
    controller.abort();
    // A late abort alone retains historical JSON; actual cancellation must
    // invalidate it while pending WebCrypto memory stays charged.
    await native.body!.cancel();
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
  async function harness(options: { status?: number; body?: BodyInit; mode?: "legacy" | "v4-controls"; path?: string; method?: string } = {}) {
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
    await observeDirectoryV2Responses(context as unknown as BrowserContext, options.mode ?? "legacy");
    const newDocument = () =>
      install(fetch, async (epoch) => register({ page, frame }, epoch), options.mode ?? "legacy");
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
        method: () => (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase(),
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
      const route = options.path ?? path, method = options.method ?? "GET";
      const key = `${method} ${new URL(route, origin).href}`;
      const ordinal = observer().count(key);
      await readDirectoryV2JSON(
        await window.fetch(route, { method, signal: controller.signal }),
        controller.signal,
      );
      await settle(key, ordinal);
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
  const v4Pairs = [
    ["POST", "/api/directory/v4/sync"], ["POST", "/api/directory/v4/cancel"],
    ["GET", "/api/directory/v4/receipt"], ["GET", "/api/directory/v4/task"],
    ["GET", "/api/directory/v4/observation"], ["GET", "/api/group-assets/v4/members"],
  ];
  it.each(v4Pairs)("observes only opted-in original v4 %s %s without a second fetch", async (method, route) => {
    const { consume, fetch } = await harness({ mode: "v4-controls", path: route, method });
    const original = await consume();
    await expect(directoryV4ControlsResponseJSON(original)).resolves.toEqual({ sequence: 1 });
    await expect(directoryV2ResponseWitness(original)).resolves.toMatchObject({ state: "complete", uncanceledEOF: true, released: 1 });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it.each(v4Pairs)("does not observe v4 %s %s in legacy mode or with the wrong method", async (method, route) => {
    for (const mode of ["legacy", "v4-controls"] as const) {
      const { latest } = await harness({ mode });
      const actualMethod = mode === "legacy" ? method : method === "GET" ? "POST" : "GET";
      await readDirectoryV2JSON(await window.fetch(route, { method: actualMethod }), signal());
      expect(observer().count(`${actualMethod} ${origin}${route}`)).toBe(0);
      await expect(directoryV4ControlsResponseJSON(latest())).rejects.toThrow("V4 controls response was not observed");
    }
  });
  it("rejects unlisted v4 paths and leaves legacy-only paths out of opt-in scope", async () => {
    for (const route of ["/api/directory/v4/other", "/api/directory/v4/task/extra", "/api/group-assets/v4/members/extra", "/api/directory/v3/task"]) {
      const { latest } = await harness({ mode: "v4-controls" });
      await readDirectoryV2JSON(await window.fetch(route), signal());
      expect(observer().count(`GET ${origin}${route}`)).toBe(0);
      await expect(directoryV4ControlsResponseJSON(latest())).rejects.toThrow("V4 controls response was not observed");
    }
  });
  it("keeps same-mode installation idempotent and rejects scope upgrades", async () => {
    for (const mode of ["legacy", "v4-controls"] as const) {
      const { context } = await harness({ mode });
      await observeDirectoryV2Responses(context as unknown as BrowserContext, mode);
      expect(context.addInitScript).toHaveBeenCalledTimes(1);
      await expect(observeDirectoryV2Responses(context as unknown as BrowserContext, mode === "legacy" ? "v4-controls" : "legacy")).rejects.toThrow("Conflicting directory observer mode");
      expect(context.addInitScript).toHaveBeenCalledTimes(1);
    }
  });
  it("does not copy original sync proof or CSRF headers into response records", async () => {
    const { latest } = await harness({ mode: "v4-controls" });
    const key = `POST ${origin}/api/directory/v4/sync`;
    const native = await window.fetch("/api/directory/v4/sync", { method: "POST", headers: { "X-CSRF-Token": "private-csrf" }, body: JSON.stringify({ actorPassword: "private-password", totpCode: "private-code" }) });
    await readDirectoryV2JSON(native, signal());
    // Native EOF/release precede the asynchronous raw-byte digest. Match
    // consume() before entering the host harness's single-check wait mock.
    await settle(key, 0);
    expect(observer().peek(key, 0)).toMatchObject({ state: "complete", released: 1 });
    // Inspect the retained record before the host's atomic take clears text.
    expect(JSON.stringify(observer().peek(key, 0))).not.toContain("private-");
    await expect(directoryV4ControlsResponseJSON(latest())).resolves.toEqual({ sequence: 1 });
    await expect(directoryV2ResponseWitness(latest())).resolves.toMatchObject({ state: "complete", uncanceledEOF: true, released: 1 });
    expect(JSON.stringify(observer().peek(key, 0))).not.toContain("private-");
  });
  it.each(['{"key":1,"key":2}', '{"key":1,"\\u006bey":2}'])("rejects duplicate v4 keys before discarding original text", async body => {
    const { consume } = await harness({ mode: "v4-controls", path: "/api/directory/v4/task", body });
    await expect(directoryV4ControlsResponseJSON(await consume())).rejects.toThrow("json_valid=false");
    expect(observer().peek(`GET ${origin}/api/directory/v4/task`, 0)?.text).toBe("");
  });
  it("keeps the legacy JSON parser and unlisted-response fallback unchanged", async () => {
    const { consume } = await harness({ body: '{"key":1,"key":2}' });
    await expect(directoryV2ResponseJSON(await consume())).resolves.toEqual({ key: 2 });
    const json = vi.fn(async () => ({ legacy: true }));
    await expect(directoryV2ResponseJSON({ url: () => origin + "/api/group-assets/v4/members", json } as unknown as BrowserResponse)).resolves.toEqual({ legacy: true });
    expect(json).toHaveBeenCalledTimes(1);
  });
  it("preserves v4 historical completed JSON but not native witness after cleanup abort", async () => {
    const { consume } = await harness({ mode: "v4-controls", path: "/api/directory/v4/task" });
    const controller = new AbortController(), original = await consume(controller);
    controller.abort();
    await expect(directoryV4ControlsResponseJSON(original)).resolves.toEqual({ sequence: 1 });
    await expect(directoryV2ResponseWitness(original)).rejects.toThrow("body observation failed");
  });
  it("rejects v4 abort before EOF/release rather than inventing a completed body", async () => {
    const { latest } = await harness({ mode: "v4-controls" });
    const controller = new AbortController();
    const native = await window.fetch("/api/directory/v4/task", { signal: controller.signal });
    const reader = native.body!.getReader();
    await reader.read(); controller.abort(); reader.releaseLock();
    await expect(directoryV4ControlsResponseJSON(latest())).rejects.toThrow("body observation failed");
  });
  it("keeps v4 out-of-order response identity and rejects an old document", async () => {
    const { consume, newDocument } = await harness({ mode: "v4-controls", path: "/api/directory/v4/task" });
    const first = await consume(), second = await consume();
    await expect(directoryV4ControlsResponseJSON(second)).resolves.toEqual({ sequence: 2 });
    await expect(directoryV4ControlsResponseJSON(first)).resolves.toEqual({ sequence: 1 });
    const old = await consume(); newDocument();
    await expect(directoryV4ControlsResponseJSON(old)).rejects.toThrow("document changed");
  });
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
        completedRead: true,
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

  it.each([200, 404])(
    "rejects HTTP %i signal abort between EOF and native reader release in both APIs",
    async (status) => {
      const { latest } = await harness({ status, body: errorBody });
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
    },
  );

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

  it.each([200, 404])(
    "rejects HTTP %i abort during a pending read even after the complete JSON prefix arrived",
    async (status) => {
      const stream = new ReadableStream(
        {
          start(controller) {
            controller.enqueue(new TextEncoder().encode(errorBody));
          },
        },
        { highWaterMark: 0 },
      );
      const { latest } = await harness({ status, body: stream });
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
    },
  );

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

  it.each([200, 201, 302])(
    "retains only historical JSON after a completed HTTP %i cleanup abort",
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

  it.each(["failed", "PRIVATE_STATE"])(
    "reports only fixed flags for untrusted observation state %s",
    async (state) => {
      const { consume } = await harness();
      const response = await consume();
      const record = observer().peek(identity, 0)!;
      Object.assign(record, {
        state,
        sha256: null,
        failure: "PRIVATE_NATIVE_ERROR",
        text: "PRIVATE_RAW_BODY",
        actor: "PRIVATE_ACTOR",
        cache: "PRIVATE_CACHE",
      });
      record.identity.url = "https://private.invalid/PRIVATE_QUERY";
      let message = "";
      try {
        await directoryV2ResponseWitness(response);
      } catch (error) {
        message = (error as Error).message;
      }
      expect(message).toContain("Directory browser body observation failed");
      expect(message).toContain(
        `state=${state === "failed" ? "failed" : "missing"};eof=true;signal_aborted=false;completed_read=true`,
      );
      expect(message).toContain(
        "readers_one=true;released_once=true;cancel_free=true",
      );
      expect(message).toContain("hash_valid=false;reason=other");
      expect(message).not.toContain("PRIVATE_");
      expect(message).not.toContain("private.invalid");
      expect(message).not.toContain(origin);
      expect(message).not.toContain("x-adtr-user-id");
    },
  );

  it.each(["identity", "metadata", "JSON"])(
    "censors private %s fields in final validation diagnostics",
    async (kind) => {
      const { consume } = await harness();
      const response = await consume();
      const record = observer().peek(identity, 0)!;
      if (kind === "identity")
        record.identity.url = "https://private.invalid/PRIVATE_QUERY";
      else if (kind === "metadata") record.actor = "PRIVATE_ACTOR_HEADER";
      else record.text = "{PRIVATE_RAW_BODY";
      let message = "";
      try {
        await directoryV2ResponseJSON(response);
      } catch (error) {
        message = (error as Error).message;
      }
      expect(message).toContain("Directory browser");
      expect(message).toContain(
        kind === "identity"
          ? "url_match=false"
          : kind === "metadata"
            ? "actor_match=false"
            : "json_valid=false",
      );
      expect(message).not.toContain("PRIVATE_");
      expect(message).not.toContain("private.invalid");
      expect(message).not.toContain(origin);
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
      state: "complete",
      completedRead: true,
      signalAborted: true,
      text: '{"sequence":1}',
      sha256: expect.stringMatching(/^[0-9a-f]{64}$/u),
    });
    await expect(directoryV2ResponseJSON(response)).resolves.toEqual({
      sequence: 1,
    });
    await expect(directoryV2ResponseWitness(response)).rejects.toThrow(
      "signal_aborted=true",
    );
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

  describe("C observed raw-byte-equivalent host transfer", () => {
    const route = "/api/user-assets/v3";
    const cIdentity = `GET ${origin}${route}`;
    const stream = (chunks: Uint8Array[]) =>
      new ReadableStream<Uint8Array>({
        start(controller) {
          for (const chunk of chunks) controller.enqueue(chunk);
          controller.close();
        },
      });
    const closed = async (work: Promise<unknown>, check?: string) => {
      let caught: unknown;
      try {
        await work;
      } catch (error) {
        caught = error;
      }
      expect(caught).toBeInstanceOf(Error);
      expect((caught as Error).message).toMatch(
        /^uac_observed_response_invalid:observed_(body_read|byte_binding)$/u,
      );
      expect((caught as Error).cause).toBeUndefined();
      if (check) expect(directoryUACResponseFailureCheck(caught)).toBe(check);
      return caught as Error;
    };

    it.each([
      ["GET", "/api/user-assets/v3"],
      ["POST", "/api/user-assets/v3/exports"],
      ["GET", "/api/user-assets/v3/exports/receipt"],
      ["GET", "/api/user-assets/v3/exports/detail"],
    ])(
      "transfers exact native bytes for the closed %s %s pair without inspector reads",
      async (method, route) => {
        const raw = Buffer.from(' { "escaped" : "\\u0078", "emoji":"😀" } \n');
        const { consume, fetch, page } = await harness({
          path: route,
          method,
          body: stream([...raw].map((byte) => new Uint8Array([byte]))),
        });
        const captured = await consume();
        const inspector = vi.fn(() => {
          throw new Error("UNIT_PRIVATE_INSPECTOR_ERROR");
        });
        Object.assign(captured, {
          body: inspector,
          json: inspector,
          text: inspector,
        });
        const take = vi.spyOn(observer(), "take");
        const bytes = await directoryUACResponseBytes(captured);
        expect(bytes.equals(raw)).toBe(true);
        expect(fetch).toHaveBeenCalledOnce();
        expect(inspector).not.toHaveBeenCalled();
        expect(take).toHaveBeenCalledOnce();
        expect(observer().peek(`${method} ${origin}${route}`, 0)?.text).toBe(
          "",
        );
        expect(observer().buffers()).toMatchObject({
          chunks: 0,
          hashing: 0,
          text: 0,
        });
        expect(page.waitForFunction).toHaveBeenLastCalledWith(
          expect.any(Function),
          expect.any(Object),
          { timeout: 15_000, polling: 50 },
        );
        bytes.fill(0);
      },
    );

    it.each([[3], [1, 2], [2, 1], [1, 1, 1], [0, 1, 0, 1, 0, 1, 0]])(
      "rejects native BOM split %j before allocating returned bytes",
      async (...sizes) => {
        const raw = Buffer.from('{"value":"😀"}'),
          bom = Buffer.from([239, 187, 191]);
        let offset = 0;
        const chunks = sizes.map((size) => {
          const next = bom.subarray(offset, offset + size);
          offset += size;
          return next;
        });
        chunks.push(raw);
        const { consume } = await harness({
          path: route,
          body: stream(chunks),
        });
        const captured = await consume();
        expect(observer().peek(cIdentity, 0)?.byteLength).toBe(raw.length + 3);
        const from = vi.spyOn(Buffer, "from");
        let error: unknown;
        try {
          await directoryUACResponseBytes(captured);
        } catch (caught) {
          error = caught;
        }
        const allocations = from.mock.calls.length;
        from.mockRestore();
        expect(allocations).toBe(0);
        expect(directoryUACResponseFailureCheck(error)).toBe(
          "observed_byte_binding",
        );
        expect(observer().peek(cIdentity, 0)?.text).toBe("");
      },
    );

    it.each([
      [new Uint8Array([255])],
      [new Uint8Array([0xe2]), new Uint8Array([0x28, 0xa1])],
      [new Uint8Array([0xed, 0xa0]), new Uint8Array([0x80])],
    ])(
      "rejects invalid native UTF-8 %j without a raw-text fallback",
      async (...invalid) => {
        const chunks = [Buffer.from('{"x":"'), ...invalid, Buffer.from('"}')];
        const { consume, latest } = await harness({
          path: route,
          body: stream(chunks),
        });
        await expect(consume()).rejects.toThrow();
        await settle(cIdentity, 0);
        await closed(directoryUACResponseBytes(latest()), "observed_body_read");
        expect(observer().buffers()).toMatchObject({
          chunks: 0,
          hashing: 0,
          text: 0,
        });
      },
    );

    it.each(['{"x":1,"x":2}', '{"x":1,"\\u0078":2}'])(
      "preserves raw duplicate evidence for the existing strict parser: %s",
      async (raw) => {
        const { consume } = await harness({
          path: route,
          body: stream(
            [...Buffer.from(raw)].map((byte) => new Uint8Array([byte])),
          ),
        });
        const bytes = await directoryUACResponseBytes(await consume());
        expect(bytes.toString("utf8") === raw).toBe(true);
        expect(() =>
          parseDirectoryV4UniqueJSON(bytes.toString("utf8")),
        ).toThrow();
        bytes.fill(0);
      },
    );

    it.each([1 << 20, (1 << 20) + 1])(
      "enforces the %i-byte cap before allocation",
      async (size) => {
        const raw = '{"x":"' + "a".repeat(size - 8) + '"}';
        const { consume } = await harness({
          path: route,
          body: stream([Buffer.from(raw)]),
        });
        const captured = await consume();
        const from = vi.spyOn(Buffer, "from");
        let bytes: Buffer | undefined, error: unknown;
        try {
          bytes = await directoryUACResponseBytes(captured);
        } catch (caught) {
          error = caught;
        }
        const allocations = from.mock.calls.length;
        from.mockRestore();
        if (size === 1 << 20) {
          expect(bytes?.length).toBe(size);
          expect(error).toBeUndefined();
          expect(allocations).toBe(1);
          bytes?.fill(0);
        } else {
          expect(allocations).toBe(0);
          expect(directoryUACResponseFailureCheck(error)).toBe(
            "observed_byte_binding",
          );
        }
      },
    );

    it.each([
      "empty",
      "oversized_text_small_native",
      "text_mismatch",
      "native_zero",
      "native_fraction",
      "native_nan",
      "native_over_cap",
      "text_type",
    ])("rejects %s before Buffer.from", async (kind) => {
      const { consume } = await harness({ path: route, body: '{"ok":true}' });
      const captured = await consume(),
        record = observer().peek(cIdentity, 0)!;
      if (kind === "empty") {
        record.text = "";
        record.byteLength = 0;
      }
      if (kind === "oversized_text_small_native") {
        record.text = "a".repeat((1 << 20) + 1);
        record.byteLength = 1;
      }
      if (kind === "text_mismatch") record.text += " ";
      if (kind === "native_zero") record.byteLength = 0;
      if (kind === "native_fraction") record.byteLength = 1.5;
      if (kind === "native_nan") record.byteLength = NaN;
      if (kind === "native_over_cap") record.byteLength = (1 << 20) + 1;
      if (kind === "text_type") Object.assign(record, { text: null });
      const from = vi.spyOn(Buffer, "from");
      let error: unknown;
      try {
        await directoryUACResponseBytes(captured);
      } catch (caught) {
        error = caught;
      }
      const allocations = from.mock.calls.length;
      from.mockRestore();
      expect(allocations).toBe(0);
      expect(directoryUACResponseFailureCheck(error)).toBeDefined();
    });

    it("clears allocated bytes and both text owners when the native digest disagrees", async () => {
      const { consume, page } = await harness({
        path: route,
        body: '{"x":"UNIT_PRIVATE_RESPONSE"}',
      });
      const captured = await consume();
      observer().peek(cIdentity, 0)!.sha256 = "0".repeat(64);
      let hostRecord: Observation | undefined;
      page.evaluate.mockImplementationOnce(async (fn, argument) => {
        const value = fn(argument);
        hostRecord = value.record;
        return value;
      });
      const from = vi.spyOn(Buffer, "from");
      let error: unknown;
      try {
        await directoryUACResponseBytes(captured);
      } catch (caught) {
        error = caught;
      }
      const buffers = from.mock.results
        .filter((result) => result.type === "return")
        .map((result) => result.value as Buffer);
      from.mockRestore();
      expect(directoryUACResponseFailureCheck(error)).toBe(
        "observed_byte_binding",
      );
      expect(buffers.length).toBe(1);
      expect(buffers[0].every((byte) => byte === 0)).toBe(true);
      expect(hostRecord?.text).toBe("");
      expect(observer().peek(cIdentity, 0)?.text).toBe("");
      expect((error as Error).message.includes("UNIT_PRIVATE")).toBe(false);
      expect((error as Error).cause).toBeUndefined();
    });

    it.each([
      ["GET", "/api/user-assets/v3/exports", "legacy"],
      ["POST", "/api/user-assets/v3", "legacy"],
      ["PUT", "/api/user-assets/v3/exports", "legacy"],
      ["GET", "/api/user-assets/v2", "legacy"],
      ["GET", "/api/directory/v4/task", "v4-controls"],
    ] as const)(
      "rejects a non-C pair/mode %s %s %s before taking",
      async (method, route, mode) => {
        const { consume } = await harness({ method, path: route, mode });
        const captured = await consume(),
          take = vi.spyOn(observer(), "take");
        await closed(directoryUACResponseBytes(captured), "observed_body_read");
        expect(take).not.toHaveBeenCalled();
      },
    );

    it.each(["registration", "response_url", "document", "frame"])(
      "rejects %s mismatch without inspector fallback",
      async (kind) => {
        const { consume, newDocument } = await harness({ path: route });
        const captured = await consume(),
          request = captured.request();
        if (kind === "registration")
          Object.assign(captured, { request: () => ({ ...request }) });
        if (kind === "response_url")
          Object.assign(captured, {
            url: () => origin + route + "?UNIT_PRIVATE",
          });
        if (kind === "document") newDocument();
        if (kind === "frame")
          Object.assign(request, { frame: () => ({ page: () => ({}) }) });
        const error = await closed(
          directoryUACResponseBytes(captured),
          "observed_body_read",
        );
        expect(error.message.includes("UNIT_PRIVATE")).toBe(false);
      },
    );

    it.each([
      "epoch",
      "ordinal",
      "method",
      "url",
      "status",
      "actor",
      "cache",
      "eof",
      "readers",
      "released",
      "cancelCalls",
    ])("rejects actual final-record %s tampering", async (key) => {
      const { consume } = await harness({ path: route });
      const captured = await consume(),
        record = observer().peek(cIdentity, 0)!;
      if (["epoch", "method", "url"].includes(key))
        Object.assign(record.identity, { [key]: "UNIT_PRIVATE_IDENTITY" });
      else if (key === "ordinal") record.identity.ordinal++;
      else
        Object.assign(record, {
          [key]:
            key === "status"
              ? 201
              : key === "eof"
                ? false
                : key === "readers"
                  ? 2
                  : key === "released"
                    ? 0
                    : key === "cancelCalls"
                      ? 1
                      : "UNIT_PRIVATE_METADATA",
        });
      const error = await closed(
        directoryUACResponseBytes(captured),
        "observed_body_read",
      );
      expect(error.message.includes("UNIT_PRIVATE")).toBe(false);
    });

    it("rejects host-count advancement during the atomic take", async () => {
      const { consume, context, page } = await harness({ path: route });
      const captured = await consume();
      page.evaluate.mockImplementationOnce(async (fn, argument) => {
        const snapshot = fn(argument);
        context.emit("request", captured.request());
        return snapshot;
      });
      await closed(directoryUACResponseBytes(captured), "observed_body_read");
    });

    it("rejects an invocation/request count mismatch within the existing parity deadline", async () => {
      const { consume, omitNextRequest } = await harness({ path: route });
      omitNextRequest();
      await expect(window.fetch(route)).rejects.toThrow();
      const captured = await consume();
      const now = vi
        .spyOn(Date, "now")
        .mockReturnValueOnce(0)
        .mockReturnValue(15_000);
      try {
        await closed(directoryUACResponseBytes(captured), "observed_body_read");
      } finally {
        now.mockRestore();
      }
    });

    it.each([
      "legacy_first",
      "strict_first",
      "concurrent_legacy_first",
      "concurrent_strict_first",
      "two_raw_callers",
    ])("permits only one successful atomic raw take in %s", async (order) => {
      const { consume } = await harness({ path: route });
      const captured = await consume();
      const take = vi.spyOn(observer(), "take");
      const legacy = () => directoryV2ResponseJSON(captured),
        strict = () => directoryUACResponseBytes(captured);
      if (order === "legacy_first") {
        await legacy();
        await closed(strict(), "observed_body_read");
      } else if (order === "strict_first") {
        (await strict()).fill(0);
        await expect(legacy()).rejects.toThrow();
      } else {
        const calls =
          order === "concurrent_legacy_first"
            ? [legacy(), strict()]
            : order === "concurrent_strict_first"
              ? [strict(), legacy()]
              : [strict(), strict()];
        const results = await Promise.allSettled(calls);
        expect(
          results.filter((result) => result.status === "fulfilled").length,
        ).toBe(1);
        for (const result of results)
          if (result.status === "fulfilled" && Buffer.isBuffer(result.value))
            result.value.fill(0);
      }
      expect(
        take.mock.results.filter((result) => result.type === "return").length,
      ).toBe(1);
      expect(observer().peek(cIdentity, 0)?.text).toBe("");
    });

    it("accepts only the existing completed historical-read policy, without asserting a live witness", async () => {
      const { consume } = await harness({ path: route });
      const controller = new AbortController();
      const captured = await consume(controller);
      controller.abort();
      const bytes = await directoryUACResponseBytes(captured);
      expect(bytes.length).toBeGreaterThan(0);
      bytes.fill(0);
      expect(observer().peek(cIdentity, 0)?.signalAborted).toBe(true);
    });

    it("waits for the actual delayed native raw digest before the one take", async () => {
      let releaseHash!: () => void;
      vi.stubGlobal("crypto", {
        randomUUID: () => webcrypto.randomUUID(),
        subtle: {
          digest: (algorithm: string, raw: Uint8Array<ArrayBuffer>) =>
            new Promise<ArrayBuffer>((resolve, reject) => {
              releaseHash = () => {
                void webcrypto.subtle
                  .digest(algorithm, raw)
                  .then(resolve, reject);
              };
            }),
        },
      });
      const { latest, page } = await harness({ path: route });
      await readDirectoryV2JSON(await window.fetch(route), signal());
      expect(observer().peek(cIdentity, 0)?.state).toBe("hashing");
      let finishWait!: () => void;
      page.waitForFunction.mockImplementationOnce(async (fn, argument) => {
        expect(fn(argument)).toBe(false);
        await new Promise<void>((resolve) => {
          finishWait = resolve;
        });
        expect(fn(argument)).toBe(true);
      });
      const take = vi.spyOn(observer(), "take"),
        pending = directoryUACResponseBytes(latest());
      expect(page.waitForFunction).toHaveBeenLastCalledWith(
        expect.any(Function),
        expect.any(Object),
        { timeout: 15_000, polling: 50 },
      );
      expect(take).not.toHaveBeenCalled();
      releaseHash();
      await settle(cIdentity, 0);
      finishWait();
      const bytes = await pending;
      expect(bytes.length).toBeGreaterThan(0);
      bytes.fill(0);
      expect(take).toHaveBeenCalledOnce();
    });

    it("never exposes unknown errors or accepts forged mutable failure messages", async () => {
      const { consume, page } = await harness({ path: route });
      const captured = await consume();
      const privateError = new Error("UNIT_PRIVATE_ERROR", {
        cause: new Error("UNIT_PRIVATE_CAUSE"),
      });
      page.waitForFunction.mockRejectedValueOnce(privateError);
      const first = await closed(
        directoryUACResponseBytes(captured),
        "observed_body_read",
      );
      expect(first).not.toBe(privateError);
      expect(first.stack?.includes("UNIT_PRIVATE")).toBe(false);
      expect(
        directoryUACResponseFailureCheck(new Error(first.message)),
      ).toBeUndefined();
      expect(
        directoryUACResponseFailureCheck({ message: first.message }),
      ).toBeUndefined();
      first.message = "UNIT_PRIVATE_MUTATION";
      expect(directoryUACResponseFailureCheck(first)).toBe(
        "observed_body_read",
      );
      page.waitForFunction.mockRejectedValueOnce(privateError);
      const second = await closed(
        directoryUACResponseBytes(captured),
        "observed_body_read",
      );
      expect(second).not.toBe(first);
      expect(second.message.includes("UNIT_PRIVATE")).toBe(false);
    });
    it("feeds actual captured bytes to C with one attempted take and independent cached clones", async () => {
      const raw = '{"rows":[{"label":"unredacted"}],"count":1}';
      const { consume } = await harness({
        path: route,
        body: stream(
          [...Buffer.from(raw)].flatMap((byte) => [
            new Uint8Array(0),
            new Uint8Array([byte]),
          ]),
        ),
      });
      const captured = await consume();
      const inspector = vi.fn(() => {
        throw new Error("UNIT_PRIVATE_INSPECTOR_ERROR");
      });
      Object.assign(captured, {
        body: inspector,
        json: inspector,
        text: inspector,
      });
      const take = vi.spyOn(observer(), "take");
      const [first, second] = await Promise.all([
        safeResponse(captured, 1, []),
        safeResponse(captured, 1, []),
      ]);
      expect(first).toEqual({ rows: [{ label: "unredacted" }], count: 1 });
      expect(second).toEqual(first);
      expect(second).not.toBe(first);
      (first.rows as { label: string }[])[0].label = "caller mutation";
      expect((second.rows as { label: string }[])[0].label).toBe("unredacted");
      expect(await safeResponse(captured, 1, [])).toEqual(second);
      expect(take).toHaveBeenCalledOnce();
      expect(inspector).not.toHaveBeenCalled();
      expect(observer().buffers()).toMatchObject({
        chunks: 0,
        hashing: 0,
        text: 0,
      });
    });

    it.each([
      ['{"x":1,"x":2}', [], "body_json"],
      ['{"x":1,"\\u0078":2}', [], "body_json"],
      ['\uFEFF{"x":1}', [], "observed_byte_binding"],
      ['{"x":"UNIT_PRIVATE_VALUE"}', ["UNIT_PRIVATE_VALUE"], "body_privacy"],
      ['{"secret":"value"}', [], "body_privacy"],
      ["[1,2]", [], "body_object"],
    ] as const)(
      "keeps C strict checks on the native-stream path %#",
      async (raw, secrets, check) => {
        const { consume } = await harness({
          path: route,
          body: stream(
            [...Buffer.from(raw)].map((byte) => new Uint8Array([byte])),
          ),
        });
        const captured = await consume();
        let error: unknown;
        try {
          await safeResponse(captured, 1, secrets);
        } catch (caught) {
          error = caught;
        }
        const message = uacFailureMessage(error, "list_read");
        expect(message).toBe(
          `uac_evidence_invalid:post_binding;stage=list_read;check=${check}`,
        );
        expect(message.includes("UNIT_PRIVATE")).toBe(false);
        expect(observer().buffers()).toMatchObject({
          chunks: 0,
          hashing: 0,
          text: 0,
        });
      },
    );

    it("rechecks each caller's secrets after actual stream capture and never revives poisoned cache", async () => {
      const { consume } = await harness({
        path: route,
        body: '{"label":"UNIT_NEWLY_KNOWN"}',
      });
      const captured = await consume(),
        take = vi.spyOn(observer(), "take");
      await expect(safeResponse(captured, 1, [])).resolves.toEqual({
        label: "UNIT_NEWLY_KNOWN",
      });
      const secrets = ["UNIT_NEWLY_KNOWN"];
      for (const current of [secrets, []]) {
        let error: unknown;
        try {
          await safeResponse(captured, 1, current);
        } catch (caught) {
          error = caught;
        }
        expect(uacFailureMessage(error, "list_read")).toBe(
          "uac_evidence_invalid:post_binding;stage=list_read;check=body_privacy",
        );
      }
      expect(take).toHaveBeenCalledOnce();
    });
    it("binds repeated C URLs and distinct query URLs to their actual request ordinals", async () => {
      const { consume, latest } = await harness({ path: route });
      const first = await consume(),
        second = await consume();
      const query = `${route}?page=2`;
      await readDirectoryV2JSON(await window.fetch(query), signal());
      await settle(`GET ${origin}${query}`, 0);
      const third = latest();
      for (const [response, sequence] of [
        [second, 2],
        [third, 3],
        [first, 1],
      ] as const) {
        const bytes = await directoryUACResponseBytes(response);
        expect(bytes.toString("utf8")).toBe(JSON.stringify({ sequence }));
        bytes.fill(0);
      }
      await closed(directoryUACResponseBytes(first), "observed_body_read");
    });

    it("rejects an actual empty native stream before allocating host bytes", async () => {
      const { consume, latest } = await harness({
        path: route,
        body: stream([new Uint8Array(0)]),
      });
      await expect(consume()).rejects.toThrow();
      await settle(cIdentity, 0);
      expect(observer().peek(cIdentity, 0)?.byteLength).toBe(0);
      // The real application reader cancels after empty JSON fails to parse.
      expect(observer().peek(cIdentity, 0)?.cancelCalls).toBe(1);
      const from = vi.spyOn(Buffer, "from");
      let error: unknown;
      try {
        await directoryUACResponseBytes(latest());
      } catch (caught) {
        error = caught;
      }
      const allocations = from.mock.calls.length;
      from.mockRestore();
      expect(allocations).toBe(0);
      expect(directoryUACResponseFailureCheck(error)).toBe(
        "observed_body_read",
      );
    });

    it.each([
      [0xc0, 0xaf],
      [0xe0, 0x80, 0xaf],
      [0xf0, 0x80, 0x80, 0xaf],
      [0xf4, 0x90, 0x80, 0x80],
      [0xe2, 0x82],
      [0xf0, 0x9f, 0x98],
    ])(
      "rejects malformed UTF-8 split into zero-separated native chunks %#",
      async (...bad) => {
        const { consume, latest } = await harness({
          path: route,
          body: stream([
            Buffer.from('{"x":"'),
            ...bad.flatMap((byte) => [
              new Uint8Array(0),
              new Uint8Array([byte]),
            ]),
            Buffer.from('"}'),
          ]),
        });
        await expect(consume()).rejects.toThrow();
        await settle(cIdentity, 0);
        await closed(directoryUACResponseBytes(latest()), "observed_body_read");
        expect(observer().buffers()).toMatchObject({
          chunks: 0,
          hashing: 0,
          text: 0,
        });
      },
    );

    it("handles hostile and revoked error-like objects without property access", () => {
      const inspect = vi.fn(() => {
        throw new Error("UNIT_PRIVATE_GETTER");
      });
      const error = new Proxy(
        {},
        { get: inspect, getOwnPropertyDescriptor: inspect },
      );
      const revocable = Proxy.revocable({}, {});
      revocable.revoke();
      for (const candidate of [
        error,
        revocable.proxy,
        null,
        undefined,
        "observed_body_read",
        1,
      ])
        expect(directoryUACResponseFailureCheck(candidate)).toBeUndefined();
      expect(inspect).not.toHaveBeenCalled();
    });
    it("measures and encodes one text/count/hash snapshot even with hostile host getters", async () => {
      const { consume, page } = await harness({
        path: route,
        body: '{"ok":true}',
      });
      const captured = await consume();
      let textReads = 0,
        countReads = 0,
        hashReads = 0,
        cleared = false;
      page.evaluate.mockImplementationOnce(async (fn, argument) => {
        const result = fn(argument),
          record = result.record;
        const text = record.text,
          count = record.byteLength,
          hash = record.sha256;
        Object.defineProperties(record, {
          text: {
            get: () => (++textReads === 1 ? text : "a".repeat((1 << 20) + 1)),
            set: (value) => {
              cleared = value === "";
            },
          },
          byteLength: {
            get: () => (++countReads <= 4 ? count : (1 << 20) + 1),
          },
          sha256: {
            get: () => {
              hashReads++;
              return hash;
            },
          },
        });
        return result;
      });
      const bytes = await directoryUACResponseBytes(captured);
      expect(bytes.toString("utf8")).toBe('{"ok":true}');
      expect(textReads).toBe(1);
      expect(countReads).toBe(4);
      expect(hashReads).toBe(3);
      expect(cleared).toBe(true);
      bytes.fill(0);
    });
  });
});
