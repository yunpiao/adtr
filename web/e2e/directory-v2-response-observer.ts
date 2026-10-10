import type {
  BrowserContext,
  Page,
  Request as BrowserRequest,
  Response,
} from "@playwright/test";

// Chromium can discard the inspector copy of a no-store Fetch body consumed
// through getReader(), even after the application receives every byte and EOF:
// https://github.com/microsoft/playwright/issues/42742
// Observe those exact application reads. Do not clone/tee, make another request,
// intercept delivery, change cache policy, or replace the production parser.
export function installDirectoryV2ResponseObserver() {
  if (window !== window.top) return;
  const key = "__adtrDirectoryV2ResponseObserver";
  const epoch = crypto.randomUUID();
  const limit = 8 * 1024 * 1024;
  // Aggregate per-document quotas, not per-request allocations. Hash staging
  // remains charged until WebCrypto settles, including after failure/abort.
  // Text has a separate 8 MiB UTF-8 input budget; at most 2048 tombstones remain.
  let chunksHeld = 0,
    hashingHeld = 0,
    textHeld = 0,
    recordCount = 0;
  let registrationFailed = false,
    capacityFailed = false;
  void (window as any).__adtrDirectoryV2Document(epoch).catch(() => {
    registrationFailed = true;
  });
  type Identity = {
    epoch: string;
    method: string;
    url: string;
    ordinal: number;
  };
  type Record = {
    identity: Identity;
    state: "pending" | "hashing" | "complete" | "failed";
    status?: number;
    actor?: string | null;
    cache?: string | null;
    text: string;
    byteLength: number;
    sha256: string | null;
    eof: boolean;
    readers: number;
    released: number;
    cancelCalls: number;
    signalAborted: boolean;
    completedRead: boolean;
    taken: boolean;
    failure?: string;
  };
  const records = new Map<string, Record[]>();
  const guards = new WeakMap<
    Record,
    { refresh: () => void; clear: () => void; fail: (reason: string) => void }
  >();
  const paths = new Set([
    "/api/user-assets/v2",
    "/api/user-assets/v2/detail",
    "/api/directory/v2/observation",
    "/api/directory/v2/receipt",
    "/api/directory/v2/task",
    "/api/directory/v2/sync",
    "/api/directory/v2/cancel",
    ...[
      "accounts",
      "grants",
      "roles",
      "effective",
      "mutation",
      "grant",
      "revoke",
    ].map((suffix) => `/api/directory-credential-use/v2/${suffix}`),
  ]);
  const originalFetch = window.fetch;
  window.fetch = function (...args: Parameters<typeof fetch>) {
    const pending = originalFetch.apply(this, args);
    try {
      const [input, init] = args;
      const request = input instanceof Request ? input : undefined;
      const signal = init?.signal !== undefined ? init.signal : request?.signal;
      if (signal != null && !(signal instanceof AbortSignal)) return pending;
      // Native Fetch emits no browser request for a pre-aborted signal.
      if (signal?.aborted) return pending;
      let url: URL;
      try {
        url = new URL(request ? request.url : String(input), location.href);
      } catch {
        return pending;
      }
      if (url.origin !== location.origin || !paths.has(url.pathname))
        return pending;
      if (recordCount >= 2048) {
        capacityFailed = true;
        return pending;
      }
      const method = (init?.method ?? request?.method ?? "GET").toUpperCase();
      const id = `${method} ${url.href}`;
      const list = records.get(id) ?? [];
      const record: Record = {
        identity: { epoch, method, url: url.href, ordinal: list.length },
        state: "pending",
        text: "",
        byteLength: 0,
        sha256: null,
        eof: false,
        readers: 0,
        released: 0,
        cancelCalls: 0,
        signalAborted: false,
        completedRead: false,
        taken: false,
      };
      list.push(record);
      records.set(id, list);
      recordCount++;
      let chunks: Uint8Array[] = [],
        chunkBytes = 0,
        textBytes = 0;
      const clearChunks = () => {
        chunksHeld -= chunkBytes;
        chunkBytes = 0;
        chunks = [];
      };
      const clearText = () => {
        textHeld -= textBytes;
        textBytes = 0;
        record.text = "";
      };
      const aborted = () => {
        record.signalAborted = true;
        // An ordinary response may already have been parsed and released
        // before success/error UI cleanup aborts its controller. Preserve that bounded
        // historical body only; the live strict witness still rejects abort.
        if (
          record.completedRead &&
          (record.state === "hashing" || record.state === "complete")
        ) {
          signal?.removeEventListener("abort", aborted);
          return;
        }
        fail("signal aborted");
      };
      const fail = (failure: string) => {
        if (record.state === "failed") return;
        record.state = "failed";
        record.failure = failure;
        record.completedRead = false;
        record.sha256 = null;
        clearChunks();
        clearText();
        signal?.removeEventListener("abort", aborted);
      };
      const refresh = () => {
        if (signal?.aborted) aborted();
      };
      const clear = () => {
        clearChunks();
        clearText();
        signal?.removeEventListener("abort", aborted);
      };
      guards.set(record, { refresh, clear, fail });
      signal?.addEventListener("abort", aborted, { once: true });
      refresh();
      // Binding occurs after invocation registration in either wrapper order.
      // No transport behavior is changed by this observer callback.
      try {
        (window as any).__adtrUserAssetAbortIsolation?.bindRequest(
          signal,
          record.identity,
        );
      } catch {
        fail("transport binding failed");
      }
      const finalize = () => {
        refresh();
        if (
          record.state === "hashing" &&
          record.sha256 !== null &&
          record.released === 1
        )
          record.state = "complete";
      };
      const hash = () => {
        if (record.byteLength > limit - hashingHeld) {
          fail("hash quota exceeded");
          return;
        }
        const size = record.byteLength;
        hashingHeld += size;
        let raw: Uint8Array<ArrayBuffer>;
        try {
          raw = new Uint8Array(size);
          let offset = 0;
          for (const chunk of chunks) {
            raw.set(chunk, offset);
            offset += chunk.byteLength;
          }
        } catch {
          hashingHeld -= size;
          fail("hash failed");
          return;
        }
        clearChunks();
        record.state = "hashing";
        // The digest is over native raw chunks, never decoded/re-encoded JSON.
        let digest: Promise<ArrayBuffer>;
        try {
          digest = crypto.subtle.digest("SHA-256", raw);
        } catch {
          hashingHeld -= size;
          fail("hash failed");
          return;
        }
        void digest
          .then(
            (value) => {
              if (record.state !== "hashing") return;
              record.sha256 = Array.from(new Uint8Array(value), (byte) =>
                byte.toString(16).padStart(2, "0"),
              ).join("");
              finalize();
            },
            () => fail("hash failed"),
          )
          .finally(() => {
            hashingHeld -= size;
          });
      };
      // Original fetch/read promises, objects, arguments and rejection identities
      // are returned unchanged. Observing never issues another body read.
      void pending.then(
        (response) => {
          try {
            record.status = response.status;
            record.actor = response.headers.get("X-ADTR-User-ID");
            record.cache = response.headers.get("Cache-Control");
            if (!response.body) {
              fail("missing body");
              return;
            }
            const body = response.body;
            const cancelBody = body.cancel;
            body.cancel = function (...args) {
              if (this !== body) fail("foreign body receiver");
              record.cancelCalls++;
              fail(record.eof ? "cancelled after EOF" : "cancelled before EOF");
              return cancelBody.apply(this, args);
            };
            const getReader = body.getReader;
            body.getReader = function (
              this: ReadableStream<Uint8Array>,
              ...args: [ReadableStreamGetReaderOptions?]
            ) {
              let reader: ReadableStreamDefaultReader<Uint8Array>;
              try {
                reader = Reflect.apply(
                  getReader,
                  this,
                  args,
                ) as ReadableStreamDefaultReader<Uint8Array>;
              } catch (error) {
                fail("reader acquisition failed");
                throw error;
              }
              if (this !== body) {
                fail("foreign body receiver");
                return reader;
              }
              record.readers++;
              if (record.readers !== 1) fail("multiple readers");
              if (args[0]?.mode !== undefined) {
                fail("unsupported reader mode");
                return reader;
              }
              const decoder = new TextDecoder("utf-8", { fatal: true });
              const read = reader.read,
                cancel = reader.cancel,
                release = reader.releaseLock;
              reader.cancel = function (...args) {
                if (this !== reader) fail("foreign reader receiver");
                record.cancelCalls++;
                fail(
                  record.eof ? "cancelled after EOF" : "cancelled before EOF",
                );
                return cancel.apply(this, args);
              };
              reader.releaseLock = function (...args) {
                if (this !== reader) fail("foreign reader receiver");
                record.released++;
                if (!record.eof) fail("reader released before EOF");
                if (record.released !== 1)
                  fail("reader released more than once");
                try {
                  const value = release.apply(this, args);
                  refresh();
                  // Latch only after successful native release with the signal
                  // still live, never merely on observing done:true. WebCrypto
                  // may still be pending when success/error UI cleanup unmounts.
                  if (
                    (record.state === "hashing" ||
                      record.state === "complete") &&
                    record.eof === true &&
                    record.readers === 1 &&
                    record.released === 1 &&
                    record.cancelCalls === 0 &&
                    record.signalAborted === false
                  )
                    record.completedRead = true;
                  finalize();
                  return value;
                } catch (error) {
                  fail("reader release failed");
                  throw error;
                }
              };
              reader.read = function (...args) {
                if (this !== reader) fail("foreign reader receiver");
                if (record.eof) fail("read after EOF");
                let pendingRead: ReturnType<typeof read>;
                try {
                  pendingRead = read.apply(this, args);
                } catch (error) {
                  fail("body read rejected");
                  throw error;
                }
                void pendingRead.then(
                  (part) => {
                    if (record.state !== "pending") return;
                    try {
                      if (part.done) {
                        record.eof = true;
                        record.text += decoder.decode();
                        JSON.parse(record.text);
                        hash();
                      } else {
                        const size = part.value.byteLength;
                        // Zero-byte reads carry no UTF-8 input and must not grow
                        // the retained chunk-reference list without byte charge.
                        if (size === 0) return;
                        if (size > limit - record.byteLength) {
                          fail("body limit exceeded");
                          return;
                        }
                        if (
                          size > limit - chunksHeld ||
                          size > limit - textHeld
                        ) {
                          fail("observer byte quota exceeded");
                          return;
                        }
                        const copy = part.value.slice();
                        chunks.push(copy);
                        chunkBytes += size;
                        chunksHeld += size;
                        record.byteLength += size;
                        textBytes += size;
                        textHeld += size;
                        record.text += decoder.decode(part.value, {
                          stream: true,
                        });
                      }
                    } catch {
                      fail("invalid UTF-8 or JSON");
                    }
                  },
                  () => fail("body read rejected"),
                );
                return pendingRead;
              };
              return reader;
            } as typeof body.getReader;
          } catch {
            fail("observer setup failed");
          }
        },
        () => fail("fetch rejected"),
      );
      return pending;
    } catch {
      // Instrumentation must never turn a native rejected Promise (for example
      // malformed RequestInit.signal) into a synchronous exception.
      return pending;
    }
  };
  const checked = (id: string, ordinal: number): Record | undefined => {
    const record = records.get(id)?.[ordinal];
    if (record) {
      guards.get(record)!.refresh();
      if (registrationFailed || capacityFailed)
        guards
          .get(record)!
          .fail(
            registrationFailed
              ? "document registration failed"
              : "observer record limit exceeded",
          );
    }
    return record;
  };
  Object.defineProperty(window, key, {
    configurable: true,
    value: {
      epoch,
      count: (id: string) => records.get(id)?.length ?? 0,
      peek: checked,
      check: checked,
      buffers: () => ({
        chunks: chunksHeld,
        hashing: hashingHeld,
        text: textHeld,
        records: recordCount,
      }),
      take: (id: string, ordinal: number) => {
        const record = checked(id, ordinal);
        if (!record || record.state === "pending" || record.state === "hashing")
          throw new Error("Missing completed directory response");
        if (record.taken)
          throw new Error("Directory response was already taken");
        // Live signal/state checked in the same JavaScript task as the returned
        // copy. The host validates THIS copy, never an earlier peek snapshot.
        const result = {
          ...record,
          identity: record.identity && { ...record.identity },
        };
        const original = records.get(id)?.[ordinal];
        if (original) {
          original.taken = true;
          guards.get(original)!.clear();
        }
        return result;
      },
    },
  });
}

