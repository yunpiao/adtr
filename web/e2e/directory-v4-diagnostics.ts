import type { BrowserContext } from "@playwright/test";

// V4-only localization. No URLs, identifiers, proof, response data or errors are
// accepted. These console markers are not acceptance evidence.
const phases = [
  "suite-start", "setup-start", "bootstrap-ready", "producer-login",
  "producer-ready", "binding-start", "binding-complete", "source-read",
  "account-read", "effective-read", "identity-read", "grant-proof",
  "grant-request", "grant-body", "setup-complete", "workspace-open",
  "workspace-ready", "source-select", "sync-prepare", "proof-start",
  "proof-ready", "mutation-click", "mutation-response", "body-start",
  "body-complete", "controls-first", "controls-loss", "controls-absent",
  "readers-tabs", "readers-focus", "readers-history", "readers-stale",
  "readers-authority", "cancel-queued", "cancel-worker", "cancel-running",
] as const;
let emitted = 0;
export function v4Phase(phase: typeof phases[number]): void {
  if (!phases.includes(phase)) throw new Error("Invalid v4 diagnostic phase");
  if (emitted >= 256) return;
  emitted++;
  console.log(`[v4-phase] ${phase}`);
}
export function configureV4Deadlines(context: BrowserContext): void {
  // Existing fixture API calls use 15 s. This also bounds locator/response waits,
  // including new tabs, without changing genuine TOTP waits or suite budgets.
  context.setDefaultTimeout(15_000);
  context.setDefaultNavigationTimeout(15_000);
  v4Phase("suite-start");
}
export async function boundedV4<T>(operation: Promise<T>, milliseconds: number, message: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([operation, new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error(message)), milliseconds);
    })]);
  } finally { if (timer) clearTimeout(timer); }
}
