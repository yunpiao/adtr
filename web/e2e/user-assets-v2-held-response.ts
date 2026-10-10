import {
  expect,
  type APIResponse,
  type Page,
  type Request,
  type Route,
  type Response,
} from "@playwright/test";
import { createHash, randomUUID } from "node:crypto";
import {
  directoryV2RequestDiagnostic,
  directoryV2RequestIdentity,
  directoryV2ResponseWitness,
} from "./directory-v2-response-observer";
import {
  nativeStreamMode,
  nativeRequestTerminal,
  proveNativeStream,
  type NativeTerminal,
} from "./native-stream-proof";

async function bounded<T>(
  promise: Promise<T>,
  milliseconds: number,
  message: string,
): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error(message)), milliseconds);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}
const digest = (bytes: Uint8Array) =>
  createHash("sha256").update(bytes).digest("hex");
const failureCode = (value: string | undefined) =>
  [
    "net::ERR_ABORTED",
    "net::ERR_FAILED",
    "net::ERR_CONNECTION_CLOSED",
    "net::ERR_CONNECTION_RESET",
    "net::ERR_EMPTY_RESPONSE",
    "net::ERR_TIMED_OUT",
    "net::ERR_NETWORK_CHANGED",
  ].includes(value ?? "")
    ? value!
    : "other";

// route.fetch errors can contain Playwright request logs and session cookies.
// Keep this genuine upstream read single-attempt and fail with fixed safe text.
export async function fetchHeldResponse(route: Pick<Route, "fetch">) {
  try {
    return await route.fetch({
      maxRedirects: 0,
      maxRetries: 0,
      timeout: 15_000,
    });
  } catch {
    throw new Error(
      "Held genuine upstream fetch failed; transport details withheld",
    );
  }
}

