import { ApiError, type Profile } from "./api";
import {
  parseSimulationResponse,
  type SimulationDataset,
  type SimulationDatasets,
} from "./simulation-event-api";

export type RiskResult = {
  simulated: true;
  dataset: SimulationDataset;
  contract: string;
  mappingVersion: string;
  revision: number;
  sourceEventCount: number;
  noRiskEventCount: number;
  version: "simulated-risk-v1";
  scope: { tenant_id: string; domain_id: string; user_id: string };
  as_of: string;
  score: number;
  evidence_count: number;
  duplicates: number;
  ignored_future: number;
  reset_boundary?: string;
  distribution: {
    kind: string;
    count: number;
    weight: number;
    raw_points: number;
  }[];
  records: {
    id: string;
    at: string;
    kind: string;
    reason?: string;
    before: number;
    after: number;
    delta: number;
  }[];
  trend: { at: string; score: number }[];
};
export type RiskQuery = {
  domainId: string;
  datasetId: string;
  subject: string;
};
export type RiskReset = RiskQuery & {
  operationId: string;
  reason: string;
  expectedRevision: number;
};
export type RiskReceipt = {
  simulation: true;
  operationId: string;
  actor: string;
  reason: string;
  result: RiskResult;
};
const object = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const text = (v: unknown): v is string => typeof v === "string" && v.length > 0;
const integer = (v: unknown): v is number =>
  Number.isSafeInteger(v) && Number(v) >= 0;
const score = (v: unknown): v is number => integer(v) && v <= 100;
const date = (v: unknown) => text(v) && Number.isFinite(Date.parse(v));
function validResult(v: unknown): boolean {
  if (
    !object(v) ||
    v.simulated !== true ||
    v.version !== "simulated-risk-v1" ||
    v.contract !== "adtr.risk.simulated.v1" ||
    v.mappingVersion !== "illustrative-event-risk-v1" ||
    !integer(v.revision) ||
    v.revision < 1
  )
    return false;
  try {
    parseSimulationResponse("datasets", {
      simulation: true,
      datasets: [v.dataset],
      total: 1,
    });
  } catch {
    return false;
  }
  return (
    object(v.scope) &&
    [v.scope.tenant_id, v.scope.domain_id, v.scope.user_id].every(text) &&
    date(v.as_of) &&
    score(v.score) &&
    [
      v.evidence_count,
      v.duplicates,
      v.ignored_future,
      v.sourceEventCount,
      v.noRiskEventCount,
    ].every(integer) &&
    (v.reset_boundary === undefined || date(v.reset_boundary)) &&
    Array.isArray(v.distribution) &&
    v.distribution.every(
      (b) =>
        object(b) &&
        text(b.kind) &&
        [b.count, b.weight, b.raw_points].every(integer),
    ) &&
    Array.isArray(v.records) &&
    v.records.every(
      (r) =>
        object(r) &&
        text(r.id) &&
        date(r.at) &&
        text(r.kind) &&
        (r.reason === undefined || text(r.reason)) &&
        score(r.before) &&
        score(r.after) &&
        Number.isSafeInteger(r.delta) &&
        r.delta === r.after - r.before,
    ) &&
    Array.isArray(v.trend) &&
    v.trend.every((p) => object(p) && date(p.at) && score(p.score))
  );
}
export function parseRiskResponse(
  kind: "datasets" | "query" | "receipt",
  value: unknown,
): unknown {
  if (kind === "datasets") return parseSimulationResponse("datasets", value);
  const valid =
    kind === "query"
      ? validResult(value)
      : object(value) &&
        value.simulation === true &&
        text(value.operationId) &&
        text(value.actor) &&
        text(value.reason) &&
        validResult(value.result);
  if (!valid) throw new ApiError("invalid_response");
  return value;
}
async function request<T>(
  path: string,
  kind: Parameters<typeof parseRiskResponse>[0],
  profile: Profile,
  signal: AbortSignal,
  body?: unknown,
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api/simulation/risk${path}`, {
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
  return parseRiskResponse(kind, data) as T;
}
const params = (v: Record<string, string | number>) =>
  new URLSearchParams(
    Object.entries(v).map(([k, v]) => [k, String(v)]),
  ).toString();
export const simulationRiskAPI = {
  datasets: (domainId: string, profile: Profile, signal: AbortSignal) =>
    request<SimulationDatasets>(
      `/datasets?${params({ domainId, offset: 0, limit: 100 })}`,
      "datasets",
      profile,
      signal,
    ),
  query: (q: RiskQuery, profile: Profile, signal: AbortSignal) =>
    request<RiskResult>("/query", "query", profile, signal, {
      domainId: q.domainId,
      datasetId: q.datasetId,
      subject: q.subject,
    }),
  reset: (q: RiskReset, profile: Profile, signal: AbortSignal) =>
    request<RiskReceipt>("/reset", "receipt", profile, signal, q),
  operation: (
    q: Pick<RiskReset, "domainId" | "datasetId" | "operationId">,
    profile: Profile,
    signal: AbortSignal,
  ) =>
    request<RiskReceipt>(
      `/operation?${params({ domainId: q.domainId, datasetId: q.datasetId, operationId: q.operationId })}`,
      "receipt",
      profile,
      signal,
    ),
};
export const ambiguousRiskWrite = (e: unknown) =>
  !(e instanceof ApiError) ||
  e.status >= 500 ||
  ["network", "invalid_response"].includes(e.code);
