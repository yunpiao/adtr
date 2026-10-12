import type { Page, Request, Response } from "@playwright/test";
import { performance } from "node:perf_hooks";
import { appendSafeEvent, safeRoute } from "./safe-runtime-diagnostics";

const started = performance.now();
const limit = 1_000_000;
let ordinal = 0;
const phases = [
  "directory-get",
  "fixture-get",
  "page-request",
  "test-start",
  "test-finish",
  "administrator-login",
  "version-switch",
  "source-select",
] as const;
const errors = [
  "none",
  "econnreset",
  "socket_hang_up",
  "response_aborted",
  "context_disposed",
  "target_closed",
  "connection_refused",
  "timeout",
  "unknown_error",
  "non_error",
  "page_request_failed",
] as const;
export type DiagnosticPhase = (typeof phases)[number];
type ErrorClass = (typeof errors)[number];
export type Snapshot = {
  view: "v2" | "v3" | "login" | "other" | "unavailable";
  picker: "visible" | "hidden" | "absent" | "unavailable";
  source: "loading" | "empty" | "radio-present" | "absent" | "unavailable";
  alert: boolean;
  permission_denied: boolean;
  page_closed: boolean;
  unavailable: boolean;
};
export type SafeRequestEvent = {
  origin: "api-request" | "page-request" | "step";
  phase: DiagnosticPhase;
  operation: number;
  attempt: number;
  method: "GET" | "POST" | "other" | "none";
  route: string;
  outcome:
    | "start"
    | "response"
    | "finished"
    | "transport-error"
    | "complete"
    | "failed";
  status: number | null;
  error_class: ErrorClass;
  at_ms: number;
  elapsed_ms: number;
  snapshot: Snapshot | null;
};
const integer = (n: unknown, low: number, high: number): n is number =>
  typeof n === "number" && Number.isInteger(n) && n >= low && n <= high;
const keys = (value: object, fields: string[]) =>
  Object.keys(value).sort().join(",") === fields.sort().join(",");
const oneOf = (value: unknown, values: readonly string[]) =>
  typeof value === "string" && values.includes(value);
function validSnapshot(value: Snapshot): boolean {
  return (
    value !== null &&
    typeof value === "object" &&
    keys(value, [
      "view",
      "picker",
      "source",
      "alert",
      "permission_denied",
      "page_closed",
      "unavailable",
    ]) &&
    oneOf(value.view, ["v2", "v3", "login", "other", "unavailable"]) &&
    oneOf(value.picker, ["visible", "hidden", "absent", "unavailable"]) &&
    oneOf(value.source, [
      "loading",
      "empty",
      "radio-present",
      "absent",
      "unavailable",
    ]) &&
    [
      value.alert,
      value.permission_denied,
      value.page_closed,
      value.unavailable,
    ].every((v) => typeof v === "boolean") &&
    (value.unavailable
      ? value.view === "unavailable" &&
        value.picker === "unavailable" &&
        value.source === "unavailable" &&
        !value.alert &&
        !value.permission_denied
      : value.view !== "unavailable" &&
        value.picker !== "unavailable" &&
        value.source !== "unavailable" &&
        !value.page_closed)
  );
}
export function validRequestEvent(e: SafeRequestEvent): boolean {
  if (
    !e ||
    typeof e !== "object" ||
    !keys(e, [
      "origin",
      "phase",
      "operation",
      "attempt",
      "method",
      "route",
      "outcome",
      "status",
      "error_class",
      "at_ms",
      "elapsed_ms",
      "snapshot",
    ]) ||
    !oneOf(e.origin, ["api-request", "page-request", "step"]) ||
    !oneOf(e.phase, phases) ||
    !integer(e.operation, 1, limit) ||
    !integer(e.attempt, 0, 2) ||
    !oneOf(e.method, ["GET", "POST", "other", "none"]) ||
    typeof e.route !== "string" ||
    (e.route !== "unknown" && safeRoute(e.route) !== e.route) ||
    !oneOf(e.outcome, [
      "start",
      "response",
      "finished",
      "transport-error",
      "complete",
      "failed",
    ]) ||
    !oneOf(e.error_class, errors) ||
    !(e.status === null || integer(e.status, 100, 599)) ||
    !integer(e.at_ms, 0, 86_400_000) ||
    !integer(e.elapsed_ms, 0, 86_400_000)
  )
    return false;
  if (e.origin === "step")
    return (
      !["directory-get", "fixture-get", "page-request"].includes(e.phase) &&
      e.method === "none" &&
      e.route === "unknown" &&
      e.attempt === 0 &&
      e.status === null &&
      e.error_class === "none" &&
      ["start", "complete", "failed"].includes(e.outcome) &&
      e.snapshot !== null &&
      validSnapshot(e.snapshot)
    );
  if (e.snapshot !== null || e.method === "none") return false;
  if (
    e.origin === "api-request" &&
    (!(["directory-get", "fixture-get"] as string[]).includes(e.phase) ||
      e.method !== "GET" ||
      !integer(e.attempt, 1, e.phase === "fixture-get" ? 2 : 1) ||
      !["start", "response", "transport-error"].includes(e.outcome))
  )
    return false;
  if (
    e.origin === "page-request" &&
    (e.phase !== "page-request" ||
      e.attempt !== 0 ||
      !["start", "response", "finished", "transport-error"].includes(e.outcome))
  )
    return false;
  if (e.outcome === "start")
    return e.status === null && e.error_class === "none";
  if (e.outcome === "response" || e.outcome === "finished")
    return e.status !== null && e.error_class === "none";
  return e.origin === "api-request"
    ? e.status === null &&
        e.error_class !== "none" &&
        e.error_class !== "page_request_failed"
    : e.error_class === "page_request_failed";
}
export function recordRequestEvent(event: SafeRequestEvent) {
  try {
    if (validRequestEvent(event)) appendSafeEvent(event);
  } catch {
    /* Never mask the test outcome. */
  }
}
const milliseconds = (n: number) =>
  Math.min(86_400_000, Math.max(0, Math.floor(n)));
