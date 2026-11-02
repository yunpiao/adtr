import { ApiError, type Profile } from "./api";
export const exceptionContract = "adtr.simulation-detection-exceptions.v1";
export const exceptionFields = [
  "event.category",
  "event.outcome",
  "actor.name",
  "target.name",
  "source.ip",
] as const;
export type Condition = {
  field: string;
  operator: "eq" | "in";
  values: string[];
};
export type Declaration = {
  id: string;
  global: boolean;
  sourceTaskId: string;
  ruleId: string;
  reason: string;
  notBefore: string;
  expiresAt: string;
  conditions: Condition[];
};
export type ExceptionEntry = {
  id: string;
  scope: { tenant: string; domain: string };
  rule_id?: string;
  rule_digest?: string;
  global: boolean;
  enabled: boolean;
  archived: boolean;
  reason: string;
  not_before: string;
  expires_at: string;
  conditions: Condition[];
};
export type ExceptionRecord = {
  contract: string;
  simulation: true;
  snapshot: {
    contract: string;
    provenance: string;
    scope: { tenant: string; domain: string };
    revision: number;
    entries: ExceptionEntry[];
  };
  sourceTasks: Record<string, string>;
  actorId: number;
  action: string;
  at: string;
  hash: string;
};
export type Match = {
  contract: string;
  provenance: string;
  rule_id: string;
  rule_version: number;
  rule_digest: string;
  event_id: string;
  scope: { tenant: string; domain: string };
};
export type Evaluation = {
  contract: string;
  simulation: true;
  id: string;
  actorId: number;
  sourceTaskId: string;
  sourceResultHash: string;
  source: {
    simulation: true;
    contract: string;
    matches: Match[];
    [key: string]: unknown;
  };
  revision: number;
  snapshotHash: string;
  evaluatedAt: string;
  matches: {
    match: Match;
    exceptions: { exception_id: string; reason: string; simulated: boolean }[];
  }[];
  hash: string;
};
export type Action =
  | "create"
  | "edit"
  | "enable"
  | "disable"
  | "archive"
  | "import"
  | "evaluate";
export type Write = {
  domainId: string;
  operationId: string;
  expectedRevision: number;
  id?: string;
  declaration?: Declaration;
  entries?: Declaration[];
  sourceTaskId?: string;
};
export type Preview = {
  domainId: string;
  sourceTaskId: string;
  eventId: string;
  ruleId: string;
  id: string;
  reason: string;
  notBefore: string;
  expiresAt: string;
};
export type EvaluationSummary = {
  id: string;
  sourceTaskId: string;
  revision: number;
  evaluatedAt: string;
};
export type Intent = {
  domainId: string;
  action: string;
  operationId: string;
  expectedRevision: number;
  id: string;
  declaration: Declaration | null;
  entries: Declaration[] | null;
  sourceTaskId: string;
};
export type Receipt = {
  contract: string;
  simulation: true;
  operationId: string;
  action: string;
  actorId: number;
  at: string;
  intent: Intent;
  requestHash: string;
  revision: number;
  snapshotHash: string;
  evaluation?: Evaluation;
};
const obj = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const uint = (v: unknown) => Number.isSafeInteger(v) && (v as number) >= 0;
const hash = (v: unknown) => typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
const text = (v: unknown, n = 128) =>
  typeof v === "string" &&
  v.length > 0 &&
  new TextEncoder().encode(v).length <= n &&
  !/[\x00-\x1f\x7f]/.test(v);
const date = (v: unknown) =>
  typeof v === "string" && Number.isFinite(Date.parse(v));