const identities = new WeakMap<
  BrowserRequest,
  { epoch: string; ordinal: number }
>();
const installed = new WeakSet<BrowserContext>();
const documents = new WeakMap<
  Page,
  { epoch: string; counts: Map<string, number> }
>();
const directoryPath = (url: URL) =>
  ["/api/user-assets/v2", "/api/user-assets/v2/detail"].includes(
    url.pathname,
  ) ||
  /^\/api\/directory\/v2\/(?:observation|receipt|task|sync|cancel)$/u.test(
    url.pathname,
  ) ||
  /^\/api\/directory-credential-use\/v2\/(?:accounts|grants|roles|effective|mutation|grant|revoke)$/u.test(
    url.pathname,
  );

export async function observeDirectoryV2Responses(context: BrowserContext) {
  if (installed.has(context)) return;
  installed.add(context);
  await context.exposeBinding(
    "__adtrDirectoryV2Document",
    ({ page, frame }, epoch: string) => {
      if (frame === page.mainFrame())
        documents.set(page, { epoch, counts: new Map() });
    },
  );
  context.on("request", (request) => {
    if (!directoryPath(new URL(request.url()))) return;
    const frame = request.frame();
    const page = frame.page();
    if (
      frame !== page.mainFrame() ||
      request.resourceType() !== "fetch" ||
      new URL(request.url()).origin !== new URL(frame.url()).origin
    )
      return;
    const document = documents.get(page);
    if (!document) return;
    const id = `${request.method()} ${request.url()}`;
    const ordinal = document.counts.get(id) ?? 0;
    identities.set(request, { epoch: document.epoch, ordinal });
    document.counts.set(id, ordinal + 1);
  });
  await context.addInitScript(installDirectoryV2ResponseObserver);
}

