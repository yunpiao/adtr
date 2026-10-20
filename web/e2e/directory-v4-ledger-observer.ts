import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { createHash } from "node:crypto";

const run = promisify(execFile);
const fail = (): never => { throw new Error("Invalid isolated v4 ledger evidence"); };
const identifier = /^[A-Za-z0-9_.-]{1,128}$/u;
const positive = /^[1-9][0-9]{0,18}$/u;
const decimal = /^(0|[1-9][0-9]{0,18})$/u;
export type DirectoryV4Original = {
  taskUUID: string; domainId: string; actorId: number;
  expectedRevision: string; expectedCredentialGeneration: string;
};
export type DirectoryV4LedgerFacts = {
  state: "reserved" | "opened" | "quiesced";
  reason: null | "never_opened_terminal" | "executor_returned";
  taskState: "queued" | "running" | "cancel_requested" | "cancelled" | "succeeded" | "failed";
  attempt: number; progress: number; dependencies: number; observations: number;
  bindingPreserved: boolean; identityMatched: boolean; liveOpenerMatched: boolean;
  submittedEvents: number; cancelledEvents: number; executionEvents: number;
};
type Raw = Record<string, string | number | boolean | null>;
const raws = new WeakMap<DirectoryV4LedgerSnapshot, Raw>();
const stringKeys = ["taskUUID", "domainId", "actorId", "revision", "generation", "accountId", "accountRevision", "grantRole", "grantRevision", "policyRevision", "state", "taskState", "fence", "leaseOwner", "resultVersion", "updatedAt"];
const nullableKeys = ["reason", "openedAt", "quiescedAt", "openerOwner", "openerFence", "leaseUntil", "terminalAt"];
const numberKeys = ["attempt", "progress", "openerAttempt", "dependencies", "matchedDependencies", "bindingDependencies", "observations", "submittedEvents", "cancelledEvents", "executionEvents", "otherEvents"];
const booleanKeys = ["identityMatched", "payloadMatched", "emptyResult", "emptyCursor", "liveOpenerMatched"];
const keys = [...stringKeys, ...nullableKeys, ...numberKeys, ...booleanKeys].sort();
const immutable = ["taskUUID", "domainId", "actorId", "revision", "generation", "accountId", "accountRevision", "grantRole", "grantRevision", "policyRevision"];
const opener = ["openedAt", "openerOwner", "openerFence", "openerAttempt"];
const digest = (value: unknown) => createHash("sha256").update(JSON.stringify(value)).digest("hex");
function date(value: unknown): boolean {
  return typeof value === "string" && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}Z$/u.test(value) && Number.isFinite(Date.parse(value));
}
function originalValid(v: DirectoryV4Original) {
  if (!identifier.test(v.taskUUID) || !identifier.test(v.domainId) || !Number.isSafeInteger(v.actorId) || v.actorId <= 0 || !positive.test(v.expectedRevision) || !positive.test(v.expectedCredentialGeneration)) fail();
}
function raw(snapshot: DirectoryV4LedgerSnapshot): Raw { return raws.get(snapshot) ?? fail(); }
function same(a: Raw, b: Raw, fields: string[]) { if (fields.some(key => a[key] !== b[key])) fail(); }
// Raw owner/fence/account/grant material never becomes enumerable or serializable.
// Do not put the snapshot into an assertion reporter; use these safe checks/facts.
export class DirectoryV4LedgerSnapshot {
  private constructor(value: Raw) { raws.set(this, value); }
  static parse(text: string, original: DirectoryV4Original): DirectoryV4LedgerSnapshot {
    originalValid(original);
    // The fixed row_to_json projection has literal ASCII keys and validated
    // ASCII scalar strings. Escapes have no valid use here; rejecting them
    // closes decoded duplicate-key aliases before JSON.parse loses duplicates.
    if (!text.trim() || text.includes("\\") || Buffer.byteLength(text, "utf8") > 16384) fail();
    let v: Raw;
    try { v = JSON.parse(text) as Raw; } catch { return fail(); }
    if (!v || typeof v !== "object" || Array.isArray(v) || Object.keys(v).sort().join(" ") !== keys.join(" ")) fail();
    // Flat scalar SQL projection only. Count key tokens to reject duplicate keys;
    // values below reject escapes/quotes, so no value can impersonate a key token.
    const tokens = [...text.matchAll(/"([A-Za-z]+)"\s*:/gu)].map(match => match[1]);
    if (tokens.length !== keys.length || new Set(tokens).size !== keys.length) fail();
    if (stringKeys.some(key => typeof v[key] !== "string") || nullableKeys.some(key => v[key] !== null && typeof v[key] !== "string") || booleanKeys.some(key => typeof v[key] !== "boolean")) fail();
    for (const key of numberKeys) {
      if (key === "openerAttempt" && v[key] === null) continue;
      if (!Number.isSafeInteger(v[key]) || Number(v[key]) < 0 || Number(v[key]) > 100000) fail();
    }
    for (const key of ["taskUUID", "domainId", "accountId", "grantRole"]) if (!identifier.test(String(v[key]))) fail();
    for (const key of ["actorId", "revision", "generation", "accountRevision", "grantRevision"]) if (!positive.test(String(v[key]))) fail();
    for (const key of ["fence", "resultVersion"]) if (!decimal.test(String(v[key]))) fail();
    if (!/^[0-9a-f]{64}$/u.test(String(v.policyRevision))) fail();
    if (!date(v.updatedAt) || ["openedAt", "quiescedAt", "leaseUntil", "terminalAt"].some(key => v[key] !== null && !date(v[key]))) fail();
    for (const key of ["leaseOwner", "openerOwner"]) if (v[key] !== null && !/^[A-Za-z0-9_.:/-]{0,256}$/u.test(String(v[key]))) fail();
    if (v.openerFence !== null && !positive.test(String(v.openerFence))) fail();
    if (!["reserved", "opened", "quiesced"].includes(String(v.state)) || !["queued", "running", "cancel_requested", "cancelled", "succeeded", "failed"].includes(String(v.taskState)) || ![null, "never_opened_terminal", "executor_returned"].includes(v.reason as null | string)) fail();
    if (v.taskUUID !== original.taskUUID || v.domainId !== original.domainId || v.actorId !== String(original.actorId) || v.revision !== original.expectedRevision || v.generation !== original.expectedCredentialGeneration || !v.identityMatched || !v.payloadMatched || !v.emptyResult || !v.emptyCursor || v.bindingDependencies !== 1 || v.dependencies !== v.matchedDependencies || Number(v.dependencies) > 1 || Number(v.attempt) > 1 || Number(v.progress) > 100) fail();
    if (v.state === "reserved" && (opener.some(key => v[key] !== null) || v.quiescedAt !== null || v.reason !== null || v.dependencies !== 1)) fail();
    if (v.state === "opened" && (v.openedAt === null || !v.openerOwner || v.openerFence === null || v.openerAttempt !== 1 || v.quiescedAt !== null || v.reason !== null || v.dependencies !== 1)) fail();
    if (v.state === "quiesced" && (v.quiescedAt === null || v.dependencies !== 0 || v.reason === null)) fail();
    if (v.reason === "never_opened_terminal" && opener.some(key => v[key] !== null)) fail();
    if (v.reason === "executor_returned" && (v.openedAt === null || !v.openerOwner || v.openerFence === null || v.openerAttempt !== 1)) fail();
    const active = ["queued", "running", "cancel_requested"].includes(String(v.taskState));
    if (active !== (v.terminalAt === null) || (v.taskState === "queued" && v.attempt !== 0) || (["running", "cancel_requested", "succeeded"].includes(String(v.taskState)) && v.attempt !== 1)) fail();
    if ((v.leaseOwner === "") !== (v.leaseUntil === null)) fail();
    if (v.state === "quiesced" && active) fail();
    if (["running", "cancel_requested"].includes(String(v.taskState)) && v.leaseOwner === "") fail();
    if (v.taskState === "succeeded" && v.progress !== 100) fail();
    if (!active && v.leaseOwner !== "") fail();
    return new DirectoryV4LedgerSnapshot(v);
  }
  toJSON(): never { return fail(); }
  get facts(): DirectoryV4LedgerFacts {
    const v = raw(this);
    return { state: v.state as DirectoryV4LedgerFacts["state"], reason: v.reason as DirectoryV4LedgerFacts["reason"], taskState: v.taskState as DirectoryV4LedgerFacts["taskState"], attempt: Number(v.attempt), progress: Number(v.progress), dependencies: Number(v.dependencies), observations: Number(v.observations), bindingPreserved: v.bindingDependencies === 1, identityMatched: v.identityMatched === true && v.payloadMatched === true, liveOpenerMatched: v.liveOpenerMatched === true, submittedEvents: Number(v.submittedEvents), cancelledEvents: Number(v.cancelledEvents), executionEvents: Number(v.executionEvents) };
  }
  get evidence() {
    const v = raw(this);
    return {
      taskSHA256: digest([v.taskUUID]), domainSHA256: digest([v.domainId]),
      actorSHA256: digest([v.actorId]), sourceSHA256: digest([v.domainId, v.revision, v.generation]),
      identitySHA256: digest(immutable.map(key => v[key])),
      openerSHA256: v.openedAt === null ? null : digest(opener.map(key => v[key])),
      ...this.facts, matchedDependencies: Number(v.matchedDependencies),
      bindingDependencies: Number(v.bindingDependencies), otherEvents: Number(v.otherEvents),
      fencingZero: v.fence === "0",
    };
  }
}
export const parseDirectoryV4Use = (text: string, original: DirectoryV4Original) => DirectoryV4LedgerSnapshot.parse(text, original);
export function assertQueuedReserved(snapshot: DirectoryV4LedgerSnapshot, cancelled = false): void {
  const v = raw(snapshot);
  if (v.state !== "reserved" || v.taskState !== (cancelled ? "cancelled" : "queued") || v.attempt !== 0 || v.fence !== "0" || v.observations !== 0 || v.submittedEvents !== 1 || v.cancelledEvents !== (cancelled ? 1 : 0) || v.executionEvents !== 0 || v.otherEvents !== 0) fail();
}
export function assertQueuedQuiesced(snapshot: DirectoryV4LedgerSnapshot, original: DirectoryV4LedgerSnapshot): void {
  const v = raw(snapshot); same(v, raw(original), immutable);
  if (v.state !== "quiesced" || v.reason !== "never_opened_terminal" || v.taskState !== "cancelled" || v.attempt !== 0 || v.fence !== "0" || v.observations !== 0 || v.submittedEvents !== 1 || v.cancelledEvents !== 1 || v.executionEvents !== 0 || v.otherEvents !== 0) fail();
}
export function assertRunningOpened(snapshot: DirectoryV4LedgerSnapshot): void {
  const v = raw(snapshot);
  if (v.state !== "opened" || v.taskState !== "running" || v.attempt !== 1 || !v.liveOpenerMatched || v.observations !== 0 || v.submittedEvents !== 1 || v.cancelledEvents !== 0 || Number(v.executionEvents) < 2 || v.otherEvents !== 0) fail();
}
export function assertExecutorReturned(snapshot: DirectoryV4LedgerSnapshot, opened: DirectoryV4LedgerSnapshot): void {
  assertRunningOpened(opened);
  const v = raw(snapshot); same(v, raw(opened), [...immutable, ...opener]);
  if (v.state !== "quiesced" || v.reason !== "executor_returned" || v.taskState !== "cancelled" || v.attempt !== 1 || v.observations !== 0 || v.submittedEvents !== 1 || v.cancelledEvents !== 1 || Number(v.executionEvents) < 3 || v.otherEvents !== 0) fail();
}

