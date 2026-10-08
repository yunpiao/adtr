import type { Profile } from "./api";
import { object } from "./task-api";
import { validDomainID } from "./domain-api";
import { validAccountID, type MutationKind } from "./operation-account-api";
export type OperationAccountIntent = {
  kind: MutationKind;
  key: string;
  domainId: string;
  accountId?: string;
};
const storage = "adtr.operation-account-intent.v1";
const memory = new Map<string, OperationAccountIntent>();
const owner = (profile: Profile) => `${profile.ID}:${profile.username}`;
const storageKey = (profile: Profile) => `${storage}:${owner(profile)}`;
function project(value: unknown): OperationAccountIntent | null {
  if (
    !object(value) ||
    !["create", "update", "delete"].includes(String(value.kind)) ||
    typeof value.key !== "string" ||
    !/^[A-Za-z0-9_-]{8,128}$/u.test(value.key) ||
    !validDomainID(value.domainId) ||
    (value.kind !== "create" && !validAccountID(value.accountId))
  )
    return null;
  return {
    kind: value.kind as MutationKind,
    key: value.key,
    domainId: value.domainId,
    ...(value.kind === "create"
      ? {}
      : { accountId: value.accountId as string }),
  };
}
export function discardOperationAccountIntent(profile?: Profile) {
  if (profile) memory.delete(owner(profile));
  else memory.clear();
  try {
    if (profile) sessionStorage.removeItem(storageKey(profile));
    else
      for (const key of Object.keys(sessionStorage)) {
        if (key.startsWith(`${storage}:`)) sessionStorage.removeItem(key);
      }
  } catch {
    /* Memory is cleared even when storage is blocked. */
  }
}
export function readOperationAccountIntent(
  profile: Profile,
): OperationAccountIntent | null {
  const saved = memory.get(owner(profile));
  if (saved) return saved;
  try {
    const data: unknown = JSON.parse(
      sessionStorage.getItem(storageKey(profile)) ?? "null",
    );
    if (object(data) && data.owner === owner(profile)) {
      const intent = project(data.intent);
      if (intent) {
        memory.set(owner(profile), intent);
        return intent;
      }
    }
  } catch {
    /* Invalid and unavailable storage is ignored. */
  }
  discardOperationAccountIntent(profile);
  return null;
}
export function saveOperationAccountIntent(
  profile: Profile,
  value: OperationAccountIntent,
) {
  const intent = project(value);
  if (!intent) throw new Error("Invalid operation intent");
  memory.set(owner(profile), intent);
  try {
    sessionStorage.setItem(
      storageKey(profile),
      JSON.stringify({ owner: owner(profile), intent }),
    );
  } catch {
    /* Navigation can still recover the key from memory. */
  }
}