// Read-only failure diagnostics for an exact browser Request. Never return
// URLs, identity headers or body text; never read, clone, take or refetch a body.
// A snapshot is diagnostic only, not a substitute for the final witness checks.
export async function directoryV2RequestDiagnostic(request: BrowserRequest) {
  const captured = identities.get(request);
  if (!captured)
    return {
      registered: false,
      sameDocument: false,
      ordinal: null,
      hostCount: null,
      browserCount: null,
      countsMatch: false,
      state: "missing",
      failure: null,
    };
  const page = request.frame().page();
  const document = documents.get(page);
  const id = `${request.method()} ${request.url()}`;
  const hostCount =
    document?.epoch === captured.epoch ? (document.counts.get(id) ?? 0) : null;
  const snapshot = await page.evaluate(
    ({ id, ordinal, epoch }) => {
      const observer = (window as any).__adtrDirectoryV2ResponseObserver;
      const sameDocument = observer?.epoch === epoch;
      const record = sameDocument ? observer.peek(id, ordinal) : undefined;
      const failures = [
        "fetch rejected",
        "cancelled before EOF",
        "body read rejected",
        "body limit exceeded",
        "invalid UTF-8 or JSON",
        "observer setup failed",
        "missing body",
        "unsupported reader mode",
        "document registration failed",
        "signal aborted",
        "cancelled after EOF",
        "hash failed",
        "hash quota exceeded",
        "observer byte quota exceeded",
        "observer record limit exceeded",
        "reader acquisition failed",
        "multiple readers",
        "reader released before EOF",
        "reader released more than once",
        "reader release failed",
        "read after EOF",
        "transport binding failed",
        "foreign body receiver",
        "foreign reader receiver",
      ];
      return {
        sameDocument,
        browserCount: sameDocument ? observer.count(id) : null,
        state: ["pending", "hashing", "complete", "failed"].includes(
          record?.state,
        )
          ? record.state
          : "missing",
        failure:
          record?.failure === undefined
            ? null
            : failures.includes(record.failure)
              ? record.failure
              : "other",
      };
    },
    { id, ...captured },
  );
  return {
    registered: true,
    ordinal: captured.ordinal,
    hostCount,
    ...snapshot,
    sameDocument:
      snapshot.sameDocument &&
      document?.epoch === captured.epoch &&
      documents.get(page)?.epoch === captured.epoch,
    countsMatch: hostCount !== null && hostCount === snapshot.browserCount,
  };
}

