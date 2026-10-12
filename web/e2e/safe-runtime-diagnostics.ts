import { writeFileSync } from "node:fs";
import { performance } from "node:perf_hooks";

const routes = new Set([
  "/api/domain-selection",
  "/api/domain-selection/resolve",
  "/api/access/menu",
  "/api/access/check",
  "/api/user-assets/v2",
  "/api/user-assets/v3",
  "/api/resources/tenant",
  "/api/auth/me",
  "/api/domains/detail",
  "/api/directory-credential-use/effective",
  "/api/directory-credential-use/v2/effective",
  "/api/credential-use/effective",
  "/api/credential-use/grant",
  "/api/tasks",
  "/api/tasks/detail",
  "/api/directory/v2/task",
  "/api/directory/v2/receipt",
  "/api/directory/observation",
  "/api/directory/v2/observation",
]);
const started = performance.now();
const records: string[] = [];
let bytes = 0;
let dropped = 0;
const boundedMs = (value: number) =>
  Math.min(86_400_000, Math.max(0, Math.floor(value)));

export function safeRoute(path: string): string {
  const pathname = path.split("?", 1)[0];
  return routes.has(pathname) ? pathname : "unknown";
}

// No response object, exception, user identifier, query, header or log is ever
// accepted here. Upload repeats an independent strict schema and byte check.
export function recordSafeEvent(
  phase: "read-json" | "totp-wait" | "grant-v2-complete",
  path: string,
  outcome: "start" | "response" | "transport-error" | "complete",
  elapsed = 0,
  status: number | null = null,
) {
  if (
    !["read-json", "totp-wait", "grant-v2-complete"].includes(phase) ||
    !["start", "response", "transport-error", "complete"].includes(outcome) ||
    !Number.isFinite(elapsed) ||
    !(
      status === null ||
      (Number.isInteger(status) && status >= 100 && status <= 599)
    )
  )
    return;
  appendSafeEvent({
    phase,
    method: phase === "read-json" ? "GET" : "none",
    route: safeRoute(path),
    elapsed_ms: boundedMs(elapsed),
    at_ms: boundedMs(performance.now() - started),
    outcome,
    status,
  });
}

// Only callers in this module and the strictly validated request recorder use this.
export function appendSafeEvent(event: object) {
  const file = process.env.ADTR_E2E_SAFE_DIAGNOSTICS;
  if (!file) return;
  const record = JSON.stringify(event);
  records.push(record);
  bytes += Buffer.byteLength(record) + 1;
  // Leave room for the fixed envelope and bounded dropped counter.
  while (records.length > 256 || bytes > 61_440) {
    bytes -= Buffer.byteLength(records.shift()!) + 1;
    dropped = Math.min(1_000_000, dropped + 1);
  }
  try {
    writeFileSync(
      file,
      `{"dropped":${dropped},"events":[${records.join(",")}]}`,
      {
        encoding: "utf8",
        mode: 0o600,
      },
    );
  } catch {
    // Evidence collection must not replace the original strict test failure.
    // No dynamic error text is printed; a missing file stays visible in runtime
    // phase evidence and is never a reason to retry the request.
  }
}
