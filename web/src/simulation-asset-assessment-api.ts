import { ApiError, type Profile } from "./api";
export const assessmentContract = "adtr.simulation-asset-assessment.v1";
export const prefix = "/api/simulation/asset-assessment";
export type Finding = { value: string; reason: string };
export type AssetRow = {
  version: string;
  source: string;
  tenantId: string;
  domainId: string;
  objectId: string;
  observationId: string;
  kind: string;
  observedAt: string;
  asOf: string;
  accountStatus: Finding;
  activity: Finding;
  groupType: Finding;
  groupScope: Finding;
};
export type Summary = {
  total: number;
  complete: boolean;
  kinds: Record<string, number>;
  accountStatus: Record<string, number>;
  activity: Record<string, number>;
  groupType: Record<string, number>;
  groupScope: Record<string, number>;
  reasons: Record<string, number>;
};
export type Observation = {
  contract: string;
  simulation: true;
  id: string;
  tenantId: string;
  domainId: string;
  complete: boolean;
  completenessReason: string;
  objects: unknown[];
  scenarios: { id: string; asOf: string }[];
  hash: string;
};
export type CreateInput = {
  id: string;
  operationId: string;
  observationId: string;
  expectedSourceHash: string;
  scenarioId: string;
};
export type Report = {
  contract: string;
  simulation: true;
  id: string;
  tenantId: string;
  domainId: string;
  actorId: number;
  epoch: number;
  observationId: string;
  sourceHash: string;
  scenarioId: string;
  version: string;
  asOf: string;
  createdAt: string;
  rows: AssetRow[];
  summary: Summary;
  hash: string;
};
export type Receipt = {
  contract: string;
  simulation: true;
  operationId: string;
  requestHash: string;
  input: CreateInput;
  report: Report;
};
export type ReportSummary = Pick<
  Report,
  "id" | "sourceHash" | "scenarioId" | "asOf" | "createdAt" | "hash" | "summary"
>;
export type History = {
  total: number;
  offset: number;
  limit: number;
  reports: ReportSummary[];
};
export type RowsPage = {
  reportId: string;
  reportHash: string;
  summary: Summary;
  matched: number;
  offset: number;
  limit: number;
  rows: AssetRow[];
};
export type Filters = {
  kind: string;
  accountStatus: string;
  activity: string;
  groupType: string;
  groupScope: string;
  offset: number;
  limit: number;
};
export const emptyFilters: Filters = {
  kind: "",
  accountStatus: "",
  activity: "",
  groupType: "",
  groupScope: "",
  offset: 0,
  limit: 20,
};
const object = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const string = (v: unknown): v is string =>
  typeof v === "string" && v.length > 0 && v.length <= 512;
const uint = (v: unknown): v is number =>
  Number.isSafeInteger(v) && Number(v) >= 0;
const hash = (v: unknown) => typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
const date = (v: unknown) =>
  typeof v === "string" && Number.isFinite(Date.parse(v));
const envelope = (v: Record<string, unknown>) =>
  v.contract === assessmentContract && v.simulation === true;
const counts = (v: unknown) =>
  object(v) &&
  Object.entries(v).every(([k, n]) => string(k) && uint(n) && n <= 256);
export function canonical(v: unknown): string {
  if (Array.isArray(v)) return "[" + v.map(canonical).join(",") + "]";
  if (object(v))
    return (
      "{" +
      Object.keys(v)
        .sort()
        .map((k) => JSON.stringify(k) + ":" + canonical(v[k]))
        .join(",") +
      "}"
    );
  return JSON.stringify(v);
}
export function validSummary(v: unknown): v is Summary {
  return (
    object(v) &&
    uint(v.total) &&
    v.total <= 64 &&
    typeof v.complete === "boolean" &&
    [
      "kinds",
      "accountStatus",
      "activity",
      "groupType",
      "groupScope",
      "reasons",
    ].every((k) => counts(v[k])) &&
    ["kinds", "accountStatus", "activity", "groupType", "groupScope"].every(
      (k) =>
        Object.values(v[k] as Record<string, number>).reduce(
          (a, b) => a + b,
          0,
        ) === v.total,
    )
  );
}
const finding = (v: unknown) =>
  object(v) &&
  string(v.value) &&
  [
    "missing_evidence",
    "stale_observation",
    "asset_kind",
    "explicit_security_flag",
    "explicit_scope",
    "explicit_disabled_flag",
    "explicit_flags_clear",
    "explicit_locked_flag",
    "explicit_never_assertion",
    "last_login_within_window",
    "last_login_at_or_before_boundary",
  ].includes(String(v.reason));
