import { validDomainID } from "./domain-api";
import { integer, object } from "./task-api";
import { exportKind, validExportID } from "./group-asset-export-v3-api";
export const exportRecoveryStorage = "adtr.group-assets-v3.export-recovery";
export interface ExportRecovery { version: 1; kind: typeof exportKind; actorID: number; domainId: string; idempotencyKey: string }
let fallback: ExportRecovery | null = null;
// This is an untrusted recovery locator, never an authority or task result.
// No request text, username, proof, token, artifact or private response is saved.
export function readExportRecovery(actorID: number): ExportRecovery | null {
  let value: unknown = fallback;
  try {
    const raw = sessionStorage.getItem(exportRecoveryStorage);
    if (raw !== null) {
      value = null;
      if (raw.length <= 1024) value = JSON.parse(raw);
    }
  } catch { /* Memory still permits recovery when browser storage is disabled. */ }
  if (!object(value) || Object.keys(value).sort().join(",") !== "actorID,domainId,idempotencyKey,kind,version" ||
    value.version !== 1 || value.kind !== exportKind || !integer(value.actorID, 1) || value.actorID !== actorID ||
    !validDomainID(value.domainId) || !validExportID(value.idempotencyKey)) {
    clearExportRecovery(); return null;
  }
  fallback = value as unknown as ExportRecovery;
  return { ...fallback };
}
export function writeExportRecovery(value: ExportRecovery) {
  fallback = { version: 1, kind: exportKind, actorID: value.actorID, domainId: value.domainId, idempotencyKey: value.idempotencyKey };
  try { sessionStorage.setItem(exportRecoveryStorage, JSON.stringify(fallback)); } catch { /* Keep memory locator. */ }
}
export function clearExportRecovery() {
  fallback = null;
  try { sessionStorage.removeItem(exportRecoveryStorage); } catch { /* Storage disabled. */ }
}
