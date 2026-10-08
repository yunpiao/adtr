import type { Profile } from "./api";
import { object } from "./task-api";
import { validDomainID, validRevision } from "./domain-api";
import type { SourceOperation, SourceReceipt } from "./domain-source-api";
// Never persist an account pointer, pair, proof, grant or raw request body.
export interface SourceIntent {
  operation: SourceOperation;
  domainId: string;
  expectedRevision: string;
  expectedConnectionCredentialGeneration: string;
  idempotencyKey: string;
}
const prefix = "adtr.domain-source-intent.v1:";
const memory = new Map<string, SourceIntent>();
const owner = (p: Profile) => `${p.ID}:${p.username}`;
function project(v: unknown): SourceIntent | null {
  if (
    !object(v) ||
    !["reference", "custom", "detach"].includes(String(v.operation)) ||
    !validDomainID(v.domainId) ||
    !validRevision(v.expectedRevision) ||
    !validRevision(v.expectedConnectionCredentialGeneration) ||
    typeof v.idempotencyKey !== "string" ||
    !/^[A-Za-z0-9_-]{8,128}$/u.test(v.idempotencyKey)
  )
    return null;
  return {
    operation: v.operation as SourceOperation,
    domainId: v.domainId,
    expectedRevision: v.expectedRevision,
    expectedConnectionCredentialGeneration:
      v.expectedConnectionCredentialGeneration,
    idempotencyKey: v.idempotencyKey,
  };
}
export function discardSourceIntent(p: Profile) {
  memory.delete(owner(p));
  try {
    sessionStorage.removeItem(prefix + owner(p));
  } catch {
    /* Memory cleared. */
  }
}
export function readSourceIntent(p: Profile): SourceIntent | null {
  const saved = memory.get(owner(p));
  if (saved) return saved;
  try {
    const v: unknown = JSON.parse(
      sessionStorage.getItem(prefix + owner(p)) ?? "null",
    );
    if (object(v) && v.owner === owner(p)) {
      const intent = project(v.intent);
      if (intent) {
        memory.set(owner(p), intent);
        return intent;
      }
    }
  } catch {
    /* Invalid storage is discarded. */
  }
  discardSourceIntent(p);
  return null;
}
export function saveSourceIntent(p: Profile, value: SourceIntent) {
  const intent = project(value);
  if (!intent) throw Error("Invalid source intent");
  memory.set(owner(p), intent);
  try {
    sessionStorage.setItem(
      prefix + owner(p),
      JSON.stringify({ owner: owner(p), intent }),
    );
  } catch {
    /* Memory preserves same-tab recovery. */
  }
}
export const matchesSourceIntent = (r: SourceReceipt, i: SourceIntent) =>
  r.operation === i.operation &&
  r.domainId === i.domainId &&
  BigInt(r.revision) === BigInt(i.expectedRevision) + 1n &&
  BigInt(r.connectionCredentialGeneration) ===
    BigInt(i.expectedConnectionCredentialGeneration) + 1n;