function declarationTime(v: unknown): v is string {
  if (
    typeof v !== "string" ||
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,3})?Z$/.test(v) ||
    !Number.isFinite(Date.parse(v))
  )
    return false;
  const trim = (x: string) =>
    x.replace(/\.0+Z$/, "Z").replace(/(\.\d*?[1-9])0+Z$/, "$1Z");
  return trim(new Date(v).toISOString()) === trim(v);
}
export function validConditions(v: unknown): v is Condition[] {
  return (
    Array.isArray(v) &&
    v.length > 0 &&
    v.length <= 5 &&
    new Set(v.map((c) => (obj(c) ? c.field : null))).size === v.length &&
    v.every(
      (c) =>
        obj(c) &&
        Object.keys(c).every((k) =>
          ["field", "operator", "values"].includes(k),
        ) &&
        exceptionFields.includes(c.field as (typeof exceptionFields)[number]) &&
        ["eq", "in"].includes(String(c.operator)) &&
        Array.isArray(c.values) &&
        c.values.length > 0 &&
        c.values.length <= (c.operator === "eq" ? 1 : 32) &&
        c.values.every((x) => text(x, 512)),
    )
  );
}
export function validDeclaration(v: unknown): v is Declaration {
  return (
    obj(v) &&
    Object.keys(v).every((k) =>
      [
        "id",
        "global",
        "sourceTaskId",
        "ruleId",
        "reason",
        "notBefore",
        "expiresAt",
        "conditions",
      ].includes(k),
    ) &&
    text(v.id) &&
    typeof v.global === "boolean" &&
    typeof v.sourceTaskId === "string" &&
    typeof v.ruleId === "string" &&
    (v.global
      ? v.sourceTaskId === "" && v.ruleId === ""
      : text(v.sourceTaskId) && text(v.ruleId)) &&
    text(v.reason, 512) &&
    declarationTime(v.notBefore) &&
    declarationTime(v.expiresAt) &&
    Date.parse(v.notBefore as string) < Date.parse(v.expiresAt as string) &&
    validConditions(v.conditions)
  );
}
export function parseDraftImport(raw: string): Declaration[] {
  if (new TextEncoder().encode(raw).length > 256 * 1024)
    throw new Error("导入最多 256 KiB。");
  const v: unknown = JSON.parse(raw);
  if (
    !Array.isArray(v) ||
    !v.length ||
    v.length > 32 ||
    !v.every(validDeclaration) ||
    new Set(v.map((x) => x.id)).size !== v.length
  )
    throw new Error(
      "需要 1–32 个不重复的禁用草稿声明；不接受摘要、身份、历史或启用字段。",
    );
  return v;
}
const scope = (v: unknown) => obj(v) && text(v.tenant) && text(v.domain);
const match = (v: unknown): v is Match =>
  obj(v) &&
  text(v.rule_id) &&
  text(v.event_id) &&
  uint(v.rule_version) &&
  hash(v.rule_digest) &&
  scope(v.scope);