const values: Record<string, string[]> = {
  accountStatus: ["enabled", "disabled", "locked", "unknown", "not_applicable"],
  activity: [
    "recent",
    "inactive",
    "never_observed",
    "unknown",
    "not_applicable",
  ],
  groupType: ["security", "distribution", "unknown", "not_applicable"],
  groupScope: [
    "global",
    "domain_local",
    "universal",
    "unknown",
    "not_applicable",
  ],
};
export function validRow(v: unknown): v is AssetRow {
  return (
    object(v) &&
    v.version === "simulated-asset-v1" &&
    v.source === "synthetic" &&
    ["tenantId", "domainId", "objectId", "observationId"].every((k) =>
      string(v[k]),
    ) &&
    ["user", "computer", "group"].includes(String(v.kind)) &&
    date(v.observedAt) &&
    date(v.asOf) &&
    ["accountStatus", "activity", "groupType", "groupScope"].every(
      (k) =>
        finding(v[k]) && values[k].includes(String((v[k] as Finding).value)),
    )
  );
}
function summaryMatchesRows(summary: Summary, rows: AssetRow[]): boolean {
  const derived: Summary = {
    total: rows.length,
    complete: summary.complete,
    kinds: {},
    accountStatus: {},
    activity: {},
    groupType: {},
    groupScope: {},
    reasons: {},
  };
  for (const row of rows) {
    derived.kinds[row.kind] = (derived.kinds[row.kind] ?? 0) + 1;
    for (const key of [
      "accountStatus",
      "activity",
      "groupType",
      "groupScope",
    ] as const) {
      const f = row[key];
      derived[key][f.value] = (derived[key][f.value] ?? 0) + 1;
      derived.reasons[f.reason] = (derived.reasons[f.reason] ?? 0) + 1;
    }
  }
  return canonical(summary) === canonical(derived);
}
export function validReport(v: unknown): v is Report {
  return (
    object(v) &&
    envelope(v) &&
    ["id", "tenantId", "domainId", "observationId", "scenarioId"].every((k) =>
      string(v[k]),
    ) &&
    uint(v.actorId) &&
    v.actorId > 0 &&
    uint(v.epoch) &&
    v.epoch > 0 &&
    v.version === "simulated-asset-v1" &&
    v.observationId === "synthetic-assets-v1" &&
    ["baseline", "freshness-boundary", "stale"].includes(
      String(v.scenarioId),
    ) &&
    hash(v.sourceHash) &&
    hash(v.hash) &&
    date(v.asOf) &&
    date(v.createdAt) &&
    validSummary(v.summary) &&
    Array.isArray(v.rows) &&
    v.rows.length === v.summary.total &&
    v.rows.every(
      (r) =>
        validRow(r) &&
        r.domainId === v.domainId &&
        r.tenantId === v.tenantId &&
        r.observationId === v.observationId &&
        r.asOf === v.asOf,
    ) &&
    new Set(v.rows.map((r) => r.objectId)).size === v.rows.length &&
    summaryMatchesRows(v.summary, v.rows)
  );
}
export function verifyReceipt(
  v: unknown,
  input: CreateInput | undefined,
  domainId: string,
  actorId: number,
  operationId: string,
): v is Receipt {
  if (
    !object(v) ||
    !envelope(v) ||
    v.operationId !== operationId ||
    !hash(v.requestHash) ||
    !object(v.input) ||
    !validReport(v.report)
  )
    return false;
  const i = v.input,
    r = v.report;
  return (
    [
      "id",
      "operationId",
      "observationId",
      "expectedSourceHash",
      "scenarioId",
    ].every(
      (k) => string(i[k]) && (!input || i[k] === input[k as keyof CreateInput]),
    ) &&
    Object.keys(i).length === 5 &&
    i.operationId === operationId &&
    r.id === i.id &&
    r.observationId === i.observationId &&
    r.sourceHash === i.expectedSourceHash &&
    r.scenarioId === i.scenarioId &&
    r.domainId === domainId &&
    r.actorId === actorId
  );
}
function validObservation(v: unknown, domain: string): v is Observation {
  return (
    object(v) &&
    envelope(v) &&
    v.id === "synthetic-assets-v1" &&
    v.domainId === domain &&
    string(v.tenantId) &&
    hash(v.hash) &&
    typeof v.complete === "boolean" &&
    typeof v.completenessReason === "string" &&
    Array.isArray(v.objects) &&
    v.objects.length <= 64 &&
    Array.isArray(v.scenarios) &&
    v.scenarios.length > 0 &&
    v.scenarios.length <= 3 &&
    v.scenarios.every(
      (s) =>
        object(s) &&
        ["baseline", "freshness-boundary", "stale"].includes(String(s.id)) &&
        date(s.asOf),
    ) &&
    new Set(v.scenarios.map((s) => s.id)).size === v.scenarios.length
  );
}
const q = (v: Record<string, string | number>) =>
  new URLSearchParams(
    Object.entries(v).map(([k, x]) => [k, String(x)]),
  ).toString();
