import { expect, type Page, type Locator, type BrowserContext, type APIResponse, type Request, type Response, type Frame, type TestInfo } from "@playwright/test";
import { request as nodeRequest } from "node:http";
import { createHash } from "node:crypto";
import { types } from "node:util";
import { writeV4ScenarioEvidence } from "./directory-v4-controls-evidence";
import { parseDirectoryV4Task } from "../src/directory-v4-controls-api";
import { parseDirectoryV4UniqueJSON } from "../src/directory-v4-controls-json";
import { parseGroupMembersV4Page } from "../src/group-members-v4-read";
import { parseGroupMembersV4 } from "../src/group-members-v4-api";
import { directoryV4DisplayMember } from "../src/directory-v4-facts";
import { freshCode, fillSecret, totp, type Authenticator } from "./directory-v4-fixture";
import type { Task } from "../src/task-api";
import { v4Phase } from "./directory-v4-diagnostics";
import { recordV4BodyClass, readV4ObservedObject, waitForV4RawMembers } from "./directory-v4-result-observation";
import { directoryV4ControlsResponseJSON } from "./directory-v2-response-observer";

export const V4 = "/api/directory/v4/";
export function invariant(ok: unknown, message: string): asserts ok { if (!ok) throw new Error(message); }
export async function bounded<T>(operation: Promise<T>, milliseconds: number, message: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try { return await Promise.race([operation, new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error(message)), milliseconds); })]); }
  finally { if (timer) clearTimeout(timer); }
}
export type IdentityJSONFailureClass = "json_syntax_error" | "cdp_body_no_data" | "cdp_body_error" | "playwright_api_wrapper" | "unknown";
const identitySyntaxErrorPrototype = SyntaxError.prototype;
// This projects a native-error shape, not authenticated provenance or a cause.
// In particular, do not read SyntaxError.message: it may quote the JSON body.
export function classifyIdentityJSONFailure(reason: unknown): IdentityJSONFailureClass {
  try {
    if (typeof reason !== "object" || reason === null || types.isProxy(reason) || !types.isNativeError(reason)) return "unknown";
    if (Object.getPrototypeOf(reason) === identitySyntaxErrorPrototype) return "json_syntax_error";
    const descriptor = Object.getOwnPropertyDescriptor(reason, "message");
    if (!descriptor || !Object.hasOwn(descriptor, "value") || typeof descriptor.value !== "string") return "unknown";
    const message = descriptor.value;
    // Diagnostic admission only, not a limit on the original response body.
    if (message.length === 0 || message.length > 2048) return "unknown";
    if (message === "response.json: Protocol error (Network.getResponseBody): No data found for resource with given identifier") return "cdp_body_no_data";
    for (const prefix of [
      "response.json: Protocol error (Network.getResponseBody): ",
      "response.json: Protocol error (Network.loadNetworkResource): ",
      "response.json: Protocol error (IO.read): ",
      "response.json: Protocol error (IO.close): ",
    ]) if (message.startsWith(prefix) && message.length > prefix.length) return "cdp_body_error";
    const wrapper = "response.json: ";
    return message.startsWith(wrapper) && message.length > wrapper.length ? "playwright_api_wrapper" : "unknown";
  } catch { return "unknown"; }
}
export function identityJSONFailureMessage(value: unknown): string {
  switch (value) {
    case "json_syntax_error": return "identity_response_json_failed; json_failure_class=json_syntax_error";
    case "cdp_body_no_data": return "identity_response_json_failed; json_failure_class=cdp_body_no_data";
    case "cdp_body_error": return "identity_response_json_failed; json_failure_class=cdp_body_error";
    case "playwright_api_wrapper": return "identity_response_json_failed; json_failure_class=playwright_api_wrapper";
    default: return "identity_response_json_failed; json_failure_class=unknown";
  }
}
export type IdentityRequestFailureClass = "aborted" | "connection_reset" | "connection_closed" | "connection_refused" | "timed_out" | "failed" | "unknown";
// Exact allowlist projection only. Never stringify a Request failure or invoke
// an accessor: unknown strings may contain URLs or other private text.
export function classifyIdentityRequestFailure(value: unknown): IdentityRequestFailureClass {
  try {
    if (typeof value !== "object" || value === null || types.isProxy(value)) return "unknown";
    const descriptor = Object.getOwnPropertyDescriptor(value, "errorText");
    if (!descriptor || !Object.hasOwn(descriptor, "value")) return "unknown";
    switch (descriptor.value) {
      case "net::ERR_ABORTED": return "aborted";
      case "net::ERR_CONNECTION_RESET": return "connection_reset";
      case "net::ERR_CONNECTION_CLOSED": return "connection_closed";
      case "net::ERR_CONNECTION_REFUSED": return "connection_refused";
      case "net::ERR_TIMED_OUT": return "timed_out";
      case "net::ERR_FAILED": return "failed";
      default: return "unknown";
    }
  } catch { return "unknown"; }
}
// Read the first eligible App-passive-purpose original Playwright Response,
// not the formerly ambiguous first /me from any owner, a replacement, or an
// application-native JSON observation. The fixed client purpose is not authority
// or cryptographic owner authentication; reviewed source and the original actor
// assertion remain mandatory. FRESH means first host-observed after
// arming; the bounded Page request snapshot cannot prove native initiation time.
export async function readNativeFocusIdentity(page: Page): Promise<number> {
  type Failure = "identity_prepare_failed" | "identity_request_ambiguous" | "identity_response_metadata" |
    "identity_response_json_failed" | "identity_focus_failed" | "identity_deadline" | "identity_page_changed" |
    "identity_invalid_id" | "identity_record_limit";
  const state: { phase: "PREPARING" | "ARMED" | "SELECTED" | "SUCCESS" | "FAILURE"; failure?: Failure } = { phase: "PREPARING" };
  const deadline = Date.now() + 15_000, old = new Set<Request>(), fresh = new Set<Request>();
  const removers: (() => void)[] = [];
  let active = true, pending = 0, focusDone = false, id: number | undefined;
  let jsonFailureClass: IdentityJSONFailureClass = "unknown";
  let frame: Frame | undefined, origin = "", selectedRequest: Request | undefined, selectedResponse: Response | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined, cleanupFailed = false;
  // Host observations only, not native initiation/finish or causal proof. Keep
  // the existing deadline and first-result state machine independent of logging.
  type DiagnosticEvent = "prepare" | "armed" | "focus_call" | "focus_resolved" | "focus_rejected" |
    "selected" | "json_call" | "json_resolved" | "json_rejected" | "request_finished" | "request_failed" |
    "first_failure" | "success" | "cleanup" | "old_request" | "fresh_request";
  type RequestObservation = { finishedAtMs: number | null; failedAtMs: number | null; failureClass: IdentityRequestFailureClass | "not_observed" };
  const startedAt = deadline - 15_000;
  const events: { event: DiagnosticEvent; atMs: number }[] = [];
  const requestObservations = new Map<Request, RequestObservation>();
  const diagnosticRemovers: (() => void)[] = [];
  let diagnosticFault = false, eventsDropped = 0, laterMatchingResponses = 0, repeatedSelectedResponses = 0;
  let selectedObservation: RequestObservation | undefined;
  let selectedStatus: 200 | null = null;
  let focusSettled = false, focusSettledWhenJSONRejected: boolean | null = null;
  const observedRequests = { old: 0, fresh: 0 };
  const relativeTime = () => Math.min(15_000, Math.max(0, Math.floor(Date.now() - startedAt)));
  const note = (event: DiagnosticEvent) => {
    if (!active) return;
    try {
      if (events.length < 32) events.push({ event, atMs: relativeTime() });
      else eventsDropped = Math.min(65_535, eventsDropped + 1);
    } catch { diagnosticFault = true; }
  };
  const observeTerminal = (request: Request, failed: boolean) => {
    if (!active || state.phase === "SUCCESS" || state.phase === "FAILURE") return;
    try {
      // Object identity only: no URL/body/header reinspection or new command.
      const observation = requestObservations.get(request);
      if (!observation) return;
      if (failed && observation.failedAtMs === null) {
        observation.failedAtMs = relativeTime();
        try { observation.failureClass = classifyIdentityRequestFailure(request.failure()); }
        catch { observation.failureClass = "unknown"; diagnosticFault = true; }
      } else if (!failed && observation.finishedAtMs === null) observation.finishedAtMs = relativeTime();
      if (request === selectedRequest) note(failed ? "request_failed" : "request_finished");
    } catch { diagnosticFault = true; }
  };
  const requestFinished = (request: Request) => { observeTerminal(request, false); };
  const requestFailed = (request: Request) => { observeTerminal(request, true); };
  const installDiagnostic = (add: () => void, remove: () => void) => {
    try { add(); diagnosticRemovers.push(remove); }
    catch { diagnosticFault = true; }
  };
  note("prepare");
  let complete!: () => void;
  const completion = new Promise<void>(resolve => { complete = resolve; });
  const finish = () => {
    if (state.phase === "FAILURE") { if (pending === 0) complete(); }
    else if (state.phase === "SELECTED" && focusDone && id !== undefined) { state.phase = "SUCCESS"; note("success"); complete(); }
  };
  const fail = (failure: Failure) => {
    if (!active || state.phase === "SUCCESS") return;
    if (state.phase !== "FAILURE") { state.failure = failure; state.phase = "FAILURE"; note("first_failure"); }
    finish();
  };
  const failJSON = (reason: unknown) => {
    // Settlement timing only, including a rejection after another first failure.
    if (active && focusSettledWhenJSONRejected === null) focusSettledWhenJSONRejected = focusSettled;
    // Do not inspect a late reason or let a new deadline check change the winner.
    if (active && state.phase === "SELECTED") jsonFailureClass = classifyIdentityJSONFailure(reason);
    fail("identity_response_json_failed");
  };
  const expire = () => { fail("identity_deadline"); complete(); };
  const live = () => {
    if (!active || state.phase === "SUCCESS" || state.phase === "FAILURE") return false;
    if (Date.now() >= deadline) { expire(); return false; }
    return true;
  };
  const record = (request: Request, beforeAction: boolean) => {
    if (old.has(request)) return;
    if (!fresh.has(request) && old.size + fresh.size >= 256) { fail("identity_record_limit"); return; }
    try {
      if (!requestObservations.has(request)) {
        requestObservations.set(request, { finishedAtMs: null, failedAtMs: null, failureClass: "not_observed" });
        const kind = beforeAction ? "old" : "fresh";
        observedRequests[kind]++;
        if (observedRequests[kind] === 1) note(beforeAction ? "old_request" : "fresh_request");
      }
    } catch { diagnosticFault = true; }
    if (beforeAction) { fresh.delete(request); old.add(request); }
    else fresh.add(request);
  };
  const requestSeen = (request: Request) => {
    if (!live()) return;
    try { if (new URL(request.url()).pathname === "/api/auth/me") record(request, state.phase === "PREPARING"); }
    catch { fail("identity_response_metadata"); }
  };
  const responseSeen = (response: Response) => {
    if (!live()) return;
    try {
      if (state.phase === "PREPARING") {
        if (new URL(response.url()).pathname === "/api/auth/me") record(response.request(), true);
        return;
      }
      const request = response.request();
      if (old.has(request)) return;
      if (selectedResponse) {
        if (new URL(response.url()).pathname !== "/api/auth/me") return;
        // Matching pathname only, not another eligible/canonical200 candidate.
        // Counting ends at first failure/success via live(); no post-failure reads.
        laterMatchingResponses = Math.min(65_535, laterMatchingResponses + 1);
        if (request === selectedRequest) repeatedSelectedResponses = Math.min(65_535, repeatedSelectedResponses + 1);
        if (request === selectedRequest && response !== selectedResponse) fail("identity_request_ambiguous");
        return;
      }
      // Synchronous provisional Request metadata only. Inspect no other header
      // value and never issue a complete-header protocol request before JSON.
      // Missing purpose is non-target; present malformed/inherited metadata is
      // terminal, including a marked wrong-path response. No unmarked fallback.
      const headers = request.headers();
      if (!headers || typeof headers !== "object" || Array.isArray(headers)) {
        fail("identity_response_metadata"); return;
      }
      const purpose = Object.getOwnPropertyDescriptor(headers, "x-adtr-identity-purpose");
      if (!purpose) {
        if ("x-adtr-identity-purpose" in headers) fail("identity_response_metadata");
        return;
      }
      if (!("value" in purpose) || purpose.value !== "app-passive-session") {
        fail("identity_response_metadata"); return;
      }
      // The first declared App-purpose candidate must pass all metadata before
      // the original status rule. Bad metadata cannot select a later good body.
      const canonical = origin + "/api/auth/me";
      if (response.url() !== canonical || request.url() !== canonical || request.method() !== "GET" ||
          request.resourceType() !== "fetch" || request.isNavigationRequest() || request.frame() !== frame ||
          frame?.page() !== page || request.redirectedFrom() !== null || request.redirectedTo() !== null ||
          request.serviceWorker() !== null || response.fromServiceWorker()) {
        fail("identity_response_metadata"); return;
      }
      if (response.status() !== 200) return;
      if (!fresh.has(request)) { fail("identity_request_ambiguous"); return; }
      if (!live()) return;
      selectedRequest = request; selectedResponse = response; state.phase = "SELECTED"; pending++;
      selectedStatus = 200; // Already verified above; no extra status/URL read.
      try { selectedObservation = requestObservations.get(request); } catch { diagnosticFault = true; }
      note("selected");
      try {
        // Synchronous call in this response event, exactly once on this object.
        // Playwright owns its original body implementation and any internal fallback.
        note("json_call");
        void response.json().then(value => {
          note("json_resolved");
          pending--;
          if (live()) {
            try {
              const ownID = value && typeof value === "object" && !Array.isArray(value)
                ? Object.getOwnPropertyDescriptor(value, "ID")?.value : undefined;
              if (typeof ownID !== "number" || !Number.isSafeInteger(ownID) || ownID <= 0) fail("identity_invalid_id");
              else id = ownID;
            } catch { fail("identity_invalid_id"); }
          }
          finish();
        }, reason => { note("json_rejected"); pending--; failJSON(reason); finish(); });
      } catch (reason) { note("json_rejected"); pending--; failJSON(reason); }
    } catch { fail("identity_response_metadata"); }
  };
  const changed = () => { if (live()) fail("identity_page_changed"); };
  const navigated = (current: Frame) => { if (current === frame) changed(); };
  const install = (add: () => void, remove: () => void) => { add(); removers.push(remove); };
  try {
    try {
      timer = setTimeout(expire, Math.max(0, deadline - Date.now()));
      frame = page.mainFrame(); origin = new URL(page.url()).origin;
      install(() => { page.on("request", requestSeen); }, () => { page.off("request", requestSeen); });
      install(() => { page.on("response", responseSeen); }, () => { page.off("response", responseSeen); });
      install(() => { page.on("close", changed); }, () => { page.off("close", changed); });
      install(() => { page.on("crash", changed); }, () => { page.off("crash", changed); });
      install(() => { page.on("framenavigated", navigated); }, () => { page.off("framenavigated", navigated); });
      installDiagnostic(() => { page.on("requestfinished", requestFinished); }, () => { page.off("requestfinished", requestFinished); });
      installDiagnostic(() => { page.on("requestfailed", requestFailed); }, () => { page.off("requestfailed", requestFailed); });
      if (page.isClosed()) changed();
      if (live()) {
        pending++;
        try {
          // Metadata inspection only. The public API retains at most 100 Requests.
          void page.requests().then(requests => {
            pending--;
            if (!live()) { finish(); return; }
            try {
              for (const request of requests) {
                if (!live()) break;
                if (new URL(request.url()).pathname === "/api/auth/me") record(request, true);
              }
              if (!live()) { finish(); return; }
              // Arm and invoke the native action in the same synchronous segment.
              state.phase = "ARMED"; pending++; note("armed");
              try {
                note("focus_call");
                void page.bringToFront().then(() => {
                  if (active) focusSettled = true; note("focus_resolved");
                  pending--;
                  if (live()) focusDone = true;
                  finish();
                }, () => { if (active) focusSettled = true; note("focus_rejected"); pending--; fail("identity_focus_failed"); finish(); });
              } catch { if (active) focusSettled = true; note("focus_rejected"); pending--; fail("identity_focus_failed"); }
            } catch { fail("identity_prepare_failed"); }
            finish();
          }, () => { pending--; fail("identity_prepare_failed"); finish(); });
        } catch { pending--; fail("identity_prepare_failed"); }
      }
    } catch { fail("identity_prepare_failed"); }
    await completion;
  } finally {
    // Pending original protocol commands are quarantined, not cancelled/joined.
    // The existing Page/context fixture owns their eventual normal disposal.
    note("cleanup");
    active = false;
    for (const remove of removers) { try { remove(); } catch { cleanupFailed = true; } }
    removers.length = 0;
    for (const remove of diagnosticRemovers) { try { remove(); } catch { diagnosticFault = true; } }
    diagnosticRemovers.length = 0;
    if (timer !== undefined) clearTimeout(timer);
    old.clear(); fresh.clear(); selectedRequest = undefined; selectedResponse = undefined; frame = undefined;
    requestObservations.clear();
    // One bounded line after cleanup; logging failure cannot replace the outcome.
    // Missing host events remain not_observed, never inferred success/abort.
    try {
      console.log("[v4-focus-identity] " + JSON.stringify({
        schema: "v1", phase: state.phase, failure: state.failure ?? null, jsonFailureClass,
        events, eventsDropped, diagnosticFault, cleanupFailed,
        selected: selectedStatus === 200, selectedStatus,
        selectedRequest: selectedObservation ?? null, focusSettledWhenJSONRejected,
        laterMatchingResponses, repeatedSelectedResponses, observedRequests,
        counterWindow: "selected_until_first_terminal_state",
        requestTerminalWindow: "observed_request_until_first_terminal_state", eventWindow: "prepare_until_cleanup",
      }));
    } catch { /* Diagnostics cannot change the original success/failure. */ }
  }
  if (state.failure || cleanupFailed) throw Object.assign(new Error(state.failure === "identity_response_json_failed" ? identityJSONFailureMessage(jsonFailureClass) : state.failure ?? "identity_cleanup_failed"),
    cleanupFailed ? { cleanupFailure: "identity_cleanup_failed" } : {});
  return id!;
}
export type Original = { domainId: string; expectedRevision: string; expectedCredentialGeneration: string; idempotencyKey: string; taskUUID: string };
export type Actor = { username: string; password: string; auth: Authenticator; actorId: number };
export type Mutation = { operation: "sync" | "cancel"; at: number; currentProof: boolean; original: Partial<Original> };
export const evidenceDigest = (values: unknown[]) => createHash("sha256").update(JSON.stringify(values)).digest("hex");
export function originalEvidence(original: Original, actorId: number) {
  return { taskSHA256: evidenceDigest([original.taskUUID]), domainSHA256: evidenceDigest([original.domainId]),
    actorSHA256: evidenceDigest([String(actorId)]), sourceSHA256: evidenceDigest([original.domainId, original.expectedRevision, original.expectedCredentialGeneration]),
    keySHA256: evidenceDigest([original.idempotencyKey]) };
}
export function mutationEvidence(entries: Mutation[]) {
  return entries.map(entry => ({
    operation: entry.operation, dispatchMilliseconds: entry.at, counterOffsetMilliseconds: entry.at % 30_000, currentProof: entry.currentProof,
    domainSHA256: entry.original.domainId ? evidenceDigest([entry.original.domainId]) : null,
    sourceSHA256: entry.operation === "sync" ? evidenceDigest([entry.original.domainId, entry.original.expectedRevision, entry.original.expectedCredentialGeneration]) : null,
    keySHA256: entry.original.idempotencyKey ? evidenceDigest([entry.original.idempotencyKey]) : null,
    taskSHA256: entry.original.taskUUID ? evidenceDigest([entry.original.taskUUID]) : null,
  }));
}
export function guardLeaks(context: BrowserContext, secrets: string[]) {
  let leaked = false;
  const pages = new Set<Page>();
  const check = (text: string) => { if (secrets.some(secret => secret && text.includes(secret))) leaked = true; };
  const consoleMessage = (message: import("@playwright/test").ConsoleMessage) => check(message.text());
  const pageError = (error: Error) => check(error.message);
  const attach = (page: Page) => { pages.add(page); page.on("console", consoleMessage); page.on("pageerror", pageError); };
  const request = (r: import("@playwright/test").Request) => {
    if (r.method() !== "POST") return;
    try { const body = r.postDataJSON(); for (const key of ["actorPassword", "totpCode"]) if (typeof body?.[key] === "string" && body[key]) secrets.push(body[key]); } catch { /* Non-JSON transport has no decoded proof. */ }
  };
  context.pages().forEach(attach); context.on("page", attach); context.on("request", request);
  return {
    assertClean: () => invariant(!leaked, "Proof appeared in browser console/error output"),
    close: () => { context.off("page", attach); context.off("request", request); for (const page of pages) { page.off("console", consoleMessage); page.off("pageerror", pageError); } },
  };
}
// Never expose request bodies, secret strings, raw exceptions or arbitrary response objects.
export function observeMutations(context: BrowserContext, actor: Actor) {
  const entries: Mutation[] = [];
  const listener = (request: import("@playwright/test").Request) => {
    const path = new URL(request.url()).pathname;
    if (request.method() !== "POST" || ![V4 + "sync", V4 + "cancel"].includes(path)) return;
    try {
      const body = request.postDataJSON(), at = Date.now();
      const operation = path.endsWith("sync") ? "sync" : "cancel";
      entries.push({ operation, at, currentProof: body.totpCode === totp(actor.auth.secret, at), original: operation === "sync" ? {
        domainId: body.domainId, expectedRevision: body.expectedRevision, expectedCredentialGeneration: body.expectedCredentialGeneration, idempotencyKey: body.idempotencyKey,
      } : { taskUUID: body.taskUUID } });
    } catch { throw new Error("Invalid actual UI mutation request"); }
  };
  context.on("request", listener);
  return { entries, counts: () => ({ sync: entries.filter(e => e.operation === "sync").length, cancel: entries.filter(e => e.operation === "cancel").length }), close: () => context.off("request", listener) };
}
export async function actualJSON(response: Response | APIResponse, actorId: number): Promise<Record<string, unknown>> {
  invariant(response.status() === 200, "Expected genuine successful API response");
  const headers = response.headers();
  invariant(headers["cache-control"] === "no-store" && headers["x-adtr-user-id"] === String(actorId) && headers["x-content-type-options"] === "nosniff", "Real response metadata did not match actor/no-store contract");
  v4Phase("body-start");
  // Only a closed transport/media-type enum, never actual headers or URLs.
  recordV4BodyClass("request" in response, headers["content-type"]);
  // Browser reads use the exact original native reader's completed/released
  // historical bytes, not the disposable CDP body copy. This is a JSON
  // cross-check, not strict native-finish or live-delivery proof. Node
  // APIResponse (including route.fetch) keeps its existing direct path.
  const value = "request" in response
    ? await bounded(directoryV4ControlsResponseJSON(response), 15_000, "Original native browser body observation deadline exceeded")
    : await readV4ObservedObject(() => response.body());
  invariant(value && typeof value === "object" && !Array.isArray(value), "Real response must be an object");
  v4Phase("body-complete");
  return value as Record<string, unknown>;
}
export async function taskResponse(response: Response | APIResponse, actor: Actor, domainId: string, operation: "sync" | "cancel" | "read", taskUUID?: string) {
  const value = await actualJSON(response, actor.actorId);
  invariant(Object.keys(value).sort().join(",") === (operation === "sync" ? "replayed,task" : "task"), "Unexpected real task envelope");
  if (operation === "sync") invariant(value.replayed === false, "Expected a new actual UI admission");
  try { return parseDirectoryV4Task(value.task, domainId, taskUUID); }
  catch { throw new Error("Real task failed exact v4 identity/grammar checks"); }
}
export async function openCollection(page: Page) {
  v4Phase("workspace-open");
  await page.goto("/#directory-v4");
  await expect(page.getByRole("heading", { name: "字典 4 目录采集", exact: true })).toBeVisible();
  v4Phase("workspace-ready");
}
export async function prepareSync(page: Page) {
  v4Phase("source-select");
  await page.getByRole("region", { name: "字典 4 已保存来源", exact: true }).getByRole("button", { name: /^synthetic\.invalid · 配置 /u }).click();
  v4Phase("sync-prepare");
  await page.getByRole("button", { name: "准备采集字典 4", exact: true }).click();
}
export function form(page: Page, operation: "sync" | "cancel") {
  return page.getByRole("form", { name: operation === "sync" ? "字典 4 采集证明" : "原任务取消证明", exact: true });
}
export async function enterProof(page: Page, actor: Actor, operation: "sync" | "cancel", code?: string) {
  v4Phase("proof-start");
  await expect(form(page, operation).locator('input[type="password"], input[inputmode="numeric"]')).toHaveCount(2);
  await fillSecret(form(page, operation).getByLabel("当前用户密码", { exact: true }), actor.password);
  await fillSecret(form(page, operation).getByLabel("当前六位验证码", { exact: true }), code ?? await freshCode(actor.auth));
  invariant(await form(page, operation).locator('input[type="password"], input[inputmode="numeric"]').evaluateAll(nodes => nodes.length === 2 && nodes.every(node => (node as HTMLInputElement).value.length > 0)), "Actual proof inputs were not populated");
  v4Phase("proof-ready");
}
export async function clickMutation(page: Page, actor: Actor, domainId: string, operation: "sync" | "cancel", taskUUID?: string) {
  v4Phase("mutation-click");
  const pending = page.waitForResponse(r => new URL(r.url()).pathname === V4 + operation && r.request().method() === "POST");
  void pending.catch(() => undefined);
  await form(page, operation).getByRole("button", { name: operation === "sync" ? "确认提交采集" : "确认取消原任务", exact: true }).click();
  const response = await pending;
  v4Phase("mutation-response");
  return taskResponse(response, actor, domainId, operation, taskUUID);
}
export function originalFrom(task: Task, mutation: Mutation): Original {
  const v = mutation.original;
  invariant(mutation.operation === "sync" && typeof v.domainId === "string" && typeof v.expectedRevision === "string" && typeof v.expectedCredentialGeneration === "string" && typeof v.idempotencyKey === "string", "Missing actual admitted source pins");
  return { domainId: v.domainId, expectedRevision: v.expectedRevision, expectedCredentialGeneration: v.expectedCredentialGeneration, idempotencyKey: v.idempotencyKey, taskUUID: task.taskUUID };
}
export async function readTask(context: BrowserContext, actor: Actor, original: Original) {
  return taskResponse(await context.request.get(V4 + "task", { params: { taskUUID: original.taskUUID } }), actor, original.domainId, "read", original.taskUUID);
}
export async function terminal(context: BrowserContext, actor: Actor, original: Original, state: "succeeded" | "cancelled") {
  let result: Task | undefined;
  await expect.poll(async () => {
    result = await readTask(context, actor, original);
    invariant(!["failed", "dead_letter", "partial_failed", state === "succeeded" ? "cancelled" : "succeeded"].includes(result.state), "Actual task reached wrong terminal state");
    return result.state;
  }, { timeout: 60_000, intervals: [100, 250, 500], message: "Original real task must reach required terminal state" }).toBe(state);
  return result!;
}
let recoveryMarkers = 0;
export function v4CancelUnknownUIReady(root: Element): boolean {
  const notice = "结果尚未确认或响应未通过校验。请查询原操作；不会自动重新采集或取消。 请点击“重新核验并恢复”查询原回执。";
  const statuses = Array.from(root.querySelectorAll('[role="status"]'));
  if (!statuses.some(node => node.textContent === notice)) return false;
  const buttons = Array.from(root.querySelectorAll("button"));
  const recover = buttons.filter(node => node.textContent?.trim() === "重新核验并恢复");
  if (recover.length !== 1 || recover[0].disabled) return false;
  if (root.querySelector('form, input[type="password"], input[inputmode="numeric"], input[autocomplete="one-time-code"]')) return false;
  if (root.querySelector('[aria-label="原操作恢复"], [aria-label="原任务状态"], [aria-label="本任务的原始组成员结果"], [aria-label="组原始成员详情"], [aria-label="字典 4 已保存来源"]')) return false;
  const forbidden = ["准备采集字典 4", "申请取消原任务", "确认提交采集", "确认取消原任务", "使用原键和原版本显式重试", "查看本任务的原始组成员结果", "确认结束原操作并清除恢复记录"];
  return buttons.every(node => !forbidden.includes(node.textContent?.trim() ?? ""));
}
export async function waitForV4CancelUnknownUI(page: Page, counts: () => { sync: number; cancel: number }, expected: { sync: number; cancel: number }) {
  const workspace = page.getByRole("region", { name: "字典 4 采集工作区", exact: true });
  await expect.poll(async () => {
    const current = counts();
    return current.sync === expected.sync && current.cancel === expected.cancel && await workspace.isVisible() &&
      await workspace.getByRole("status").filter({ hasText: "结果尚未确认或响应未通过校验。" }).isVisible() &&
      await workspace.getByRole("button", { name: "重新核验并恢复", exact: true }).isVisible() && await workspace.evaluate(v4CancelUnknownUIReady);
  }, { timeout: 5000, message: "Committed cancel loss did not render query-only unknown UI" }).toBe(true);
  const after = counts();
  invariant(after.sync === expected.sync && after.cancel === expected.cancel, "Unknown cancel UI changed business POST count");
}
function recoveryPhase(phase: "start" | "main-frame-navigated" | "dom-content-loaded" | "load" | "reload-returned" | "receipt-response" | "receipt-not-observed" | "navigation-request" | "navigation-response-2xx" | "navigation-response-3xx" | "navigation-response-4xx" | "navigation-response-5xx" | "navigation-response-other" | "navigation-finished" | "navigation-failed" | "page-closed" | "page-crashed") {
  if (recoveryMarkers++ < 64) console.log("[v4-recovery] " + phase);
}
export async function recover(page: Page, actor: Actor, original: Original) {
  const receipt = page.waitForResponse(r => new URL(r.url()).pathname === V4 + "receipt" && r.request().method() === "GET");
  void receipt.catch(() => undefined);
  void receipt.then(() => recoveryPhase("receipt-response"), () => recoveryPhase("receipt-not-observed"));
  const commit = (frame: import("@playwright/test").Frame) => { if (frame === page.mainFrame()) recoveryPhase("main-frame-navigated"); };
  const dom = () => recoveryPhase("dom-content-loaded"), load = () => recoveryPhase("load");
  const navigation = new Set<import("@playwright/test").Request>();
  const request = (value: import("@playwright/test").Request) => {
    if (navigation.size < 32 && value.isNavigationRequest() && value.resourceType() === "document" && value.frame() === page.mainFrame()) {
      navigation.add(value); recoveryPhase("navigation-request");
    }
  };
  const navigationResponse = (value: Response) => {
    if (!navigation.has(value.request())) return;
    const status = value.status();
    recoveryPhase(status >= 200 && status < 300 ? "navigation-response-2xx" : status >= 300 && status < 400 ? "navigation-response-3xx" : status >= 400 && status < 500 ? "navigation-response-4xx" : status >= 500 && status < 600 ? "navigation-response-5xx" : "navigation-response-other");
  };
  const finished = (value: import("@playwright/test").Request) => { if (navigation.has(value)) recoveryPhase("navigation-finished"); };
  const failed = (value: import("@playwright/test").Request) => { if (navigation.has(value)) recoveryPhase("navigation-failed"); };
  const closed = () => recoveryPhase("page-closed"), crashed = () => recoveryPhase("page-crashed");
  page.on("framenavigated", commit); page.on("domcontentloaded", dom); page.on("load", load);
  page.on("request", request); page.on("response", navigationResponse); page.on("requestfinished", finished); page.on("requestfailed", failed); page.on("close", closed); page.on("crash", crashed);
  try {
    recoveryPhase("start");
    await page.reload(); // Preserve existing load readiness and 15-second bound.
    recoveryPhase("reload-returned");
  } finally {
    page.off("framenavigated", commit); page.off("domcontentloaded", dom); page.off("load", load);
    page.off("request", request); page.off("response", navigationResponse); page.off("requestfinished", finished); page.off("requestfailed", failed); page.off("close", closed); page.off("crash", crashed);
    navigation.clear();
  }
  const response = await receipt, query = new URL(response.url()).searchParams;
  invariant(query.get("domainId") === original.domainId && query.get("idempotencyKey") === original.idempotencyKey, "Recovery did not query exact original receipt");
  await taskResponse(response, actor, original.domainId, "read", original.taskUUID);
  await expect(page.getByRole("region", { name: "原任务状态", exact: true })).toContainText(original.taskUUID);
  await proofEmpty(page);
  return { status: response.status(), taskSHA256: evidenceDigest([original.taskUUID]),
    keySHA256: evidenceDigest([query.get("idempotencyKey")]), domainSHA256: evidenceDigest([query.get("domainId")]) };
}
export async function refreshOriginal(page: Page) {
  await page.getByRole("button", { name: "查询原操作回执与状态", exact: true }).click();
}
export async function acknowledge(page: Page) {
  await page.getByRole("button", { name: "确认结束原操作并清除恢复记录", exact: true }).click();
  await expect(page.getByRole("region", { name: "原操作恢复", exact: true })).toHaveCount(0);
}
export async function proofEmpty(page: Page) {
  const fields = page.locator('.directory-v4-workspace input[type="password"], .directory-v4-workspace input[inputmode="numeric"]');
  invariant(await fields.evaluateAll(nodes => nodes.every(node => (node as HTMLInputElement).value === "")), "Proof not empty at safe boundary");
}
// Real layout/scroll only: never alter document content or CSS for evidence.
export function v4ScreenshotTargetStatus(node: Element): "visible" | "header_missing" | "style_hidden" | "target_bounds" | "text_missing" | "text_limit" | "text_bounds" | "text_occluded" {
  const header = document.querySelector(".soc-topbar");
  if (!header) return "header_missing";
  const box = node.getBoundingClientRect(), bottom = Math.max(0, header.getBoundingClientRect().bottom);
  const style = getComputedStyle(node);
  if (style.visibility !== "visible" || style.display === "none" || style.opacity === "0") return "style_hidden";
  if (box.width <= 0 || box.height <= 0 || box.top < bottom + 4 || box.left < 0 || box.right > innerWidth || box.bottom > innerHeight) return "target_bounds";
  // Probe actual text line interiors, not transparent rounded control corners.
  const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT);
  let current: Node | null, nodes = 0, lines = 0;
  while ((current = walker.nextNode())) {
    if (!current.textContent?.trim()) continue;
    if (++nodes > 128) return "text_limit";
    const parent = current.parentElement;
    if (!parent) return "text_missing";
    for (let ancestor: Element | null = parent; ancestor; ancestor = ancestor.parentElement) {
      const textStyle = getComputedStyle(ancestor);
      if (textStyle.visibility !== "visible" || textStyle.display === "none" || textStyle.opacity === "0") return "style_hidden";
    }
    const range = document.createRange(); range.selectNodeContents(current);
    const rectList = range.getClientRects();
    if (rectList.length > 256 - lines) return "text_limit";
    const rects = Array.from(rectList);
    if (!rects.length) return "text_missing";
    for (const rect of rects) {
      if (++lines > 256) return "text_limit";
      if (rect.width <= 0 || rect.height <= 0 || rect.top < bottom + 4 || rect.left < 0 || rect.right > innerWidth || rect.bottom > innerHeight) return "text_bounds";
      const dx = Math.min(2, rect.width / 4), dy = Math.min(2, rect.height / 4);
      const clear = [[rect.left + dx, rect.top + dy], [rect.right - dx, rect.top + dy],
        [rect.left + dx, rect.bottom - dy], [rect.right - dx, rect.bottom - dy],
        [rect.left + rect.width / 2, rect.top + rect.height / 2]].every(([x, y]) => {
        const hit = document.elementFromPoint(x, y);
        return hit !== null && (hit === parent || parent.contains(hit));
      });
      if (!clear) return "text_occluded";
    }
  }
  return lines ? "visible" : "text_missing";
}
export function v4ScreenshotTargetVisible(node: Element): boolean { return v4ScreenshotTargetStatus(node) === "visible"; }
let framingMarkers = 0;
export async function frameV4Screenshot(anchor: Locator, required: Locator[]) {
  await bounded(anchor.evaluate(node => {
    node.scrollIntoView({ block: "start", behavior: "instant" });
    const header = document.querySelector(".soc-topbar");
    if (!header) throw new Error("Screenshot shell header unavailable");
    const top = node.getBoundingClientRect().top - Math.max(0, header.getBoundingClientRect().bottom) - 8;
    let scroller = node.parentElement;
    while (scroller && !(scroller.scrollHeight > scroller.clientHeight && /^(auto|scroll|overlay)$/u.test(getComputedStyle(scroller).overflowY))) scroller = scroller.parentElement;
    if (scroller) scroller.scrollBy({ top, behavior: "instant" });
    else window.scrollBy({ top, behavior: "instant" });
  }), 5000, "Screenshot target positioning deadline exceeded");
  for (const [ordinal, target] of required.entries()) {
    let status: ReturnType<typeof v4ScreenshotTargetStatus> | "pending" = "pending";
    try {
      await expect.poll(async () => { status = await target.evaluate(v4ScreenshotTargetStatus); return status; }, { timeout: 5000, message: "Screenshot target is clipped or occluded" }).toBe("visible");
    } catch (error) {
      const viewport = anchor.page().viewportSize();
      const allowed = ["pending", "visible", "header_missing", "style_hidden", "target_bounds", "text_missing", "text_limit", "text_bounds", "text_occluded"];
      if (framingMarkers++ < 32 && ordinal < 128 && viewport && [1440, 390].includes(viewport.width) && [900, 844].includes(viewport.height) && allowed.includes(status)) {
        console.log(`[v4-capture] target=${ordinal} width=${viewport.width} height=${viewport.height} reason=${status}`);
      }
      throw error;
    }
  }
}
export async function safeScreenshots(page: Page, info: TestInfo, stem: string, secrets: readonly string[], prepareView?: () => Promise<void>) {
  invariant(/^[a-z0-9-]+$/u.test(stem), "Unsafe screenshot name");
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await prepareView?.();
    await proofEmpty(page);
    invariant(await page.locator('input[type="password"], input[autocomplete="one-time-code"], input[inputmode="numeric"]').evaluateAll(nodes => nodes.every(node => (node as HTMLInputElement).value === "")), "Unsafe screenshot proof field");
    const safety = await page.evaluate(known => {
      const contains = (value: string) => known.some(secret => secret && value.includes(secret));
      const bodyClean = !contains(document.body.textContent ?? "");
      try {
        let storageClean = true;
        for (let index = 0; index < sessionStorage.length; index++) {
          const key = sessionStorage.key(index);
          if (key !== null && (contains(key) || contains(sessionStorage.getItem(key) ?? ""))) storageClean = false;
        }
        return { bodyClean, storageClean, storageAccessible: true };
      } catch { return { bodyClean, storageClean: false, storageAccessible: false }; }
    }, [...secrets]);
    invariant(safety.bodyClean && safety.storageClean && safety.storageAccessible, "Screenshot body/storage proof safety not established");
    await page.screenshot({ path: info.outputPath(`${stem}-${width}.png`), fullPage: false });
  }
}
// The response is fetched exactly once from the server, validated, then withheld.
// Never fabricate a successful body and never repeat a POST after uncertain delivery.
export async function committedLoss(page: Page, actor: Actor, domainId: string, operation: "sync" | "cancel", expectedState?: Task["state"], taskUUID?: string) {
  let resolve!: (task: Task) => void, reject!: (error: Error) => void;
  const completed = new Promise<Task>((yes, no) => { resolve = yes; reject = no; });
  void completed.catch(() => undefined);
  await page.route("**" + V4 + operation, async route => {
    try {
      invariant(route.request().method() === "POST", "Loss route received unexpected method");
      const response = await route.fetch({ maxRetries: 0, maxRedirects: 0, timeout: 15_000 });
      const task = await taskResponse(response, actor, domainId, operation, taskUUID);
      if (expectedState) invariant(task.state === expectedState, "Real committed cancellation missed required state/window");
      await route.abort("failed"); resolve(task);
    } catch { await route.abort("failed").catch(() => undefined); reject(new Error("Genuine committed-response loss validation failed")); }
  }, { times: 1 });
  const boundedCompletion = bounded(completed, 20_000, "Committed-response loss did not finish");
  void boundedCompletion.catch(() => undefined);
  return { completed: boundedCompletion };
}
export async function pinnedResults(page: Page, actor: Actor, original: Original, capture?: { info: TestInfo; secrets: readonly string[] }) {
  const pending = page.waitForResponse(r => new URL(r.url()).pathname === V4 + "observation");
  void pending.catch(() => undefined);
  await page.getByRole("button", { name: "查看本任务的原始组成员结果", exact: true }).click();
  const response = await pending, params = new URL(response.url()).searchParams;
  invariant(params.get("observationId") === original.taskUUID && params.get("domainId") === original.domainId && params.get("kind") === "group", "Original result read lost original pins");
  const value = parseGroupMembersV4Page(await actualJSON(response, actor.actorId), { domainId: original.domainId, observationId: original.taskUUID, pageIdx: 1, pageSize: 25 });
  invariant(value.available && value.observationId === original.taskUUID && value.list.length > 0, "Expected real nonempty v4 group observation");
  const semantics = { absent: false, empty: false, values: false };
  let detailCaptured = false;
  for (const row of value.list) {
    const next = page.waitForResponse(r => new URL(r.url()).pathname === "/api/group-assets/v4/members");
    void next.catch(() => undefined);
    await page.getByRole("region", { name: "本任务的原始组成员结果", exact: true }).getByRole("button", { name: directoryV4DisplayMember(row.samAccountName ?? row.distinguishedName), exact: true }).click();
    const detailResponse = await next, q = new URL(detailResponse.url()).searchParams;
    invariant(q.get("observationId") === original.taskUUID && q.get("domainId") === original.domainId && q.get("expectedRevision") === original.expectedRevision && q.get("expectedCredentialRevision") === original.expectedCredentialGeneration && q.get("objectGUID") === row.objectGUID, "Raw detail lost original five pins");
    const detail = parseGroupMembersV4(await actualJSON(detailResponse, actor.actorId), { domainId: original.domainId, expectedRevision: original.expectedRevision, expectedCredentialRevision: original.expectedCredentialGeneration, observationId: original.taskUUID, objectGUID: row.objectGUID });
    invariant(JSON.stringify(detail.object) === JSON.stringify(row), "Raw detail differs from the same original page object");
    const region = page.getByRole("region", { name: "组原始成员详情", exact: true });
    if (row.rawMember === null) { semantics.absent = true; await expect(region).toContainText("无法区分属性不存在与 ACL 隐藏"); }
    else if (!row.rawMember.length) { semantics.empty = true; await expect(region).toContainText("不证明该组为空"); }
    else {
      semantics.values = true;
      const expected = row.rawMember.map(directoryV4DisplayMember);
      await waitForV4RawMembers(() => region.locator("ol > li").allTextContents(), expected);
      if (capture && !detailCaptured) {
        await safeScreenshots(page, capture.info, "original-result-values", capture.secrets, async () => {
          await expect(region.getByRole("heading", { name: directoryV4DisplayMember(row.distinguishedName), exact: true })).toBeVisible();
          await waitForV4RawMembers(() => region.locator("ol > li").allTextContents(), expected);
          await frameV4Screenshot(region, [region.getByRole("heading"), ...await region.locator("ol > li").all()]);
        });
        detailCaptured = true;
      }
    }
  }
  invariant(Object.values(semantics).every(Boolean), "Real fixture did not cover absent/empty/value raw semantics");
  for (const size of [50, 25]) {
    const next = page.waitForResponse(r => new URL(r.url()).pathname === V4 + "observation");
    void next.catch(() => undefined);
    await page.getByRole("region", { name: "本任务的原始组成员结果", exact: true }).getByLabel("每页组数", { exact: true }).selectOption(String(size));
    const resized = await next, q = new URL(resized.url()).searchParams;
    invariant(q.get("observationId") === original.taskUUID && q.get("domainId") === original.domainId && q.get("kind") === "group", "Page-size change fell back from original pins");
    parseGroupMembersV4Page(await actualJSON(resized, actor.actorId), { domainId: original.domainId, observationId: original.taskUUID, pageIdx: 1, pageSize: size });
  }
  const refreshed = page.waitForResponse(r => new URL(r.url()).pathname === V4 + "observation");
  void refreshed.catch(() => undefined);
  await page.getByRole("button", { name: "刷新原始结果", exact: true }).click();
  const reread = await refreshed;
  invariant(new URL(reread.url()).searchParams.get("observationId") === original.taskUUID, "Refresh fell back from original observation");
  parseGroupMembersV4Page(await actualJSON(reread, actor.actorId), { domainId: original.domainId, observationId: original.taskUUID, pageIdx: 1, pageSize: 25 });
  await expect(page.getByRole("region", { name: "本任务的原始组成员结果", exact: true }).getByText(/本观测共有/u)).toBeVisible();
  if (capture) {
    invariant(detailCaptured, "Original raw detail capture was not established");
    const region = page.getByRole("region", { name: "本任务的原始组成员结果", exact: true });
    await safeScreenshots(page, capture.info, "original-raw-members", capture.secrets, async () => {
      const pin = region.getByText(`固定原始观测：${directoryV4DisplayMember(original.taskUUID)}。不会读取最新观测；不是 AD 全域原子快照。`, { exact: true });
      await expect(pin).toBeVisible();
      await expect(region.locator("ul > li")).toHaveCount(value.list.length);
      await frameV4Screenshot(region, [pin, ...value.list.slice(0, 2).map(row =>
        region.getByRole("button", { name: directoryV4DisplayMember(row.samAccountName ?? row.distinguishedName), exact: true }))]);
    });
  }
  return { ...semantics, observationSHA256: evidenceDigest([value.observationId]), detailedGroups: value.list.length };
}