export type DirectoryV2RequestIdentity = {
  epoch: string;
  method: string;
  url: string;
  ordinal: number;
};
export type DirectoryV2ResponseWitness = {
  value: unknown;
  byteLength: number;
  sha256: string;
  state: "complete";
  uncanceledEOF: true;
  eof: true;
  status: number;
  actor: string | null;
  cache: string | null;
  identity: DirectoryV2RequestIdentity;
  readers: number;
  released: number;
  signalAborted: false;
  cancelCalls: 0;
};

// Internal comparison only. Never print document IDs or request URLs in evidence.
export function directoryV2RequestIdentity(
  request: BrowserRequest,
): DirectoryV2RequestIdentity {
  const captured = identities.get(request);
  if (!captured) throw new Error("Directory response was not observed");
  if (documents.get(request.frame().page())?.epoch !== captured.epoch)
    throw new Error("Directory response document changed");
  return { ...captured, method: request.method(), url: request.url() };
}

// Both APIs share one atomic historical body take. Ordinary JSON can retain a
// fully parsed/released body through later UI cleanup abort. This historical data is
// never itself live-delivery proof; the strict witness revalidates on EVERY call.
type ObservedBody = {
  value: any;
  record: any;
  identity: DirectoryV2RequestIdentity;
};
const bodies = new WeakMap<Response, Promise<any>>();
const observations = new WeakMap<Response, Promise<ObservedBody>>();
const witnesses = new WeakMap<Response, DirectoryV2ResponseWitness>();
function initialObservation(response: Response) {
  let observation = observations.get(response);
  if (!observation) {
    observation = readObservedBody(response);
    observations.set(response, observation);
  }
  return observation;
}
export function directoryV2ResponseJSON(response: Response): Promise<any> {
  if (!directoryPath(new URL(response.url()))) return response.json();
  let body = bodies.get(response);
  if (!body) {
    body = initialObservation(response).then(
      (observation) => observation.value,
    );
    bodies.set(response, body);
  }
  return body;
}
export async function directoryV2ResponseWitness(
  response: Response,
): Promise<DirectoryV2ResponseWitness> {
  if (!directoryPath(new URL(response.url())))
    throw new Error("Directory response was not observed");
  const observation = await initialObservation(response);
  const current = await finalRecord(response, false);
  validateRecord(response, current.record, current.identity, false);
  if (
    current.record.sha256 !== observation.record.sha256 ||
    current.record.byteLength !== observation.record.byteLength
  )
    throw new Error("Directory browser witness changed after final take");
  let witness = witnesses.get(response);
  if (!witness) {
    const record = current.record;
    witness = {
      value: observation.value,
      byteLength: record.byteLength,
      sha256: record.sha256,
      state: "complete",
      uncanceledEOF: true,
      eof: record.eof,
      status: record.status,
      actor: record.actor,
      cache: record.cache,
      identity: current.identity,
      readers: record.readers,
      released: record.released,
      signalAborted: record.signalAborted,
      cancelCalls: record.cancelCalls,
    };
    witnesses.set(response, witness);
  }
  return witness;
}

