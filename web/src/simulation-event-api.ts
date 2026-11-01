import { ApiError, type Profile } from "./api";

export type SimulationProvenance = {
  Contract: string;
  Fixture: string;
  Synthetic: true;
};
export type SimulationDataset = {
  id: string;
  fixture: string;
  fixtureHash: string;
  contract: string;
  simulation: true;
  eventCount: number;
};
export type SimulationEvent = {
  ID: string;
  Tenant: string;
  Domain: string;
  Kind: string;
  Actor: string;
  Device: string;
  Session: string;
  SourceIP: string;
  OccurredAt: string;
  Provenance: SimulationProvenance;
};
export type SimulationCase = {
  ID: string;
  State: "open" | "closed";
  Mark: "" | "suspicious" | "benign";
  Revision: number;
  Provenance: SimulationProvenance;
};
export type SimulationAudit = {
  CaseID: string;
  Action: string;
  Actor: string;
  Reason: string;
  PreviousState: string;
  State: string;
  PreviousMark: string;
  Mark: string;
  At: string;
  Revision: number;
  Provenance: SimulationProvenance;
};
export type SimulationDatasets = {
  simulation: true;
  datasets: SimulationDataset[];
  total: number;
};
export type SimulationQuery = {
  domainId: string;
  datasetId: string;
  offset: number;
  limit: number;
  kind?: string;
  actor?: string;
};
export type SimulationPage = {
  simulation: true;
  dataset: SimulationDataset;
  cases: SimulationCase[];
  page: {
    Events: SimulationEvent[];
    Total: number;
    Provenance: SimulationProvenance;
  };
};
export type SimulationDetail = {
  simulation: true;
  dataset: SimulationDataset;
  case: SimulationCase;
  evidence: SimulationEvent[];
  history: SimulationAudit[];
  historyTotal: number;
};
export type SimulationTransition = {
  domainId: string;
  datasetId: string;
  caseId: string;
  operationId: string;
  action: "mark_suspicious" | "mark_benign" | "unmark" | "close";
  reason: string;
  expectedRevision: number;
};
export type SimulationReceipt = {
  dataset: SimulationDataset;
  simulation: true;
  datasetId: string;
  operationId: string;
  case: SimulationCase;
  audit: SimulationAudit;
};
const object = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const text = (v: unknown): v is string => typeof v === "string";
const integer = (v: unknown): v is number =>
  Number.isSafeInteger(v) && Number(v) >= 0;
const provenance = (v: unknown): boolean =>
  object(v) &&
  v.Synthetic === true &&
  v.Contract === "adtr.securityevents.simulated.v1" &&
  text(v.Fixture) &&
  !!v.Fixture;
const dataset = (v: unknown): boolean =>
  object(v) &&
  v.simulation === true &&
  text(v.id) &&
  text(v.fixture) &&
  text(v.fixtureHash) &&
  v.contract === "adtr.securityevents.simulated.v1" &&
  integer(v.eventCount);
const event = (v: unknown): boolean =>
  object(v) &&
  [
    "ID",
    "Tenant",
    "Domain",
    "Kind",
    "Actor",
    "Device",
    "Session",
    "SourceIP",
    "OccurredAt",
  ].every((k) => text(v[k])) &&
  provenance(v.Provenance);
const caseView = (v: unknown): boolean =>
  object(v) &&
  text(v.ID) &&
  ["open", "closed"].includes(String(v.State)) &&
  ["", "suspicious", "benign"].includes(String(v.Mark)) &&
  integer(v.Revision) &&
  v.Revision > 0 &&
  provenance(v.Provenance);
const audit = (v: unknown): boolean =>
  object(v) &&
  [
    "CaseID",
    "Action",
    "Actor",
    "Reason",
    "PreviousState",
    "State",
    "PreviousMark",
    "Mark",
    "At",
  ].every((k) => text(v[k])) &&
  integer(v.Revision) &&
  provenance(v.Provenance);
export function parseSimulationResponse(
  kind: "datasets" | "query" | "detail" | "receipt",
  v: unknown,
): unknown {
  let valid = object(v) && v.simulation === true;
  if (valid && object(v)) {
    if (kind === "datasets")
      valid =
        Array.isArray(v.datasets) &&
        v.datasets.every(dataset) &&
        integer(v.total);
    if (kind === "query")
      valid =
        dataset(v.dataset) &&
        Array.isArray(v.cases) &&
        v.cases.every(caseView) &&
        object(v.page) &&
        Array.isArray(v.page.Events) &&
        v.page.Events.every(event) &&
        integer(v.page.Total) &&
        provenance(v.page.Provenance);
    if (kind === "detail")
      valid =
        dataset(v.dataset) &&
        caseView(v.case) &&
        Array.isArray(v.evidence) &&
        v.evidence.every(event) &&
        Array.isArray(v.history) &&
        v.history.every(audit) &&
        integer(v.historyTotal);
    if (kind === "receipt")
      valid =
        dataset(v.dataset) &&
        text(v.datasetId) &&
        text(v.operationId) &&
        caseView(v.case) &&
        audit(v.audit);
  }
  if (!valid) throw new ApiError("invalid_response");
  return v;
}
async function request<T>(
  path: string,
  kind: Parameters<typeof parseSimulationResponse>[0],
  signal: AbortSignal,
  profile: Profile,
  body?: unknown,
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api/simulation/events${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      cache: "no-store",
      signal,
      headers:
        body === undefined
          ? {}
          : {
              "Content-Type": "application/json",
              "X-CSRF-Token": profile.csrfToken,
            },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
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
  const data: unknown = await response.json().catch(() => {
    throw new ApiError("invalid_response", response.status);
  });
  if (!response.ok)
    throw new ApiError(
      object(data) && text(data.error) ? data.error : "internal",
      response.status,
    );
  return parseSimulationResponse(kind, data) as T;
}
const params = (v: Record<string, string | number>) =>
  new URLSearchParams(
    Object.entries(v).map(([k, value]) => [k, String(value)]),
  ).toString();
export const simulationEventAPI = {
  datasets: (domainId: string, profile: Profile, signal: AbortSignal) =>
    request<SimulationDatasets>(
      `/datasets?${params({ domainId, offset: 0, limit: 100 })}`,
      "datasets",
      signal,
      profile,
    ),
  query: (q: SimulationQuery, profile: Profile, signal: AbortSignal) =>
    request<SimulationPage>("/query", "query", signal, profile, q),
  detail: (
    domainId: string,
    datasetId: string,
    caseId: string,
    profile: Profile,
    signal: AbortSignal,
  ) =>
    request<SimulationDetail>(
      `/detail?${params({ domainId, datasetId, caseId, historyOffset: 0, historyLimit: 100 })}`,
      "detail",
      signal,
      profile,
    ),
  transition: (
    q: SimulationTransition,
    profile: Profile,
    signal: AbortSignal,
  ) => request<SimulationReceipt>("/transition", "receipt", signal, profile, q),
  operation: (
    q: Pick<SimulationTransition, "domainId" | "datasetId" | "operationId">,
    profile: Profile,
    signal: AbortSignal,
  ) =>
    request<SimulationReceipt>(
      `/operation?${params({ domainId: q.domainId, datasetId: q.datasetId, operationId: q.operationId })}`,
      "receipt",
      signal,
      profile,
    ),
};
export const ambiguousSimulationWrite = (e: unknown) =>
  !(e instanceof ApiError) ||
  e.status >= 500 ||
  ["network", "invalid_response"].includes(e.code);
