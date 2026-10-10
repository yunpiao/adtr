// Standalone synthetic browser/engine regression, not AD/LDAP acceptance.
// No response routing, replacement parser, clone, cache-policy workaround or
// client timing sleep. The server gates its next write/EOF on an original read.
import { test, expect, type Request } from "@playwright/test";
import { createHash } from "node:crypto";
import { createServer, type ServerResponse } from "node:http";
import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { build } from "vite";

const payload = { marker: "synthetic-stream", value: "😀", count: 7 };
const bytes = Buffer.from(JSON.stringify(payload));
const parserPath = fileURLToPath(
  new URL("../src/directory-v2-json.ts", import.meta.url),
);
const limit = 8 * 1024 * 1024;
const sha256 = (value: Uint8Array | string) =>
  createHash("sha256").update(value).digest("hex");
type Kind =
  | "held-eof"
  | "held-second-chunk"
  | "abort"
  | "truncated"
  | "byte-cap";
type Terminal = {
  event: "finished" | "failed";
  failure: string | null;
  cancelled: boolean | null;
};
const failureCode = (value: string | undefined) =>
  [
    "net::ERR_ABORTED",
    "net::ERR_CONTENT_LENGTH_MISMATCH",
    "net::ERR_INCOMPLETE_CHUNKED_ENCODING",
    "net::ERR_FAILED",
    "net::ERR_CONNECTION_CLOSED",
  ].includes(value ?? "")
    ? value!
    : "other";
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((ready) => {
    resolve = ready;
  });
  return { promise, resolve };
}
async function bounded<T>(
  promise: Promise<T>,
  message: string,
  timeout = 10_000,
): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error(message)), timeout);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

let bundle = "";
let parserSHA = "";
test.use({ headless: false, trace: "off", screenshot: "off", video: "off" });
test.beforeAll(async () => {
  // Compile the actual imported production source and its ApiError dependency.
  // Vite is already a declared locked dependency; nothing is written to disk.
  parserSHA = sha256(await readFile(parserPath));
  const built = await build({
    configFile: false,
    logLevel: "silent",
    build: {
      write: false,
      minify: false,
      lib: { entry: parserPath, name: "ADTRBoundedReader", formats: ["iife"] },
    },
  });
  const output = Array.isArray(built) ? built[0] : built;
  if (!("output" in output)) throw new Error("Parser bundle did not finish");
  const chunks = output.output.filter((part) => part.type === "chunk");
  if (chunks.length !== 1 || chunks[0].imports.length !== 0)
    throw new Error("Parser bundle must be one self-contained script");
  bundle = chunks[0].code;
});

