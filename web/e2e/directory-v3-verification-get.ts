import type { APIRequestContext, APIResponse } from "@playwright/test";
import { safeErrorClass } from "./safe-request-diagnostics";

// One original GET, with the same receiver/path/options and no retry or timer.
// HTTP status, metadata and JSON checks remain in the caller, outside this catch.
export async function directoryV3VerificationGET(
  request: Pick<APIRequestContext, "get">,
  path: string,
): Promise<APIResponse> {
  try {
    return await request.get(path);
  } catch (error) {
    const classification = safeErrorClass(error);
    const category =
      classification === "socket_hang_up" || classification === "timeout"
        ? classification
        : "request_failed";
    // Never retain the original error/cause: Playwright call logs may contain
    // cookies, authorization headers, response bodies or other private values.
    throw new Error(`Directory-v3 verification GET failed: ${category}`);
  }
}
