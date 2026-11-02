import { ApiError, type Profile } from "./api";
import { object } from "./task-api";
export const labelsContract = "adtr.simulated-sensitive-assets.v1";
export type Page<T> = {
  items: T[];
  count: number;
  offset: number;
  limit: number;
};
export type Dataset = {
  id: string;
  contract: string;
  producer: string;
  simulation: true;
  hash: string;
  observationCount: number;
};
export type Asset = {
  objectId: string;
  name: string;
  description: string | null;
};
export type Observation = {
  id: string;
  sequence: number;
  complete: boolean;
  hash: string;
  assets: Asset[];
  snapshot: {
    simulated: true;
    scope: { tenantId: string; domainId: string };
    observationId: string;
    nodes: { id: string; kind: string; membersKnown: boolean }[];
  };
};
export type Spec = {
  name: string;
  sensitiveGroupIds: string[];
  manualLabels: { objectId: string; label: string }[];
};
export type Policy = {
  id: string;
  datasetId: string;
  revision: number;
  status: string;
  spec: Spec;
  hash: string;
  actor: string;
  reason: string;
  at: string;
};
export type Provenance = {
  label: string;
  source: string;
  groupId?: string;
  distance: number;
};
export type AssetLabels = Asset & {
  kind: string;
  state: string;
  labels: Provenance[];
  isClick: boolean;
  isHoneypot: null;
  honeypotAvailability: string;
  reason: string;
};
export type Evaluation = {
  id: string;
  dataset: Dataset;
  observation: Observation;
  policy: Policy;
  result: {
    simulated: true;
    completeWithinSnapshot: boolean;
    scope: { tenantId: string; domainId: string };
    objects: unknown[];
  };
  hash: string;
  actor: string;
  at: string;
};
export type Comparison = {
  id: string;
  datasetId: string;
  beforeId: string;
  afterId: string;
  hash: string;
  actor: string;
  at: string;
  alerts: {
    id: string;
    objectId: string;
    simulation: true;
    delivery: string;
    changes: { objectId: string; action: string; provenance: Provenance }[];
  }[];
};
export type Receipt = {
  simulation: true;
  operationId: string;
  actor: string;
  action: string;
  datasetId: string;
  reason: string;
  at: string;
  policy?: Policy;
  evaluation?: Evaluation;
  comparison?: Comparison;
};
export type WriteKind =
  | "create"
  | "update"
  | "archive"
  | "restore"
  | "evaluate"
  | "compare";
export type Write = {
  domainId: string;
  datasetId: string;
  operationId: string;
  reason: string;
  policyId?: string;
  expectedRevision?: number;
  spec?: Spec;
  observationId?: string;
  beforeId?: string;
  afterId?: string;
};
const str = (v: unknown): v is string => typeof v === "string";
const nonempty = (v: unknown) => str(v) && v.length > 0;
const hash = (v: unknown) => str(v) && /^[a-f0-9]{64}$/.test(v);
const int = (v: unknown) => Number.isSafeInteger(v) && Number(v) >= 0;
const array = (v: unknown, fn: (x: unknown) => boolean) =>
  Array.isArray(v) && v.every(fn);
const asset = (v: unknown) =>
  object(v) &&
  nonempty(v.objectId) &&
  str(v.name) &&
  (v.description === null || str(v.description));
const provenance = (v: unknown) =>
  object(v) &&
  str(v.label) &&
  str(v.source) &&
  int(v.distance) &&
  (v.groupId === undefined || str(v.groupId));
