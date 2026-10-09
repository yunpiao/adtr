import type { APIRequestContext } from "@playwright/test";

// This fixture waits for real 30-second TOTP counters between writes, matching
// the API's idle keep-alive timeout. Playwright's shared HTTP agent can select a
// socket just as the server closes it. Retry only one ECONNRESET for these GETs;
// HTTP error responses and all proof-bearing writes remain single attempts.
// The Python harness separately fails on any HTTP-handler panic, including one
// followed by a successful retry. Never print Playwright's cookie-bearing log.
export async function credentialUseRead(
  request: Pick<APIRequestContext, "get">,
  path: string,
) {
  try {
    return await request.get(path, { maxRetries: 1 });
  } catch {
    throw new Error(
      "Credential-use fixture GET failed before a complete HTTP response; request diagnostics withheld",
    );
  }
}
