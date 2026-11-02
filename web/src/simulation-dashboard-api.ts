import { ApiError, type Profile } from "./api";
import {
  parseSimulationResponse,
  simulationEventAPI,
  type SimulationDataset,
} from "./simulation-event-api";

export type DashboardWindow = "1h" | "24h" | "7d";
export type DashboardQuery = {
  domainId: string;
  datasetId: string;
  subject?: string;
  window: DashboardWindow;
};
type NamedCount = { name: string; count: number };
export type DashboardSummary = {
  contract: "adtr.dashboard.simulated.v1";
  simulation: true;
  productAccepted: false;
  snapshotId: string;
  asOf: string;
  window: {
    preset: DashboardWindow;
    start: string;
    end: string;
    bucketSeconds: number;
  };
  dataset: SimulationDataset;
  coverage: {
    complete: true;
    eventCount: number;
    caseCount: number;
    sourceFirstAt: string | null;
    sourceLastAt: string | null;
    excludedFutureEvents: number;
  };
  cases: {
    scope: "dataset_current";
    total: number;
    open: number;
    closed: number;
    marks: { unmarked: number; benign: number; suspicious: number };
  };
  eventActivity: {
    scope: "selected_window";
    total: number;
    byKind: { kind: string; count: number }[];
    buckets: { start: string; countsByKind: Record<string, number> }[];
    topActors: NamedCount[];
    topDevices: NamedCount[];
    topSourceIPs: NamedCount[];
    topLimit: 5;
    distinctActors: number;
    distinctDevices: number;
  };
  caseCloseThroughput: {
    status: "available";
    unit: "case_closes_per_minute";
    windowTotal: number;
    last60Seconds: number;
    buckets: { start: string; count: number }[];
  };
  risk: {
    status: "available" | "not_selected" | "subject_not_found";
    subject?: string;
    mappingVersion?: string;
    revision?: number;
    score?: number;
    trend: { at: string; score: number }[] | null;
    resetBoundary: string | null;
  };
  latestManualDetection: {
    status: "available" | "no_result";
    taskUUID?: string;
    completedAt?: string;
    templateId?: string;
    templateVersion?: number;
    templateHash?: string;
    eventCount?: number;
    matchCount?: number;
  };
  unavailable: { metric: string; reason: string }[];
};
const object = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const text = (v: unknown): v is string =>
  typeof v === "string" && v.length > 0 && v.length <= 256;
const integer = (v: unknown): v is number =>
  Number.isSafeInteger(v) && Number(v) >= 0;
const date = (v: unknown): v is string =>
  text(v) && Number.isFinite(Date.parse(v));
const hash = (v: unknown): v is string =>
  typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
const score = (v: unknown): v is number => integer(v) && v <= 100;
const array = (v: unknown, max: number): v is unknown[] =>
  Array.isArray(v) && v.length <= max;
