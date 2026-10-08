import type { Profile } from "./api";
import { object } from "./task-api";
import { validDomainID, validRevision } from "./domain-api";
import { validAccountID } from "./operation-account-api";
import {
  credentialPurpose,
  validCredentialRoleID,
  type CredentialInput,
  type CredentialMutation,
  type CredentialReceipt,
} from "./credential-use-api";
export interface CredentialIntent extends CredentialInput {
  kind: CredentialMutation;
  domainId: string;
}
const prefix = "adtr.credential-use-intent.v1:";
const memory = new Map<string, CredentialIntent>();
const owner = (profile: Profile) => `${profile.ID}:${profile.username}`;
function project(v: unknown): CredentialIntent | null {
  if (
    !object(v) ||
    !["grant", "revoke"].includes(String(v.kind)) ||
    !validAccountID(v.accountId) ||
    !validDomainID(v.domainId) ||
    !validCredentialRoleID(v.roleId) ||
    v.roleId === "viewer" ||
    v.purpose !== credentialPurpose ||
    !validRevision(v.expectedAccountRevision) ||
    !validRevision(v.expectedCredentialRevision) ||
    !(
      validRevision(v.expectedGrantRevision) ||
      (v.kind === "grant" && v.expectedGrantRevision === "0")
    ) ||
    typeof v.idempotencyKey !== "string" ||
    !/^[A-Za-z0-9_-]{8,128}$/u.test(v.idempotencyKey)
  )
    return null;
  // Project an explicit nonsecret allowlist before either storage destination.
  return {
    kind: v.kind as CredentialMutation,
    accountId: v.accountId,
    domainId: v.domainId,
    roleId: v.roleId,
    purpose: credentialPurpose,
    expectedAccountRevision: v.expectedAccountRevision,
    expectedCredentialRevision: v.expectedCredentialRevision,
    expectedGrantRevision: v.expectedGrantRevision,
    idempotencyKey: v.idempotencyKey,
  };
}
export function discardCredentialIntent(profile: Profile) {
  memory.delete(owner(profile));
  try {
    sessionStorage.removeItem(prefix + owner(profile));
  } catch {
    /* Memory remains cleared. */
  }
}
export function readCredentialIntent(
  profile: Profile,
): CredentialIntent | null {
  const saved = memory.get(owner(profile));
  if (saved) return saved;
  try {
    const value: unknown = JSON.parse(
      sessionStorage.getItem(prefix + owner(profile)) ?? "null",
    );
    if (object(value) && value.owner === owner(profile)) {
      const intent = project(value.intent);
      if (intent) {
        memory.set(owner(profile), intent);
        return intent;
      }
    }
  } catch {
    /* Ignore malformed or unavailable storage. */
  }
  discardCredentialIntent(profile);
  return null;
}
export function saveCredentialIntent(
  profile: Profile,
  value: CredentialIntent,
) {
  const intent = project(value);
  if (!intent) throw new Error("Invalid credential intent");
  memory.set(owner(profile), intent);
  try {
    sessionStorage.setItem(
      prefix + owner(profile),
      JSON.stringify({ owner: owner(profile), intent }),
    );
  } catch {
    /* Memory preserves navigation recovery. */
  }
}
export function matchesCredentialIntent(
  receipt: CredentialReceipt,
  intent: CredentialIntent,
) {
  return (
    receipt.operation === intent.kind &&
    receipt.accountId === intent.accountId &&
    receipt.domainId === intent.domainId &&
    receipt.roleId === intent.roleId &&
    receipt.purpose === intent.purpose &&
    receipt.accountCredentialRevision === intent.expectedCredentialRevision
  );
}