async function request(
  path: string,
  p: Profile,
  signal: AbortSignal,
  body?: unknown,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(prefix + path, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      cache: "no-store",
      signal,
      headers:
        body === undefined
          ? {}
          : { "Content-Type": "application/json", "X-CSRF-Token": p.csrfToken },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  } catch (e) {
    if (signal.aborted) throw e;
    throw new ApiError("network");
  }
  if (response.ok && response.headers.get("X-ADTR-User-ID") !== String(p.ID))
    throw new ApiError("unauthenticated", 401);
  const raw = await response.text();
  if (new TextEncoder().encode(raw).length > 524288)
    throw new ApiError("invalid_response");
  let v: unknown;
  try {
    v = JSON.parse(raw);
  } catch {
    throw new ApiError("invalid_response");
  }
  if (!response.ok)
    throw new ApiError(
      object(v) && typeof v.error === "string" ? v.error : "internal",
      response.status,
    );
  return v;
}
function checked<T>(v: unknown, valid: boolean): T {
  if (!valid) throw new ApiError("invalid_response");
  return v as T;
}
export const simulationAssetAssessmentAPI = {
  observation: async (domainId: string, p: Profile, s: AbortSignal) => {
    const v = await request(
      "/observation?" + q({ domainId, observationId: "synthetic-assets-v1" }),
      p,
      s,
    );
    return checked<Observation>(v, validObservation(v, domainId));
  },
  create: async (
    domainId: string,
    input: CreateInput,
    p: Profile,
    s: AbortSignal,
  ) => {
    const v = await request("/create", p, s, { domainId, ...input });
    return checked<Receipt>(
      v,
      verifyReceipt(v, input, domainId, p.ID, input.operationId),
    );
  },
  receipt: async (
    domainId: string,
    operationId: string,
    p: Profile,
    s: AbortSignal,
    input?: CreateInput,
  ) => {
    const v = await request("/receipt?" + q({ domainId, operationId }), p, s);
    return checked<Receipt>(
      v,
      verifyReceipt(v, input, domainId, p.ID, operationId),
    );
  },
  detail: async (domainId: string, id: string, p: Profile, s: AbortSignal) => {
    const v = await request("/detail?" + q({ domainId, id }), p, s);
    return checked<Report>(
      v,
      validReport(v) &&
        v.domainId === domainId &&
        v.actorId === p.ID &&
        v.id === id,
    );
  },
  history: async (
    domainId: string,
    offset: number,
    p: Profile,
    s: AbortSignal,
  ) => {
    const v = await request(
      "/history?" + q({ domainId, offset, limit: 20 }),
      p,
      s,
    );
    return checked<History>(
      v,
      object(v) &&
        uint(v.total) &&
        v.total <= 64 &&
        v.offset === offset &&
        v.limit === 20 &&
        Array.isArray(v.reports) &&
        v.reports.length === Math.min(20, Math.max(0, v.total - offset)) &&
        new Set(v.reports.map((r) => (object(r) ? r.id : null))).size ===
          v.reports.length &&
        v.reports.every(
          (r) =>
            object(r) &&
            string(r.id) &&
            hash(r.hash) &&
            hash(r.sourceHash) &&
            string(r.scenarioId) &&
            date(r.asOf) &&
            date(r.createdAt) &&
            validSummary(r.summary),
        ),
    );
  },
  rows: async (
    domainId: string,
    report: Report,
    filters: Filters,
    p: Profile,
    s: AbortSignal,
  ) => {
    const v = await request(
      "/rows?" + q({ domainId, id: report.id, ...filters }),
      p,
      s,
    );
    const matched = report.rows.filter(
      (r) =>
        (!filters.kind || r.kind === filters.kind) &&
        (
          ["accountStatus", "activity", "groupType", "groupScope"] as const
        ).every((k) => !filters[k] || r[k].value === filters[k]),
    );
    return checked<RowsPage>(
      v,
      object(v) &&
        v.reportId === report.id &&
        v.reportHash === report.hash &&
        validSummary(v.summary) &&
        canonical(v.summary) === canonical(report.summary) &&
        uint(v.matched) &&
        v.matched === matched.length &&
        v.offset === filters.offset &&
        v.limit === filters.limit &&
        Array.isArray(v.rows) &&
        canonical(v.rows) ===
          canonical(
            matched.slice(filters.offset, filters.offset + filters.limit),
          ) &&
        v.rows.every(
          (r) =>
            validRow(r) &&
            report.rows.some((a) => canonical(a) === canonical(r)),
        ),
    );
  },
};