export function validRecord(v: unknown): v is ExceptionRecord {
  return (
    obj(v) &&
    v.contract === exceptionContract &&
    v.simulation === true &&
    hash(v.hash) &&
    obj(v.snapshot) &&
    v.snapshot.contract === "adtr.detection-exceptions.simulated.v1" &&
    scope(v.snapshot.scope) &&
    uint(v.snapshot.revision) &&
    Array.isArray(v.snapshot.entries) &&
    v.snapshot.entries.length <= 32 &&
    v.snapshot.entries.every(
      (e) =>
        obj(e) &&
        text(e.id) &&
        scope(e.scope) &&
        typeof e.global === "boolean" &&
        typeof e.enabled === "boolean" &&
        typeof e.archived === "boolean" &&
        !(e.archived && e.enabled) &&
        text(e.reason, 512) &&
        date(e.not_before) &&
        date(e.expires_at) &&
        validConditions(e.conditions) &&
        (e.global
          ? !e.rule_id && !e.rule_digest
          : text(e.rule_id) && hash(e.rule_digest)),
    ) &&
    obj(v.sourceTasks) &&
    Object.values(v.sourceTasks).every((x) => typeof x === "string")
  );
}
export function validEvaluation(v: unknown): v is Evaluation {
  return (
    obj(v) &&
    v.contract === exceptionContract &&
    v.simulation === true &&
    text(v.id) &&
    uint(v.actorId) &&
    text(v.sourceTaskId) &&
    hash(v.sourceResultHash) &&
    hash(v.hash) &&
    hash(v.snapshotHash) &&
    uint(v.revision) &&
    date(v.evaluatedAt) &&
    obj(v.source) &&
    v.source.simulation === true &&
    Array.isArray(v.source.matches) &&
    Array.isArray(v.matches) &&
    v.matches.length === v.source.matches.length &&
    v.matches.every(
      (a, i) =>
        obj(a) &&
        match(a.match) &&
        canonical(a.match) ===
          canonical((v.source as { matches: unknown[] }).matches[i]) &&
        Array.isArray(a.exceptions) &&
        a.exceptions.every(
          (e) =>
            obj(e) &&
            text(e.exception_id) &&
            text(e.reason, 512) &&
            e.simulated === true,
        ),
    )
  );
}
export function canonical(v: unknown): string {
  function order(x: unknown): unknown {
    return Array.isArray(x)
      ? x.map(order)
      : obj(x)
        ? Object.fromEntries(
            Object.keys(x)
              .sort()
              .map((k) => [k, order(x[k])]),
          )
        : x;
  }
  return JSON.stringify(order(v));
}
export function receiptIntent(action: Action, w: Write): Intent {
  return {
    domainId: w.domainId,
    action,
    operationId: w.operationId,
    expectedRevision: w.expectedRevision,
    id: w.id ?? "",
    declaration: w.declaration ?? null,
    entries: w.entries ?? null,
    sourceTaskId: w.sourceTaskId ?? "",
  };
}
function normalizedIntent(v: Intent): string {
  const normalize = (d: Declaration) => {
    if (!validDeclaration(d)) throw new ApiError("invalid_response");
    return {
      ...d,
      notBefore: new Date(d.notBefore).toISOString(),
      expiresAt: new Date(d.expiresAt).toISOString(),
    };
  };
  try {
    return canonical({
      ...v,
      declaration: v.declaration ? normalize(v.declaration) : null,
      entries: v.entries ? v.entries.map(normalize) : null,
    });
  } catch {
    throw new ApiError("invalid_response");
  }
}
export function verifyReceipt(
  r: Receipt,
  action: Action,
  w: Write,
  actor: number,
) {
  if (
    !obj(r) ||
    r.contract !== exceptionContract ||
    r.simulation !== true ||
    r.actorId !== actor ||
    r.operationId !== w.operationId ||
    r.action !== action ||
    !hash(r.requestHash) ||
    !hash(r.snapshotHash) ||
    !uint(r.revision) ||
    normalizedIntent(r.intent) !== normalizedIntent(receiptIntent(action, w)) ||
    (action === "evaluate" &&
      (!validEvaluation(r.evaluation) ||
        r.evaluation.sourceTaskId !== w.sourceTaskId ||
        r.evaluation.id !== w.id ||
        r.evaluation.actorId !== actor))
  )
    throw new ApiError("invalid_response");
}
export function parseResponse(kind: string, v: unknown): unknown {
  if (
    kind === "evaluations" &&
    (!Array.isArray(v) ||
      v.length > 64 ||
      !v.every(
        (x) =>
          obj(x) &&
          text(x.id) &&
          text(x.sourceTaskId) &&
          uint(x.revision) &&
          date(x.evaluatedAt),
      ))
  )
    throw new ApiError("invalid_response");
  if (
    (kind === "record" && !validRecord(v)) ||
    (kind === "evaluation" && !validEvaluation(v)) ||
    (kind === "preview" && !validDeclaration(v)) ||
    (kind === "receipt" &&
      (!obj(v) ||
        v.contract !== exceptionContract ||
        v.simulation !== true ||
        !obj(v.intent) ||
        !hash(v.requestHash) ||
        !uint(v.actorId) ||
        typeof v.operationId !== "string" ||
        typeof v.action !== "string"))
  )
    throw new ApiError("invalid_response");
  return v;
}
async function request<T>(
  path: string,
  kind: string,
  p: Profile,
  s: AbortSignal,
  body?: unknown,
): Promise<T> {
  let r: Response;
  try {
    r = await fetch("/api/simulation/detection-exceptions" + path, {
      method: body ? "POST" : "GET",
      credentials: "same-origin",
      cache: "no-store",
      signal: s,
      headers: body
        ? { "Content-Type": "application/json", "X-CSRF-Token": p.csrfToken }
        : {},
      ...(body ? { body: JSON.stringify(body) } : {}),
    });
  } catch (e) {
    if (s.aborted) throw e;
    throw new ApiError("network");
  }
  if (r.ok && r.headers.get("X-ADTR-User-ID") !== String(p.ID))
    throw new ApiError("unauthenticated", 401);
  const t = await r.text();
  if (new TextEncoder().encode(t).length > 512 * 1024)
    throw new ApiError("invalid_response");
  let v: unknown;
  try {
    v = JSON.parse(t);
  } catch {
    throw new ApiError("invalid_response", r.status);
  }
  if (!r.ok)
    throw new ApiError(
      obj(v) && typeof v.error === "string" ? v.error : "internal",
      r.status,
    );
  const parsed = parseResponse(kind, v);
  const query = new URLSearchParams(path.split("?")[1] ?? "");
  const domain = obj(body) ? body.domainId : query.get("domainId");
  if (kind === "record") {
    const rec = parsed as ExceptionRecord;
    if (
      rec.snapshot.scope.domain !== domain ||
      rec.snapshot.entries.some(
        (e) => canonical(e.scope) !== canonical(rec.snapshot.scope),
      ) ||
      (query.has("revision") &&
        rec.snapshot.revision !== Number(query.get("revision")))
    )
      throw new ApiError("invalid_response");
  }
  if (kind === "evaluation") {
    const ev = parsed as Evaluation;
    if (
      ev.actorId !== p.ID ||
      ev.id !== query.get("id") ||
      ev.matches.some((x) => x.match.scope.domain !== domain)
    )
      throw new ApiError("invalid_response");
  }
  if (kind === "preview" && obj(body)) {
    const d = parsed as Declaration;
    if (
      d.global ||
      d.sourceTaskId !== body.sourceTaskId ||
      d.ruleId !== body.ruleId ||
      d.id !== body.id ||
      d.reason !== body.reason ||
      Date.parse(d.notBefore) !== Date.parse(String(body.notBefore)) ||
      Date.parse(d.expiresAt) !== Date.parse(String(body.expiresAt))
    )
      throw new ApiError("invalid_response");
  }
  return parsed as T;
}
const q = (v: Record<string, string | number>) =>
  new URLSearchParams(
    Object.entries(v).map(([k, x]) => [k, String(x)]),
  ).toString();
