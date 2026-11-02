import { ApiError, type Profile } from "./api";
import {
  parseSimulationResponse,
  type SimulationDatasets,
} from "./simulation-event-api";
import { object, integer, validTask, type Task } from "./task-api";
export const detectionContract = "adtr.detection.simulated.v1";
export const detectionProvenance = "synthetic-business-contract-2026-11-01";
export const detectionMapping = "adtr.simulation-detection.mapping.v1";
export type Declaration = {
  id: string;
  enabled: boolean;
  combine: "all" | "any";
  conditions: { field: string; operator: string; values: string[] }[];
};
export type DetectionTemplate = {
  id: string;
  name: string;
  version: number;
  hash: string;
  rules: (Declaration & {
    contract: string;
    provenance: string;
    version: number;
    scope: { tenant: string; domain: string };
  })[];
  archived: boolean;
  simulation: true;
};
export type DetectionReceipt = {
  operationId: string;
  kind: string;
  template?: DetectionTemplate;
  submission?: { task: Task; replayed: boolean };
  task?: Task;
};
export type DetectionWrite = {
  domainId: string;
  operationId: string;
  templateId?: string;
  name?: string;
  rules?: Declaration[];
  expectedVersion?: number;
  datasetId?: string;
  templateVersion?: number;
  taskUUID?: string;
};
export type WriteKind = "create" | "update" | "archive" | "run" | "cancel";
const txt = (v: unknown): v is string => typeof v === "string" && v.length > 0;
const hash = (v: unknown) => typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
const boundedText = (v: unknown, max: number) =>
  txt(v) &&
  new TextEncoder().encode(v).length <= max &&
  !/[\x00-\x1f\x7f]/.test(v);
const fields = [
  "event.code",
  "event.category",
  "event.outcome",
  "actor.name",
  "target.name",
  "source.ip",
  "object.kind",
];
export function parseDeclarations(raw: string): Declaration[] {
  if (new TextEncoder().encode(raw).length > 262144)
    throw new Error("规则 JSON 不能超过 256 KiB。");
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    throw new Error("规则必须是有效 JSON 数组。");
  }
  if (!Array.isArray(value) || value.length < 1 || value.length > 128)
    throw new Error("每个模板需要 1–128 条规则。");
  const ids = new Set<string>();
  for (const r of value) {
    if (
      !object(r) ||
      Object.keys(r).some(
        (k) => !["id", "enabled", "combine", "conditions"].includes(k),
      ) ||
      !boundedText(r.id, 128) ||
      ids.has(r.id as string) ||
      typeof r.enabled !== "boolean" ||
      !["all", "any"].includes(String(r.combine)) ||
      !Array.isArray(r.conditions) ||
      !r.conditions.length ||
      r.conditions.length > 32
    )
      throw new Error(
        "规则须包含唯一 id、enabled 布尔值、all/any combine 和 1–32 个 conditions；不允许额外字段。",
      );
    ids.add(r.id as string);
    for (const c of r.conditions) {
      if (
        !object(c) ||
        Object.keys(c).some(
          (k) => !["field", "operator", "values"].includes(k),
        ) ||
        !fields.includes(String(c.field)) ||
        !["exists", "eq", "neq", "contains", "prefix", "in"].includes(
          String(c.operator),
        ) ||
        !Array.isArray(c.values) ||
        !c.values.every((v) => boundedText(v, 512)) ||
        (c.operator === "exists"
          ? c.values.length !== 0
          : c.operator === "in"
            ? c.values.length < 1 || c.values.length > 32
            : c.values.length !== 1)
      )
        throw new Error(
          "条件字段或运算符无效：exists 需要空 values；in 需要 1–32 项；其他运算符需要 1 项，每项最多 512 字节。",
        );
    }
  }
  return value as Declaration[];
}
export const editableRules = (t: DetectionTemplate) =>
  t.rules.map(({ id, enabled, combine, conditions }) => ({
    id,
    enabled,
    combine,
    conditions,
  }));
