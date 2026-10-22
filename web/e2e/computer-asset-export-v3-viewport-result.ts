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

export type ComputerButtonGeometry = { status: "unavailable" } | {
  status: "sampled"; connected: boolean;
  left: number; top: number; right: number; bottom: number; width: number; height: number;
  innerWidth: number; innerHeight: number; clientWidth: number; clientHeight: number;
};

// Serialized by locator.evaluate: deliberately self-contained and synchronous.
// This is a pre-assert border-box sample, not the later matcher's intersection.
export function captureComputerButtonGeometry(element: Element): ComputerButtonGeometry {
  try {
    const document = element.ownerDocument;
    const view = document?.defaultView, root = document?.documentElement;
    if (!view || !root) return { status: "unavailable" };
    const rect = element.getBoundingClientRect();
    const connected = element.isConnected;
    const left = rect.left, top = rect.top, right = rect.right, bottom = rect.bottom;
    const width = rect.width, height = rect.height;
    const innerWidth = view.innerWidth, innerHeight = view.innerHeight;
    const clientWidth = root.clientWidth, clientHeight = root.clientHeight;
    if (typeof connected !== "boolean" ||
        !Number.isFinite(left) || !Number.isFinite(top) || !Number.isFinite(right) || !Number.isFinite(bottom) ||
        !Number.isFinite(width) || !Number.isFinite(height) ||
        Math.abs(left) > 1_000_000 || Math.abs(top) > 1_000_000 || Math.abs(right) > 1_000_000 || Math.abs(bottom) > 1_000_000 ||
        width < 0 || width > 1_000_000 || height < 0 || height > 1_000_000 || right < left || bottom < top ||
        !Number.isInteger(innerWidth) || innerWidth < 1 || innerWidth > 16_384 ||
        !Number.isInteger(innerHeight) || innerHeight < 1 || innerHeight > 16_384 ||
        !Number.isInteger(clientWidth) || clientWidth < 1 || clientWidth > 16_384 ||
        !Number.isInteger(clientHeight) || clientHeight < 1 || clientHeight > 16_384) return { status: "unavailable" };
    return { status: "sampled", connected, left, top, right, bottom, width, height, innerWidth, innerHeight, clientWidth, clientHeight };
  } catch {
    return { status: "unavailable" };
  }
}

export function projectComputerButtonGeometry(value: unknown): ComputerButtonGeometry {
  try {
    if (!ordinaryObject(value)) return { status: "unavailable" };
    const keys = Reflect.ownKeys(value), status = ownData(value, "status");
    if (status === "unavailable" && keys.length === 1) return { status: "unavailable" };
    if (status !== "sampled" || keys.length !== 12) return { status: "unavailable" };
    const connected = ownData(value, "connected");
    const left = ownData(value, "left"), top = ownData(value, "top"), right = ownData(value, "right"), bottom = ownData(value, "bottom");
    const width = ownData(value, "width"), height = ownData(value, "height");
    const innerWidth = ownData(value, "innerWidth"), innerHeight = ownData(value, "innerHeight");
    const clientWidth = ownData(value, "clientWidth"), clientHeight = ownData(value, "clientHeight");
    if (typeof connected !== "boolean" || typeof left !== "number" || typeof top !== "number" ||
        typeof right !== "number" || typeof bottom !== "number" || typeof width !== "number" || typeof height !== "number" ||
        typeof innerWidth !== "number" || typeof innerHeight !== "number" || typeof clientWidth !== "number" || typeof clientHeight !== "number" ||
        !Number.isFinite(left) || !Number.isFinite(top) || !Number.isFinite(right) || !Number.isFinite(bottom) ||
        !Number.isFinite(width) || !Number.isFinite(height) ||
        Math.abs(left) > 1_000_000 || Math.abs(top) > 1_000_000 || Math.abs(right) > 1_000_000 || Math.abs(bottom) > 1_000_000 ||
        width < 0 || width > 1_000_000 || height < 0 || height > 1_000_000 || right < left || bottom < top ||
        !Number.isInteger(innerWidth) || innerWidth < 1 || innerWidth > 16_384 ||
        !Number.isInteger(innerHeight) || innerHeight < 1 || innerHeight > 16_384 ||
        !Number.isInteger(clientWidth) || clientWidth < 1 || clientWidth > 16_384 ||
        !Number.isInteger(clientHeight) || clientHeight < 1 || clientHeight > 16_384) return { status: "unavailable" };
    return { status: "sampled", connected, left, top, right, bottom, width, height, innerWidth, innerHeight, clientWidth, clientHeight };
  } catch {
    return { status: "unavailable" };
  }
}

function computerGeometryClip(g: Extract<ComputerButtonGeometry, { status: "sampled" }>, width: number, height: number): string | undefined {
  const mask = (g.top < 0 ? 1 : 0) | (g.right > width ? 2 : 0) | (g.bottom > height ? 4 : 0) | (g.left < 0 ? 8 : 0);
  switch (mask) {
    case 0: return "none";
    case 1: return "top";
    case 2: return "right";
    case 3: return "top_right";
    case 4: return "bottom";
    case 5: return "top_bottom";
    case 6: return "right_bottom";
    case 7: return "top_right_bottom";
    case 8: return "left";
    case 9: return "top_left";
    case 10: return "right_left";
    case 11: return "top_right_left";
    case 12: return "bottom_left";
    case 13: return "top_bottom_left";
    case 14: return "right_bottom_left";
    case 15: return "top_right_bottom_left";
  }
}

export function computerButtonGeometrySuffix(value: unknown): string {
  const unavailable = "; viewport_geometry=source:pre_assert,status:unavailable";
  try {
    const g = projectComputerButtonGeometry(value);
    if (g.status !== "sampled") return unavailable;
    const inner = computerGeometryClip(g, g.innerWidth, g.innerHeight), client = computerGeometryClip(g, g.clientWidth, g.clientHeight);
    if (inner === undefined || client === undefined) return unavailable;
    const suffix = `; viewport_geometry=source:pre_assert,status:sampled,connected:${g.connected},left:${g.left},top:${g.top},right:${g.right},bottom:${g.bottom},width:${g.width},height:${g.height},innerWidth:${g.innerWidth},innerHeight:${g.innerHeight},clientWidth:${g.clientWidth},clientHeight:${g.clientHeight},zero_area:${g.width === 0 || g.height === 0},clip_inner:${inner},clip_client:${client}`;
    return suffix.length <= 512 && /^[\x20-\x7e]+$/.test(suffix) ? suffix : unavailable;
  } catch {
    return unavailable;
  }
}
