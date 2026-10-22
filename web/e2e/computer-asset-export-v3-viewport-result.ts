import { types } from "node:util";

export type ComputerViewportRatioClass =
  | "zero"
  | "positive_not_matching"
  | "within_tolerance"
  | "exact_one"
  | "unavailable";

function ordinaryObject(value: unknown): value is object {
  // Native detection must precede reflection, including Array.isArray on a
  // revoked Proxy. No supplied getter, proxy trap or coercion may run.
  return typeof value === "object" && value !== null && !types.isProxy(value) && !Array.isArray(value);
}

function ownData(value: object, key: string): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  return descriptor && Object.prototype.hasOwnProperty.call(descriptor, "value") ? descriptor.value : undefined;
}

// Playwright 1.56.1 supplies a synthetic received string, not a numeric actual.
// Only the original button ratio1 failure calls this projection. It neither
// reads browser state nor establishes geometry or a cause for that failure.
export function classifyComputerViewportResult(error: unknown): ComputerViewportRatioClass {
  try {
    if (!ordinaryObject(error)) return "unavailable";
    const result = ownData(error, "matcherResult");
    if (!ordinaryObject(result)) return "unavailable";
    if (ownData(result, "name") !== "toBeInViewport" ||
        ownData(result, "expected") !== "in viewport" || ownData(result, "pass") !== false) return "unavailable";
    const actual = ownData(result, "actual");
    const prefix = "viewport ratio ";
    if (typeof actual !== "string" || actual.length > 64 || !actual.startsWith(prefix)) return "unavailable";
    const token = actual.slice(prefix.length);
    if (!/^(?:0|1|0\.[0-9]+|[1-9](?:\.[0-9]+)?e-[1-9][0-9]{0,2})$/.test(token)) return "unavailable";
    const ratio = Number(token);
    if (!Number.isFinite(ratio) || ratio < 0 || ratio > 1 || String(ratio) !== token) return "unavailable";
    if (ratio === 0) return "zero";
    if (ratio === 1) return "exact_one";
    // The pinned matcher uses strict >, so equality is still non-matching.
    return ratio > 1 - 1e-9 ? "within_tolerance" : "positive_not_matching";
  } catch {
    return "unavailable";
  }
}

// Never interpolate an input. Empty means this particular assertion did not
// fail; even an unexpected caller value can produce only a fixed safe suffix.
export function computerViewportResultSuffix(value: unknown): string {
  switch (value) {
    case "": return "";
    case "zero": return "; viewport_ratio_class=zero";
    case "positive_not_matching": return "; viewport_ratio_class=positive_not_matching";
    case "within_tolerance": return "; viewport_ratio_class=within_tolerance";
    case "exact_one": return "; viewport_ratio_class=exact_one";
    default: return "; viewport_ratio_class=unavailable";
  }
}