// One SELECT, one MVCC statement snapshot. No caller SQL, mutations, locks,
// procedure calls, task state injection or maintenance invocation are accepted.
export function directoryV4ObserverQuery(original: DirectoryV4Original): string {
  originalValid(original);
  const stamp = (column: string) => `to_char(${column} AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')`;
  return `SELECT row_to_json(observed) FROM (SELECT
 t.task_id AS "taskUUID",t.domain_id AS "domainId",t.actor_id::text AS "actorId",
 u.connection_revision::text AS revision,u.connection_credential_generation::text AS generation,
 u.account_id AS "accountId",u.account_credential_revision::text AS "accountRevision",
 u.grant_role_id AS "grantRole",u.grant_revision::text AS "grantRevision",u.policy_revision AS "policyRevision",
 u.state,u.quiescence_reason AS reason,t.state AS "taskState",t.attempt,t.progress,
 t.fencing_token::text AS fence,t.lease_owner AS "leaseOwner",${stamp("t.lease_until")} AS "leaseUntil",
 t.result_version::text AS "resultVersion",${stamp("t.updated_at")} AS "updatedAt",${stamp("t.terminal_at")} AS "terminalAt",
 ${stamp("u.opened_at")} AS "openedAt",${stamp("u.quiesced_at")} AS "quiescedAt",
 u.opener_owner AS "openerOwner",u.opener_fencing_token::text AS "openerFence",u.opener_attempt AS "openerAttempt",
 (t.payload_version=1 AND t.max_attempts=1 AND t.parent_task_id='' AND t.actor_id=u.actor_id AND u.dictionary_version=4) AS "identityMatched",
 (t.payload=jsonb_build_object('credentialSource','operation_account','connectionRevision',u.connection_revision::text,'connectionCredentialGeneration',u.connection_credential_generation::text,'accountId',u.account_id,'accountCredentialRevision',u.account_credential_revision::text,'grantRoleId',u.grant_role_id,'grantRevision',u.grant_revision::text,'policyRevision',u.policy_revision,'dictionaryVersion',4)) AS "payloadMatched",
 (t.result='{}'::jsonb) AS "emptyResult",(t.cursor='{}'::jsonb) AS "emptyCursor",
 COALESCE(u.opener_owner=t.lease_owner AND u.opener_fencing_token=t.fencing_token AND u.opener_attempt=t.attempt AND t.lease_until>statement_timestamp(),false) AS "liveOpenerMatched",
 (SELECT count(*)::int FROM adtr.operation_account_dependencies d WHERE d.object_id=t.task_id AND d.consumer_kind IN ('domain.directory_read','domain.directory_read.v2','domain.directory_read.v3','domain.directory_read.v4')) AS dependencies,
 (SELECT count(*)::int FROM adtr.operation_account_dependencies d WHERE d.object_id=t.task_id AND d.consumer_kind=u.purpose AND d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.account_credential_revision=u.account_credential_revision AND d.connection_credential_generation=u.connection_credential_generation) AS "matchedDependencies",
 (SELECT count(*)::int FROM adtr.operation_account_dependencies d WHERE d.consumer_kind='domain.connection_binding' AND d.object_id=u.domain_id AND d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.account_credential_revision=u.account_credential_revision AND d.connection_credential_generation=u.connection_credential_generation) AS "bindingDependencies",
 (SELECT count(*)::int FROM adtr.domain_directory_observations o WHERE o.task_id=t.task_id) AS observations,
 (SELECT count(*)::int FROM adtr.task_events e WHERE e.task_id=t.task_id AND e.action='submitted') AS "submittedEvents",
 (SELECT count(*)::int FROM adtr.task_events e WHERE e.task_id=t.task_id AND e.action='cancel_requested') AS "cancelledEvents",
 (SELECT count(*)::int FROM adtr.task_events e WHERE e.task_id=t.task_id AND e.action IN ('leased','started','progress','finished')) AS "executionEvents",
 (SELECT count(*)::int FROM adtr.task_events e WHERE e.task_id=t.task_id AND e.action NOT IN ('submitted','cancel_requested','leased','started','progress','finished')) AS "otherEvents"
 FROM adtr.tasks t JOIN adtr.domain_directory_task_uses u ON u.tenant_id=t.tenant_id AND u.domain_id=t.domain_id AND u.task_id=t.task_id
 WHERE t.tenant_id='default' AND t.kind='domain.directory_read.v4' AND u.purpose='domain.directory_read.v4' AND t.task_id='${original.taskUUID}' AND t.domain_id='${original.domainId}' AND t.actor_id=${original.actorId}) observed`;
}
export async function readDirectoryV4Use(original: DirectoryV4Original): Promise<DirectoryV4LedgerSnapshot> {
  const sql = directoryV4ObserverQuery(original);
  const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container)) fail();
  let stdout: string;
  try {
    ({ stdout } = await run("docker", ["exec", "-e", "PGOPTIONS=-c statement_timeout=2000 -c default_transaction_read_only=on", container, "psql", "-U", "postgres", "-d", "adtr_e2e", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", sql], { encoding: "utf8", timeout: 5000, maxBuffer: 16384 }));
  } catch { throw new Error("Isolated v4 ledger observer failed"); }
  return parseDirectoryV4Use(stdout, original);
}

export type DirectoryV4ReaderCounts = {
  taskSHA256: string; originalState: "succeeded"; originalResultVersion: string;
  observationSHA256: string; tasks: number; uses: number; dependencies: number;
  observations: number; readerTasks: number; readerUses: number;
};
export function directoryV4ReaderCountQuery(input: { taskUUID: string; domainId: string; actorId: number }) {
  if (!identifier.test(input.taskUUID) || !identifier.test(input.domainId) || !Number.isSafeInteger(input.actorId) || input.actorId <= 0) fail();
  return `SELECT row_to_json(observed) FROM (SELECT t.task_id AS "originalTaskUUID",t.state AS "originalState",t.result_version::text AS "originalResultVersion",o.sha256 AS "observationSHA256",
 (SELECT count(*)::int FROM adtr.tasks x WHERE x.tenant_id=t.tenant_id AND x.domain_id=t.domain_id) AS tasks,
 (SELECT count(*)::int FROM adtr.domain_directory_task_uses x WHERE x.tenant_id=t.tenant_id AND x.domain_id=t.domain_id) AS uses,
 (SELECT count(*)::int FROM adtr.operation_account_dependencies x WHERE x.tenant_id=t.tenant_id AND x.domain_id=t.domain_id) AS dependencies,
 (SELECT count(*)::int FROM adtr.domain_directory_observations x WHERE x.tenant_id=t.tenant_id AND x.domain_id=t.domain_id) AS observations,
 (SELECT count(*)::int FROM adtr.tasks x WHERE x.tenant_id=t.tenant_id AND x.domain_id=t.domain_id AND x.actor_id=${input.actorId}) AS "readerTasks",
 (SELECT count(*)::int FROM adtr.domain_directory_task_uses x WHERE x.tenant_id=t.tenant_id AND x.domain_id=t.domain_id AND x.actor_id=${input.actorId}) AS "readerUses"
 FROM adtr.tasks t JOIN adtr.domain_directory_observations o ON o.task_id=t.task_id AND o.tenant_id=t.tenant_id AND o.domain_id=t.domain_id AND o.dictionary_version=4
 WHERE t.tenant_id='default' AND t.kind='domain.directory_read.v4' AND t.task_id='${input.taskUUID}' AND t.domain_id='${input.domainId}' AND t.state='succeeded') observed`;
}
export function parseDirectoryV4ReaderCounts(text: string, taskUUID: string): DirectoryV4ReaderCounts {
  if (!identifier.test(taskUUID) || text.includes("\\") || Buffer.byteLength(text) > 4096) fail();
  let v: Record<string, unknown>;
  try { v = JSON.parse(text); } catch { return fail(); }
  const counts = ["tasks", "uses", "dependencies", "observations", "readerTasks", "readerUses"];
  const expected = [...counts, "originalTaskUUID", "originalState", "originalResultVersion", "observationSHA256"].sort();
  const tokens = [...text.matchAll(/"([A-Za-z0-9]+)"\s*:/gu)].map(match => match[1]);
  if (!v || typeof v !== "object" || Array.isArray(v) || Object.keys(v).sort().join(" ") !== expected.join(" ") ||
    tokens.length !== expected.length || new Set(tokens).size !== expected.length ||
    v.originalTaskUUID !== taskUUID || v.originalState !== "succeeded" ||
    typeof v.originalResultVersion !== "string" || !decimal.test(v.originalResultVersion) ||
    typeof v.observationSHA256 !== "string" || !/^[0-9a-f]{64}$/u.test(v.observationSHA256) ||
    counts.some(key => !Number.isSafeInteger(v[key]) || Number(v[key]) < 0 || Number(v[key]) > 100000) ||
    Number(v.tasks) < 1 || Number(v.uses) < 1 || Number(v.observations) < 1 || v.readerTasks !== 0 || v.readerUses !== 0) fail();
  return { taskSHA256: digest([taskUUID]), originalState: "succeeded", originalResultVersion: v.originalResultVersion as string,
    observationSHA256: v.observationSHA256 as string, tasks: Number(v.tasks), uses: Number(v.uses), dependencies: Number(v.dependencies),
    observations: Number(v.observations), readerTasks: Number(v.readerTasks), readerUses: Number(v.readerUses) };
}
export async function readDirectoryV4ReaderCounts(input: { taskUUID: string; domainId: string; actorId: number }) {
  const sql = directoryV4ReaderCountQuery(input), container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container)) fail();
  let stdout: string;
  try {
    ({ stdout } = await run("docker", ["exec", "-e", "PGOPTIONS=-c statement_timeout=2000 -c default_transaction_read_only=on", container, "psql", "-U", "postgres", "-d", "adtr_e2e", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", sql], { encoding: "utf8", timeout: 5000, maxBuffer: 4096 }));
  } catch { throw new Error("Isolated v4 reader count observer failed"); }
  return { ...parseDirectoryV4ReaderCounts(stdout, input.taskUUID),
    readerActorSHA256: digest([String(input.actorId)]), domainSHA256: digest([input.domainId]) };
}

export type DirectoryV4KeyInput = {
  domainId: string; actorId: number; expectedRevision: string;
  expectedCredentialGeneration: string; idempotencyKey: string;
};
export function directoryV4KeyQuery(input: DirectoryV4KeyInput) {
  if (!identifier.test(input.domainId) || !identifier.test(input.idempotencyKey) || !Number.isSafeInteger(input.actorId) || input.actorId <= 0 || !positive.test(input.expectedRevision) || !positive.test(input.expectedCredentialGeneration)) fail();
  // t key scope matches actual admission uniqueness. Count conflicting actors as
  // an identity error rather than filtering them away into a false zero result.
  return `SELECT row_to_json(observed) FROM (SELECT count(*)::int AS tasks,count(u.task_id)::int AS uses,count(o.task_id)::int AS observations,
 min(t.task_id) AS "taskUUID",min(t.state) AS "taskState",min(o.sha256) AS "observationSHA256",
 COALESCE(bool_and(t.actor_id=${input.actorId} AND t.payload_version=1 AND t.max_attempts=1 AND t.parent_task_id='' AND u.task_id IS NOT NULL AND u.actor_id=t.actor_id AND u.purpose=t.kind AND u.dictionary_version=4 AND u.connection_revision=${input.expectedRevision} AND u.connection_credential_generation=${input.expectedCredentialGeneration} AND adtr.domain_directory_use_pins(u)),true) AS "identityMatched"
 FROM adtr.tasks t LEFT JOIN adtr.domain_directory_task_uses u ON u.task_id=t.task_id AND u.tenant_id=t.tenant_id AND u.domain_id=t.domain_id
 LEFT JOIN adtr.domain_directory_observations o ON o.task_id=t.task_id AND o.tenant_id=t.tenant_id AND o.domain_id=t.domain_id AND o.dictionary_version=4
 WHERE t.tenant_id='default' AND t.domain_id='${input.domainId}' AND t.kind='domain.directory_read.v4' AND t.idempotency_key='${input.idempotencyKey}') observed`;
}
export function parseDirectoryV4KeyCounts(text: string, input: DirectoryV4KeyInput) {
  directoryV4KeyQuery(input);
  if (text.includes("\\") || Buffer.byteLength(text) > 4096) fail();
  let v: Record<string, unknown>;
  try { v = JSON.parse(text); } catch { return fail(); }
  const expected = ["tasks", "uses", "observations", "taskUUID", "taskState", "observationSHA256", "identityMatched"].sort();
  const tokens = [...text.matchAll(/"([A-Za-z0-9]+)"\s*:/gu)].map(match => match[1]);
  if (!v || typeof v !== "object" || Array.isArray(v) || Object.keys(v).sort().join(" ") !== expected.join(" ") || tokens.length !== expected.length || new Set(tokens).size !== expected.length ||
    ![0,1].includes(v.tasks as number) || v.uses !== v.tasks || ![0,1].includes(v.observations as number) || v.identityMatched !== true) fail();
  if (v.tasks === 0 && (v.observations !== 0 || v.taskUUID !== null || v.taskState !== null || v.observationSHA256 !== null)) fail();
  if (v.tasks === 1 && (typeof v.taskUUID !== "string" || !identifier.test(v.taskUUID) || v.taskState !== "succeeded" || v.observations !== 1 || typeof v.observationSHA256 !== "string" || !/^[0-9a-f]{64}$/u.test(v.observationSHA256))) fail();
  return { actorSHA256: digest([String(input.actorId)]), domainSHA256: digest([input.domainId]), sourceSHA256: digest([input.domainId,input.expectedRevision,input.expectedCredentialGeneration]), keySHA256: digest([input.idempotencyKey]),
    taskSHA256: v.taskUUID === null ? null : digest([v.taskUUID]), taskState: v.taskState as "succeeded" | null,
    tasks: Number(v.tasks), uses: Number(v.uses), observations: Number(v.observations), observationSHA256: v.observationSHA256 as string | null, identityMatched: true };
}
export async function readDirectoryV4KeyCounts(input: DirectoryV4KeyInput) {
  const sql = directoryV4KeyQuery(input), container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container)) fail();
  let stdout: string;
  try {
    ({ stdout } = await run("docker", ["exec", "-e", "PGOPTIONS=-c statement_timeout=2000 -c default_transaction_read_only=on", container, "psql", "-U", "postgres", "-d", "adtr_e2e", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", sql], { encoding: "utf8", timeout: 5000, maxBuffer: 4096 }));
  } catch { throw new Error("Isolated v4 original-key observer failed"); }
  return parseDirectoryV4KeyCounts(stdout, input);
}
