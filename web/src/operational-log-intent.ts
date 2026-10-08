import { ApiError, type Profile } from "./api";
import { validExportID } from "./audit-history";
import {
  validSelection,
  type BundleInput,
  type Selection,
} from "./operational-log-api";

export interface BundleIntent {
  input: BundleInput;
  attempted: boolean;
}
const storagePrefix = "adtr.pending-operational-bundle.";
const pending = new Map<string, BundleIntent>();
const ownerKey = (profile: Profile) =>
  `${storagePrefix}${profile.ID}.${encodeURIComponent(profile.username)}`;
// Session transitions clear in-memory state, while non-secret uncertain keys
// remain isolated by actor. Returning as that actor must reconcile the original
// request rather than create another task after a session renewal.
export function forgetBundleSession() {
  pending.clear();
}
export function discardBundleIntent(profile?: Profile) {
  if (profile) pending.delete(ownerKey(profile));
  else pending.clear();
  try {
    if (profile) sessionStorage.removeItem(ownerKey(profile));
    else
      for (const key of Object.keys(sessionStorage))
        if (key.startsWith(storagePrefix)) sessionStorage.removeItem(key);
  } catch {
    /* No stored intent can be changed when storage is disabled. */
  }
}
export function bundleIntent(profile: Profile): BundleIntent | undefined {
  const key = ownerKey(profile);
  if (!pending.has(key)) {
    try {
      const saved = JSON.parse(sessionStorage.getItem(key) ?? "null"),
        input = saved?.input;
      const selection: Selection = {
        startTm: input?.startTm,
        endTm: input?.endTm,
        systemType: input?.systemType,
      };
      if (
        saved?.owner === profile.ID &&
        saved?.username === profile.username &&
        validSelection(selection) &&
        validExportID(input?.idempotencyKey)
      ) {
        // Restore only the non-secret request whitelist. Proof, tokens, response
        // rows and arbitrary stored fields never enter a write.
        pending.set(key, {
          attempted: true,
          input: {
            ...selection,
            systemType: [...selection.systemType],
            idempotencyKey: input.idempotencyKey,
          },
        });
      } else sessionStorage.removeItem(key);
    } catch {
      /* Reading the page remains possible without session storage. */
    }
  }
  return pending.get(key);
}
export function beginBundleIntent(
  profile: Profile,
  selection: Selection,
): BundleIntent {
  const old = bundleIntent(profile);
  if (old?.attempted) return old;
  if (!validSelection(selection)) throw new ApiError("invalid_input");
  const intent = {
    attempted: false,
    input: {
      ...selection,
      systemType: [...selection.systemType].sort() as Selection["systemType"],
      idempotencyKey: crypto.randomUUID(),
    },
  };
  pending.set(ownerKey(profile), intent);
  return intent;
}
export function markBundleAttempted(profile: Profile, intent: BundleIntent) {
  const saved = JSON.stringify({
      owner: profile.ID,
      username: profile.username,
      input: intent.input,
    }),
    key = ownerKey(profile);
  // Persist before sending so reload cannot turn an uncertain submit into
  // another task. A disabled/full store is a preflight failure, not a write.
  try {
    sessionStorage.setItem(key, saved);
    if (sessionStorage.getItem(key) !== saved) throw new Error();
  } catch {
    throw new ApiError("storage_unavailable");
  }
  intent.attempted = true;
}
