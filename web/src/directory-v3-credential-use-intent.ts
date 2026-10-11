import type { Profile } from "./api";
import { object } from "./task-api";
import { validDomainID, validRevision } from "./domain-api";
import { validAccountID } from "./operation-account-api";
import {
  directoryV3CredentialPurpose,
  validDirectoryV3CredentialRoleID,
  type DirectoryV3CredentialInput,
  type DirectoryV3CredentialMutation,
  type DirectoryV3CredentialReceipt,
} from "./directory-v3-credential-use-api";
export interface DirectoryV3CredentialIntent
  extends DirectoryV3CredentialInput {
  kind: DirectoryV3CredentialMutation;
  domainId: string;
}
const prefix = "adtr.directory-credential-use-intent.v3:";
const memory = new Map<string, DirectoryV3CredentialIntent>();
const owner = (profile: Profile) => `${profile.ID}:${profile.username}`;
function project(v: unknown): DirectoryV3CredentialIntent | null {
  if (
    !object(v) ||
    !["grant", "revoke"].includes(String(v.kind)) ||
    !validAccountID(v.accountId) ||
    !validDomainID(v.domainId) ||
    !validDirectoryV3CredentialRoleID(v.roleId) ||
    v.roleId === "viewer" ||
    v.purpose !== directoryV3CredentialPurpose ||
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
    kind: v.kind as DirectoryV3CredentialMutation,
    accountId: v.accountId,
    domainId: v.domainId,
    roleId: v.roleId,
    purpose: directoryV3CredentialPurpose,
    expectedAccountRevision: v.expectedAccountRevision,
    expectedCredentialRevision: v.expectedCredentialRevision,
    expectedGrantRevision: v.expectedGrantRevision,
    idempotencyKey: v.idempotencyKey,
  };
}
export function discardDirectoryV3CredentialIntent(profile: Profile) {
  memory.delete(owner(profile));
  try {
    sessionStorage.removeItem(prefix + owner(profile));
  } catch {
    /* Memory remains cleared. */
  }
}
export function readDirectoryV3CredentialIntent(
  profile: Profile,
): DirectoryV3CredentialIntent | null {
  const saved = memory.get(owner(profile));
  if (saved) return saved;
  try {
    const raw = sessionStorage.getItem(prefix + owner(profile));
    const value: unknown = raw && raw.length < 8192 ? JSON.parse(raw) : null;
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
  discardDirectoryV3CredentialIntent(profile);
  return null;
}
export function saveDirectoryV3CredentialIntent(
  profile: Profile,
  value: DirectoryV3CredentialIntent,
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
export function matchesDirectoryV3CredentialIntent(
  receipt: DirectoryV3CredentialReceipt,
  intent: DirectoryV3CredentialIntent,
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
