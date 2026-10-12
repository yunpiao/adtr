import type { APIRequestContext, APIResponse } from "@playwright/test";
import {
  beginGET,
  nextOperation,
  safeErrorClass,
} from "./safe-request-diagnostics";

function retryable(error: unknown): boolean {
  try {
    // Timeout/cancellation subclasses take precedence even with reset-like text.
    // Never inspect call-log lines, causes, URLs, headers or response bodies.
    if (!(error instanceof Error) || error.name !== "Error") return false;
    const classification = safeErrorClass(error);
    return (
      classification === "econnreset" || classification === "socket_hang_up"
    );
  } catch {
    return false;
  }
}

// Only directory-v2 readJSON setup/verification uses this helper. Its existing
// context and framework test budget remain authoritative: no new timeout,
// native retry, timer, response cache or browser/held-response interception.
export async function directoryV2VerificationGET(
  request: Pick<APIRequestContext, "get">,
  path: string,
): Promise<APIResponse> {
  const operation = nextOperation();
  for (let attempt = 1; attempt <= 2; attempt++) {
    const capture = beginGET(
      "directory-v2-verification-get",
      path,
      operation,
      attempt,
    );
    let response: APIResponse;
    try {
      response = await request.get(path);
    } catch (error) {
      capture.failure(error);
      if (attempt === 1 && retryable(error)) continue;
      throw error;
    }
    // Response inspection belongs outside the retry catch. In particular, HTTP
    // rejection, caller assertions and JSON parsing must never cause another GET.
    capture.response(response);
    if (attempt === 2) {
      try {
        // Fixed receipt plus attempt-2 response events make recovery countable.
        console.log("Directory-v2 verification GET recovered: attempt=2/2");
      } catch {
        // Diagnostic output cannot replace the original response.
      }
    }
    return response;
  }
  throw new Error("Unreachable directory-v2 verification GET attempt");
}
