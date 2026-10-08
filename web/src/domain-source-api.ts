import { ApiError } from "./api";
import type { Proof } from "./access-api";
import { integer, object } from "./task-api";
import {
  domainError,
  validDomainID,
  validRevision,
  type Created,
  type EndpointInput,
} from "./domain-api";
import { validAccountID, validAccountLabel } from "./operation-account-api";
import { credentialPurpose } from "./credential-use-api";
export type CredentialSource = "custom" | "operation_account" | "unconfigured";
export type SourceOperation = "reference" | "custom" | "detach";
export const sourceLabels: Record<CredentialSource, string> = {
  custom: "自定义凭据",
  operation_account: "已登记操作账户",
  unconfigured: "未配置凭据",
};
export interface SourceReference {
  accountId: string;
  label: string;
  accountRevision: string;
  accountCredentialRevision: string;
  purpose: typeof credentialPurpose;
  explicitlyGranted: boolean;
  eligible: boolean;
  grantRevision: string;
}
export interface SourceDetail {
  domainId: string;
  revision: string;
  connectionCredentialGeneration: string;
  credentialSource: CredentialSource;
  credentialConfigured: boolean;
  testEligible: boolean;
  reference: SourceReference | null;
}
export interface SourceReceipt {
  result: "SUCCESS";
  operation: SourceOperation;
  domainId: string;
  revision: string;
  connectionCredentialGeneration: string;
  credentialSource: CredentialSource;
  replayed: boolean;
  deleted: boolean;
  currentRevision: string;
  currentConnectionCredentialGeneration: string;
  currentCredentialSource: CredentialSource;
}
export interface SourceBase {
  domainId: string;
  expectedRevision: string;
  expectedConnectionCredentialGeneration: string;
  idempotencyKey: string;
}
export type SourceInput =
  | ({
      operation: "reference";
      accountId: string;
      expectedAccountRevision: string;
      expectedAccountCredentialRevision: string;
      expectedGrantRevision: string;
    } & SourceBase)
  | ({ operation: "custom"; username: string; password: string } & SourceBase)
  | ({ operation: "detach" } & SourceBase);
const exact = (v: Record<string, unknown>, keys: string[]) =>
  Object.keys(v).length === keys.length &&
  Object.keys(v).every((k) => keys.includes(k));
const source = (v: unknown): v is CredentialSource =>
  typeof v === "string" && Object.hasOwn(sourceLabels, v);
