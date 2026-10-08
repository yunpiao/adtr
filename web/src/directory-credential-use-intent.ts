import type { Profile } from "./api";
import { object } from "./task-api";
import { validDomainID, validRevision } from "./domain-api";
import { validAccountID } from "./operation-account-api";
import {
  directoryCredentialPurpose,
  validDirectoryCredentialRoleID,
  type DirectoryCredentialInput,
  type DirectoryCredentialMutation,
  type DirectoryCredentialReceipt,
} from "./directory-credential-use-api";
export interface DirectoryCredentialIntent extends DirectoryCredentialInput {
  kind: DirectoryCredentialMutation;
  domainId: string;
}
const prefix = "adtr.directory-credential-use-intent.v1:";
const memory = new Map<string, DirectoryCredentialIntent>();
const owner = (profile: Profile) => `${profile.ID}:${profile.username}`;
function project(v: unknown): DirectoryCredentialIntent | null {
  if (
    !object(v) ||
    !["grant", "revoke"].includes(String(v.kind)) ||
    !validAccountID(v.accountId) ||
    !validDomainID(v.domainId) ||
    !validDirectoryCredentialRoleID(v.roleId) ||
    v.roleId === "viewer" ||
    v.purpose !== directoryCredentialPurpose ||
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
    kind: v.kind as DirectoryCredentialMutation,
    accountId: v.accountId,
    domainId: v.domainId,
    roleId: v.roleId,
    purpose: directoryCredentialPurpose,
    expectedAccountRevision: v.expectedAccountRevision,
    expectedCredentialRevision: v.expectedCredentialRevision,
    expectedGrantRevision: v.expectedGrantRevision,
    idempotencyKey: v.idempotencyKey,
  };
}
export function discardDirectoryCredentialIntent(profile: Profile) {
  memory.delete(owner(profile));
  try {
    sessionStorage.removeItem(prefix + owner(profile));
  } catch {
    /* Memory remains cleared. */
  }
}
export function readDirectoryCredentialIntent(
  profile: Profile,
): DirectoryCredentialIntent | null {
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
  discardDirectoryCredentialIntent(profile);
  return null;
}
export function saveDirectoryCredentialIntent(
  profile: Profile,
  value: DirectoryCredentialIntent,
) {
  const intent = project(value);
  if (!intent) throw new Error("Invalid directory credential intent");
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
export function matchesDirectoryCredentialIntent(
  receipt: DirectoryCredentialReceipt,
  intent: DirectoryCredentialIntent,
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