// Hold one genuine authenticated API response. Its original bytes are captured
// before fulfill; the passive observer witnesses the application's own reader.
// Return application consumption and native completion as separate facts.
export async function holdAssetResponse(
  page: Page,
  path: string,
  actorId: number,
  expectedDetail: unknown,
) {
  if (!["/api/user-assets/v2", "/api/user-assets/v2/detail"].includes(path))
    throw new Error("Unsupported held user asset route");
  const browser = page.context().browser();
  if (!browser || browser.browserType().name() !== "chromium")
    throw new Error("Native held proof requires Chromium");
  const mode = nativeStreamMode();
  if (mode === "stable-body" && browser.version() !== "141.0.7390.37")
    throw new Error("Stable body mode requires the proven Chromium baseline");
  const cdp = await page.context().newCDPSession(page);
  let frameID: string;
  try {
    frameID = (await cdp.send("Page.getFrameTree")).frameTree.frame.id;
    await cdp.send("Network.enable");
  } catch (cause) {
    await cdp.detach().catch(() => {});
    throw cause;
  }
  const origin = new URL(page.url()).origin;
  const trackedNative = new Set<string>();
  const nativeRequests = new Map<string, string[]>();
  const hostRequests = new Map<string, Request[]>();
  const terminals = new Map<string, NativeTerminal[]>();
  const pw: NativeTerminal[] = [];
  let held: Request | undefined, response: APIResponse | undefined;
  let genuineBody: any, genuineBytes: Buffer | undefined;
  let responseHeadersReceived = false,
    phase = "armed",
    error: unknown;
  const token = randomUUID(),
    started = Date.now();
  const events: {
    event: string;
    phase: string;
    elapsedMilliseconds: number;
  }[] = [];
  const record = (event: string) =>
    events.push({ event, phase, elapsedMilliseconds: Date.now() - started });
  const key = (method: string, url: string) => `${method} ${url}`;
  const eligible = (request: Request) =>
    request.frame() === page.mainFrame() &&
    request.resourceType() === "fetch" &&
    new URL(request.url()).origin === origin &&
    new URL(request.url()).pathname === path;
  const requested = (request: Request) => {
    if (!eligible(request)) return;
    const id = key(request.method(), request.url()),
      list = hostRequests.get(id) ?? [];
    list.push(request);
    hostRequests.set(id, list);
  };
  const ended = (request: Request) => {
    if (request === held) {
      pw.push({ event: "finished", failure: null, cancelled: null });
      record("requestfinished");
    }
  };
  const failed = (request: Request) => {
    if (request === held) {
      pw.push({
        event: "failed",
        failure: failureCode(request.failure()?.errorText),
        cancelled: null,
      });
      record("requestfailed");
    }
  };
  const headers = (candidate: Response) => {
    if (candidate.request() === held) {
      responseHeadersReceived = true;
      record("response headers");
    }
  };
  page.on("request", requested);
  page.on("requestfinished", ended);
  page.on("requestfailed", failed);
  page.on("response", headers);
  cdp.on("Network.requestWillBeSent", (event) => {
    if (
      event.frameId !== frameID ||
      event.type !== "Fetch" ||
      new URL(event.request.url).origin !== origin ||
      new URL(event.request.url).pathname !== path
    )
      return;
    const id = key(event.request.method, event.request.url),
      list = nativeRequests.get(id) ?? [];
    list.push(event.requestId);
    nativeRequests.set(id, list);
    trackedNative.add(event.requestId);
  });
  cdp.on("Network.loadingFinished", (event) => {
    if (!trackedNative.has(event.requestId)) return;
    const list = terminals.get(event.requestId) ?? [];
    list.push({ event: "finished", failure: null, cancelled: null });
    terminals.set(event.requestId, list);
  });
  cdp.on("Network.loadingFailed", (event) => {
    if (!trackedNative.has(event.requestId)) return;
    const list = terminals.get(event.requestId) ?? [];
    list.push({
      event: "failed",
      failure: failureCode(event.errorText),
      cancelled: event.canceled ?? false,
    });
    terminals.set(event.requestId, list);
  });
  const nativeForHeld = () => {
    if (!held) return { bound: false, events: [] as NativeTerminal[] };
    const id = key(held.method(), held.url()),
      host = hostRequests.get(id) ?? [],
      native = nativeRequests.get(id) ?? [];
    return nativeRequestTerminal(held, host, native, terminals);
  };
  let ready!: () => void,
    reject!: (error: unknown) => void,
    release!: () => void,
    finish!: () => void;
  const fetched = new Promise<void>((resolve, fail) => {
    ready = resolve;
    reject = fail;
  });
  void fetched.catch(() => {});
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const completed = new Promise<void>((resolve) => {
    finish = resolve;
  });
  const isolation = async () => {
    if (!held) throw new Error("Held proof has no browser request");
    const identity = directoryV2RequestIdentity(held);
    const witness = await page.evaluate(
      (token) => (window as any).__adtrUserAssetAbortIsolation.witness(token),
      token,
    );
    const identityMatches =
      witness.identityValid === true &&
      ["epoch", "method", "url", "ordinal"].every(
        (field) =>
          witness.requestIdentity?.[field] === (identity as any)[field],
      ) &&
      witness.path === path &&
      witness.requestURL === held.url();
    return {
      identityMatches,
      suppressed: witness.suppressed,
      aborted: witness.aborted,
    };
  };
  const diagnose = async (
    stage:
      | "genuine detail captured"
      | "API list401 observed"
      | "native session401 observed"
      | "release started"
      | "release settled"
      | "release failed",
  ) => {
    if (!held) throw new Error("Held diagnostic has no browser request");
    const witness = await isolation(),
      observer = await directoryV2RequestDiagnostic(held),
      native = nativeForHeld();
    console.log(
      `UserAssetsV2 held diagnostic: ${JSON.stringify({ stage, phase, mode, responseHeadersReceived, events, playwright: pw, native: native.events, nativeBound: native.bound, ...witness, observer })}`,
    );
    expect(witness.identityMatches).toBe(true);
    expect(observer).toMatchObject({ registered: true, sameDocument: true });
  };
  const predicate = (url: URL) => url.pathname === path;
  const handler = async (route: Route) => {
    try {
      held = route.request();
      phase = "capturing upstream";
      record("request intercepted");
      response = await fetchHeldResponse(route);
      expect(response.status()).toBe(200);
      expect(response.headers()["cache-control"]).toBe("no-store");
      expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId));
      genuineBytes = await response.body();
      expect(genuineBytes.length).toBeLessThanOrEqual(8 * 1024 * 1024);
      genuineBody = JSON.parse(
        new TextDecoder("utf-8", { fatal: true }).decode(genuineBytes),
      );
      const query = new URL(held.url()).searchParams;
      expect(genuineBody.dictionaryVersion).toBe(2);
      expect(genuineBody.observationId).toBeTruthy();
      expect(genuineBody.selection).toEqual({
        domainId: query.get("domainId"),
        revision: query.get("expectedRevision"),
        credentialRevision: query.get("expectedCredentialRevision"),
      });
      if (query.has("observationId"))
        expect(genuineBody.observationId).toBe(query.get("observationId"));
      if (path.endsWith("/detail")) {
        expect(genuineBody.object).toEqual(expectedDetail);
        expect(genuineBody.object.objectGUID).toBe(query.get("objectGUID"));
      }
      phase = "holding genuine response";
      record("upstream body captured");
      ready();
      await bounded(
        gate,
        120_000,
        "Held genuine user response was not released",
      );
      phase = "fulfilling";
      record("fulfill started");
      await route.fulfill({ response });
      phase = "fulfilled";
      record("fulfill finished");
    } catch (cause) {
      error = cause;
      reject(cause);
      await route.abort().catch(() => {});
    } finally {
      try {
        await response?.dispose();
      } catch (cause) {
        error ??= cause;
      }
      finish();
    }
  };
  try {
    await page.route(predicate, handler, { times: 1 });
    await page.evaluate(
      ({ path, token }) =>
        (window as any).__adtrUserAssetAbortIsolation.arm(path, token),
      { path, token },
    );
  } catch (cause) {
    page.off("request", requested);
    page.off("requestfinished", ended);
    page.off("requestfailed", failed);
    page.off("response", headers);
    const cleanupErrors: unknown[] = [];
    for (const cleanup of [
      () => page.unroute(predicate, handler),
      () => cdp.detach(),
    ]) {
      try {
        await bounded(cleanup(), 5_000, "Held setup cleanup did not finish");
      } catch (error) {
        cleanupErrors.push(error);
      }
    }
    if (cleanupErrors.length)
      throw new AggregateError([cause, ...cleanupErrors], "Held setup failed", {
        cause,
      });
    throw cause;
  }
  let result: Promise<ReturnType<typeof proveNativeStream>> | undefined;
  return {
    fetched: () =>
      bounded(fetched, 20_000, "No genuine user asset response captured"),
    diagnose,
    release() {
      if (!result)
        result = (async () => {
          let primary: unknown,
            caseFailed = false,
            value: ReturnType<typeof proveNativeStream> | undefined;
          const cleanupErrors: unknown[] = [];
          try {
            if (!held) throw new Error("No genuine held request was issued");
            await diagnose("release started");
            release();
            await bounded(
              completed,
              20_000,
              "Held user asset route failed to finish",
            );
            if (error) throw error;
            await expect
              .poll(() => pw.length > 0 && nativeForHeld().events.length > 0, {
                timeout: 15_000,
              })
              .toBe(true);
            await diagnose("release settled");
            const browserResponse = await held.response();
            if (!browserResponse || !genuineBytes)
              throw new Error("Held proof lacks original response bytes");
            const witness = await directoryV2ResponseWitness(browserResponse);
            expect(witness.value).toEqual(genuineBody);
            const armed = await isolation();
            expect(armed.suppressed).toBeGreaterThan(0);
            expect(armed.aborted).toBe(false);
            const metadataMatches =
              witness.status === 200 &&
              witness.actor === String(actorId) &&
              witness.cache === "no-store";
            const native = nativeForHeld();
            value = proveNativeStream(
              mode,
              browser.version(),
              {
                uncanceledEOF: witness.uncanceledEOF,
                expectedByteLength: genuineBytes.length,
                byteLength: witness.byteLength,
                expectedSHA256: digest(genuineBytes),
                sha256: witness.sha256,
                readers: witness.readers,
                released: witness.released,
                signalAborted: witness.signalAborted,
                cancelCalls: witness.cancelCalls,
                metadataMatches,
                identityMatches:
                  armed.identityMatches &&
                  native.bound &&
                  ["epoch", "method", "url", "ordinal"].every(
                    (field) =>
                      (witness.identity as any)[field] ===
                      (directoryV2RequestIdentity(held!) as any)[field],
                  ),
              },
              pw,
              native.events,
            );
            console.log(
              `UserAssetsV2 held proof: ${JSON.stringify({ ...value, byteLength: witness.byteLength, sha256: witness.sha256, uncanceledEOF: witness.uncanceledEOF, suppressed: armed.suppressed, identityMatches: armed.identityMatches, nativeBound: native.bound })}`,
            );
          } catch (cause) {
            caseFailed = true;
            primary = cause;
            if (held) {
              try {
                await bounded(
                  diagnose("release failed"),
                  1_000,
                  "Held failure diagnostics did not settle",
                );
              } catch (diagnosticError) {
                cleanupErrors.push(diagnosticError);
              }
            }
          } finally {
            release();
            for (const cleanup of [
              () =>
                page.evaluate(
                  (token) =>
                    (window as any).__adtrUserAssetAbortIsolation.disarm(token),
                  token,
                ),
              () => page.unroute(predicate, handler),
              () => cdp.detach(),
            ]) {
              try {
                await bounded(
                  cleanup(),
                  5_000,
                  "Held proof cleanup did not finish",
                );
              } catch (cause) {
                cleanupErrors.push(cause);
              }
            }
            page.off("request", requested);
            page.off("requestfinished", ended);
            page.off("requestfailed", failed);
            page.off("response", headers);
          }
          if (caseFailed && cleanupErrors.length === 0) throw primary;
          if (caseFailed || cleanupErrors.length)
            throw new AggregateError(
              [...(caseFailed ? [primary] : []), ...cleanupErrors],
              "Held proof or cleanup failed",
              { cause: caseFailed ? primary : cleanupErrors[0] },
            );
          return value!;
        })();
      return result;
    },
  };
}