export const simulationDetectionExceptionsAPI = {
  evaluations: (domainId: string, p: Profile, s: AbortSignal) =>
    request<EvaluationSummary[]>(
      `/evaluations?${q({ domainId })}`,
      "evaluations",
      p,
      s,
    ),
  snapshot: (domainId: string, p: Profile, s: AbortSignal) =>
    request<ExceptionRecord>(`/snapshot?${q({ domainId })}`, "record", p, s),
  history: (domainId: string, revision: number, p: Profile, s: AbortSignal) =>
    request<ExceptionRecord>(
      `/history?${q({ domainId, revision })}`,
      "record",
      p,
      s,
    ),
  export: (domainId: string, p: Profile, s: AbortSignal) =>
    request<ExceptionRecord>(`/export?${q({ domainId })}`, "record", p, s),
  receipt: (
    domainId: string,
    operationId: string,
    p: Profile,
    s: AbortSignal,
  ) =>
    request<Receipt>(
      `/receipt?${q({ domainId, operationId })}`,
      "receipt",
      p,
      s,
    ),
  evaluation: (domainId: string, id: string, p: Profile, s: AbortSignal) =>
    request<Evaluation>(
      `/evaluation?${q({ domainId, id })}`,
      "evaluation",
      p,
      s,
    ),
  preview: (body: Preview, p: Profile, s: AbortSignal) =>
    request<Declaration>("/preview", "preview", p, s, body),
  write: (action: Action, body: Write, p: Profile, s: AbortSignal) =>
    request<Receipt>(`/${action}`, "receipt", p, s, body),
};
export function downloadExceptions(record: ExceptionRecord) {
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(record, null, 2)], { type: "application/json" }),
  );
  const a = document.createElement("a");
  a.href = url;
  a.download = `SIMULATED-exceptions-revision-${record.snapshot.revision}.json`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
