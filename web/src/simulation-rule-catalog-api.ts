import { ApiError, type Profile } from "./api";
export const catalogContract = "adtr.simulation-rule-catalog.v1";
export const copyAcknowledgement = "independent-template-copy-no-cascade";
export const fields = [
  "event.code",
  "event.category",
  "event.outcome",
  "actor.name",
  "target.name",
  "source.ip",
  "object.kind",
] as const;
export const operators = [
  "eq",
  "neq",
  "contains",
  "prefix",
  "in",
  "exists",
] as const;
export type Condition = { field: string; operator: string; values: string[] };
export type Declaration = { combine: "all" | "any"; conditions: Condition[] };
export type Fixture = {
  id: string;
  fields: Record<string, string>;
  wantMatch: boolean;
};
export type Rule = Declaration & {
  id: string;
  version: number;
  enabled: boolean;
  scope: { tenant: string; domain: string };
};
export type Report = {
  digest: string;
  passed: boolean;
  cases: {
    event: { id: string; fields: Record<string, string> };
    want_match: boolean;
  }[];
};
export type Published = {
  rule: Rule;
  digest: string;
  test: Report;
  reviewer: string;
};
export type Summary = {
  copySupported: boolean;
  copyUnsupportedFields: string[];
  id: string;
  revision: number;
  draftVersion: number;
  latestPublishedVersion: number;
  activeVersion: number;
  archived: boolean;
  testPassed: boolean | null;
  lastEditorId: number;
  updatedAt: string;
};
export type CatalogRecord = Summary & {
  caseResults?: { id: string; wantMatch: boolean; actualMatch: boolean }[];
  contract: string;
  simulation: true;
  learningSupported: false;
  hash: string;
  snapshot: {
    scope: { tenant: string; domain: string };
    revision: number;
    entries: {
      id: string;
      draft: Rule | null;
      test: Report | null;
      published: Published[];
      active_version: number;
      archived: boolean;
    }[];
  };
};
export type Page = {
  items: Summary[];
  count: number;
  offset: number;
  limit: number;
};
export type ReceiptIntent = {
  domainId: string;
  id: string;
  action: string;
  operationId: string;
  expectedRevision: number;
  declaration: Declaration | null;
  cases: Fixture[] | null;
  publishedVersion: number;
  publishedDigest: string;
  templateId: string;
  name: string;
  acknowledgement: string;
};
export type Receipt = {
  intent: ReceiptIntent;
  requestHash: string;
  simulation: true;
  operationId: string;
  action: string;
  actorId: number;
  at: string;
  rule?: Summary;
  origin?: {
    ruleId: string;
    publishedVersion: number;
    publishedDigest: string;
    catalogRevision: number;
    templateId: string;
    templateVersion: number;
    templateHash: string;
    acknowledgement: string;
  };
};
export type Action =
  | "create"
  | "edit"
  | "test"
  | "publish"
  | "enable"
  | "disable"
  | "archive"
  | "create-template";
export type Write = {
  domainId: string;
  id: string;
  operationId: string;
  expectedRevision: number;
  declaration?: Declaration;
  cases?: Fixture[];
  publishedVersion?: number;
  publishedDigest?: string;
  templateId?: string;
  name?: string;
  acknowledgement?: string;
};
export type Export = {
  simulation: true;
  contract: string;
  id: string;
  publications: {
    rule: Published;
    reviewedBy: number;
    lastEditorId: number;
    testedBy: number;
    testRevision: number;
    publishedAt: string;
  }[];
};
const obj = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const uint = (v: unknown) => Number.isSafeInteger(v) && (v as number) >= 0;
const hash = (v: unknown) => typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
const summary = (v: unknown): boolean =>
  obj(v) &&
  typeof v.id === "string" &&
  v.id.length > 0 &&
  uint(v.revision) &&
  uint(v.draftVersion) &&
  uint(v.latestPublishedVersion) &&
  uint(v.activeVersion) &&
  typeof v.archived === "boolean" &&
  typeof v.copySupported === "boolean" &&
  Array.isArray(v.copyUnsupportedFields) &&
  v.copyUnsupportedFields.every((x) => typeof x === "string") &&
  (v.testPassed === null || typeof v.testPassed === "boolean") &&
  uint(v.lastEditorId) &&
  typeof v.updatedAt === "string";
