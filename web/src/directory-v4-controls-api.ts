import { ApiError, type Profile } from "./api";
import type { Proof } from "./access-api";
import { connectionStates, validDomainID } from "./domain-api";
import { parseSourcePage, parseResolvedSource, validSourceChoice, validSourceKeyword,
  type SourceChoice, type SourceQuery } from "./domain-selection-api";
import { parseGroupMembersV4Page, type GroupMembersV4PageQuery } from "./group-members-v4-read";
import { parseGroupMembersV4, validGroupMembersV4Query, type GroupMembersV4Query } from "./group-members-v4-api";
import { integer, object, validTask, type Task } from "./task-api";
import { readDirectoryV4ControlsJSON, parseDirectoryV4UniqueJSON } from "./directory-v4-controls-json";
import {  directoryV4Input, directoryV4IntentPrefix, directoryV4OwnerKey, parseDirectoryV4Intent, sameDirectoryV4Input, validDirectoryV4Input,
  type DirectoryV4Input, type DirectoryV4Intent, type DirectoryV4Owner, type DirectoryV4IntentRead, type DirectoryV4Storage } from "./directory-v4-intent";
export type { DirectoryV4Input, DirectoryV4Intent } from "./directory-v4-intent";
export { validDirectoryV4Input } from "./directory-v4-intent";
export type DirectoryV4Scope = "source" | "receipt" | "task" | "sync" | "cancel" | "page" | "detail";
const directoryV4ScopePaths: Record<DirectoryV4Scope, readonly string[]> = {
  source: ["GET /api/domain-selection", "GET /api/domain-selection/resolve"],
  receipt: ["GET /api/directory/v4/receipt"],
  task: ["GET /api/directory/v4/receipt", "GET /api/directory/v4/task"],
  sync: ["GET /api/domain-selection/resolve", "POST /api/directory/v4/sync"],
  cancel: ["GET /api/directory/v4/receipt", "GET /api/directory/v4/task", "POST /api/directory/v4/cancel"],
  page: ["GET /api/domain-selection/resolve", "GET /api/directory/v4/observation"],
  detail: ["GET /api/domain-selection/resolve", "GET /api/group-assets/v4/members"],
};
const exact = (v: Record<string, unknown>, keys: string[]) => Object.keys(v).length === keys.length && Object.keys(v).every(k => keys.includes(k));
const invalid = (): never => { throw new ApiError("invalid_response"); };
const utc = (v: unknown): v is string => typeof v === "string" &&
  /^(?!0000)\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/u.test(v) &&
  Number.isFinite(Date.parse(v)) && new Date(v).toISOString().slice(0, 19) === v.slice(0, 19);
// UTC grammar has already been validated. Preserve Go nanoseconds, unlike Date.
const utcOrder = (v: string) => `${v.slice(0, 19)}.${(v.slice(19, -1).replace(/^\./u, "")).padEnd(9, "0")}Z`;
const same = (a: DirectoryV4Intent, b: DirectoryV4Intent) => JSON.stringify(a) === JSON.stringify(b);
export const directoryV4TaskClass = (task: Task): "active" | "terminal" | "diagnostic" =>
  ["partial_failed", "dead_letter"].includes(task.state) ? "diagnostic" :
    ["succeeded", "failed", "cancelled"].includes(task.state) ? "terminal" : "active";