export function nextOperation() {
  return ordinal < limit ? ++ordinal : 0;
}
export function safeErrorClass(error: unknown): ErrorClass {
  try {
    if (!(error instanceof Error)) return "non_error";
    const line = error.message.split("\n", 1)[0];
    if (
      error.name === "TimeoutError" ||
      /^apiRequestContext\.get: (?:Timeout|Request timed out)/u.test(line)
    )
      return "timeout";
    if (
      /^apiRequestContext\.get: (?:read |write |connect )?ECONNRESET$/u.test(
        line,
      )
    )
      return "econnreset";
    if (
      /^apiRequestContext\.get: connect ECONNREFUSED(?: (?:127\.0\.0\.1|::1):[0-9]{1,5})?$/u.test(
        line,
      )
    )
      return "connection_refused";
    switch (line) {
      case "apiRequestContext.get: socket hang up":
        return "socket_hang_up";
      case "apiRequestContext.get: aborted":
        return "response_aborted";
      case "apiRequestContext.get: Request context disposed.":
        return "context_disposed";
      case "apiRequestContext.get: Target page, context or browser has been closed":
        return "target_closed";
      default:
        return "unknown_error";
    }
  } catch {
    return "unknown_error";
  }
}
export function beginGET(
  phase: "directory-get" | "fixture-get",
  path: string,
  operation = nextOperation(),
  attempt = 1,
) {
  const start = performance.now();
  const emit = (
    outcome: SafeRequestEvent["outcome"],
    status: number | null,
    error_class: ErrorClass,
  ) =>
    recordRequestEvent({
      origin: "api-request",
      phase,
      operation,
      attempt,
      method: "GET",
      route: safeRoute(path),
      outcome,
      status,
      error_class,
      at_ms: milliseconds(performance.now() - started),
      elapsed_ms: milliseconds(performance.now() - start),
      snapshot: null,
    });
  emit("start", null, "none");
  return {
    response: (response: { status(): number }) => {
      try {
        emit("response", response.status(), "none");
      } catch {
        /* Diagnostic status access cannot change the original response. */
      }
    },
    failure: (error: unknown, deadline = false) =>
      emit(
        "transport-error",
        null,
        deadline ? "timeout" : safeErrorClass(error),
      ),
  };
}
export async function diagnosticGET<T extends { status(): number }>(
  get: () => Promise<T>,
  path: string,
): Promise<T> {
  const capture = beginGET("directory-get", path);
  try {
    const response = await get();
    capture.response(response);
    return response;
  } catch (error) {
    capture.failure(error);
    throw error;
  }
}

