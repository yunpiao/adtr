// Same-origin invalidation hints never carry identity or credentials. The caller
// must re-read /api/auth/me; neither a notice nor its absence proves a session.
export const SESSION_INVALIDATION_CHANNEL = "adtr.session-invalidation.v1";
export const SESSION_INVALIDATION_STORAGE = "adtr.session-invalidation.v1";

export type SessionInvalidation = {
  publish: () => void;
  close: () => void;
};

const noncePattern = /^[0-9a-f]{32}$/;
const maxRemembered = 64;

export function listenForSessionInvalidation(
  invalidate: () => void,
): SessionInvalidation {
  const seen = new Set<string>();
  let closed = false;
  let channel: BroadcastChannel | undefined;
  const remember = (nonce: unknown): nonce is string => {
    if (
      typeof nonce !== "string" ||
      !noncePattern.test(nonce) ||
      closed ||
      seen.has(nonce)
    )
      return false;
    seen.add(nonce);
    if (seen.size > maxRemembered) seen.delete(seen.values().next().value!);
    return true;
  };
  const receive = (nonce: unknown) => {
    if (remember(nonce)) invalidate();
  };
  const message = (event: MessageEvent<unknown>) => receive(event.data);
  const storage = (event: StorageEvent) => {
    if (event.key !== SESSION_INVALIDATION_STORAGE) return;
    try {
      if (event.storageArea !== window.localStorage) return;
    } catch {
      return;
    }
    receive(event.newValue);
  };
  try {
    channel = new BroadcastChannel(SESSION_INVALIDATION_CHANNEL);
    channel.addEventListener("message", message);
  } catch {
    // Storage events and focus/visibility verification still work when a
    // browser has no BroadcastChannel support or refuses the constructor.
    channel?.close();
    channel = undefined;
  }
  window.addEventListener("storage", storage);
  return {
    publish() {
      if (closed) return;
      let nonce: string;
      try {
        nonce = crypto.randomUUID().replaceAll("-", "");
      } catch {
        return; // No persistent identifier or weak-randomness fallback.
      }
      if (!remember(nonce)) return;
      try {
        channel?.postMessage(nonce);
      } catch {
        // A broken channel must not prevent the independent storage fallback.
      }
      try {
        // Events retain newValue after removal. No session data is persisted,
        // and nothing is read from storage on startup as an identity claim.
        window.localStorage.setItem(SESSION_INVALIDATION_STORAGE, nonce);
        window.localStorage.removeItem(SESSION_INVALIDATION_STORAGE);
      } catch {
        // Disabled/full storage is optional; focus and visibility reverify.
      }
    },
    close() {
      if (closed) return;
      closed = true;
      window.removeEventListener("storage", storage);
      channel?.removeEventListener("message", message);
      channel?.close();
      seen.clear();
    },
  };
}
