import { ApiError, type Profile } from "./api";
import { validTask, type Task } from "./task-api";
export const dnsIPContract = "adtr.simulation-dns-ip.v1";
export const dnsIPKind = "simulation.dns_ip.export";
export const dnsIPColumns = [
  "ID",
  "Domain",
  "DNS name",
  "IP",
  "Observation",
  "Source",
  "Contract version",
  "Simulation",
];
export type Observation = {
  id: string;
  dnsDomain: string;
  source: string;
  hash: string;
  recordCount: number;
  complete: boolean;
  contract: string;
  simulation: boolean;
};
export type RecordRow = {
  id: string;
  domain: string;
  dnsName: string;
  ip: string;
  observation: string;
  source: string;
  version: string;
  simulation: boolean;
};
export type Query = {
  observationId: string;
  keyword: string;
  family: string;
  offset: number;
  limit: number;
};
export type DNSIPPage = {
  observation: Observation;
  query: Query;
  rows: RecordRow[];
  total: number;
  exhausted: boolean;
};
export type ExportInput = {
  observationId: string;
  observationHash: string;
  keyword: string;
  family: string;
  operationId: string;
};
export type Payload = Omit<ExportInput, "operationId"> & {
  dnsDomain: string;
  contract: string;
};
export type Manifest = {
  contract: string;
  simulation: boolean;
  taskId: string;
  payload: Payload;
  rowCount: number;
  columns: string[];
  bytes: number;
  sha256: string;
  filename: string;
};
export type Action = "export" | "cancel";
export type Write = {
  domainId: string;
  operationId: string;
  observationId?: string;
  observationHash?: string;
  keyword?: string;
  family?: string;
  taskId?: string;
};
export type Receipt = {
  contract: string;
  simulation: boolean;
  operationId: string;
  action: Action;
  actorId: number;
  intent: {
    domainId: string;
    action: Action;
    operationId: string;
    export?: ExportInput;
    taskId: string;
  };
  requestHash: string;
  submission?: { task: Task; replayed: boolean };
  task?: Task;
};
const obj = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const hash = (v: unknown): v is string =>
  typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
const id = (v: unknown): v is string =>
  typeof v === "string" && /^[A-Za-z0-9_.-]{1,128}$/.test(v);
const uint = (v: unknown, max: number) =>
  typeof v === "number" && Number.isSafeInteger(v) && v >= 0 && v <= max;
const text = (v: unknown, max: number): v is string =>
  typeof v === "string" &&
  v.length > 0 &&
  new TextEncoder().encode(v).length <= max &&
  !/[\x00-\x1f\x7f]/.test(v);