// Fixed enums and booleans only. Never put response bodies, headers, URLs,
// request parameters, native error messages or authentication proof in errors.
function observationError(
  prefix: string,
  record: any,
  comparisons: { [key: string]: boolean } = {},
) {
  const boolean = (value: unknown) =>
    value === true ? "true" : value === false ? "false" : "invalid";
  const state = ["pending", "hashing", "complete", "failed"].includes(
    record?.state,
  )
    ? record.state
    : "missing";
  const reasons = [
    "fetch rejected",
    "cancelled before EOF",
    "cancelled after EOF",
    "body read rejected",
    "body limit exceeded",
    "invalid UTF-8 or JSON",
    "observer setup failed",
    "missing body",
    "unsupported reader mode",
    "document registration failed",
    "signal aborted",
    "hash failed",
    "hash quota exceeded",
    "observer byte quota exceeded",
    "observer record limit exceeded",
    "reader acquisition failed",
    "multiple readers",
    "reader released before EOF",
    "reader released more than once",
    "reader release failed",
    "read after EOF",
    "transport binding failed",
    "foreign body receiver",
    "foreign reader receiver",
  ];
  const reason =
    record?.failure === undefined
      ? "none"
      : reasons.includes(record.failure)
        ? record.failure.replaceAll(" ", "_")
        : "other";
  const flags = {
    state,
    eof: boolean(record?.eof),
    signal_aborted: boolean(record?.signalAborted),
    completed_read: boolean(record?.completedRead),
    readers_one: record?.readers === 1,
    released_once: record?.released === 1,
    cancel_free: record?.cancelCalls === 0,
    byte_count_valid:
      Number.isSafeInteger(record?.byteLength) &&
      record.byteLength >= 0 &&
      record.byteLength <= 8 * 1024 * 1024,
    hash_valid:
      typeof record?.sha256 === "string" &&
      /^[0-9a-f]{64}$/u.test(record.sha256),
    reason,
    ...comparisons,
  };
  return new Error(
    `${prefix} (${Object.entries(flags)
      .map(([key, value]) => `${key}=${value}`)
      .join(";")})`,
  );
}

