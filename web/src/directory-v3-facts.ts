import { directoryV2DisplayText } from "./directory-v2-json";
import {
  validDirectoryV2Object,
  type DirectoryV2Object,
} from "./directory-v2-api";
import { integer, object } from "./task-api";

export interface DirectoryV3Object extends DirectoryV2Object {
  primaryGroupID: number | null;
  servicePrincipalName: string[] | null;
}
// Strip exactly the two version-3 fields, then validate the unchanged closed v2
// component. No v2 parser or accepted object shape is expanded.
export function validDirectoryV3Object(
  value: unknown,
): value is DirectoryV3Object {
  if (
    !object(value) ||
    !Object.hasOwn(value, "primaryGroupID") ||
    !Object.hasOwn(value, "servicePrincipalName")
  )
    return false;
  const { primaryGroupID, servicePrincipalName, ...base } = value;
  if (
    !validDirectoryV2Object(base) ||
    !(primaryGroupID === null || integer(primaryGroupID, 0, 4294967295))
  )
    return false;
  if (servicePrincipalName === null) return true;
  if (
    !Array.isArray(servicePrincipalName) ||
    servicePrincipalName.length < 1 ||
    servicePrincipalName.length > 128
  )
    return false;
  const seen = new Set<string>();
  let bytes = 0;
  const encoder = new TextEncoder();
  for (const spn of servicePrincipalName) {
    if (
      typeof spn !== "string" ||
      spn.length === 0 ||
      spn.length > 1024 ||
      /[\ud800-\udfff]/u.test(spn) ||
      seen.has(spn)
    )
      return false;
    const size = encoder.encode(spn).length;
    if (size > 4096) return false;
    bytes += size;
    if (bytes > 32768) return false;
    seen.add(spn);
  }
  return true;
}

// Presentation is reversible: original backslashes are escaped BEFORE control
// characters. Literal "\\u0000" and an observed NUL must remain distinguishable.
export function directoryV3DisplaySPN(value: string): string {
  return directoryV2DisplayText(value.replaceAll("\\", "\\\\"));
}
