import type { Profile } from "./api";
import { object } from "./task-api";
import { validDomainID, validRevision } from "./domain-api";
import { validAccountID } from "./operation-account-api";
import {
  directoryV2CredentialPurpose,
  validDirectoryV2CredentialRoleID,
  type DirectoryV2CredentialInput,
  type DirectoryV2CredentialMutation,
  type DirectoryV2CredentialReceipt,
} from "./directory-v2-credential-use-api";
export interface DirectoryV2CredentialIntent
  extends DirectoryV2CredentialInput {
  kind: DirectoryV2CredentialMutation;
  domainId: string;
}
const prefix = "adtr.directory-credential-use-intent.v2:";
const memory = new Map<string, DirectoryV2CredentialIntent>();
const owner = (profile: Profile) => `${profile.ID}:${profile.username}`;
function project(v: unknown): DirectoryV2CredentialIntent | null {
  if (
    !object(v) ||
    !["grant", "revoke"].includes(String(v.kind)) ||
    !validAccountID(v.accountId) ||
    !validDomainID(v.domainId) ||
    !validDirectoryV2CredentialRoleID(v.roleId) ||
    v.roleId === "viewer" ||
    v.purpose !== directoryV2CredentialPurpose ||
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
    kind: v.kind as DirectoryV2CredentialMutation,
    accountId: v.accountId,
    domainId: v.domainId,
    roleId: v.roleId,
    purpose: directoryV2CredentialPurpose,
    expectedAccountRevision: v.expectedAccountRevision,
    expectedCredentialRevision: v.expectedCredentialRevision,
    expectedGrantRevision: v.expectedGrantRevision,
    idempotencyKey: v.idempotencyKey,
  };
}
export function discardDirectoryV2CredentialIntent(profile: Profile) {
  memory.delete(owner(profile));
  try {
    sessionStorage.removeItem(prefix + owner(profile));
  } catch {
    /* Memory remains cleared. */
  }
}
export function readDirectoryV2CredentialIntent(
  profile: Profile,
): DirectoryV2CredentialIntent | null {
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
  discardDirectoryV2CredentialIntent(profile);
  return null;
}
export function saveDirectoryV2CredentialIntent(
  profile: Profile,
  value: DirectoryV2CredentialIntent,
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
export function matchesDirectoryV2CredentialIntent(
  receipt: DirectoryV2CredentialReceipt,
  intent: DirectoryV2CredentialIntent,
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
