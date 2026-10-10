import type { APIRequestContext, APIResponse } from "@playwright/test";

// Only fixture-side GET verification uses this helper. Browser requests, held
// responses and mutations retain their own strict transport assertions.
export const fixtureGetDeadlineMilliseconds = 30_000;
const retryDelayMilliseconds = 250;
const retryReceipt = "User-assets fixture GET retry: ECONNRESET; attempt=2/2";

function reset(error: unknown): boolean {
  // Playwright's public Error loses Node's code, but retains this exact first
  // line. Never search its call log: URLs, headers or bodies can contain text
  // that resembles an error code. Unknown errors must not enable a retry.
  return (
    error instanceof Error &&
    /^apiRequestContext\.get: (?:read |write |connect )?ECONNRESET$/u.test(
      error.message.split("\n", 1)[0],
    )
  );
}

function timedOut(error: unknown): boolean {
  return (
    error instanceof Error &&
    (error.name === "TimeoutError" ||
      /^apiRequestContext\.get: (?:Timeout|Request timed out)/u.test(
        error.message.split("\n", 1)[0],
      ))
  );
}

function transportDiagnostic(
  error: unknown,
):
  | "socket_hang_up"
  | "response_aborted"
  | "context_disposed"
  | "target_closed"
  | "connection_refused"
  | "unknown_error"
  | "non_error" {
  // Diagnostic labels never authorize retries. Match only complete first
  // lines and return constants, never messages, URLs, headers, bodies or cause.
  if (!(error instanceof Error)) return "non_error";
  const firstLine = error.message.split("\n", 1)[0];
  switch (firstLine) {
    case "apiRequestContext.get: socket hang up":
      return "socket_hang_up";
    case "apiRequestContext.get: aborted":
      return "response_aborted";
    case "apiRequestContext.get: Request context disposed.":
      return "context_disposed";
    case "apiRequestContext.get: Target page, context or browser has been closed":
      return "target_closed";
  }
  if (
    /^apiRequestContext\.get: connect ECONNREFUSED(?: (?:127\.0\.0\.1|::1):[0-9]{1,5})?$/u.test(
      firstLine,
    )
  )
    return "connection_refused";
  return "unknown_error";
}

export async function fixtureGET(
  request: Pick<APIRequestContext, "get">,
  path: string,
): Promise<APIResponse> {
  const deadline = performance.now() + fixtureGetDeadlineMilliseconds;
  const timeoutError = new Error(
    "User-assets fixture GET failed: deadline_exceeded",
  );
  let timer: ReturnType<typeof setTimeout> | undefined;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  const expired = new Promise<never>((_, reject) => {
    timer = setTimeout(
      () => reject(timeoutError),
      fixtureGetDeadlineMilliseconds,
    );
  });
  try {
    for (let attempt = 0; attempt < 2; attempt++) {
      if (attempt === 1) {
        await Promise.race([
          new Promise<void>((resolve) => {
            retryTimer = setTimeout(resolve, retryDelayMilliseconds);
          }),
          expired,
        ]);
      }
      const remaining = Math.ceil(deadline - performance.now());
      if (remaining <= 0) throw timeoutError;
      if (attempt === 1) console.log(retryReceipt);
      try {
        // Keep the existing context/cookie jar and exact path. Each attempt has
        // no hidden Playwright retry and shares one overall 30-second deadline.
        // HTTP responses, including 401/403/500, are returned unchanged.
        return await Promise.race([
          request.get(path, { maxRetries: 0, timeout: remaining }),
          expired,
        ]);
      } catch (error) {
        if (error === timeoutError || timedOut(error)) throw timeoutError;
        if (reset(error)) {
          if (attempt === 0) continue;
          throw new Error(
            "User-assets fixture GET failed: ECONNRESET_after_retry",
          );
        }
        // Do not attach the original error as cause: Playwright's call log can
        // contain a session Cookie, response body or other synthetic secrets.
        throw new Error(
          `User-assets fixture GET failed: transport_error; reason=${transportDiagnostic(error)}; attempt=${attempt + 1}/2`,
        );
      }
    }
    throw new Error("User-assets fixture GET failed: transport_error");
  } finally {
    clearTimeout(timer);
    clearTimeout(retryTimer);
  }
}
