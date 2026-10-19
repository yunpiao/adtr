export type CredentialCategory =
  | "password"
  | "account_name"
  | "mfa_seed"
  | "totp"
  | "csrf";

export type CredentialProbe = { category: CredentialCategory; value: string };
type LocationCategory =
  | "selection"
  | "observation_id"
  | "object_guid"
  | "object_sid"
  | "asset_text"
  | "asset_timestamp"
  | "pagination"
  | "source_identity"
  | "source_timestamp"
  | "source_counter"
  | "unknown";

type Diagnostic = {
  credentialCategory: CredentialCategory;
  locationCategory: LocationCategory;
  exactValueMatch: boolean;
  embeddedMatch: boolean;
};

// Only these literal schema positions can acquire a public location label.
// Paths and values remain local and are never returned, logged or attached.
function location(path: (string | number)[]): LocationCategory {
  if (path.length === 1 && path[0] === "observationId") return "observation_id";
  if (
    path.length === 2 &&
    path[0] === "selection" &&
    ["domainId", "revision", "credentialRevision"].includes(String(path[1]))
  )
    return "selection";
  if (
    path.length === 2 &&
    path[0] === "page" &&
    ["pageIdx", "pageSize", "total", "totalPage"].includes(String(path[1]))
  )
    return "pagination";
  if (path.length === 2 && path[0] === "source") {
    if (["started_at", "completed_at"].includes(String(path[1])))
      return "source_timestamp";
    if (["elapsed_milliseconds", "pages"].includes(String(path[1])))
      return "source_counter";
    if (
      ["server_name", "dc_host_name", "domain", "naming_context"].includes(
        String(path[1]),
      )
    )
      return "source_identity";
  }
  let fields: (string | number)[];
  if (path[0] === "list" && typeof path[1] === "number") fields = path.slice(2);
  else if (path[0] === "object") fields = path.slice(1);
  else return "unknown";
  if (fields.length === 1) {
    if (fields[0] === "objectGUID") return "object_guid";
    if (fields[0] === "objectSid") return "object_sid";
    if (fields[0] === "whenCreated") return "asset_timestamp";
    if (
      ["distinguishedName", "samAccountName", "mail"].includes(
        String(fields[0]),
      )
    )
      return "asset_text";
  }
  if (
    fields.length === 2 &&
    ["description", "objectClass"].includes(String(fields[0])) &&
    typeof fields[1] === "number"
  )
    return "asset_text";
  return "unknown";
}

// Diagnostic only. The caller retains its original serialized.includes(secret)
// assertion. Inspect the same serialization so getters/toJSON are not re-run.
export function credentialLeakDiagnostic(
  serialized: string,
  secret: string,
  credentialCategory: CredentialCategory,
): Diagnostic {
  if (
    !["password", "account_name", "mfa_seed", "totp", "csrf"].includes(
      credentialCategory,
    )
  )
    throw new Error("Invalid credential diagnostic category");
  const fallback: Diagnostic = {
    credentialCategory,
    locationCategory: "unknown",
    exactValueMatch: false,
    embeddedMatch: serialized.includes(secret),
  };
  try {
    const pending: { value: unknown; path: (string | number)[] }[] = [
      { value: JSON.parse(serialized), path: [] },
    ];
    const locations = new Set<LocationCategory>();
    let exactValueMatch = false;
    let embeddedMatch = false;
    while (pending.length) {
      const { value, path } = pending.pop()!;
      if (value !== null && typeof value === "object") {
        if (Array.isArray(value)) {
          value.forEach((entry, index) =>
            pending.push({ value: entry, path: [...path, index] }),
          );
        } else {
          for (const [key, entry] of Object.entries(value)) {
            if (JSON.stringify(key).includes(secret)) {
              locations.add("unknown");
              embeddedMatch = true;
            }
            pending.push({ value: entry, path: [...path, key] });
          }
        }
      } else if (JSON.stringify(value).includes(secret)) {
        locations.add(location(path));
        const exact =
          typeof value === "string"
            ? value === secret
            : JSON.stringify(value) === secret;
        exactValueMatch ||= exact;
        embeddedMatch ||= !exact;
      }
    }
    if (locations.size === 0) return fallback;
    return {
      credentialCategory,
      locationCategory: locations.size === 1 ? [...locations][0] : "unknown",
      exactValueMatch,
      embeddedMatch,
    };
  } catch {
    return fallback;
  }
}