function template(v: unknown): v is DetectionTemplate {
  if (
    !object(v) ||
    v.simulation !== true ||
    !txt(v.id) ||
    !txt(v.name) ||
    !integer(v.version, 1) ||
    !hash(v.hash) ||
    typeof v.archived !== "boolean" ||
    !Array.isArray(v.rules)
  )
    return false;
  try {
    parseDeclarations(
      JSON.stringify(
        v.rules.map((r) =>
          object(r)
            ? {
                id: r.id,
                enabled: r.enabled,
                combine: r.combine,
                conditions: r.conditions,
              }
            : r,
        ),
      ),
    );
  } catch {
    return false;
  }
  return v.rules.every(
    (r) =>
      object(r) &&
      r.contract === detectionContract &&
      r.provenance === detectionProvenance &&
      r.version === v.version &&
      object(r.scope) &&
      txt(r.scope.tenant) &&
      txt(r.scope.domain),
  );
}
function task(v: unknown): v is Task {
  if (!validTask(v) || v.taskName !== "simulation.detection") return false;
  if (v.state !== "succeeded") return true;
  const r = v.result;
  return (
    object(r) &&
    r.simulation === true &&
    r.contract === detectionContract &&
    r.mappingVersion === detectionMapping &&
    txt(r.templateId) &&
    integer(r.templateVersion, 1) &&
    hash(r.templateHash) &&
    txt(r.datasetId) &&
    hash(r.fixtureHash) &&
    integer(r.eventCount, 0, 1000) &&
    Array.isArray(r.matches) &&
    r.matches.length <= 10000 &&
    r.matches.every(
      (m) =>
        object(m) &&
        m.contract === detectionContract &&
        m.provenance === detectionProvenance &&
        txt(m.rule_id) &&
        integer(m.rule_version, 1) &&
        hash(m.rule_digest) &&
        txt(m.event_id) &&
        object(m.scope) &&
        txt(m.scope.tenant) &&
        m.scope.domain === v.domainId,
    )
  );
}
export function parseDetectionResponse(
  kind: "datasets" | "templates" | "template" | "receipt" | "task",
  v: unknown,
): unknown {
  if (kind === "datasets") return parseSimulationResponse("datasets", v);
  let valid = false;
  if (object(v)) {
    if (kind === "templates")
      valid =
        v.simulation === true &&
        Array.isArray(v.templates) &&
        v.templates.length <= 100 &&
        v.templates.every(template);
    if (kind === "template")
      valid = v.simulation === true && template(v.template);
    if (kind === "task") valid = v.simulation === true && task(v.task);
    if (kind === "receipt")
      valid =
        txt(v.operationId) &&
        ["create", "update", "archive", "run", "cancel"].includes(
          String(v.kind),
        ) &&
        (v.kind === "run"
          ? object(v.submission) &&
            task(v.submission.task) &&
            typeof v.submission.replayed === "boolean"
          : v.kind === "cancel"
            ? task(v.task)
            : template(v.template));
  }
  if (!valid) throw new ApiError("invalid_response");
  return v;
}
const query = (p: Record<string, string | number>) =>
  new URLSearchParams(
    Object.entries(p).map(([k, v]) => [k, String(v)]),
  ).toString();
async function request<T>(
  path: string,
  kind: Parameters<typeof parseDetectionResponse>[0],
  p: Profile,
  s: AbortSignal,
  body?: unknown,
): Promise<T> {
  let r: Response;
  try {
    r = await fetch(`/api/simulation/detection${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      cache: "no-store",
      signal: s,
      headers:
        body === undefined
          ? {}
          : { "Content-Type": "application/json", "X-CSRF-Token": p.csrfToken },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  } catch (e) {
    if (s.aborted) throw e;
    throw new ApiError("network");
  }
  if (r.ok && r.headers.get("X-ADTR-User-ID") !== String(p.ID))
    throw new ApiError("unauthenticated", 401);
  const v: unknown = await r.json().catch(() => {
    throw new ApiError("invalid_response", r.status);
  });
  if (!r.ok)
    throw new ApiError(
      object(v) && txt(v.error) ? v.error : "internal",
      r.status,
    );
  return parseDetectionResponse(kind, v) as T;
}
export const simulationDetectionAPI = {
  datasets: (domainId: string, p: Profile, s: AbortSignal) =>
    request<SimulationDatasets>(
      `/datasets?${query({ domainId, offset: 0, limit: 100 })}`,
      "datasets",
      p,
      s,
    ),
  templates: (domainId: string, p: Profile, s: AbortSignal) =>
    request<{ simulation: true; templates: DetectionTemplate[] }>(
      `/templates?${query({ domainId })}`,
      "templates",
      p,
      s,
    ),
  template: (
    domainId: string,
    templateId: string,
    p: Profile,
    s: AbortSignal,
  ) =>
    request<{ simulation: true; template: DetectionTemplate }>(
      `/template?${query({ domainId, templateId })}`,
      "template",
      p,
      s,
    ),
  write: (kind: WriteKind, input: DetectionWrite, p: Profile, s: AbortSignal) =>
    request<DetectionReceipt>(`/${kind}`, "receipt", p, s, input),
  receipt: (
    domainId: string,
    operationId: string,
    p: Profile,
    s: AbortSignal,
  ) =>
    request<DetectionReceipt>(
      `/receipt?${query({ domainId, operationId })}`,
      "receipt",
      p,
      s,
    ),
  task: (domainId: string, taskUUID: string, p: Profile, s: AbortSignal) =>
    request<{ simulation: true; task: Task }>(
      `/task?${query({ domainId, taskUUID })}`,
      "task",
      p,
      s,
    ),
};
export const ambiguousDetectionWrite = (e: unknown) =>
  !(e instanceof ApiError) ||
  e.status >= 500 ||
  ["network", "invalid_response"].includes(e.code);
