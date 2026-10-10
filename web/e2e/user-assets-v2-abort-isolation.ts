// Active transport-failure injection for held-response E2E only. The passive
// directory response observer remains unchanged: no clone, tee or extra fetch.
// Arm one request, then model an AbortController whose cancellation is ignored.
// Native fetch arguments, response bytes and the production bounded parser are
// untouched, so late data reaches parsing and only the UI generation can drop it.
export function installUserAssetAbortIsolation() {
  if (window !== window.top) return;
  const paths = new Set(["/api/user-assets/v2", "/api/user-assets/v2/detail"]);
  type Witness = {
    path: string;
    requestURL: string | null;
    suppressed: number;
    signal?: AbortSignal;
    epoch: string;
    requestIdentity: {
      epoch: string;
      method: string;
      url: string;
      ordinal: number;
    } | null;
    identityValid: boolean;
  };
  const witnesses = new Map<string, Witness>();
  const protectedSignals = new WeakMap<AbortSignal, Witness>();
  let armed: Witness | undefined;
  const originalFetch = window.fetch;
  const originalAbort = AbortController.prototype.abort;
  AbortController.prototype.abort = function (...args) {
    const witness = protectedSignals.get(this.signal);
    if (witness) {
      if (
        witness.identityValid &&
        witness.requestIdentity &&
        (window as any).__adtrDirectoryV2ResponseObserver?.epoch ===
          witness.epoch
      ) {
        witness.suppressed++;
        return;
      }
      witness.identityValid = false;
    }
    return originalAbort.apply(this, args);
  };
  window.fetch = function (...args: Parameters<typeof fetch>) {
    const [input, init] = args;
    const request = input instanceof Request ? input : undefined;
    const signal = init?.signal !== undefined ? init.signal : request?.signal;
    let url: URL;
    try {
      url = new URL(request ? request.url : String(input), location.href);
    } catch {
      return originalFetch.apply(this, args);
    }
    const method = (init?.method ?? request?.method ?? "GET").toUpperCase();
    if (signal && protectedSignals.has(signal)) {
      protectedSignals.get(signal)!.identityValid = false;
      throw new Error("Held user-asset signal was reused by another request");
    }
    if (
      armed &&
      url.origin === location.origin &&
      method === "GET" &&
      url.pathname === armed.path
    ) {
      const witness = armed;
      armed = undefined;
      if (!(signal instanceof AbortSignal))
        throw new Error("Held user-asset request has no owned AbortSignal");
      witness.requestURL = url.href;
      witness.signal = signal;
      // A pre-aborted signal keeps standard Fetch rejection. Never revive it.
      if (!signal.aborted) protectedSignals.set(signal, witness);
    }
    return originalFetch.apply(this, args);
  };
  Object.defineProperty(window, "__adtrUserAssetAbortIsolation", {
    configurable: true,
    value: {
      arm(path: string, token: string) {
        if (
          !paths.has(path) ||
          armed ||
          witnesses.has(token) ||
          witnesses.size >= 2048
        )
          throw new Error("Invalid held user-asset transport arm");
        const epoch = (window as any).__adtrDirectoryV2ResponseObserver?.epoch;
        if (typeof epoch !== "string" || !epoch)
          throw new Error(
            "Held user-asset transport requires the response observer",
          );
        armed = {
          path,
          requestURL: null,
          suppressed: 0,
          epoch,
          requestIdentity: null,
          identityValid: false,
        };
        witnesses.set(token, armed);
      },
      bindRequest(
        signal: AbortSignal | undefined,
        identity: {
          epoch: string;
          method: string;
          url: string;
          ordinal: number;
        },
      ) {
        const witness = signal && protectedSignals.get(signal);
        if (!witness) return;
        if (
          witness.requestIdentity ||
          identity.epoch !== witness.epoch ||
          (window as any).__adtrDirectoryV2ResponseObserver?.epoch !==
            witness.epoch ||
          identity.method !== "GET" ||
          identity.url !== witness.requestURL ||
          !Number.isSafeInteger(identity.ordinal) ||
          identity.ordinal < 0
        ) {
          witness.identityValid = false;
          throw new Error(
            "Held user-asset transport request identity did not match",
          );
        }
        witness.requestIdentity = { ...identity };
        witness.identityValid = true;
      },
      witness(token: string) {
        const witness = witnesses.get(token);
        if (!witness)
          throw new Error("Missing held user-asset transport witness");
        return {
          path: witness.path,
          requestURL: witness.requestURL,
          suppressed: witness.suppressed,
          aborted: witness.signal?.aborted ?? null,
          requestIdentity: witness.requestIdentity && {
            ...witness.requestIdentity,
          },
          identityValid:
            witness.identityValid &&
            (window as any).__adtrDirectoryV2ResponseObserver?.epoch ===
              witness.epoch,
        };
      },
      disarm(token: string) {
        const witness = witnesses.get(token);
        if (armed === witness) armed = undefined;
        if (witness) {
          if (witness.signal) protectedSignals.delete(witness.signal);
          witness.signal = undefined;
          witness.identityValid = false;
        }
      },
    },
  });
}
