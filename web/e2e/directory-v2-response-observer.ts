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
  // The binding event is sent before any app script can issue a Fetch. It
  // identifies a new document without mistaking pushState/hash for a reload.
  let registrationFailed = false;
  void (window as any).__adtrDirectoryV2Document(epoch).catch(() => {
    registrationFailed = true;
  });
  type Record = {
    state: "pending" | "complete" | "failed";
    status?: number;
    actor?: string | null;
    cache?: string | null;
    text: string;
    failure?: string;
  };
  const records = new Map<string, Record[]>();
  const paths = new Set([
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
    const [input, init] = args;
    const request = input instanceof Request ? input : undefined;
    // Fetch rejects a signal already aborted before dispatch without emitting
    // a browser request. It must not consume a network-response ordinal.
    // Explicit null overrides an input Request's signal; undefined inherits it.
    const signal = init?.signal !== undefined ? init.signal : request?.signal;
    if (signal?.aborted) return pending;
    let url: URL;
    try {
      url = new URL(request ? request.url : String(input), location.href);
    } catch {
      return pending;
    }
    if (url.origin !== location.origin || !paths.has(url.pathname))
      return pending;
    const method =
      init?.method ?? (input instanceof Request ? input.method : "GET");
    const id = `${method.toUpperCase()} ${url.href}`;
    const record: Record = { state: "pending", text: "" };
    const list = records.get(id) ?? [];
    list.push(record);
    records.set(id, list);
    const fail = (failure: string) => {
      if (record.state === "failed") return;
      record.state = "failed";
      record.failure = failure;
      record.text = "";
    };
    // Return the original promises below. Observing must not add an await to
    // application delivery or turn a rejected read into a successful read.
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
            if (record.state !== "complete") fail("cancelled before EOF");
            return cancelBody.apply(this, args);
          };
          const getReader = body.getReader;
          body.getReader = function (
            this: ReadableStream<Uint8Array>,
            ...args: [ReadableStreamGetReaderOptions?]
          ) {
            const reader = Reflect.apply(
              getReader,
              this,
              args,
            ) as ReadableStreamDefaultReader<Uint8Array>;
            if (args[0]?.mode !== undefined) {
              fail("unsupported reader mode");
              return reader;
            }
            const decoder = new TextDecoder("utf-8", { fatal: true });
            let size = 0;
            const read = reader.read;
            const cancel = reader.cancel;
            reader.cancel = function (...args) {
              if (record.state !== "complete") fail("cancelled before EOF");
              return cancel.apply(this, args);
            };
            reader.read = function (...args) {
              const pendingRead = read.apply(this, args);
              void pendingRead.then(
                (part) => {
                  if (record.state !== "pending") return;
                  try {
                    if (part.done) {
                      record.text += decoder.decode();
                      JSON.parse(record.text);
                      record.state = "complete";
                    } else if (part.value.byteLength > 8 * 1024 * 1024 - size) {
                      fail("body limit exceeded");
                    } else {
                      size += part.value.byteLength;
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
  };
  Object.defineProperty(window, key, {
    configurable: true,
    value: {
      epoch,
      count: (id: string) => records.get(id)?.length ?? 0,
      peek: (id: string, ordinal: number) =>
        registrationFailed
          ? {
              state: "failed",
              text: "",
              failure: "document registration failed",
            }
          : records.get(id)?.[ordinal],
      take: (id: string, ordinal: number) => {
        if (registrationFailed)
          throw new Error("Directory observer document registration failed");
        const record = records.get(id)?.[ordinal];
        if (!record || record.state === "pending")
          throw new Error("Missing completed directory response");
        const result = { ...record };
        record.text = "";
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

// Correlate by the exact method/URL and invocation ordinal in the same document.
// Repeated task polls must never borrow a previous body or issue a fallback GET.
const bodies = new WeakMap<Response, Promise<unknown>>();
export function directoryV2ResponseJSON(response: Response): Promise<any> {
  if (!directoryPath(new URL(response.url()))) return response.json();
  let body = bodies.get(response);
  if (!body) {
    body = readObservedBody(response);
    bodies.set(response, body);
  }
  return body;
}
async function readObservedBody(response: Response): Promise<unknown> {
  const request = response.request();
  const captured = identities.get(request);
  if (!captured) throw new Error("Directory response was not observed");
  const page = request.frame().page();
  const identity = { id: `${request.method()} ${request.url()}`, ...captured };
  const deadline = Date.now() + 15_000;
  await page.waitForFunction(
    ({ id, ordinal, epoch }) => {
      const observer = (window as any).__adtrDirectoryV2ResponseObserver;
      if (observer && observer.epoch !== epoch) return true;
      const record = observer?.peek(id, ordinal);
      return record && record.state !== "pending";
    },
    identity,
    { timeout: 15_000, polling: 50 },
  );
  let record;
  for (;;) {
    const document = documents.get(page);
    if (document?.epoch !== identity.epoch)
      throw new Error("Directory response document changed");
    // Snapshot host first: a later request must not accidentally balance a
    // missing earlier host event against an already-stale page snapshot.
    const hostCount = document.counts.get(identity.id) ?? 0;
    const snapshot = await page.evaluate(({ id, ordinal, epoch }) => {
      const observer = (window as any).__adtrDirectoryV2ResponseObserver;
      if (observer?.epoch !== epoch)
        throw new Error("Directory response document changed");
      return {
        count: observer.count(id),
        record: { ...observer.peek(id, ordinal) },
      };
    }, identity);
    if (documents.get(page)?.epoch !== identity.epoch)
      throw new Error("Directory response document changed");
    if (snapshot.count === hostCount) {
      record = snapshot.record;
      break;
    }
    // Invalid-init/CSP and an abort before network dispatch can produce a
    // Fetch invocation without a host request event. Allow event delivery to
    // settle, but never borrow another ordinal when those counts diverge.
    if (Date.now() >= deadline)
      throw new Error(
        "Directory response invocation/request counts did not match",
      );
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  await page.evaluate(({ id, ordinal, epoch }) => {
    const observer = (window as any).__adtrDirectoryV2ResponseObserver;
    if (observer?.epoch !== epoch)
      throw new Error("Directory response document changed");
    observer.take(id, ordinal);
  }, identity);
  if (record.state !== "complete")
    throw new Error(
      `Directory browser body observation failed: ${record.failure}`,
    );
  if (
    record.status !== response.status() ||
    record.actor !== (response.headers()["x-adtr-user-id"] ?? null) ||
    record.cache !== (response.headers()["cache-control"] ?? null)
  )
    throw new Error("Directory browser response metadata did not match");
  return JSON.parse(record.text);
}
