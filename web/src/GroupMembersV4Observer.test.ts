// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  groupMembersV4ObservedPath,
  installGroupMembersV4ResponseObserver,
  observeGroupMembersV4Responses,
  groupMembersV4ResponseJSON,
  groupMembersV4RequestDiagnostic,
  groupMembersV4ResponseWitness,
  groupMembersV4RequestIdentity,
} from "../e2e/group-members-v4-response-observer";
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
const path = "/api/directory/v4/observation?domainId=synthetic";
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
      __adtrGroupMembersV4ResponseObserver: {
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
  ).__adtrGroupMembersV4ResponseObserver;
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
    __adtrGroupMembersV4Document: register,
  });
  Object.assign(window, { top: window });
  installGroupMembersV4ResponseObserver();
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
  it.each(["/api/directory/v4/observation", "/api/group-assets/v4/members"])(
    "observes original bounded-reader bytes for %s",
    async (route) => {
      const text = JSON.stringify({
        dictionaryVersion: 4,
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
    },
  );
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
    await window.fetch("/api/directory/v4/observation", init);
    await readDirectoryV2JSON(native, signal());
    const observed = await takeRecord(
      `POST ${origin}/api/directory/v4/observation`,
      0,
    );
    expect(observed.text).toBe('{"task":"synthetic"}');
    expect(JSON.stringify(observed)).not.toContain("private-proof");
    expect(fetch).toHaveBeenLastCalledWith(
      "/api/directory/v4/observation",
      init,
    );
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

describe("v4-only host path allowlist", () => {
  it.each(["/api/directory/v4/observation", "/api/group-assets/v4/members"])(
    "includes exact %s",
    (path) => {
      expect(groupMembersV4ObservedPath(new URL(path, origin))).toBe(true);
    },
  );
  it.each([
    "/api/directory/v4/sync",
    "/api/directory/v4/observation/",
    "/api/group-assets/v4/members/extra",
    "/api/directory/v3/observation",
    "/api/auth/me",
  ])("excludes %s", (path) => {
    expect(groupMembersV4ObservedPath(new URL(path, origin))).toBe(false);
  });
});

describe("v4 native host binding", () => {
  async function harness(
    route = path,
    options: { status?: number; body?: BodyInit } = {},
  ) {
    const path = route,
      identity = `GET ${origin}${route}`;
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
    await observeGroupMembersV4Responses(context as unknown as BrowserContext);
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

  it.each(["/api/directory/v4/observation", "/api/group-assets/v4/members"])(
    "binds exact native bytes for %s and installs once",
    async (route) => {
      const h = await harness(route);
      await observeGroupMembersV4Responses(
        h.context as unknown as BrowserContext,
      );
      expect(h.context.exposeBinding).toHaveBeenCalledTimes(1);
      expect(h.context.addInitScript).toHaveBeenCalledTimes(1);
      const response = await h.consume();
      expect(await groupMembersV4ResponseJSON(response)).toEqual({
        sequence: 1,
      });
      const witness = await groupMembersV4ResponseWitness(response);
      expect(witness).toMatchObject({
        state: "complete",
        eof: true,
        uncanceledEOF: true,
        readers: 1,
        released: 1,
        cancelCalls: 0,
        actor: "1",
        cache: "no-store",
      });
      expect(witness.sha256).toBe(
        createHash("sha256").update('{"sequence":1}').digest("hex"),
      );
      expect(witness.identity.url).toBe(origin + route);
      expect(witness.identity.ordinal).toBe(0);
      expect(h.fetch).toHaveBeenCalledTimes(1);
    },
  );
});
