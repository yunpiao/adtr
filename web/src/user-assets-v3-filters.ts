import {
  validUserAssetsV2Filters,
  type UserAssetsV2Filters,
} from "./user-assets-v2-filters";

export const uacDisableBits = ["set", "clear", "not_returned"] as const;
export type UACDisableBit = (typeof uacDisableBits)[number];
export interface UserAssetsV3Filters extends UserAssetsV2Filters {
  uacDisableBit?: UACDisableBit;
}
export const uacDisableBitLabels: Record<UACDisableBit, string> = {
  set: "禁用位已设置",
  clear: "禁用位未设置",
  not_returned: "UAC未返回",
};
export const validUACDisableBit = (value: unknown): value is UACDisableBit =>
  uacDisableBits.some((bit) => bit === value);
export function validUserAssetsV3Filters(q: UserAssetsV3Filters): boolean {
  return (
    validUserAssetsV2Filters(q) &&
    (!Object.hasOwn(q, "uacDisableBit") || validUACDisableBit(q.uacDisableBit))
  );
}
// Call only after the strict directory-object parser validates uint32 | null.
// The low ACCOUNTDISABLE bit is independent of JS signed bitwise conversion.
export function userUACDisableBit(uac: number | null): UACDisableBit {
  return uac === null ? "not_returned" : (uac & 2) === 2 ? "set" : "clear";
}
export const matchesUserUACDisableBit = (
  uac: number | null,
  filter?: UACDisableBit,
): boolean => filter === undefined || userUACDisableBit(uac) === filter;