export function validDeclaration(v: unknown): boolean {
  return (
    obj(v) &&
    ["all", "any"].includes(String(v.combine)) &&
    Array.isArray(v.conditions) &&
    v.conditions.length > 0 &&
    v.conditions.length <= 32 &&
    v.conditions.every(
      (c) =>
        obj(c) &&
        fields.includes(c.field as (typeof fields)[number]) &&
        operators.includes(c.operator as (typeof operators)[number]) &&
        Array.isArray(c.values) &&
        c.values.every(
          (x) =>
            typeof x === "string" &&
            x.length > 0 &&
            new TextEncoder().encode(x).length <= 512,
        ) &&
        (c.operator === "exists"
          ? c.values.length === 0
          : c.operator === "in"
            ? c.values.length > 0 && c.values.length <= 32
            : c.values.length === 1),
    )
  );
}
function rule(v: unknown): boolean {
  return (
    validDeclaration(v) &&
    obj(v) &&
    typeof v.id === "string" &&
    uint(v.version) &&
    typeof v.enabled === "boolean" &&
    obj(v.scope) &&
    typeof v.scope.domain === "string"
  );
}
function report(v: unknown): boolean {
  return (
    obj(v) &&
    hash(v.digest) &&
    typeof v.passed === "boolean" &&
    Array.isArray(v.cases) &&
    v.cases.length <= 8 &&
    v.cases.every(
      (c) =>
        obj(c) &&
        typeof c.want_match === "boolean" &&
        obj(c.event) &&
        typeof c.event.id === "string" &&
        obj(c.event.fields) &&
        Object.values(c.event.fields).every((x) => typeof x === "string"),
    )
  );
}
function published(v: unknown): boolean {
  return (
    obj(v) &&
    rule(v.rule) &&
    hash(v.digest) &&
    report(v.test) &&
    typeof v.reviewer === "string"
  );
}
export function parseCatalogResponse(kind: string, v: unknown): unknown {
  let ok = false;
  if (kind === "list")
    ok =
      obj(v) &&
      Array.isArray(v.items) &&
      v.items.length <= 32 &&
      v.items.every(summary) &&
      uint(v.count) &&
      uint(v.offset) &&
      uint(v.limit) &&
      (v.limit as number) > 0 &&
      (v.limit as number) <= 32 &&
      v.items.length <= (v.limit as number);
  if (kind === "detail")
    ok =
      summary(v) &&
      obj(v) &&
      v.simulation === true &&
      v.learningSupported === false &&
      (v.caseResults === null ||
        (Array.isArray(v.caseResults) &&
          v.caseResults.length <= 8 &&
          v.caseResults.every(
            (c) =>
              obj(c) &&
              typeof c.id === "string" &&
              typeof c.wantMatch === "boolean" &&
              typeof c.actualMatch === "boolean",
          ))) &&
      v.contract === catalogContract &&
      hash(v.hash) &&
      obj(v.snapshot) &&
      obj(v.snapshot.scope) &&
      typeof v.snapshot.scope.domain === "string" &&
      Array.isArray(v.snapshot.entries) &&
      v.snapshot.entries.length === 1 &&
      v.snapshot.entries.every(
        (e) =>
          obj(e) &&
          e.id === v.id &&
          (e.draft === null || rule(e.draft)) &&
          (e.test === null || report(e.test)) &&
          Array.isArray(e.published) &&
          e.published.length <= 8 &&
          e.published.every(published),
      );
  if (kind === "receipt")
    ok =
      obj(v) &&
      v.simulation === true &&
      typeof v.operationId === "string" &&
      hash(v.requestHash) &&
      obj(v.intent) &&
      typeof v.action === "string" &&
      uint(v.actorId) &&
      typeof v.at === "string" &&
      (v.rule === undefined || summary(v.rule)) &&
      (v.origin === undefined ||
        (obj(v.origin) &&
          hash(v.origin.publishedDigest) &&
          hash(v.origin.templateHash) &&
          typeof v.origin.ruleId === "string" &&
          typeof v.origin.templateId === "string" &&
          uint(v.origin.catalogRevision) &&
          uint(v.origin.publishedVersion) &&
          uint(v.origin.templateVersion) &&
          v.origin.acknowledgement === copyAcknowledgement));
  if (kind === "export")
    ok =
      obj(v) &&
      v.simulation === true &&
      v.contract === catalogContract &&
      typeof v.id === "string" &&
      Array.isArray(v.publications) &&
      v.publications.length <= 8 &&
      v.publications.every(
        (p) =>
          obj(p) &&
          published(p.rule) &&
          uint(p.reviewedBy) &&
          uint(p.lastEditorId) &&
          p.reviewedBy !== p.lastEditorId,
      );
  if (!ok) throw new ApiError("invalid_response");
  return v;
}
export function receiptIntent(action: Action, input: Write): ReceiptIntent {
  return {
    domainId: input.domainId,
    id: input.id,
    action: action === "create-template" ? "copy-template" : action,
    operationId: input.operationId,
    expectedRevision: input.expectedRevision,
    declaration: input.declaration ?? null,
    cases: input.cases ?? null,
    publishedVersion: input.publishedVersion ?? 0,
    publishedDigest: input.publishedDigest ?? "",
    templateId: input.templateId ?? "",
    name: input.name ?? "",
    acknowledgement: input.acknowledgement ?? "",
  };
}
function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical);
  if (obj(value))
    return Object.fromEntries(
      Object.keys(value)
        .sort()
        .map((k) => [k, canonical(value[k])]),
    );
  return value;
}
export function intentFingerprint(action: Action, input: Write): string {
  return JSON.stringify(canonical(receiptIntent(action, input)));
}
export function verifyReceipt(
  r: Receipt,
  action: Action,
  input: Write,
  actor: number,
) {
  const expected = receiptIntent(action, input);
  if (
    r.operationId !== input.operationId ||
    r.actorId !== actor ||
    r.action !== expected.action ||
    !hash(r.requestHash) ||
    JSON.stringify(canonical(r.intent)) !==
      JSON.stringify(canonical(expected)) ||
    (action === "create-template"
      ? r.origin?.ruleId !== input.id ||
        r.origin.publishedDigest !== input.publishedDigest ||
        r.origin.publishedVersion !== input.publishedVersion ||
        r.origin.catalogRevision !== input.expectedRevision ||
        r.origin.templateId !== input.templateId ||
        r.origin.acknowledgement !== input.acknowledgement
      : r.rule?.id !== input.id)
  )
    throw new ApiError("invalid_response");
}
async function request<T>(
  path: string,
  kind: string,
  p: Profile,
  s: AbortSignal,
  body?: Write,
): Promise<T> {
  let r: Response;
  try {
    r = await fetch(`/api/simulation/rule-catalog${path}`, {
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
  const text = await r.text();
  if (new TextEncoder().encode(text).length > 512 * 1024)
    throw new ApiError("invalid_response");
  let v: unknown;
  try {
    v = JSON.parse(text);
  } catch {
    throw new ApiError("invalid_response", r.status);
  }
  if (!r.ok)
    throw new ApiError(
      obj(v) && typeof v.error === "string" ? v.error : "internal",
      r.status,
    );
  return parseCatalogResponse(kind, v) as T;
}
const q = (v: Record<string, string | number>) =>
  new URLSearchParams(
    Object.entries(v).map(([k, v]) => [k, String(v)]),
  ).toString();
export const simulationRuleCatalogAPI = {
  list: (
    domainId: string,
    query: string,
    offset: number,
    p: Profile,
    s: AbortSignal,
  ) =>
    request<Page>(
      `/list?${q({ domainId, query, offset, limit: 20 })}`,
      "list",
      p,
      s,
    ),
  detail: (domainId: string, id: string, p: Profile, s: AbortSignal) =>
    request<CatalogRecord>(`/detail?${q({ domainId, id })}`, "detail", p, s),
  export: (domainId: string, id: string, p: Profile, s: AbortSignal) =>
    request<Export>(`/export?${q({ domainId, id })}`, "export", p, s),
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
  write: (action: Action, input: Write, p: Profile, s: AbortSignal) =>
    request<Receipt>(`/${action}`, "receipt", p, s, input),
};
export function downloadCatalog(value: Export) {
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(value, null, 2)], { type: "application/json" }),
  );
  const a = document.createElement("a");
  a.href = url;
  a.download = `simulated-rule-${value.id.replace(/[^a-zA-Z0-9_-]/g, "_")}.json`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
