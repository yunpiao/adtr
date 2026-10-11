import { expect, type Page, type Request } from "@playwright/test";
import { createHash } from "node:crypto";

// Test-control reads must fail locally rather than consume the entire suite.
export async function sourceProbeDeadline<T>(promise: Promise<T>): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_, reject) => {
        timer = setTimeout(
          () => reject(new Error("Source probe control read timed out")),
          20_000,
        );
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

// Test-only post-consumption scheduling seam. Native fetch, its promise,
// Response, json parser and AbortSignal are preserved. No production hook exists.
export async function holdConsumedSource(
  page: Page,
  path: string,
  actor: number,
) {
  const expectedURL = new URL(path, page.url());
  if (
    expectedURL.origin !== new URL(page.url()).origin ||
    ![
      "/api/domains/credential-source",
      "/api/domains/credential-source/mutation",
      "/api/operation-accounts",
    ].includes(expectedURL.pathname)
  )
    throw new Error("Unsupported source delivery probe");
  const fullURL = expectedURL.href;
  const events: { event: string; nodeMilliseconds: number }[] = [];
  const started = performance.now();
  let matched: Request | undefined;
  const finished = (request: Request) => {
    if (request.url() === fullURL && request.method() === "GET") {
      if (matched) throw new Error("Duplicate source probe request");
      matched = request;
      events.push({
        event: "native_requestfinished",
        nodeMilliseconds: performance.now() - started,
      });
    }
  };
  page.on("requestfinished", finished);
  const nativeResponse = page.waitForResponse(
    (r) => r.url() === fullURL && r.request().method() === "GET",
    { timeout: 20_000 },
  );
  void nativeResponse.catch(() => {});
  const control = await sourceProbeDeadline(
    page.evaluateHandle(
      ({ fullURL, actor }) => {
        const originalDescriptor = Object.getOwnPropertyDescriptor(
          Response.prototype,
          "json",
        )!;
        const originalJSON = Response.prototype.json;
        let release!: () => void;
        const gate = new Promise<void>((resolve) => {
          release = resolve;
        });
        let claimed = false;
        const evidence = {
          consumed: false,
          released: false,
          parsedSHA256: "",
          actor: "",
          cache: "",
          bodyUsed: false,
          events: [] as { event: string; browserMilliseconds: number }[],
          error: false,
        };
        const jsonProbe: typeof Response.prototype.json = function (
          this: Response,
        ) {
          if (!(this instanceof Response) || claimed || this.url !== fullURL)
            return originalJSON.call(this);
          claimed = true;
          return (async () => {
            const value = await originalJSON.call(this);
            if (
              this.status !== 200 ||
              this.headers.get("x-adtr-user-id") !== String(actor) ||
              this.headers.get("cache-control") !== "no-store"
            ) {
              evidence.error = true;
              throw new Error("Source consumption identity check failed");
            }
            evidence.actor = this.headers.get("x-adtr-user-id")!;
            evidence.cache = this.headers.get("cache-control")!;
            evidence.bodyUsed = this.bodyUsed;
            evidence.parsedSHA256 = Array.from(
              new Uint8Array(
                await crypto.subtle.digest(
                  "SHA-256",
                  new TextEncoder().encode(JSON.stringify(value)),
                ),
              ),
            )
              .map((byte) => byte.toString(16).padStart(2, "0"))
              .join("");
            evidence.events.push({
              event: "native_json_completed",
              browserMilliseconds: performance.now(),
            });
            evidence.consumed = true;
            let timer: ReturnType<typeof setTimeout> | undefined;
            try {
              await Promise.race([
                gate,
                new Promise<never>((_, reject) => {
                  timer = setTimeout(
                    () =>
                      reject(
                        new Error("Source application delivery gate expired"),
                      ),
                    120_000,
                  );
                }),
              ]);
            } finally {
              clearTimeout(timer);
            }
            evidence.events.push({
              event: "original_parsed_value_released",
              browserMilliseconds: performance.now(),
            });
            evidence.released = true;
            return value;
          })();
        };
        Response.prototype.json = jsonProbe;
        return {
          read: () => ({ ...evidence }),
          release() {
            release();
          },
          close() {
            release();
            if (Response.prototype.json === jsonProbe)
              Object.defineProperty(
                Response.prototype,
                "json",
                originalDescriptor,
              );
          },
        };
      },
      { fullURL, actor },
    ),
  ).catch((error) => {
    page.off("requestfinished", finished);
    throw error;
  });
  let originalSHA256 = "";
  let nativeParsedSHA256 = "";
  return {
    async consumed(check: (value: unknown) => void) {
      const response = await nativeResponse;
      expect(response.status()).toBe(200);
      expect(response.headers()["x-adtr-user-id"]).toBe(String(actor));
      expect(await sourceProbeDeadline(response.finished())).toBeNull();
      await expect.poll(() => matched === response.request()).toBe(true);
      const bytes = await sourceProbeDeadline(response.body());
      expect(bytes.length).toBeLessThan(256 * 1024);
      const value: unknown = JSON.parse(bytes.toString("utf8"));
      check(value);
      originalSHA256 = createHash("sha256").update(bytes).digest("hex");
      nativeParsedSHA256 = createHash("sha256")
        .update(JSON.stringify(value))
        .digest("hex");
      await expect
        .poll(() => control.evaluate((c) => c.read()))
        .toMatchObject({
          consumed: true,
          released: false,
          error: false,
          bodyUsed: true,
          parsedSHA256: nativeParsedSHA256,
        });
      events.push({
        event: "application_consumed_before_switch",
        nodeMilliseconds: performance.now() - started,
      });
    },
    async release(verified: { actorID: number; verifiedAtNode: number }) {
      expect(verified.actorID).not.toBe(actor);
      expect(verified.verifiedAtNode - started).toBeGreaterThan(
        events.at(-1)!.nodeMilliseconds,
      );
      events.push({
        event: "verified_session_before_release",
        nodeMilliseconds: verified.verifiedAtNode - started,
      });
      await sourceProbeDeadline(control.evaluate((c) => c.release()));
      await expect
        .poll(() => control.evaluate((c) => c.read().released))
        .toBe(true);
      events.push({
        event: "application_value_released",
        nodeMilliseconds: performance.now() - started,
      });
      const evidence = await sourceProbeDeadline(
        control.evaluate((c) => c.read()),
      );
      expect(evidence).toMatchObject({
        parsedSHA256: nativeParsedSHA256,
        error: false,
      });
      return {
        boundary:
          "native_body_consumed_before_switch_application_value_released_after_session_reverification",
        reverifiedActor: verified.actorID,
        originalSHA256,
        eventsOnNodeClock: events,
        nativeParsedSHA256,
        ...evidence,
      };
    },
    async cleanup() {
      page.off("requestfinished", finished);
      try {
        await sourceProbeDeadline(control.evaluate((c) => c.close()));
      } finally {
        await sourceProbeDeadline(control.dispose());
      }
    },
  };
}