export type WorkerControlState = { state: "not_started" | "starting" | "ready" | "failed" | "closing" | "closed"; launchCount: number; failure: null | string };
export function parseWorkerControlState(text: string): WorkerControlState {
  try {
    invariant(Buffer.byteLength(text) <= 2048, "Controller body limit");
    const v = parseDirectoryV4UniqueJSON(text) as WorkerControlState;
    invariant(v && Object.keys(v).sort().join(",") === "failure,launchCount,state" &&
      ["not_started", "starting", "ready", "failed", "closing", "closed"].includes(v.state) &&
      [0, 1].includes(v.launchCount) &&
      (v.failure === null || ["listener_failed", "startup_thread_failed", "launch_failed", "readiness_failed", "cleanup_failed"].includes(v.failure)) &&
      (v.state !== "not_started" || v.launchCount === 0 && v.failure === null) &&
      (v.state !== "ready" || v.launchCount === 1 && v.failure === null) &&
      (v.state !== "failed" || v.failure !== null), "Controller shape failure");
    return v;
  } catch { throw new Error("Invalid owned Worker controller state"); }
}
// Node-only controller: no page API, browser context, logging or URL in error paths.
export function workerControl() {
  const endpoint = process.env.ADTR_E2E_V4_WORKER_CONTROL_URL ?? "", token = process.env.ADTR_E2E_V4_WORKER_CONTROL_TOKEN ?? "";
  let base: URL;
  try { base = new URL(endpoint); } catch { throw new Error("Missing private owned Worker controller"); }
  invariant(base.protocol === "http:" && base.hostname === "127.0.0.1" && !!base.port && base.pathname === "/" && !base.search && !base.hash && !base.username && !base.password && /^[A-Za-z0-9_-]{43}$/u.test(token), "Invalid private owned Worker controller");
  let started = false;
  const call = (method: "GET" | "POST", path: "/state" | "/start") => new Promise<WorkerControlState>((resolve, reject) => {
    const fail = () => reject(new Error("Owned Worker controller request failed"));
    const req = nodeRequest(new URL(path, base), { method, headers: { Authorization: `Bearer ${token}`, "Content-Length": "0" }, timeout: 3000 }, response => {
      let body = "";
      response.setEncoding("utf8");
      response.on("data", chunk => { body += chunk; if (Buffer.byteLength(body) > 2048) { response.destroy(); fail(); } });
      response.on("error", fail);
      response.on("end", () => {
        try {
          invariant(response.statusCode === 200, "Controller HTTP failure");
          resolve(parseWorkerControlState(body));
        } catch { fail(); }
      });
    });
    req.on("error", fail); req.on("timeout", () => { req.destroy(); fail(); }); req.end();
  });
  return {
    state: () => call("GET", "/state"),
    async startOnce() {
      invariant(!started, "Repeated Worker start forbidden"); started = true;
      // Uncertain POST is reconciled solely by GET; never resubmit.
      await call("POST", "/start").catch(() => undefined);
      await expect.poll(async () => { const v = await call("GET", "/state"); invariant(v.failure === null && !["failed", "closing", "closed"].includes(v.state), "Owned Worker startup failed"); if (v.state === "ready") invariant(v.launchCount === 1, "Worker launch cardinality failed"); return v.state; }, { timeout: 15_000, intervals: [100] }).toBe("ready");
    },
  };
}
export async function writeEvidence(info: TestInfo, suite: "controls" | "cancel" | "readers", checks: Record<string, boolean | number | string>, witnesses: Record<string, unknown>) {
  // Final owner must register strict per-suite schemas, source provenance and cleanup
  // evidence. This file alone cannot certify a run or bypass its validator.
  invariant(Object.values(checks).every(value => typeof value === "boolean" || typeof value === "number" && Number.isSafeInteger(value) && value >= 0 || typeof value === "string" && /^[a-z_]+$/u.test(value)), "Unsafe evidence field");
  await writeV4ScenarioEvidence(info, suite, checks, witnesses);
}