export function parseDirectoryV4Task(v: unknown, domainId: string, taskUUID?: string): Task {
  if (!validDomainID(domainId) || (taskUUID !== undefined && !validDomainID(taskUUID)) || !object(v) || !validTask(v) ||
    !exact(v, ["taskUUID", "taskName", "domainId", "payloadVersion", "state", "sourceState", "error", "createdAt", "updatedAt",
      "attempt", "maxAttempts", "progress", "resultVersion", "result", "cursor", "parentTaskUUID", "terminalAt", "archived", "visibilityVersion"]) ||
    v.taskName !== "domain.directory_read.v4" || v.domainId !== domainId || !validDomainID(v.taskUUID) ||
    (taskUUID !== undefined && v.taskUUID !== taskUUID) || v.payloadVersion !== 1 || v.maxAttempts !== 1 ||
    v.parentTaskUUID !== "" || !object(v.result) || Object.keys(v.result).length !== 0 || !object(v.cursor) || Object.keys(v.cursor).length !== 0 ||
    !utc(v.createdAt) || !utc(v.updatedAt) ||
    !/^(?:[a-z][a-z0-9_]{0,63})?$/u.test(v.error) || !integer(v.attempt, 0, 1)) return invalid();
  const state = v.state;
  const active = ["queued", "running", "cancel_requested"].includes(state);
  if (state === "retry_wait" || (active ? v.terminalAt !== null : !utc(v.terminalAt)) ||
    (state === "queued" && (v.attempt !== 0 || v.sourceState !== "PENDING")) ||
    (["running", "cancel_requested"].includes(state) && (v.attempt !== 1 || v.sourceState !== "STARTED")) ||
    (state === "succeeded" && (v.attempt !== 1 || v.sourceState !== "SUCCESS" || v.progress !== 100)) ||
    (state === "cancelled" && v.sourceState !== "REVOKED") ||
    (["failed", "partial_failed", "dead_letter"].includes(state) && v.sourceState !== "FAILURE")) return invalid();
  return v;
}
const wrap = (v: unknown, original: DirectoryV4Input, id?: string) => {
  if (!object(v) || !exact(v, ["task"])) return invalid();
  return parseDirectoryV4Task(v.task, original.domainId, id);
};
// Internal evidence object: never construct from a code string or another request.
class ProofRejected extends ApiError { constructor() { super("invalid_credentials", 401); } }
const safeCode = (v: unknown): v is string => typeof v === "string" && /^[a-z][a-z0-9_]{0,63}$/u.test(v);
async function json(path: string, signal: AbortSignal, actor?: number, body?: unknown, csrf?: string, proofRequest = false): Promise<unknown> {
  signal.throwIfAborted();
  let response: Response;
  try {
    response = await fetch(path, { method: body === undefined ? "GET" : "POST", credentials: "same-origin", cache: "no-store", redirect: "error", signal,
      ...(body === undefined ? {} : { headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf ?? "" }, body: JSON.stringify(body) }) });
  } catch { signal.throwIfAborted(); throw new ApiError("network"); }
  signal.throwIfAborted();
  if (response.ok && actor !== undefined && (!integer(actor, 1) || response.headers.get("X-ADTR-User-ID") !== String(actor))) {
    await response.body?.cancel().catch(() => undefined);
    throw new ApiError("unauthenticated", 401);
  }
  let value: unknown;
  try { value = await readDirectoryV4ControlsJSON(response, signal); }
  catch (e) {
    signal.throwIfAborted();
    if ([401, 403, 409].includes(response.status)) throw new ApiError(response.status === 401 ? "unauthenticated" : response.status === 403 ? "forbidden" : "selection_changed", response.status);
    throw e;
  }
  signal.throwIfAborted();
  const metadata = response.headers.get("Content-Type")?.split(";")[0].trim().toLowerCase() === "application/json" &&
    response.headers.get("Cache-Control") === "no-store" && response.headers.get("X-Content-Type-Options") === "nosniff";
  if (!response.ok) {
    if (proofRequest && response.status === 401 && metadata && object(value) && exact(value, ["error"]) && value.error === "invalid_credentials") throw new ProofRejected();
    const code = object(value) && exact(value, ["error"]) && safeCode(value.error) ? value.error :
      response.status === 401 ? "unauthenticated" : response.status === 403 ? "forbidden" : response.status === 409 ? "selection_changed" : "invalid_response";
    throw new ApiError(code, response.status);
  }
  if (!metadata) return invalid();
  return value;
}
export interface DirectoryV4Epoch {
  // Must throw after navigation, hidden/blur, source/task boundary or session
  // replacement. Abort is insufficient when a transport ignores its signal.
  assertCurrent(): void;
}
const recoveryContextBrand: unique symbol = Symbol("directory-v4-recovery");
export interface DirectoryV4RecoveryContext {
  readonly [recoveryContextBrand]: true;
}
interface RecoveryContextState {
  generation: number;
  storage?: DirectoryV4Storage;
  stores: Map<string, DirectoryV4IntentStore>;
}
const recoveryContexts = new WeakMap<DirectoryV4RecoveryContext, RecoveryContextState>();
// Opaque, tab-memory-only capability. It contains no hint or mutation methods.
// Hold once across route/epoch recreation; never serialize or persist it.
export function createDirectoryV4RecoveryContext(): DirectoryV4RecoveryContext {
  const handle = Object.freeze({ [recoveryContextBrand]: true as const });
  recoveryContexts.set(handle, { generation: 0, stores: new Map() });
  return handle;
}
export function directoryV4SourceInput(choice: SourceChoice, idempotencyKey: string): DirectoryV4Input {
  if (!validSourceChoice(choice)) throw new ApiError("invalid_input");
  const value = { domainId: choice.domainId, expectedRevision: choice.revision,
    expectedCredentialGeneration: choice.credentialRevision, idempotencyKey };
  if (!validDirectoryV4Input(value)) throw new ApiError("invalid_input");
  return value;
}