const invalid = (): never => {
  throw new ApiError("invalid_response");
};
export function validSourceDetail(v: unknown): v is SourceDetail {
  if (
    !object(v) ||
    !exact(v, [
      "domainId",
      "revision",
      "connectionCredentialGeneration",
      "credentialSource",
      "credentialConfigured",
      "testEligible",
      "reference",
    ]) ||
    !validDomainID(v.domainId) ||
    !validRevision(v.revision) ||
    !validRevision(v.connectionCredentialGeneration) ||
    BigInt(v.connectionCredentialGeneration) > BigInt(v.revision) ||
    !source(v.credentialSource) ||
    typeof v.credentialConfigured !== "boolean" ||
    typeof v.testEligible !== "boolean" ||
    (v.credentialSource === "unconfigured" &&
      (v.credentialConfigured || v.testEligible)) ||
    (v.testEligible && !v.credentialConfigured)
  )
    return false;
  if (v.reference === null)
    return v.credentialSource !== "operation_account" || !v.testEligible;
  const r = v.reference;
  return (
    v.credentialSource === "operation_account" &&
    object(r) &&
    exact(r, [
      "accountId",
      "label",
      "accountRevision",
      "accountCredentialRevision",
      "purpose",
      "explicitlyGranted",
      "eligible",
      "grantRevision",
    ]) &&
    validAccountID(r.accountId) &&
    validAccountLabel(r.label) &&
    validRevision(r.accountRevision) &&
    validRevision(r.accountCredentialRevision) &&
    BigInt(r.accountCredentialRevision) <= BigInt(r.accountRevision) &&
    r.purpose === credentialPurpose &&
    typeof r.explicitlyGranted === "boolean" &&
    typeof r.eligible === "boolean" &&
    (r.grantRevision === "0" || validRevision(r.grantRevision)) &&
    (!r.explicitlyGranted || r.grantRevision !== "0") &&
    (!r.eligible || r.explicitlyGranted) &&
    (!v.testEligible || r.eligible)
  );
}
export function validSourceReceipt(v: unknown): v is SourceReceipt {
  return (
    object(v) &&
    exact(v, [
      "result",
      "operation",
      "domainId",
      "revision",
      "connectionCredentialGeneration",
      "credentialSource",
      "replayed",
      "deleted",
      "currentRevision",
      "currentConnectionCredentialGeneration",
      "currentCredentialSource",
    ]) &&
    v.result === "SUCCESS" &&
    ["reference", "custom", "detach"].includes(String(v.operation)) &&
    validDomainID(v.domainId) &&
    validRevision(v.revision) &&
    validRevision(v.connectionCredentialGeneration) &&
    validRevision(v.currentRevision) &&
    validRevision(v.currentConnectionCredentialGeneration) &&
    source(v.credentialSource) &&
    source(v.currentCredentialSource) &&
    v.credentialSource ===
      (v.operation === "reference"
        ? "operation_account"
        : v.operation === "detach"
          ? "unconfigured"
          : "custom") &&
    BigInt(v.connectionCredentialGeneration) <= BigInt(v.revision) &&
    BigInt(v.currentRevision) >= BigInt(v.revision) &&
    BigInt(v.currentConnectionCredentialGeneration) >=
      BigInt(v.connectionCredentialGeneration) &&
    BigInt(v.currentConnectionCredentialGeneration) <=
      BigInt(v.currentRevision) &&
    typeof v.replayed === "boolean" &&
    typeof v.deleted === "boolean" &&
    (!v.deleted || v.currentCredentialSource === "unconfigured")
  );
}
async function transport(
  path: string,
  actor: number,
  signal: AbortSignal,
  body?: unknown,
  csrf?: string,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(`/api/domains${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      redirect: "error",
      cache: "no-store",
      signal,
      headers:
        body === undefined
          ? {}
          : { "Content-Type": "application/json", "X-CSRF-Token": csrf ?? "" },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  } catch (error) {
    if (signal.aborted) throw error;
    throw new ApiError("network");
  }
  if (
    response.ok &&
    (!integer(actor, 1) ||
      response.headers.get("X-ADTR-User-ID") !== String(actor))
  )
    throw new ApiError("domain_source_identity_changed", 401);
  const data: unknown = await response.json().catch(() => {
    throw new ApiError("invalid_response", response.status);
  });
  signal.throwIfAborted();
  if (!response.ok)
    throw new ApiError(
      object(data) && typeof data.error === "string" ? data.error : "internal",
      response.status,
    );
  return data;
}
export const sourceAPI = {
  async detail(
    domainId: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<SourceDetail> {
    const d = await transport(
      `/credential-source?${new URLSearchParams({ domainId })}`,
      actor,
      signal,
    );
    if (!validSourceDetail(d) || d.domainId !== domainId) return invalid();
    return d;
  },
  async receipt(
    key: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<SourceReceipt> {
    const d = await transport(
      `/credential-source/mutation?${new URLSearchParams({ idempotencyKey: key })}`,
      actor,
      signal,
    );
    if (
      !object(d) ||
      !exact(d, ["receipt"]) ||
      !validSourceReceipt(d.receipt) ||
      !d.receipt.replayed
    )
      return invalid();
    return d.receipt;
  },
  async mutate(
    input: SourceInput,
    proof: Proof,
    csrf: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<SourceReceipt> {
    const { operation, ...body } = input;
    const d = await transport(
      `/credential-source/${operation}`,
      actor,
      signal,
      { ...body, ...proof },
      csrf,
    );
    if (
      !validSourceReceipt(d) ||
      d.operation !== operation ||
      d.domainId !== input.domainId ||
      BigInt(d.revision) !== BigInt(input.expectedRevision) + 1n ||
      BigInt(d.connectionCredentialGeneration) !==
        BigInt(input.expectedConnectionCredentialGeneration) + 1n
    )
      return invalid();
    return d;
  },
  async createUnconfigured(
    input: EndpointInput & { domain: string; idempotencyKey: string },
    proof: Proof,
    csrf: string,
    actor: number,
    signal: AbortSignal,
  ): Promise<Created> {
    const d = await transport(
      "/create-unconfigured",
      actor,
      signal,
      { ...input, ...proof },
      csrf,
    );
    if (
      !object(d) ||
      !exact(d, [
        "result",
        "domainId",
        "revision",
        "requiresResourceAssignment",
        "replayed",
      ]) ||
      d.result !== "SUCCESS" ||
      !validDomainID(d.domainId) ||
      !validRevision(d.revision) ||
      d.requiresResourceAssignment !== true ||
      typeof d.replayed !== "boolean"
    )
      return invalid();
    return d as unknown as Created;
  },
};
export const uncertainSourceError = (e: unknown) =>
  !(e instanceof ApiError) ||
  e.status >= 500 ||
  ["network", "invalid_response", "idempotency_conflict"].includes(e.code);
export const sourceError = (e: unknown) =>
  e instanceof ApiError && e.code === "domain_source_identity_changed"
    ? "登录身份已改变，请重新确认登录状态。"
    : e instanceof ApiError && e.code === "not_found"
      ? "记录不存在或当前无权访问；未找到回执不能证明原操作未提交。"
      : domainError(e);
