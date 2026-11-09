import { validDomainID } from "./domain-api";
import { integer, object } from "./task-api";
import { exportKind, validExportID } from "./group-asset-export-v3-api";
export const exportRecoveryStorage = "adtr.group-assets-v3.export-recovery";
export interface ExportRecovery {
  version: 1;
  kind: typeof exportKind;
  actorID: number;
  domainId: string;
  idempotencyKey: string;
}
let fallback: ExportRecovery | null = null;
// A failed write/clear leaves older durable data behind. Until a local write or
// clear succeeds, that data cannot replace the newer in-memory value/tombstone.
let pendingStorage = false;
// This is an untrusted recovery locator, never an authority or task result.
// No request text, username, proof, token, artifact or private response is saved.
export function readExportRecovery(actorID: number): ExportRecovery | null {
  let value: unknown = fallback;
  let raw: string | null = null;
  if (!pendingStorage) {
    try {
      raw = sessionStorage.getItem(exportRecoveryStorage);
    } catch {
      // An unread initial record is unknown, not an instruction to clear it.
      if (fallback === null) return null;
    }
  }
  if (raw !== null) {
    value = null;
    try {
      if (raw.length <= 1024) value = JSON.parse(raw);
    } catch {
      /* Observed malformed storage is invalid, unlike an unavailable read. */
    }
  }
  if (
    !object(value) ||
    Object.keys(value).sort().join(",") !==
      "actorID,domainId,idempotencyKey,kind,version" ||
    value.version !== 1 ||
    value.kind !== exportKind ||
    !integer(value.actorID, 1) ||
    value.actorID !== actorID ||
    !validDomainID(value.domainId) ||
    !validExportID(value.idempotencyKey)
  ) {
    clearExportRecovery();
    return null;
  }
  fallback = value as unknown as ExportRecovery;
  return { ...fallback };
}
export function writeExportRecovery(value: ExportRecovery) {
  fallback = {
    version: 1,
    kind: exportKind,
    actorID: value.actorID,
    domainId: value.domainId,
    idempotencyKey: value.idempotencyKey,
  };
  pendingStorage = true;
  try {
    sessionStorage.setItem(exportRecoveryStorage, JSON.stringify(fallback));
    pendingStorage = false;
  } catch {
    /* Keep memory locator. */
  }
}
export function clearExportRecovery() {
  fallback = null;
  pendingStorage = true;
  try {
    sessionStorage.removeItem(exportRecoveryStorage);
    pendingStorage = false;
  } catch {
    /* Keep the in-memory clear marker. */
  }
}