const validators: Record<string, (v: unknown) => boolean> = {
  dataset: (v) =>
    object(v) &&
    nonempty(v.id) &&
    v.contract === labelsContract &&
    v.simulation === true &&
    hash(v.hash) &&
    str(v.producer) &&
    int(v.observationCount),
  observation: (v) =>
    object(v) &&
    nonempty(v.id) &&
    int(v.sequence) &&
    typeof v.complete === "boolean" &&
    hash(v.hash) &&
    array(v.assets, asset) &&
    object(v.snapshot) &&
    v.snapshot.simulated === true &&
    object(v.snapshot.scope) &&
    nonempty(v.snapshot.scope.domainId) &&
    v.snapshot.observationId === v.id &&
    array(
      v.snapshot.nodes,
      (n) =>
        object(n) &&
        nonempty(n.id) &&
        ["user", "group", "computer"].includes(String(n.kind)) &&
        typeof n.membersKnown === "boolean",
    ),
  policy: (v) =>
    object(v) &&
    nonempty(v.id) &&
    nonempty(v.datasetId) &&
    int(v.revision) &&
    Number(v.revision) > 0 &&
    ["active", "archived"].includes(String(v.status)) &&
    hash(v.hash) &&
    object(v.spec) &&
    str(v.spec.name) &&
    array(v.spec.sensitiveGroupIds, nonempty) &&
    array(
      v.spec.manualLabels,
      (m) => object(m) && nonempty(m.objectId) && nonempty(m.label),
    ) &&
    str(v.actor) &&
    str(v.reason) &&
    str(v.at),
  asset: (v) =>
    asset(v) &&
    object(v) &&
    ["user", "group", "computer"].includes(String(v.kind)) &&
    str(v.state) &&
    array(v.labels, provenance) &&
    typeof v.isClick === "boolean" &&
    v.isHoneypot === null &&
    str(v.honeypotAvailability) &&
    str(v.reason),
  evaluation: (v) =>
    object(v) &&
    nonempty(v.id) &&
    validators.dataset(v.dataset) &&
    validators.observation(v.observation) &&
    validators.policy(v.policy) &&
    hash(v.hash) &&
    object(v.result) &&
    v.result.simulated === true &&
    typeof v.result.completeWithinSnapshot === "boolean" &&
    object(v.result.scope) &&
    Array.isArray(v.result.objects),
  comparison: (v) =>
    object(v) &&
    nonempty(v.id) &&
    nonempty(v.datasetId) &&
    nonempty(v.beforeId) &&
    nonempty(v.afterId) &&
    hash(v.hash) &&
    array(
      v.alerts,
      (a) =>
        object(a) &&
        nonempty(a.id) &&
        nonempty(a.objectId) &&
        a.simulation === true &&
        str(a.delivery) &&
        array(
          a.changes,
          (c) =>
            object(c) &&
            nonempty(c.objectId) &&
            str(c.action) &&
            provenance(c.provenance),
        ),
    ),
  receipt: (v) =>
    object(v) &&
    v.simulation === true &&
    nonempty(v.operationId) &&
    nonempty(v.actor) &&
    nonempty(v.action) &&
    nonempty(v.datasetId) &&
    str(v.reason) &&
    str(v.at) &&
    [v.policy, v.evaluation, v.comparison].filter((x) => x !== undefined)
      .length === 1 &&
    (v.policy === undefined || validators.policy(v.policy)) &&
    (v.evaluation === undefined || validators.evaluation(v.evaluation)) &&
    (v.comparison === undefined || validators.comparison(v.comparison)),
};
export function parseLabelsResponse(
  kind: string,
  value: unknown,
  page = false,
): unknown {
  const valid = validators[kind];
  if (
    !valid ||
    !(page
      ? object(value) &&
        int(value.count) &&
        int(value.offset) &&
        int(value.limit) &&
        Number(value.limit) > 0 &&
        Array.isArray(value.items) &&
        array(value.items, valid) &&
        Number(value.count) >= value.items.length
      : valid(value))
  )
    throw new ApiError("invalid_response");
  return value;
}
const kinds: Record<string, string> = {
  datasets: "dataset",
  observations: "observation",
  observation: "observation",
  policies: "policy",
  policy: "policy",
  history: "policy",
  evaluations: "evaluation",
  evaluation: "evaluation",
  assets: "asset",
  asset: "asset",
  comparisons: "comparison",
  comparison: "comparison",
  operation: "receipt",
};
const pages = new Set([
  "datasets",
  "observations",
  "policies",
  "history",
  "evaluations",
  "assets",
  "comparisons",
]);
async function request<T>(
  route: string,
  params: Record<string, string | number>,
  p: Profile,
  s: AbortSignal,
  body?: Write,
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(
      "/api/simulation/labels/" +
        route +
        (body
          ? ""
          : "?" +
            new URLSearchParams(
              Object.entries(params).map(([k, v]) => [k, String(v)]),
            )),
      {
        method: body ? "POST" : "GET",
        credentials: "same-origin",
        cache: "no-store",
        signal: s,
        headers: body
          ? { "Content-Type": "application/json", "X-CSRF-Token": p.csrfToken }
          : {},
        body: body ? JSON.stringify(body) : undefined,
      },
    );
  } catch (e) {
    if (s.aborted) throw e;
    throw new ApiError("network");
  }
  if (response.ok && response.headers.get("X-ADTR-User-ID") !== String(p.ID))
    throw new ApiError("unauthenticated", 401);
  const value: unknown = await response.json().catch(() => {
    throw new ApiError("invalid_response", response.status);
  });
  if (!response.ok)
    throw new ApiError(
      object(value) && str(value.error) ? value.error : "internal",
      response.status,
    );
  parseLabelsResponse(
    body ? "receipt" : kinds[route],
    value,
    !body && pages.has(route),
  );
  const check = (v: unknown): void => {
    if (!object(v)) return;
    if (
      v.datasetId !== undefined &&
      v.datasetId !== (body?.datasetId ?? params.datasetId)
    )
      throw new ApiError("invalid_response");
    if (
      object(v.snapshot) &&
      object(v.snapshot.scope) &&
      v.snapshot.scope.domainId !== params.domainId
    )
      throw new ApiError("invalid_response");
    if (
      object(v.dataset) &&
      v.dataset.id !== (body?.datasetId ?? params.datasetId)
    )
      throw new ApiError("invalid_response");
    if (
      object(v.result) &&
      object(v.result.scope) &&
      v.result.scope.domainId !== (body?.domainId ?? params.domainId)
    )
      throw new ApiError("invalid_response");
  };
  if (object(value)) {
    if (Array.isArray(value.items)) value.items.forEach(check);
    else check(value);
    for (const k of ["policy", "evaluation", "comparison"])
      if (value[k]) check(value[k]);
    if (
      (body || route === "operation") &&
      (value.operationId !== (body?.operationId ?? params.operationId) ||
        value.actor !== String(p.ID))
    )
      throw new ApiError("invalid_response");
    const key = {
      observation: "observationId",
      policy: "policyId",
      evaluation: "evaluationId",
      comparison: "comparisonId",
      asset: "objectId",
    }[route];
    if (key && value[route === "asset" ? "objectId" : "id"] !== params[key])
      throw new ApiError("invalid_response");
  }
  if (body) validateReceiptIntent(route as WriteKind, body, value as Receipt);
  return value as T;
}
export const simulationLabelsAPI = {
  read: <T>(
    route: string,
    params: Record<string, string | number>,
    p: Profile,
    s: AbortSignal,
  ) => request<T>(route, params, p, s),
  write: (kind: WriteKind, input: Write, p: Profile, s: AbortSignal) =>
    request<Receipt>(kind, { domainId: input.domainId }, p, s, input),
};

