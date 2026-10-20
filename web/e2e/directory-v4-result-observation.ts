import { parseDirectoryV4UniqueJSON } from "../src/directory-v4-controls-json";
import { boundedV4 } from "./directory-v4-diagnostics";

export function observedV4BodyClass(browser: boolean, contentType: string | undefined): "browser_json" | "browser_other" | "api_json" | "api_other" {
  const json = contentType?.split(";", 1)[0].trim().toLowerCase() === "application/json";
  return browser ? json ? "browser_json" : "browser_other" : json ? "api_json" : "api_other";
}
let bodyMarkers = 0;
export function recordV4BodyClass(browser: boolean, contentType: string | undefined): void {
  if (bodyMarkers >= 256) return;
  bodyMarkers++;
  console.log("[v4-body] " + observedV4BodyClass(browser, contentType));
}

// Fixed categories only. Never retain/log a transport exception or response text.
export async function readV4ObservedObject(read: () => Promise<Uint8Array>): Promise<Record<string, unknown>> {
  let body: Uint8Array;
  const deadline = "V4 observed body deadline exceeded";
  try { body = await boundedV4(read(), 15_000, deadline); }
  catch (error) {
    if (error instanceof Error && error.message === deadline) throw new Error("V4 observed body: deadline");
    throw new Error("V4 observed body: retrieval failed");
  }
  if (body.byteLength > 8 * 1024 * 1024) throw new Error("V4 observed body: size exceeded");
  let text: string;
  try { text = new TextDecoder("utf-8", { fatal: true }).decode(body); }
  catch { throw new Error("V4 observed body: invalid UTF-8"); }
  let value: unknown;
  try { value = parseDirectoryV4UniqueJSON(text); }
  catch { throw new Error("V4 observed body: invalid unique-key JSON"); }
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("V4 observed body: non-object envelope");
  return value as Record<string, unknown>;
}

// Wait for the product's post-response authorization and React commit, while
// preserving EXACT length, values, order, duplicates and display escaping.
// Only the boolean is compared; raw values never enter an assertion report.
export async function waitForV4RawMembers(read: () => Promise<string[]>, expected: readonly string[]): Promise<void> {
  const deadline = Date.now() + 5_000;
  const encoded = JSON.stringify(expected);
  for (;;) {
    const remaining = deadline - Date.now();
    if (remaining <= 0) throw new Error("Raw member values/order not preserved after render deadline");
    let actual: string[];
    try { actual = await boundedV4(read(), remaining, "Raw member render read deadline"); }
    catch { throw new Error("Raw member rendered values unavailable"); }
    if (JSON.stringify(actual) === encoded) return;
    await new Promise(resolve => setTimeout(resolve, Math.min(50, remaining)));
  }
}
