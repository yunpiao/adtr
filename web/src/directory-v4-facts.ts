import {
  directoryV3DisplaySPN,
  validDirectoryV3Object,
  type DirectoryV3Object,
} from "./directory-v3-facts";
import { integer, object } from "./task-api";

export interface DirectoryV4Object extends DirectoryV3Object {
  rawMember: string[] | null;
  returnedMemberCount: number | null;
  memberReadState: "not_returned" | "returned";
}

// These local protection budgets are mandatory, not AD capacity guarantees.
// Removing them permits unbounded input; legitimate larger groups are rejected.
export const directoryV4MemberLimits = {
  values: 256,
  valueBytes: 4096,
  totalBytes: 65536,
} as const;

export function validDirectoryV4Object(
  value: unknown,
): value is DirectoryV4Object {
  if (
    !object(value) ||
    !Object.hasOwn(value, "rawMember") ||
    !Object.hasOwn(value, "returnedMemberCount") ||
    !Object.hasOwn(value, "memberReadState")
  )
    return false;
  // Retain the closed v3 shape and validator unchanged.
  const { rawMember, returnedMemberCount, memberReadState, ...base } = value;
  if (!validDirectoryV3Object(base)) return false;
  if (rawMember === null)
    return returnedMemberCount === null && memberReadState === "not_returned";
  if (
    base.kind !== "group" ||
    memberReadState !== "returned" ||
    !Array.isArray(rawMember) ||
    rawMember.length > directoryV4MemberLimits.values ||
    !integer(returnedMemberCount, 0, directoryV4MemberLimits.values) ||
    returnedMemberCount !== rawMember.length
  )
    return false;
  const seen = new Set<string>();
  const encoder = new TextEncoder();
  let bytes = 0;
  for (const member of rawMember) {
    // Reject unpaired surrogates before TextEncoder can replace them. The u
    // flag preserves valid supplementary code points, including emoji.
    if (
      typeof member !== "string" ||
      member.length === 0 ||
      member.length > directoryV4MemberLimits.valueBytes ||
      /[\ud800-\udfff]/u.test(member) ||
      seen.has(member)
    )
      return false;
    const size = encoder.encode(member).length;
    bytes += size;
    if (
      size > directoryV4MemberLimits.valueBytes ||
      bytes > directoryV4MemberLimits.totalBytes
    )
      return false;
    seen.add(member);
  }
  return true;
}

// Presentation only, never a DN parser: visibly escape original backslashes
// before controls so a literal "\\u0000" remains distinct from an observed NUL.
export const directoryV4DisplayMember = directoryV3DisplaySPN;