export function validateReceiptIntent(
  kind: WriteKind,
  input: Write,
  r: Receipt,
): void {
  if (
    r.action !== kind ||
    r.operationId !== input.operationId ||
    r.datasetId !== input.datasetId ||
    r.reason !== input.reason
  )
    throw new ApiError("invalid_response");
  if (
    ["create", "update", "archive", "restore"].includes(kind) &&
    (!r.policy ||
      r.policy.id !== input.policyId ||
      r.policy.revision !== Number(input.expectedRevision) + 1)
  )
    throw new ApiError("invalid_response");
  // Match the backend canonicalSpec: order is insignificant, content is not.
  // Do not trim, deduplicate or normalize label text: the server rejects such inputs.
  if (
    (kind === "create" || kind === "update") &&
    (!input.spec ||
      !r.policy ||
      canonicalSpec(input.spec) !== canonicalSpec(r.policy.spec))
  )
    throw new ApiError("invalid_response");
  if (
    kind === "evaluate" &&
    (!r.evaluation ||
      r.evaluation.policy.id !== input.policyId ||
      r.evaluation.policy.revision !== input.expectedRevision ||
      r.evaluation.observation.id !== input.observationId)
  )
    throw new ApiError("invalid_response");
  if (
    kind === "compare" &&
    (!r.comparison ||
      r.comparison.beforeId !== input.beforeId ||
      r.comparison.afterId !== input.afterId)
  )
    throw new ApiError("invalid_response");
}

function canonicalSpec(spec: Spec): string {
  return JSON.stringify({
    name: spec.name,
    sensitiveGroupIds: [...spec.sensitiveGroupIds].sort(),
    manualLabels: spec.manualLabels
      .map((m) => [m.objectId, m.label])
      .sort((a, b) =>
        a[0] === b[0]
          ? a[1] < b[1]
            ? -1
            : a[1] > b[1]
              ? 1
              : 0
          : a[0] < b[0]
            ? -1
            : 1,
      ),
  });
}