export function createDirectoryV4Controls(
  profile: Profile, storage: DirectoryV4Storage, epoch: DirectoryV4Epoch,
  recoveryContext: DirectoryV4RecoveryContext = createDirectoryV4RecoveryContext(),
) {
  // Capture a new identity snapshot; caller cannot mutate it during a request.
  const identity = { ...profile };
  const recovery = recoveryContexts.get(recoveryContext);
  if (!recovery || (recovery.storage !== undefined && recovery.storage !== storage))
    throw new ApiError("invalid_recovery_context");
  recovery.storage = storage;
  const ownerKey = directoryV4OwnerKey(identity);
  let ownerStore = recovery.stores.get(ownerKey);
  if (!ownerStore) {
    ownerStore = new DirectoryV4IntentStore(identity, storage);
    recovery.stores.set(ownerKey, ownerStore);
  }
  const store = ownerStore;
  const contextGeneration = ++recovery.generation;
  if (store.owner.ID !== identity.ID || store.owner.username !== identity.username) throw new ApiError("unauthenticated", 401);
  let mutationBusy = false;
  let statusBusy = false;
  let retryReceipt: DirectoryV4Intent | undefined;
  let visibleGroups = new Set<string>();
  let pageGeneration = 0;
  let detailGeneration = 0;
  let lastAccepted: Task | undefined;
  function accept(task: Task) {
    if (lastAccepted && lastAccepted.taskUUID === task.taskUUID &&
      (task.resultVersion < lastAccepted.resultVersion || utcOrder(task.updatedAt) < utcOrder(lastAccepted.updatedAt) ||
        task.visibilityVersion < lastAccepted.visibilityVersion ||
        (task.resultVersion === lastAccepted.resultVersion && task.state !== lastAccepted.state)))
      throw new ApiError("stale_result");
    lastAccepted = { ...task };
    return task;
  }
  const checkContext = () => {
    if (contextGeneration !== recovery.generation) throw new ApiError("stale_epoch");
    epoch.assertCurrent();
  };
  const check = (signal: AbortSignal) => { signal.throwIfAborted(); checkContext(); };
  async function session(signal: AbortSignal) {
    check(signal);
    const current = await json("/api/auth/me", signal);
    check(signal);
    if (!object(current) || !integer(identity.ID, 1) || !identity.csrfToken ||
      ["ID", "username", "role", "priv", "csrfToken", "needChangePwd", "isExpired"].some(k => current[k] !== identity[k as keyof Profile]) ||
      current.needChangePwd !== false || current.isExpired !== false) throw new ApiError("unauthenticated", 401);
  }
  async function authorize(scope: DirectoryV4Scope, signal: AbortSignal) {
    await session(signal);
    const menu = await json("/api/access/menu", signal);
    const paths = directoryV4ScopePaths[scope];
    const checks = await json("/api/access/check", signal, undefined, { paths }, identity.csrfToken);
    check(signal);
    if (!object(menu) || !Array.isArray(menu.menu) || !object(checks) || !exact(checks, ["results"]) ||
      !Array.isArray(checks.results) || checks.results.length !== paths.length || checks.results.some(v => typeof v !== "boolean")) return invalid();
    const marks = scope === "source" ? ["domains"] : ["domains", "directory_assets", ...(["receipt", "task", "sync", "cancel"].includes(scope) ? ["tasks"] : [])];
    const write = scope === "sync" || scope === "cancel";
    for (const mark of marks) {
      const entries = menu.menu.filter(p => object(p) && p.mark === mark);
      if (entries.length !== 1 || !object(entries[0]) || !object(entries[0].auth) || entries[0].auth.readable !== true ||
        (write && mark !== "domains" && entries[0].auth.writeable !== true)) throw new ApiError("forbidden", 403);
    }
    if (checks.results.some(v => v !== true)) throw new ApiError("forbidden", 403);
    await session(signal);
    check(signal);
  }
  async function resolve(input: DirectoryV4Input, signal: AbortSignal): Promise<SourceChoice> {
    if (!validDirectoryV4Input(directoryV4Input(input))) throw new ApiError("invalid_input");
    const q = new URLSearchParams({ domainId: input.domainId, expectedRevision: input.expectedRevision, expectedCredentialRevision: input.expectedCredentialGeneration });
    const value = await json(`/api/domain-selection/resolve?${q}`, signal, identity.ID);
    // Validate a real, complete SourceChoice before using the existing exact-pin
    // parser. Never cast persisted pins into a fabricated SourceChoice.
    if (!object(value) || !validSourceChoice(value.selection)) return invalid();
    const selected = value.selection;
    if (selected.domainId !== input.domainId || selected.revision !== input.expectedRevision || selected.credentialRevision !== input.expectedCredentialGeneration) return invalid();
    return parseResolvedSource(value, selected).selection;
  }
  const original = () => {
    const value = store.recovery();
    if (!value) throw new ApiError("original_intent_required");
    return value;
  };
  async function rawReceipt(saved: DirectoryV4Intent, signal: AbortSignal) {
    const value = await json(`/api/directory/v4/receipt?${new URLSearchParams({ domainId: saved.domainId, idempotencyKey: saved.idempotencyKey })}`, signal, identity.ID);
    return wrap(value, saved, saved.taskUUID);
  }
  function bind(saved: DirectoryV4Intent, task: Task, requireDurable = false) {
    if (saved.taskUUID && saved.taskUUID !== task.taskUUID) return invalid();
    try { return store.bind(saved, task.taskUUID); }
    catch (error) {
      if (!(error instanceof ApiError) || error.code !== "recovery_storage_failed") throw error;
      const retained = store.retainBinding(saved, task.taskUUID);
      if (requireDurable) throw error;
      return retained;
    }
  }
  async function receipt(signal: AbortSignal) {
    if (statusBusy) throw new ApiError("read_in_progress");
    statusBusy = true;
    try {
    const saved = original();
    retryReceipt = undefined;
    await authorize("receipt", signal);
    let task: Task;
    try { task = await rawReceipt(saved, signal); }
    catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        await authorize("receipt", signal);
        check(signal);
        if (!saved.receiptOnly && !saved.taskUUID) { store.assertDurable(saved); retryReceipt = saved; }
      }
      throw e;
    }
    await authorize("receipt", signal);
    check(signal);
    accept(task);
    bind(saved, task);
    return task;
    } finally { statusBusy = false; }
  }
  async function task(signal: AbortSignal, scope: "task" | "cancel" = "task") {
    if (statusBusy) throw new ApiError("read_in_progress");
    statusBusy = true;
    try {
      let saved = original();
      await authorize(scope, signal);
      // Stored UUID is never authority: recover the original actor receipt first.
      const first = await rawReceipt(saved, signal);
      await authorize(scope, signal);
      check(signal);
      accept(first);
      saved = bind(saved, first);
      const delayed = wrap(await json(`/api/directory/v4/task?${new URLSearchParams({ taskUUID: first.taskUUID })}`, signal, identity.ID), saved, first.taskUUID);
      const fresh = await rawReceipt(saved, signal);
      if (fresh.resultVersion < delayed.resultVersion || utcOrder(fresh.updatedAt) < utcOrder(delayed.updatedAt)) return invalid();
      await authorize(scope, signal);
      check(signal);
      accept(fresh);
      try {
        if (saved.cancelState && fresh.state === "cancel_requested") store.markCancel(saved, "accepted");
        else if (saved.cancelState && directoryV4TaskClass(fresh) === "terminal") store.markCancel(saved, "terminal");
      } catch (error) {
        if (!(error instanceof ApiError) || error.code !== "recovery_storage_failed") throw error;
        // Fully verified read result remains useful; private failed write keeps
        // the conservative fallback and blocks every later mutation.
      }
      return fresh;
    } finally { statusBusy = false; }
  }
  const proofShape = (p: Proof) => {
    if (typeof p.actorPassword !== "string" || p.actorPassword.length === 0 || !/^\d{6}$/u.test(p.totpCode)) throw new ApiError("invalid_input");
  };
  function restrictOnConflict(e: unknown, saved: DirectoryV4Intent) {
    if (e instanceof ApiError && (e.status === 409 || ["revision_conflict", "idempotency_conflict", "directory_use_changed"].includes(e.code))) store.restrict(saved);
  }
  async function sync(input: DirectoryV4Input, proof: Proof, signal: AbortSignal) {
    if (mutationBusy) throw new ApiError("mutation_in_progress");
    proofShape(proof);
    if (!validDirectoryV4Input(input)) throw new ApiError("invalid_input");
    input = directoryV4Input(input);
    proof = { actorPassword: proof.actorPassword, totpCode: proof.totpCode };
    mutationBusy = true;
    let saved: DirectoryV4Intent | undefined;
    try {
      const r = store.read();
      if (r.state !== "empty") {
        if (r.state !== "valid" || r.intent.receiptOnly || r.intent.taskUUID || !sameDirectoryV4Input(r.intent, input) ||
          !retryReceipt || !sameDirectoryV4Input(retryReceipt, r.intent)) throw new ApiError("original_receipt_required");
        saved = r.intent;
      }
      // Consume the single explicit receipt-404 retry opportunity before awaits.
      retryReceipt = undefined;
      await authorize("sync", signal);
      await resolve(input, signal);
      await authorize("sync", signal);
      check(signal);
      saved = saved ?? store.begin(input);
      store.assertDurable(saved);
      const value = await json("/api/directory/v4/sync", signal, identity.ID,
        { ...directoryV4Input(input), actorPassword: proof.actorPassword, totpCode: proof.totpCode }, identity.csrfToken, true);
      if (!object(value) || !exact(value, ["task", "replayed"]) || typeof value.replayed !== "boolean") return invalid();
      const submitted = parseDirectoryV4Task(value.task, input.domainId, saved.taskUUID);
      // Bind original receipt, not the response alone, before exposing any task.
      const fresh = await rawReceipt({ ...saved, taskUUID: submitted.taskUUID }, signal);
      if (fresh.resultVersion < submitted.resultVersion || utcOrder(fresh.updatedAt) < utcOrder(submitted.updatedAt)) return invalid();
      await authorize("sync", signal);
      await authorize("receipt", signal);
      check(signal);
      accept(fresh);
      bind(saved, fresh, true);
      return { task: fresh, replayed: value.replayed };
    } catch (e) {
      if (saved) restrictOnConflict(e, saved);
      // Even exact proof rejection never erases an earlier uncertain dispatch.
      // A new explicit original receipt check is required for every next retry.
      throw e instanceof ProofRejected ? new ApiError("invalid_credentials", 401) : e;
    } finally { mutationBusy = false; }
  }
  async function cancel(proof: Proof, signal: AbortSignal) {
    if (mutationBusy) throw new ApiError("mutation_in_progress");
    proofShape(proof);
    proof = { actorPassword: proof.actorPassword, totpCode: proof.totpCode };
    mutationBusy = true;
    let marked: DirectoryV4Intent | undefined;
    try {
      const current = await task(signal, "cancel");
      const saved = original();
      if (saved.cancelState || current.archived || !["queued", "running"].includes(current.state)) throw new ApiError("cancel_query_only");
      check(signal);
      marked = store.markCancel(saved, "unknown");
      store.assertDurable(marked);
      let returned: Task;
      try {
        returned = wrap(await json("/api/directory/v4/cancel", signal, identity.ID,
          { taskUUID: current.taskUUID, actorPassword: proof.actorPassword, totpCode: proof.totpCode }, identity.csrfToken, true), marked, current.taskUUID);
      } catch (e) {
        if (e instanceof ProofRejected) {
          // Only THIS request's exact proof rejection can unlock THIS marker.
          // Renew session/path and receipt without source/grant resolution.
          await authorize("cancel", signal);
          const fresh = await rawReceipt(marked, signal);
          await authorize("cancel", signal);
          check(signal);
          accept(fresh);
          if (!fresh.archived && ["queued", "running"].includes(fresh.state)) store.clearRejectedCancel(marked);
          throw new ApiError("invalid_credentials", 401);
        }
        throw e;
      }
      if (!["cancel_requested", "cancelled"].includes(returned.state)) return invalid();
      const fresh = await rawReceipt(marked, signal);
      if (fresh.resultVersion < returned.resultVersion || utcOrder(fresh.updatedAt) < utcOrder(returned.updatedAt)) return invalid();
      await authorize("cancel", signal);
      check(signal);
      accept(fresh);
      if (directoryV4TaskClass(fresh) === "terminal") store.markCancel(marked, "terminal");
      else store.markCancel(marked, "accepted");
      return fresh;
    } finally { mutationBusy = false; }
  }
  async function pinned(signal: AbortSignal, scope: "page" | "detail") {
    const saved = original();
    if (!saved.taskUUID) throw new ApiError("original_receipt_required");
    // Result authority is reader-only after original success was verified by
    // this instance. The caller cannot provide a synthetic succeeded Task.
    if (verifiedSuccess !== saved.taskUUID) throw new ApiError("original_success_required");
    await authorize(scope, signal);
    const choice = await resolve(saved, signal);
    return { saved, choice };
  }
  let verifiedSuccess: string | undefined;
  const remember = (t: Task) => { verifiedSuccess = t.state === "succeeded" ? t.taskUUID : undefined; return t; };
  async function postResult(saved: DirectoryV4Intent, source: { domain: string; server_name: string } | undefined, signal: AbortSignal, scope: "page" | "detail") {
    const choice = await resolve(saved, signal);
    if (!source || source.domain !== choice.domain || source.server_name !== choice.dcHostName) return invalid();
    await authorize(scope, signal);
    check(signal);
  }
  return {
    authorize,
    readIntent: () => { checkContext(); return store.read(); },
    recoveryIntent: () => { checkContext(); return store.recovery(); },
    recoveryDurability: () => { checkContext(); return store.durability(); },
    async listSources(q: SourceQuery, signal: AbortSignal) {
      if (!integer(q.pageIdx, 1, 1000000) || ![10,20,30,40,50].includes(q.pageSize) || typeof q.keyword !== "string" ||
        !validSourceKeyword(q.keyword) || (q.observationState !== "" && !connectionStates.includes(q.observationState))) throw new ApiError("invalid_input");
      q = { ...q };
      await authorize("source", signal);
      const value = parseSourcePage(await json(`/api/domain-selection?${new URLSearchParams({ pageIdx: String(q.pageIdx), pageSize: String(q.pageSize), keyword: q.keyword, ...(q.observationState ? { observationState: q.observationState } : {}) })}`, signal, identity.ID), q);
      await authorize("source", signal);
      check(signal);
      return value;
    },
    async resolveSource(input: DirectoryV4Input, signal: AbortSignal) {
      if (!validDirectoryV4Input(input)) throw new ApiError("invalid_input");
      input = directoryV4Input(input);
      await authorize("source", signal);
      const value = await resolve(input, signal);
      await authorize("source", signal);
      check(signal);
      return value;
    },
    async receipt(signal: AbortSignal) { return remember(await receipt(signal)); },
    async task(signal: AbortSignal) { return remember(await task(signal)); },
    async sync(input: DirectoryV4Input, proof: Proof, signal: AbortSignal) {
      const result = await sync(input, proof, signal); remember(result.task); return result;
    },
    async cancel(proof: Proof, signal: AbortSignal) { return remember(await cancel(proof, signal)); },
    async acknowledge(signal: AbortSignal) {
      if (mutationBusy) throw new ApiError("mutation_in_progress");
      const t = await task(signal);
      check(signal);
      store.acknowledge(original(), t);
      verifiedSuccess = undefined;
      lastAccepted = undefined;
      visibleGroups.clear();
      pageGeneration++;
      detailGeneration++;
      retryReceipt = undefined;
    },
    async page(pageIdx: number, pageSize: number, signal: AbortSignal) {
      const generation = ++pageGeneration;
      detailGeneration++;
      visibleGroups.clear();
      if (!integer(pageIdx, 1, 10000) || ![25,50,100].includes(pageSize)) throw new ApiError("invalid_input");
      const { saved } = await pinned(signal, "page");
      const q: GroupMembersV4PageQuery = { domainId: saved.domainId, observationId: saved.taskUUID!, pageIdx, pageSize };
      const value = parseGroupMembersV4Page(await json(`/api/directory/v4/observation?${new URLSearchParams({ domainId: q.domainId, observationId: q.observationId!, kind: "group", pageIdx: String(pageIdx), pageSize: String(pageSize) })}`, signal, identity.ID), q);
      await postResult(saved, value.source, signal, "page");
      if (generation !== pageGeneration) throw new ApiError("stale_result");
      visibleGroups = new Set(value.list.map(row => row.objectGUID));
      return value;
    },
    async detail(objectGUID: string, signal: AbortSignal) {
      const generation = ++detailGeneration;
      const page = pageGeneration;
      if (!visibleGroups.has(objectGUID)) throw new ApiError("original_page_required");
      const { saved } = await pinned(signal, "detail");
      const q: GroupMembersV4Query = { domainId: saved.domainId, expectedRevision: saved.expectedRevision,
        expectedCredentialRevision: saved.expectedCredentialGeneration, observationId: saved.taskUUID!, objectGUID };
      if (!validGroupMembersV4Query(q)) throw new ApiError("invalid_input");
      const value = parseGroupMembersV4(await json(`/api/group-assets/v4/members?${new URLSearchParams({ ...q })}`, signal, identity.ID), q);
      await postResult(saved, value.source, signal, "detail");
      if (generation !== detailGeneration || page !== pageGeneration || !visibleGroups.has(objectGUID)) throw new ApiError("original_page_required");
      return value;
    },
  };
}
export type DirectoryV4Controls = ReturnType<typeof createDirectoryV4Controls>;
// One store per mounted actor/session. Read storage afresh for every mutation;
// memory is only a read-recovery fallback and cannot authorize dispatch.
class DirectoryV4IntentStore {
  readonly owner: DirectoryV4Owner;
  readonly key: string;
  private fallback: DirectoryV4Intent | undefined;
  private blocked = false;
  constructor(owner: Pick<Profile, "ID" | "username">, private readonly storage: DirectoryV4Storage) {
    this.owner = { ID: owner.ID, username: owner.username };
    this.key = directoryV4IntentPrefix + directoryV4OwnerKey(this.owner);
  }
  read(): DirectoryV4IntentRead {
    try {
      const raw = this.storage.getItem(this.key);
      if (raw === null) return { state: "empty" };
      if (raw.length > 8192) return { state: "unusable" };
      try { return { state: "valid", intent: parseDirectoryV4Intent(parseDirectoryV4UniqueJSON(raw), this.owner) }; }
      catch { return { state: "unusable" }; }
    } catch { return { state: "storage_unavailable" }; }
  }
  recovery(): DirectoryV4Intent | undefined {
    const r = this.read();
    if (this.blocked && this.fallback) return parseDirectoryV4Intent(this.fallback, this.owner);
    return r.state === "valid" ? r.intent : this.fallback && parseDirectoryV4Intent(this.fallback, this.owner);
  }
  durability(): "durable" | "memory_only" | "none" | "unusable" {
    const current = this.read();
    if (this.fallback && (this.blocked || current.state !== "valid")) return "memory_only";
    if (current.state === "valid" && !this.blocked) return "durable";
    return current.state === "empty" && !this.fallback ? "none" : "unusable";
  }
  retainBinding(original: DirectoryV4Intent, taskUUID: string): DirectoryV4Intent {
    if (!validDomainID(taskUUID) || (original.taskUUID && original.taskUUID !== taskUUID)) throw new ApiError("invalid_response");
    const previous = this.fallback;
    if (previous && (!sameDirectoryV4Input(previous, original) || (previous.taskUUID && previous.taskUUID !== taskUUID)))
      throw new ApiError("invalid_response");
    const kept = previous ?? original;
    this.fallback = parseDirectoryV4Intent({ ...kept, taskUUID,
      receiptOnly: kept.receiptOnly || original.receiptOnly }, this.owner);
    this.blocked = true;
    return parseDirectoryV4Intent(this.fallback, this.owner);
  }
  assertDurable(expected: DirectoryV4Intent): void {
    const r = this.read();
    if (this.blocked || r.state !== "valid" || !same(r.intent, expected)) throw new ApiError("recovery_storage_failed");
  }
  private write(next: DirectoryV4Intent, previous?: DirectoryV4Intent): DirectoryV4Intent {
    let value = parseDirectoryV4Intent(next, this.owner);
    const before = this.read();
    if (previous && before.state === "valid" &&
      (!sameDirectoryV4Input(before.intent, previous) ||
        (before.intent.taskUUID && previous.taskUUID && before.intent.taskUUID !== previous.taskUUID)))
      throw new ApiError("invalid_response");
    if (previous ? before.state !== "valid" || !same(before.intent, previous) : before.state !== "empty") {
      // A failed prewrite read is still a persistence failure. Retain the
      // conservative original transition before returning, not just setItem
      // failures. Never replace a different durable original operation.
      if (!previous && before.state === "valid") throw new ApiError("original_intent_required");
      for (const candidate of [this.fallback, before.state === "valid" ? before.intent : undefined]) {
        if (!candidate || !sameDirectoryV4Input(candidate, value)) continue;
        if (candidate.taskUUID && value.taskUUID && candidate.taskUUID !== value.taskUUID) throw new ApiError("invalid_response");
        const cancelState = candidate.cancelState === "terminal" || value.cancelState === "terminal" ? "terminal" :
          candidate.cancelState === "accepted" || value.cancelState === "accepted" ? "accepted" :
          candidate.cancelState ?? value.cancelState;
        value = { ...value, receiptOnly: candidate.receiptOnly || value.receiptOnly,
          ...(candidate.taskUUID ? { taskUUID: candidate.taskUUID } : {}),
          ...(cancelState ? { cancelState } : {}) };
      }
      this.fallback = value;
      this.blocked = true;
      throw new ApiError("recovery_storage_failed");
    }
    try {
      this.storage.setItem(this.key, JSON.stringify(value));
      const after = this.read();
      if (after.state !== "valid" || !same(after.intent, value)) throw new Error("readback");
      this.blocked = false;
      this.fallback = value;
      return value;
    } catch {
      this.blocked = true;
      // Conservative in-memory marker survives write failures. Never delete.
      this.fallback = value;
      throw new ApiError("recovery_storage_failed");
    }
  }
  begin(input: DirectoryV4Input): DirectoryV4Intent {
    if (!validDirectoryV4Input(input) || this.fallback) throw new ApiError("original_intent_required");
    return this.write({ ...directoryV4Input(input), owner: this.owner, storageVersion: 1,
      dictionaryVersion: 4, kind: "domain.directory_read.v4", payloadVersion: 1, receiptOnly: false });
  }
  bind(original: DirectoryV4Intent, taskUUID: string): DirectoryV4Intent {
    if (!validDomainID(taskUUID) || (original.taskUUID && original.taskUUID !== taskUUID)) throw new ApiError("invalid_response");
    const durable = this.read();
    if (durable.state !== "valid") throw new ApiError("recovery_storage_failed");
    if (!sameDirectoryV4Input(durable.intent, original) ||
      (durable.intent.taskUUID && durable.intent.taskUUID !== taskUUID)) throw new ApiError("invalid_response");
    const cancelState = original.cancelState === "terminal" || durable.intent.cancelState === "terminal" ? "terminal" :
      original.cancelState === "accepted" || durable.intent.cancelState === "accepted" ? "accepted" :
      original.cancelState ?? durable.intent.cancelState;
    return this.write({ ...original, taskUUID,
      receiptOnly: original.receiptOnly || durable.intent.receiptOnly,
      ...(cancelState ? { cancelState } : {}) }, durable.intent);
  }
  restrict(original: DirectoryV4Intent): DirectoryV4Intent {
    return this.write({ ...original, receiptOnly: true }, original);
  }
  markCancel(original: DirectoryV4Intent, state: NonNullable<DirectoryV4Intent["cancelState"]>): DirectoryV4Intent {
    if (!original.taskUUID || (state === "unknown" && original.cancelState) ||
      (original.cancelState === "terminal" && state !== "terminal") ||
      (original.cancelState === "accepted" && state === "unknown")) throw new ApiError("cancel_query_only");
    return this.write({ ...original, cancelState: state }, original);
  }
  // Only the guarded API may call after exact current-request proof rejection,
  // renewed original receipt/session/cancel authority and same queued/running task.
  clearRejectedCancel(original: DirectoryV4Intent): DirectoryV4Intent {
    if (original.cancelState !== "unknown") throw new ApiError("cancel_query_only");
    const next = { ...original };
    delete next.cancelState;
    try { return this.write(next, original); }
    catch (error) {
      this.blocked = true;
      const current = this.read();
      let retained = original;
      for (const candidate of [this.fallback, current.state === "valid" ? current.intent : undefined]) {
        if (!candidate || !sameDirectoryV4Input(candidate, original) || candidate.taskUUID !== original.taskUUID) continue;
        const cancelState = candidate.cancelState === "terminal" || retained.cancelState === "terminal" ? "terminal" :
          candidate.cancelState === "accepted" || retained.cancelState === "accepted" ? "accepted" : "unknown";
        retained = { ...retained, receiptOnly: retained.receiptOnly || candidate.receiptOnly, cancelState };
      }
      this.fallback = retained;
      // A nonstandard adapter may perform the removal then fail readback. Only
      // compensate a storage failure after re-reading this SAME original. Never
      // overwrite a changed key/UUID, and never downgrade a stronger marker.
      if (error instanceof ApiError && error.code === "recovery_storage_failed" &&
        current.state === "valid" && sameDirectoryV4Input(current.intent, original) &&
        current.intent.taskUUID === original.taskUUID) {
        try { this.storage.setItem(this.key, JSON.stringify(retained)); } catch { /* query-only */ }
      }
      throw error;
    }
  }
  acknowledge(original: DirectoryV4Intent, task: Task): void {
    if (!original.taskUUID || original.taskUUID !== task.taskUUID || task.domainId !== original.domainId ||
      task.taskName !== "domain.directory_read.v4" || !["succeeded", "failed", "cancelled"].includes(task.state))
      throw new ApiError("original_terminal_required");
    this.assertDurable(original);
    try {
      this.storage.removeItem(this.key);
      if (this.storage.getItem(this.key) !== null) throw new Error("readback");
      this.fallback = undefined;
      this.blocked = false;
    } catch { this.blocked = true; throw new ApiError("recovery_storage_failed"); }
  }
}