// Read-only listeners: no routing, waiting for a response, bodies or headers.
export function observeSafePageRequests(page: Page) {
  if (!process.env.ADTR_E2E_SAFE_DIAGNOSTICS) return () => {};
  let active = true;
  const pending = new Map<
    Request,
    {
      operation: number;
      start: number;
      status: number | null;
      route: string;
      method: "GET" | "POST" | "other";
    }
  >();
  const emit = (request: Request, outcome: SafeRequestEvent["outcome"]) => {
    const state = pending.get(request);
    if (state)
      recordRequestEvent({
        origin: "page-request",
        phase: "page-request",
        operation: state.operation,
        attempt: 0,
        method: state.method,
        route: state.route,
        outcome,
        status: state.status,
        error_class:
          outcome === "transport-error" ? "page_request_failed" : "none",
        at_ms: milliseconds(performance.now() - started),
        elapsed_ms: milliseconds(performance.now() - state.start),
        snapshot: null,
      });
  };
  const request = (r: Request) => {
    if (!active) return;
    try {
      const route = safeRoute(new URL(r.url()).pathname);
      if (route === "unknown" || pending.size >= 256) return;
      const method = r.method();
      pending.set(r, {
        operation: nextOperation(),
        start: performance.now(),
        status: null,
        route,
        method: method === "GET" || method === "POST" ? method : "other",
      });
      emit(r, "start");
    } catch {
      /* No dynamic diagnostics. */
    }
  };
  const response = (r: Response) => {
    try {
      const req = r.request();
      const state = pending.get(req);
      if (state) {
        state.status = r.status();
        emit(req, "response");
      }
    } catch {
      /* Best effort. */
    }
  };
  const finish = (r: Request) => {
    try {
      emit(r, "finished");
    } finally {
      pending.delete(r);
    }
  };
  const fail = (r: Request) => {
    try {
      emit(r, "transport-error");
    } finally {
      pending.delete(r);
    }
  };
  const stop = () => {
    active = false;
    for (const detach of [
      () => page.off("request", request),
      () => page.off("response", response),
      () => page.off("requestfinished", finish),
      () => page.off("requestfailed", fail),
    ]) {
      try {
        detach();
      } catch {
        /* Continue detaching remaining listeners. */
      }
    }
    pending.clear();
  };
  try {
    page.on("request", request);
    page.on("response", response);
    page.on("requestfinished", finish);
    page.on("requestfailed", fail);
  } catch {
    stop();
  }
  return stop;
}

const unavailable = (closed: boolean): Snapshot => ({
  view: "unavailable",
  picker: "unavailable",
  source: "unavailable",
  alert: false,
  permission_denied: false,
  page_closed: closed,
  unavailable: true,
});
export async function safeSnapshot(page: Page): Promise<Snapshot> {
  const closed = () => {
    try {
      return page.isClosed();
    } catch {
      return false;
    }
  };
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    if (page.isClosed()) return unavailable(true);
    // One immediate evaluation, no locator waits. Return only fixed semantic
    // states; text is compared inside the page and never crosses this boundary.
    return await Promise.race([
      page.evaluate((): Snapshot => {
        const picker = document.getElementById("asset-source-picker");
        const texts = Array.from(
          document.querySelectorAll('[role="status"]'),
          (e) => e.textContent?.trim(),
        );
        const login = Array.from(document.querySelectorAll("h1,h2,h3"), (e) =>
          e.textContent?.trim(),
        ).includes("登录账户");
        return {
          view: login
            ? "login"
            : location.hash === "#user-assets-v2"
              ? "v2"
              : location.hash === "#user-assets-v3"
                ? "v3"
                : "other",
          picker: !picker
            ? "absent"
            : picker.getClientRects().length
              ? "visible"
              : "hidden",
          source: texts.includes("正在读取已授权数据源…")
            ? "loading"
            : texts.includes("当前筛选下没有已授权数据源。")
              ? "empty"
              : picker?.querySelector('input[type="radio"]')
                ? "radio-present"
                : "absent",
          alert: !!document.querySelector('[role="alert"]'),
          permission_denied:
            texts.includes("当前账户无权选择已配置域。") ||
            texts.includes("当前账户没有用户资产读取权限。"),
          page_closed: false,
          unavailable: false,
        };
      }),
      new Promise<Snapshot>((resolve) => {
        timer = setTimeout(() => resolve(unavailable(closed())), 100);
      }),
    ]);
  } catch {
    return unavailable(closed());
  } finally {
    clearTimeout(timer);
  }
}
export async function checkpoint(
  page: Page,
  phase: Exclude<
    DiagnosticPhase,
    "directory-get" | "fixture-get" | "page-request"
  >,
  outcome: "start" | "complete" | "failed",
  operation = nextOperation(),
) {
  if (!process.env.ADTR_E2E_SAFE_DIAGNOSTICS) return;
  const snapshot = await safeSnapshot(page);
  recordRequestEvent({
    origin: "step",
    phase,
    operation,
    attempt: 0,
    method: "none",
    route: "unknown",
    outcome,
    status: null,
    error_class: "none",
    at_ms: milliseconds(performance.now() - started),
    elapsed_ms: 0,
    snapshot,
  });
}
export async function diagnosticStep<T>(
  page: Page,
  phase: "administrator-login" | "version-switch" | "source-select",
  action: () => Promise<T>,
): Promise<T> {
  const operation = nextOperation();
  await checkpoint(page, phase, "start", operation);
  try {
    const value = await action();
    await checkpoint(page, phase, "complete", operation);
    return value;
  } catch (error) {
    await checkpoint(page, phase, "failed", operation);
    throw error;
  }
}