for (const kind of [
  "held-eof",
  "held-second-chunk",
  "abort",
  "truncated",
  "byte-cap",
] as const) {
  test(`native no-store completion: ${kind}`, async ({
    page,
    context,
    browser,
  }, testInfo) => {
    test.setTimeout(30_000);
    expect(browser.browserType().name()).toBe("chromium");
    const first =
      kind === "byte-cap"
        ? Buffer.from("{")
        : kind === "held-second-chunk" || kind === "abort"
          ? bytes.subarray(0, 6)
          : bytes;
    const ended = deferred<void>();
    const serverState = {
      requests: 0,
      releaseCalls: 0,
      pendingReadWitnessed: false,
      bytesWritten: 0,
      finished: false,
      prematureClose: false,
    };
    let response: ServerResponse | undefined;
    const server = createServer((request, res) => {
      if (request.method !== "GET") {
        res.writeHead(405).end();
        return;
      }
      if (request.url === "/") {
        res.writeHead(200, {
          "Content-Type": "text/html",
          "Cache-Control": "no-store",
        });
        res.end(
          '<!doctype html><script src="/reader.js"></script><main>Synthetic native stream regression</main>',
        );
        return;
      }
      if (request.url === "/reader.js") {
        res.writeHead(200, {
          "Content-Type": "text/javascript",
          "Cache-Control": "no-store",
        });
        res.end(bundle);
        return;
      }
      if (request.url !== "/stream" || response) {
        res.writeHead(404).end();
        return;
      }
      serverState.requests++;
      response = res;
      res.on("finish", () => {
        serverState.finished = true;
        ended.resolve();
      });
      res.on("close", () => {
        serverState.prematureClose = !res.writableFinished;
        ended.resolve();
      });
      res.writeHead(200, {
        "Content-Type": "application/json",
        "Cache-Control": "no-store",
        ...(kind === "truncated"
          ? { "Content-Length": String(bytes.length + 1) }
          : {}),
      });
      // Chunked positives deliberately separate the body and transport EOF.
      serverState.bytesWritten += first.length;
      res.write(first);
    });
    const cleanups: (() => Promise<unknown>)[] = [
      async () => {
        if (!server.listening) return;
        server.closeAllConnections();
        await new Promise<void>((resolve, reject) =>
          server.close((error) => (error ? reject(error) : resolve())),
        );
      },
    ];
    let caseFailed = false;
    let caseError: unknown;
    const cleanupErrors: unknown[] = [];
    let stage = "fixture-start";
    let evidenceSaved = false;
    let requestCount = 0;
    let pwResult: Terminal | undefined, nativeResult: Terminal | undefined;
    const nativeIDs = new Set<string>();
    let consumed: Awaited<ReturnType<typeof consume>> | undefined;
    const saveEvidence = async () => {
      // A read-only metrics getter remains usable while the parser is pending.
      // Never read, clone, cancel or replace the body to obtain diagnostics.
      const partial = consumed
        ? null
        : await bounded(
            page.evaluate(
              () => (window as any).nativeStreamSnapshot?.() ?? null,
            ),
            "Partial metrics snapshot did not settle",
            1_000,
          ).catch(() => null);
      const evidence = {
        kind,
        stage,
        browser: browser.version(),
        parserSHA256: parserSHA,
        parserBundleSHA256: sha256(bundle),
        requestCount,
        nativeRequestCount: nativeIDs.size,
        server: { ...serverState },
        pageObservation: consumed
          ? "complete"
          : partial
            ? "partial"
            : "unobserved",
        page: consumed ? { ...consumed, value: undefined } : partial,
        expectedSHA256: sha256(bytes),
        playwright: pwResult ?? null,
        native: nativeResult ?? null,
      };
      console.log(`Native no-store completion: ${JSON.stringify(evidence)}`);
      const evidencePath = testInfo.outputPath(
        "native-no-store-completion.json",
      );
      await writeFile(evidencePath, JSON.stringify(evidence, null, 2));
      await testInfo.attach("native-no-store-completion", {
        path: evidencePath,
        contentType: "application/json",
      });
      evidenceSaved = true;
    };
    try {
      await bounded(
        new Promise<void>((resolve, reject) => {
          server.once("error", reject);
          server.listen(0, "127.0.0.1", resolve);
        }),
        "Loopback fixture did not start",
      );
      const address = server.address();
      if (!address || typeof address === "string")
        throw new Error("No loopback address");
      const origin = `http://127.0.0.1:${address.port}`;
      const url = `${origin}/stream`;
      const pw = deferred<Terminal>(),
        native = deferred<Terminal>();
      let browserRequest: Request | undefined;
      const requested = (request: Request) => {
        if (request.url() !== url) return;
        requestCount++;
        browserRequest = request;
      };
      const finished = (request: Request) => {
        if (request !== browserRequest) return;
        pwResult = { event: "finished", failure: null, cancelled: null };
        pw.resolve(pwResult);
      };
      const failed = (request: Request) => {
        if (request !== browserRequest) return;
        pwResult = {
          event: "failed",
          failure: failureCode(request.failure()?.errorText),
          cancelled: null,
        };
        pw.resolve(pwResult);
      };
      page.on("request", requested);
      page.on("requestfinished", finished);
      page.on("requestfailed", failed);
      cleanups.push(async () => {
        page.off("request", requested);
        page.off("requestfinished", finished);
        page.off("requestfailed", failed);
      });
      const cdp = await context.newCDPSession(page);
      cleanups.push(() => cdp.detach());
      await cdp.send("Network.enable");
      cdp.on("Network.requestWillBeSent", (event) => {
        if (
          event.request.url === url &&
          event.request.method === "GET" &&
          event.type === "Fetch"
        )
          nativeIDs.add(event.requestId);
      });
      cdp.on("Network.loadingFinished", (event) => {
        if (!nativeIDs.has(event.requestId)) return;
        nativeResult = { event: "finished", failure: null, cancelled: null };
        native.resolve(nativeResult);
      });
      cdp.on("Network.loadingFailed", (event) => {
        if (!nativeIDs.has(event.requestId)) return;
        nativeResult = {
          event: "failed",
          failure: failureCode(event.errorText),
          cancelled: event.canceled ?? false,
        };
        native.resolve(nativeResult);
      });
      await context.exposeBinding(
        "releaseStreamFixture",
        async (source, witness) => {
          if (
            source.page !== page ||
            source.frame !== page.mainFrame() ||
            !response ||
            response.destroyed ||
            response.writableEnded ||
            ++serverState.releaseCalls !== 1 ||
            witness?.bytes !== first.length ||
            witness?.nextReadIssued !== true ||
            witness?.eof !== false
          )
            throw new Error("Fixture release ownership/read witness failed");
          serverState.pendingReadWitnessed = true;
          if (kind === "abort") return; // Only the real controller abort ends this response.
          if (kind === "truncated") {
            // Valid JSON bytes are present, but the declared transport body is
            // one byte longer. A socket FIN must remain a genuine read failure.
            response.socket!.end();
            return;
          }
          const remainder =
            kind === "byte-cap"
              ? Buffer.concat([Buffer.alloc(limit, " "), Buffer.from("}")])
              : kind === "held-second-chunk"
                ? bytes.subarray(first.length)
                : Buffer.alloc(0);
          serverState.bytesWritten += remainder.length;
          response.end(remainder);
        },
      );
      stage = "page-load";
      await page.goto(origin);
      stage = "parser-settlement";
      consumed = await bounded(
        page.evaluate(consume, { kind, firstBytes: first.length }),
        "Production parser did not settle",
      );
      stage = "transport-settlement";
      await bounded(
        Promise.all([pw.promise, native.promise, ended.promise]),
        "Original transport did not reach terminal state",
      );
      stage = "terminal-before-assertions";
      await saveEvidence();
      expect(requestCount).toBe(1);
      expect(nativeIDs.size).toBe(1);
      expect(serverState.requests).toBe(1);
      expect(serverState.releaseCalls).toBe(1);
      expect(serverState.pendingReadWitnessed).toBe(true);
      expect(consumed.status).toBe(200);
      expect(consumed.cache).toBe("no-store");
      expect(consumed.limit).toBe(limit);
      expect(consumed.readers).toBe(1);
      expect(consumed.released).toBe(1);
      expect(consumed.locked).toBe(false);
      expect(pwResult!.event).toBe(nativeResult!.event);
      expect(pwResult!.failure).toBe(nativeResult!.failure);
      if (kind === "held-eof" || kind === "held-second-chunk") {
        expect(consumed.fulfilled).toBe(true);
        expect(consumed.value).toEqual(payload);
        expect(consumed.rawDone).toBe(true);
        expect(consumed.eof).toBe(true);
        expect(consumed.observedBytes).toBe(bytes.length);
        expect(consumed.sha256).toBe(sha256(bytes));
        expect(consumed.cancelCalls).toBe(0);
        expect(consumed.bodyCancelCalls).toBe(0);
        expect(consumed.cancelBeforeEOF).toBe(false);
        expect(consumed.aborted).toBe(false);
        expect(serverState.finished).toBe(true);
        expect(serverState.prematureClose).toBe(false);
        // EOF/valid JSON never substitutes for genuine native completion.
        expect(pwResult).toEqual({
          event: "finished",
          failure: null,
          cancelled: null,
        });
        expect(nativeResult).toEqual({
          event: "finished",
          failure: null,
          cancelled: null,
        });
      } else {
        expect(consumed.fulfilled).toBe(false);
        expect(consumed.eof).toBe(false);
        expect(consumed.cancelCalls).toBeGreaterThan(0);
        expect(consumed.cancelBeforeEOF).toBe(true);
        if (kind === "byte-cap") {
          expect(consumed.error).toBe("directory_limit_exceeded");
          expect(consumed.aborted).toBe(false);
          expect(consumed.observedBytes).toBeGreaterThan(limit);
          expect(consumed.retainedBytes).toBeLessThanOrEqual(limit);
          // An intentional parser cap can reject an already delivered body.
          // Its genuine transport terminal is recorded, never reclassified.
          if (nativeResult!.event === "failed")
            expect(nativeResult!.failure).toBe("net::ERR_ABORTED");
        } else {
          expect(serverState.prematureClose).toBe(true);
          expect(nativeResult!.event).toBe("failed");
          expect(nativeResult!.failure).toBe(
            kind === "abort"
              ? "net::ERR_ABORTED"
              : "net::ERR_CONTENT_LENGTH_MISMATCH",
          );
          expect(consumed.aborted).toBe(kind === "abort");
          expect(nativeResult!.cancelled).toBe(kind === "abort");
          if (kind === "abort") {
            expect(consumed.observedBytes).toBe(first.length);
            expect(consumed.sha256).toBe(sha256(first));
          }
          expect(consumed.error).toBe(
            kind === "abort" ? "AbortError" : "invalid_response",
          );
          if (kind === "truncated") {
            expect(consumed.observedBytes).toBe(bytes.length);
            expect(consumed.sha256).toBe(sha256(bytes));
          }
        }
      }
    } catch (error) {
      caseFailed = true;
      caseError = error;
    } finally {
      // Preserve a partial pre-teardown snapshot even when a parser/terminal
      // wait failed. Keep the original failure if diagnostics also fail.
      if (!evidenceSaved) {
        try {
          await saveEvidence();
        } catch (error) {
          cleanupErrors.push(error);
        }
      }
      for (const cleanup of cleanups.reverse()) {
        try {
          await bounded(
            cleanup(),
            "Owned native stream fixture cleanup did not finish",
            5_000,
          );
        } catch (error) {
          cleanupErrors.push(error);
        }
      }
    }
    if (caseFailed && cleanupErrors.length === 0) throw caseError;
    if (caseFailed || cleanupErrors.length)
      throw new AggregateError(
        [...(caseFailed ? [caseError] : []), ...cleanupErrors],
        "Native stream fixture failed; cleanup errors: " + cleanupErrors.length,
        { cause: caseFailed ? caseError : cleanupErrors[0] },
      );
  });
}