function validateRecord(
  response: Response,
  record: any,
  identity: DirectoryV2RequestIdentity,
  allowHistoricalAbort: boolean,
) {
  const historicalAbort =
    allowHistoricalAbort &&
    record?.signalAborted === true &&
    record.completedRead === true;
  if (
    !record ||
    record.state !== "complete" ||
    record.eof !== true ||
    (record.signalAborted !== false && !historicalAbort) ||
    record.cancelCalls !== 0 ||
    record.readers !== 1 ||
    record.released !== 1 ||
    !Number.isSafeInteger(record.byteLength) ||
    record.byteLength < 0 ||
    record.byteLength > 8 * 1024 * 1024 ||
    typeof record.sha256 !== "string" ||
    !/^[0-9a-f]{64}$/u.test(record.sha256)
  )
    throw observationError("Directory browser body observation failed", record);
  if (
    record.identity?.epoch !== identity.epoch ||
    record.identity?.ordinal !== identity.ordinal ||
    record.identity?.method !== identity.method ||
    record.identity?.url !== identity.url
  )
    throw observationError(
      "Directory browser request identity did not match",
      record,
      {
        epoch_match: record.identity?.epoch === identity.epoch,
        ordinal_match: record.identity?.ordinal === identity.ordinal,
        method_match: record.identity?.method === identity.method,
        url_match: record.identity?.url === identity.url,
      },
    );
  if (
    record.status !== response.status() ||
    record.actor !== (response.headers()["x-adtr-user-id"] ?? null) ||
    record.cache !== (response.headers()["cache-control"] ?? null)
  )
    throw observationError(
      "Directory browser response metadata did not match",
      record,
      {
        status_match: record.status === response.status(),
        actor_match:
          record.actor === (response.headers()["x-adtr-user-id"] ?? null),
        cache_match:
          record.cache === (response.headers()["cache-control"] ?? null),
      },
    );
}
async function finalRecord(response: Response, take: boolean) {
  const request = response.request();
  const identity = directoryV2RequestIdentity(request);
  const page = request.frame().page();
  const id = `${identity.method} ${identity.url}`;
  const deadline = Date.now() + 15_000;
  for (;;) {
    const document = documents.get(page);
    if (document?.epoch !== identity.epoch)
      throw new Error("Directory response document changed");
    const hostCount = document.counts.get(id) ?? 0;
    const snapshot = await page.evaluate(
      ({ id, identity, hostCount, take }) => {
        const observer = (window as any).__adtrDirectoryV2ResponseObserver;
        if (observer?.epoch !== identity.epoch)
          throw new Error("Directory response document changed");
        const count = observer.count(id);
        // Check count parity immediately before consuming the actual final copy.
        if (count !== hostCount) return { count, record: null };
        return {
          count,
          record: {
            ...(take
              ? observer.take(id, identity.ordinal)
              : observer.check(id, identity.ordinal)),
          },
        };
      },
      { id, identity, hostCount, take },
    );
    if (documents.get(page)?.epoch !== identity.epoch)
      throw new Error("Directory response document changed");
    if (snapshot.count === hostCount) {
      // A host request arriving while the final take was in flight cannot make
      // an earlier browser snapshot accidentally balance a missing invocation.
      if (document.counts.get(id) !== hostCount)
        throw new Error(
          "Directory response identity changed during final take",
        );
      return { identity, record: snapshot.record };
    }
    if (Date.now() >= deadline)
      throw new Error(
        "Directory response invocation/request counts did not match",
      );
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
}
async function readObservedBody(response: Response): Promise<ObservedBody> {
  const identity = directoryV2RequestIdentity(response.request());
  const id = `${identity.method} ${identity.url}`;
  const page = response.request().frame().page();
  await page.waitForFunction(
    ({ id, identity }) => {
      const observer = (window as any).__adtrDirectoryV2ResponseObserver;
      if (observer && observer.epoch !== identity.epoch) return true;
      const record = observer?.peek(id, identity.ordinal);
      return record && !["pending", "hashing"].includes(record.state);
    },
    { id, identity },
    { timeout: 15_000, polling: 50 },
  );
  const final = await finalRecord(response, true);
  validateRecord(response, final.record, final.identity, true);
  const record = final.record;
  let value: unknown;
  try {
    value = JSON.parse(record.text);
  } catch {
    throw observationError(
      "Directory browser body observation failed",
      record,
      { json_valid: false },
    );
  } finally {
    // Keep only parsed value and bounded final evidence on the host.
    record.text = "";
  }
  return { value, record, identity: final.identity };
}