export function canonical(v: unknown): string {
  if (Array.isArray(v)) return "[" + v.map(canonical).join(",") + "]";
  if (obj(v))
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
export function validFilter(keyword: string, family: string) {
  return (
    keyword.length <= 253 &&
    /^[\x20-\x7e]*$/.test(keyword) &&
    ["", "ipv4", "ipv6"].includes(family)
  );
}
export function validObservation(v: unknown): v is Observation {
  return (
    obj(v) &&
    id(v.id) &&
    text(v.dnsDomain, 253) &&
    id(v.source) &&
    hash(v.hash) &&
    uint(v.recordCount, 10000) &&
    v.complete === true &&
    v.contract === dnsIPContract &&
    v.simulation === true
  );
}
export function validPayload(v: unknown): v is Payload {
  return (
    obj(v) &&
    id(v.observationId) &&
    hash(v.observationHash) &&
    text(v.dnsDomain, 253) &&
    typeof v.keyword === "string" &&
    typeof v.family === "string" &&
    validFilter(v.keyword, v.family) &&
    v.contract === dnsIPContract
  );
}
export function validManifest(v: unknown): v is Manifest {
  return (
    obj(v) &&
    v.contract === dnsIPContract &&
    v.simulation === true &&
    id(v.taskId) &&
    validPayload(v.payload) &&
    uint(v.rowCount, 10000) &&
    canonical(v.columns) === canonical(dnsIPColumns) &&
    uint(v.bytes, 8 * 1024 * 1024) &&
    (v.bytes as number) > 0 &&
    hash(v.sha256) &&
    typeof v.filename === "string" &&
    /^[A-Za-z0-9_.-]{1,160}\.xlsx$/.test(v.filename) &&
    v.filename === `SIMULATED-dns-ip-${v.taskId}.xlsx`
  );
}
export function verifyTask(
  v: unknown,
  domainId: string,
  taskId?: string,
  payload?: Payload,
): Task {
  if (
    !validTask(v) ||
    v.taskName !== dnsIPKind ||
    v.domainId !== domainId ||
    (taskId && v.taskUUID !== taskId) ||
    v.payloadVersion !== 1
  )
    throw new ApiError("invalid_response");
  if (
    v.state === "succeeded" &&
    (!validManifest(v.result) ||
      v.result.taskId !== v.taskUUID ||
      (payload && canonical(v.result.payload) !== canonical(payload)))
  )
    throw new ApiError("invalid_response");
  return v;
}
export function verifyPage(
  v: unknown,
  query: Query,
  observation?: Observation,
): DNSIPPage {
  if (
    !obj(v) ||
    !validObservation(v.observation) ||
    canonical(v.query) !== canonical(query) ||
    v.observation.id !== query.observationId ||
    (observation && canonical(v.observation) !== canonical(observation)) ||
    !uint(v.total, 10000) ||
    (v.total as number) > v.observation.recordCount ||
    !Array.isArray(v.rows) ||
    v.rows.length > query.limit ||
    v.rows.length !==
      Math.min(query.limit, Math.max(0, (v.total as number) - query.offset)) ||
    v.exhausted !== query.offset + v.rows.length >= (v.total as number)
  )
    throw new ApiError("invalid_response");
  const seen = new Set<string>();
  for (const r of v.rows) {
    if (
      !obj(r) ||
      !id(r.id) ||
      seen.has(r.id) ||
      r.domain !== v.observation.dnsDomain ||
      r.observation !== query.observationId ||
      r.source !== v.observation.source ||
      r.version !== "dnsip-sim-v1" ||
      r.simulation !== true ||
      !text(r.dnsName, 253) ||
      !text(r.ip, 64) ||
      (!r.dnsName.toLowerCase().includes(query.keyword.toLowerCase()) &&
        !r.ip.toLowerCase().includes(query.keyword.toLowerCase())) ||
      (query.family === "ipv4" && r.ip.includes(":")) ||
      (query.family === "ipv6" && !r.ip.includes(":"))
    )
      throw new ApiError("invalid_response");
    seen.add(r.id);
  }
  return v as DNSIPPage;
}
export function receiptIntent(action: Action, w: Write): Receipt["intent"] {
  return {
    domainId: w.domainId,
    action,
    operationId: w.operationId,
    ...(action === "export"
      ? {
          export: {
            observationId: w.observationId!,
            observationHash: w.observationHash!,
            keyword: w.keyword!,
            family: w.family!,
            operationId: w.operationId,
          },
        }
      : {}),
    taskId: action === "cancel" ? w.taskId! : "",
  };
}
export function verifyReceipt(
  v: unknown,
  actor: Profile,
  domainId: string,
  operationId: string,
  expected?: { action: Action; input: Write },
): Receipt {
  if (
    !obj(v) ||
    v.contract !== dnsIPContract ||
    v.simulation !== true ||
    v.actorId !== actor.ID ||
    v.operationId !== operationId ||
    !["export", "cancel"].includes(String(v.action)) ||
    !obj(v.intent) ||
    v.intent.domainId !== domainId ||
    v.intent.operationId !== operationId ||
    v.intent.action !== v.action ||
    !hash(v.requestHash)
  )
    throw new ApiError("invalid_response");
  const r = v as Receipt;
  if (
    expected &&
    canonical(r.intent) !==
      canonical(receiptIntent(expected.action, expected.input))
  )
    throw new ApiError("invalid_response");
  if (r.action === "export") {
    const e = r.intent.export;
    if (
      !e ||
      !id(e.observationId) ||
      !hash(e.observationHash) ||
      e.operationId !== operationId ||
      typeof e.keyword !== "string" ||
      typeof e.family !== "string" ||
      !validFilter(e.keyword, e.family) ||
      r.intent.taskId !== "" ||
      !obj(r.submission) ||
      typeof r.submission.replayed !== "boolean" ||
      r.task !== undefined
    )
      throw new ApiError("invalid_response");
    verifyTask(r.submission.task, domainId);
  } else {
    if (
      !id(r.intent.taskId) ||
      r.intent.export !== undefined ||
      r.submission !== undefined
    )
      throw new ApiError("invalid_response");
    verifyTask(r.task, domainId, r.intent.taskId);
  }
  return r;
}
const prefix = "/api/simulation/dns-ip";
const queryString = (v: Record<string, string | number>) =>
  new URLSearchParams(
    Object.entries(v).map(([k, x]) => [k, String(x)]),
  ).toString();
async function response(
  path: string,
  p: Profile,
  s: AbortSignal,
  body?: unknown,
): Promise<Response> {
  let r: Response;
  try {
    r = await fetch(prefix + path, {
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
  if (!r.ok) {
    let v: unknown;
    try {
      v = JSON.parse(await r.text());
    } catch {
      throw new ApiError("invalid_response", r.status);
    }
    throw new ApiError(
      obj(v) && typeof v.error === "string" ? v.error : "internal",
      r.status,
    );
  }
  return r;
}
async function request(
  path: string,
  p: Profile,
  s: AbortSignal,
  body?: unknown,
): Promise<unknown> {
  const r = await response(path, p, s, body),
    raw = await r.text();
  if (new TextEncoder().encode(raw).length > 1024 * 1024)
    throw new ApiError("invalid_response");
  try {
    return JSON.parse(raw);
  } catch {
    throw new ApiError("invalid_response");
  }
}
export const simulationDNSIPAPI = {
  observations: async (domainId: string, p: Profile, s: AbortSignal) => {
    const v = await request("/observations?" + queryString({ domainId }), p, s);
    if (
      !obj(v) ||
      v.simulation !== true ||
      !Array.isArray(v.observations) ||
      v.observations.length > 8 ||
      !v.observations.every(validObservation) ||
      new Set(v.observations.map((x) => x.id)).size !== v.observations.length
    )
      throw new ApiError("invalid_response");
    return v.observations as Observation[];
  },
  records: async (
    domainId: string,
    q: Query,
    p: Profile,
    s: AbortSignal,
    o?: Observation,
  ) =>
    verifyPage(
      await request("/records?" + queryString({ domainId, ...q }), p, s),
      q,
      o,
    ),
  write: async (action: Action, w: Write, p: Profile, s: AbortSignal) =>
    verifyReceipt(
      await request("/" + action, p, s, w),
      p,
      w.domainId,
      w.operationId,
      { action, input: w },
    ),
  receipt: async (
    domainId: string,
    operationId: string,
    p: Profile,
    s: AbortSignal,
    expected?: { action: Action; input: Write },
  ) =>
    verifyReceipt(
      await request("/receipt?" + queryString({ domainId, operationId }), p, s),
      p,
      domainId,
      operationId,
      expected,
    ),
  task: async (
    domainId: string,
    taskId: string,
    p: Profile,
    s: AbortSignal,
    payload?: Payload,
  ) => {
    const v = await request("/task?" + queryString({ domainId, taskId }), p, s);
    if (!obj(v) || v.simulation !== true)
      throw new ApiError("invalid_response");
    return verifyTask(v.task, domainId, taskId, payload);
  },
  download: async (
    domainId: string,
    t: Task,
    p: Profile,
    s: AbortSignal,
    payload?: Payload,
  ) => {
    verifyTask(t, domainId, t.taskUUID, payload);
    if (t.state !== "succeeded" || !validManifest(t.result))
      throw new ApiError("invalid_response");
    const m = t.result;
    const r = await response(
      "/download?" + queryString({ domainId, taskId: t.taskUUID }),
      p,
      s,
    );
    const disposition = r.headers.get("Content-Disposition") ?? "";
    if (
      r.headers.get("Content-Type")?.split(";")[0] !==
        "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" ||
      r.headers.get("X-ADTR-SHA256") !== m.sha256 ||
      r.headers.get("X-ADTR-Row-Count") !== String(m.rowCount) ||
      r.headers.get("Content-Length") !== String(m.bytes) ||
      ![
        'attachment; filename="' + m.filename + '"',
        "attachment; filename=" + m.filename,
      ].includes(disposition)
    )
      throw new ApiError("invalid_response");
    const bytes = await r.arrayBuffer();
    if (s.aborted) throw new DOMException("Aborted", "AbortError");
    if (bytes.byteLength !== m.bytes || bytes.byteLength > 8 * 1024 * 1024)
      throw new ApiError("invalid_response");
    const digest = Array.from(
      new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)),
    )
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    if (s.aborted) throw new DOMException("Aborted", "AbortError");
    if (digest !== m.sha256) throw new ApiError("invalid_response");
    return {
      blob: new Blob([bytes], {
        type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
      }),
      filename: m.filename,
    };
  },
};
export function saveDNSIPFile(file: { blob: Blob; filename: string }) {
  const url = URL.createObjectURL(file.blob);
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = file.filename;
    a.click();
  } finally {
    URL.revokeObjectURL(url);
  }
}
export const ambiguousDNSIPWrite = (e: unknown) =>
  !(e instanceof ApiError) ||
  e.status === undefined ||
  e.status >= 500 ||
  e.code === "invalid_response" ||
  e.code === "network";