// Serialized into the owned loopback page. Every wrapper forwards the original
// native reader, read promise and cancel/release methods unchanged. It neither
// pulls extra bytes nor delays an application read. The binding ACK is joined
// only after the real production parser has settled.
async function consume({
  kind,
  firstBytes,
}: {
  kind: Kind;
  firstBytes: number;
}) {
  const parser = (window as any).ADTRBoundedReader as {
    directoryV2BodyLimit: number;
    readDirectoryV2JSON(
      response: Response,
      signal: AbortSignal,
    ): Promise<unknown>;
  };
  const controller = new AbortController();
  const response = await fetch("/stream", {
    cache: "no-store",
    signal: controller.signal,
  });
  const metrics = {
    readers: 0,
    reads: 0,
    observedBytes: 0,
    retainedBytes: 0,
    rawDone: false,
    eof: false,
    cancelCalls: 0,
    bodyCancelCalls: 0,
    cancelBeforeEOF: false,
    released: 0,
  };
  const chunks: Uint8Array[] = [];
  let released = false;
  let acknowledgement: Promise<void> | undefined;
  const body = response.body!;
  let parserState = "pending";
  (window as any).nativeStreamSnapshot = () => ({
    ...metrics,
    parserState,
    aborted: controller.signal.aborted,
    locked: body.locked,
    limit: parser.directoryV2BodyLimit,
    status: response.status,
    cache: response.headers.get("Cache-Control"),
  });
  const bodyCancel = body.cancel;
  body.cancel = function (...args) {
    metrics.bodyCancelCalls++;
    metrics.cancelBeforeEOF ||= !metrics.eof;
    return bodyCancel.apply(this, args);
  };
  const getReader = body.getReader;
  body.getReader = function (
    this: ReadableStream<Uint8Array>,
    ...args: Parameters<typeof body.getReader>
  ) {
    const reader = Reflect.apply(
      getReader,
      this,
      args,
    ) as ReadableStreamDefaultReader<Uint8Array>;
    metrics.readers++;
    const read = reader.read,
      cancel = reader.cancel,
      release = reader.releaseLock;
    reader.read = function (...args) {
      const pending = read.apply(this, args);
      metrics.reads++;
      // The server still owns every byte after firstBytes (or transport EOF).
      // This native read has therefore been issued before release is possible.
      if (!released && metrics.observedBytes === firstBytes) {
        released = true;
        acknowledgement = (
          (window as any).releaseStreamFixture({
            bytes: metrics.observedBytes,
            nextReadIssued: true,
            eof: metrics.eof,
          }) as Promise<void>
        ).then(() => {
          if (kind === "abort") controller.abort();
        });
        // Keep the binding rejection observable after parser settlement without
        // turning it into an unhandled page rejection in the meantime.
        void acknowledgement.catch(() => undefined);
      }
      void pending.then(
        (part) => {
          if (part.done) {
            metrics.rawDone = true;
            // reader.cancel() can resolve a pending read with done=true.
            // That is not transport EOF and must not validate an aborted body.
            metrics.eof =
              metrics.cancelCalls === 0 &&
              metrics.bodyCancelCalls === 0 &&
              !controller.signal.aborted;
            return;
          }
          metrics.observedBytes += part.value.byteLength;
          if (metrics.observedBytes <= parser.directoryV2BodyLimit) {
            chunks.push(part.value);
            metrics.retainedBytes += part.value.byteLength;
          }
        },
        () => {},
      );
      return pending;
    };
    reader.cancel = function (...args) {
      metrics.cancelCalls++;
      metrics.cancelBeforeEOF ||= !metrics.eof;
      return cancel.apply(this, args);
    };
    reader.releaseLock = function (...args) {
      metrics.released++;
      return release.apply(this, args);
    };
    return reader;
  } as typeof body.getReader;
  let value: unknown,
    error: string | null = null,
    fulfilled = false;
  try {
    value = await parser.readDirectoryV2JSON(response, controller.signal);
    fulfilled = true;
    parserState = "fulfilled";
  } catch (reason) {
    parserState = "rejected";
    const candidate = reason as { code?: string; name?: string };
    error = ["directory_limit_exceeded", "invalid_response"].includes(
      candidate.code ?? "",
    )
      ? candidate.code!
      : candidate.name === "AbortError"
        ? "AbortError"
        : "other";
  }
  await acknowledgement;
  const retained = new Uint8Array(metrics.retainedBytes);
  let offset = 0;
  for (const chunk of chunks) {
    retained.set(chunk, offset);
    offset += chunk.byteLength;
  }
  const digest = await crypto.subtle.digest("SHA-256", retained);
  return {
    fulfilled,
    value,
    error,
    ...metrics,
    aborted: controller.signal.aborted,
    locked: body.locked,
    limit: parser.directoryV2BodyLimit,
    status: response.status,
    cache: response.headers.get("Cache-Control"),
    sha256: Array.from(new Uint8Array(digest), (byte) =>
      byte.toString(16).padStart(2, "0"),
    ).join(""),
  };
}
