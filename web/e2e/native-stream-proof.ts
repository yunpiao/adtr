// Test-only evidence allocation. A native failure is never called delivery.
export type NativeStreamMode = "native-finish" | "stable-body";
export type NativeTerminal = {
  event: "finished" | "failed";
  failure: string | null;
  cancelled: boolean | null;
};
export type ConsumedBodyProof = {
  uncanceledEOF: boolean;
  expectedByteLength: number;
  byteLength: number;
  expectedSHA256: string;
  sha256: string;
  readers: number;
  released: number;
  signalAborted: boolean;
  cancelCalls: number;
  metadataMatches: boolean;
  identityMatches: boolean;
};
// Bind the exact host Request to the matching method/URL sequence from the
// independent CDP session. Callers already restrict both streams to one frame.
export function nativeRequestTerminal<T>(
  request: T,
  host: readonly T[],
  nativeIDs: readonly string[],
  terminals: ReadonlyMap<string, NativeTerminal[]>,
) {
  const ordinal = host.indexOf(request);
  const bound =
    ordinal >= 0 &&
    host.length === nativeIDs.length &&
    new Set(host).size === host.length &&
    new Set(nativeIDs).size === nativeIDs.length;
  return {
    bound,
    events: bound ? (terminals.get(nativeIDs[ordinal]) ?? []) : [],
  };
}
export function nativeStreamMode(): NativeStreamMode {
  const value = process.env.ADTR_E2E_NATIVE_STREAM_MODE ?? "native-finish";
  if (value !== "native-finish" && value !== "stable-body")
    throw new Error("Unknown native stream evidence mode");
  return value;
}
export function proveNativeStream(
  mode: NativeStreamMode,
  browserVersion: string,
  body: ConsumedBodyProof,
  playwright: readonly NativeTerminal[],
  native: readonly NativeTerminal[],
) {
  if (
    body.uncanceledEOF !== true ||
    !Number.isSafeInteger(body.byteLength) ||
    body.byteLength <= 0 ||
    body.byteLength > 8 * 1024 * 1024 ||
    body.byteLength !== body.expectedByteLength ||
    !/^[a-f0-9]{64}$/u.test(body.sha256) ||
    body.sha256 !== body.expectedSHA256 ||
    body.readers !== 1 ||
    body.released !== 1 ||
    body.signalAborted !== false ||
    body.cancelCalls !== 0 ||
    body.metadataMatches !== true ||
    body.identityMatches !== true
  )
    throw new Error("Original application reader proof is incomplete");
  if (mode !== "native-finish" && mode !== "stable-body")
    throw new Error("Unknown native stream evidence mode");
  if (mode === "stable-body" && browserVersion !== "141.0.7390.37")
    throw new Error(
      "Stable body evidence is restricted to the proven Chromium baseline",
    );
  if (playwright.length !== 1 || native.length !== 1)
    throw new Error("Missing or duplicate native terminal evidence");
  const pw = playwright[0],
    cdp = native[0];
  if (
    pw.event !== cdp.event ||
    pw.failure !== cdp.failure ||
    pw.cancelled !== null
  )
    throw new Error("Contradictory native terminal evidence");
  if (pw.event === "finished" && pw.failure === null && cdp.cancelled === null)
    return {
      application: "complete-consumed" as const,
      native: "requestfinished" as const,
      failure: null,
      mode,
      browserVersion,
      byteLength: body.byteLength,
      sha256: body.sha256,
      uncanceledEOF: true as const,
      identityMatches: true as const,
      metadataMatches: true as const,
    };
  // This branch requires the complete independent original-reader proof above,
  // exact affected engine, and agreement of both terminal event channels.
  // It records the upstream defect; it does not relabel requestfailed as finished.
  if (
    mode === "stable-body" &&
    pw.event === "failed" &&
    pw.failure === "net::ERR_ABORTED" &&
    cdp.cancelled === true
  )
    return {
      application: "complete-consumed" as const,
      native: "requestfailed" as const,
      failure: "net::ERR_ABORTED" as const,
      mode,
      browserVersion,
      byteLength: body.byteLength,
      sha256: body.sha256,
      uncanceledEOF: true as const,
      identityMatches: true as const,
      metadataMatches: true as const,
    };
  throw new Error(
    "Native stream did not meet its required completion evidence",
  );
}