const kinds = new Set(["login_failure", "login_success", "service_access"]);
export function parseDashboardResponse(
  value: unknown,
  query?: DashboardQuery,
): DashboardSummary {
  const invalid = () => {
    throw new ApiError("invalid_response");
  };
  if (
    !object(value) ||
    value.contract !== "adtr.dashboard.simulated.v1" ||
    value.simulation !== true ||
    value.productAccepted !== false ||
    !hash(value.snapshotId) ||
    !date(value.asOf)
  )
    return invalid();
  try {
    parseSimulationResponse("datasets", {
      simulation: true,
      datasets: [value.dataset],
      total: 1,
    });
  } catch {
    return invalid();
  }
  const w = value.window,
    c = value.coverage,
    cases = value.cases,
    a = value.eventActivity,
    t = value.caseCloseThroughput,
    r = value.risk,
    d = value.latestManualDetection;
  if (
    !object(w) ||
    !["1h", "24h", "7d"].includes(String(w.preset)) ||
    !date(w.start) ||
    !date(w.end) ||
    w.end !== value.asOf ||
    Date.parse(w.start) >= Date.parse(w.end) ||
    w.bucketSeconds !==
      ({ "1h": 60, "24h": 3600, "7d": 86400 } as Record<string, number>)[
        String(w.preset)
      ]
  )
    return invalid();
  if (
    !object(c) ||
    c.complete !== true ||
    ![c.eventCount, c.caseCount, c.excludedFutureEvents].every(integer) ||
    Number(c.eventCount) > 4096 ||
    Number(c.caseCount) > 100 ||
    Number(c.excludedFutureEvents) > Number(c.eventCount) ||
    !(c.sourceFirstAt === null || date(c.sourceFirstAt)) ||
    !(c.sourceLastAt === null || date(c.sourceLastAt))
  )
    return invalid();
  if (
    !object(cases) ||
    cases.scope !== "dataset_current" ||
    ![cases.total, cases.open, cases.closed].every(integer) ||
    cases.total !== Number(cases.open) + Number(cases.closed) ||
    cases.total !== c.caseCount ||
    !object(cases.marks) ||
    ![cases.marks.unmarked, cases.marks.benign, cases.marks.suspicious].every(
      integer,
    ) ||
    cases.total !==
      Number(cases.marks.unmarked) +
        Number(cases.marks.benign) +
        Number(cases.marks.suspicious)
  )
    return invalid();
  const names = (v: unknown) =>
    array(v, 5) &&
    v.every((x) => object(x) && text(x.name) && integer(x.count));
  if (
    !object(a) ||
    a.scope !== "selected_window" ||
    !integer(a.total) ||
    a.total > Number(c.eventCount) ||
    a.topLimit !== 5 ||
    ![a.distinctActors, a.distinctDevices].every(integer) ||
    ![a.topActors, a.topDevices, a.topSourceIPs].every(names) ||
    !array(a.byKind, 3) ||
    a.byKind.length !== 3 ||
    new Set(a.byKind.map((x) => (object(x) ? x.kind : null))).size !== 3 ||
    !a.byKind.every(
      (x) => object(x) && kinds.has(String(x.kind)) && integer(x.count),
    ) ||
    a.byKind.reduce<number>(
      (sum, x) => sum + Number((x as Record<string, unknown>).count),
      0,
    ) !== a.total ||
    !array(a.buckets, 169) ||
    !a.buckets.every(
      (x) =>
        object(x) &&
        date(x.start) &&
        object(x.countsByKind) &&
        Object.keys(x.countsByKind).length === 3 &&
        Object.entries(x.countsByKind).every(
          ([k, n]) => kinds.has(k) && integer(n),
        ),
    )
  )
    return invalid();
  if (
    a.buckets.reduce<number>(
      (sum, x) =>
        sum +
        Object.values(
          (x as { countsByKind: Record<string, number> }).countsByKind,
        ).reduce((n, count) => n + count, 0),
      0,
    ) !== a.total
  )
    return invalid();
  if (
    !object(t) ||
    t.status !== "available" ||
    t.unit !== "case_closes_per_minute" ||
    ![t.windowTotal, t.last60Seconds].every(integer) ||
    !array(t.buckets, 10081) ||
    !t.buckets.every((x) => object(x) && date(x.start) && integer(x.count)) ||
    t.buckets.reduce<number>(
      (sum, x) => sum + Number((x as Record<string, unknown>).count),
      0,
    ) !== t.windowTotal
  )
    return invalid();
  if (
    !object(r) ||
    !["available", "not_selected", "subject_not_found"].includes(
      String(r.status),
    ) ||
    !(r.resetBoundary === null || date(r.resetBoundary))
  )
    return invalid();
  if (
    r.status === "available" &&
    (!score(r.score) ||
      !text(r.subject) ||
      r.mappingVersion !== "illustrative-event-risk-v1" ||
      !integer(r.revision) ||
      r.revision < 1 ||
      !array(r.trend, 5121) ||
      !r.trend.every((x) => object(x) && date(x.at) && score(x.score)))
  )
    return invalid();
  if (!object(d) || !["available", "no_result"].includes(String(d.status)))
    return invalid();
  if (
    d.status === "available" &&
    (![d.eventCount, d.matchCount].every(integer) ||
      !text(d.taskUUID) ||
      !date(d.completedAt) ||
      !text(d.templateId) ||
      !integer(d.templateVersion) ||
      d.templateVersion < 1 ||
      !hash(d.templateHash))
  )
    return invalid();
  if (
    (r.status !== "available" && r.score !== undefined) ||
    (d.status !== "available" &&
      (d.eventCount !== undefined || d.matchCount !== undefined))
  )
    return invalid();
  if (
    !array(value.unavailable, 30) ||
    !value.unavailable.every(
      (x) => object(x) && text(x.metric) && text(x.reason),
    )
  )
    return invalid();
  const result = value as unknown as DashboardSummary;
  if (
    result.dataset.eventCount !== c.eventCount ||
    (query &&
      (result.dataset.id !== query.datasetId ||
        result.window.preset !== query.window ||
        (!query.subject && r.status !== "not_selected") ||
        (query.subject &&
          (r.status === "not_selected" || r.subject !== query.subject))))
  )
    return invalid();
  return result;
}
export const simulationDashboardAPI = {
  datasets: simulationEventAPI.datasets,
  async summary(
    query: DashboardQuery,
    profile: Profile,
    signal: AbortSignal,
  ): Promise<DashboardSummary> {
    const params = new URLSearchParams({
      domainId: query.domainId,
      datasetId: query.datasetId,
      window: query.window,
    });
    if (query.subject) params.set("subject", query.subject);
    let response: Response;
    try {
      response = await fetch(`/api/simulation/dashboard/summary?${params}`, {
        method: "GET",
        credentials: "same-origin",
        cache: "no-store",
        signal,
      });
    } catch (e) {
      if (signal.aborted) throw e;
      throw new ApiError("network");
    }
    if (
      response.ok &&
      response.headers.get("X-ADTR-User-ID") !== String(profile.ID)
    )
      throw new ApiError("unauthenticated", 401);
    if (!response.ok)
      throw new ApiError(
        response.status === 401 ? "unauthenticated" : "dashboard_unavailable",
        response.status,
      );
    const raw = await response.text();
    if (raw.length > 2 * 1024 * 1024) throw new ApiError("invalid_response");
    let value: unknown;
    try {
      value = JSON.parse(raw);
    } catch {
      throw new ApiError("invalid_response");
    }
    return parseDashboardResponse(value, query);
  },
};
