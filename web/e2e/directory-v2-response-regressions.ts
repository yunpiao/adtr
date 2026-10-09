import { test, expect } from "@playwright/test";
import { createServer } from "node:http";
import { directoryV2ResponseJSON } from "./directory-v2-response-observer";

// A collector regression, separate from the real API/worker/LDAP acceptance.
// The owned HTTP server sends actual no-store bytes in two chunks. It cannot
// modify application fixtures, authentication, database state or cache policy.
export function directoryV2ResponseRegressions() {
  for (const outcome of ["complete", "truncated", "aborted"] as const) {
    test(`directory body observer preserves ${outcome} streamed delivery`, async ({
      page,
    }) => {
      let body = '{"task":{"source":"synthetic HTTP observer regression"}}';
      let release!: () => void;
      const released = new Promise<void>((resolve) => {
        release = resolve;
      });
      let dataRequests = 0;
      const server = createServer(async (request, response) => {
        if (request.url !== "/api/directory/v2/receipt") {
          response.writeHead(200, { "Content-Type": "text/html" });
          response.end(
            "<!doctype html><title>Isolated response observer</title>",
          );
          return;
        }
        dataRequests++;
        response.writeHead(200, {
          "Content-Type": "application/json",
          "Cache-Control": "no-store",
          "X-ADTR-User-ID": "7",
          "Content-Length":
            Buffer.byteLength(body) + (outcome === "truncated" ? 1 : 0),
        });
        response.write(body.slice(0, 8));
        // Release only after the browser actually consumes the first chunk.
        // This is server-side fixture chunking, never delayed app delivery.
        await released;
        if (outcome === "aborted") response.destroy();
        else {
          if (outcome === "truncated") {
            const socket = response.socket;
            response.once("finish", () => socket?.end());
          }
          response.end(body.slice(8));
        }
      });
      await new Promise<void>((resolve) =>
        server.listen(0, "127.0.0.1", resolve),
      );
      try {
        const address = server.address();
        if (!address || typeof address === "string")
          throw new Error("Missing owned HTTP fixture");
        await page.exposeFunction("releaseDirectoryObserverBytes", () =>
          release(),
        );
        await page.goto(`http://127.0.0.1:${address.port}`);
        const response = page.waitForResponse(
          (candidate) =>
            new URL(candidate.url()).pathname === "/api/directory/v2/receipt",
        );
        const consumed = page.evaluate(async (outcome) => {
          const controller = new AbortController();
          try {
            const response = await fetch("/api/directory/v2/receipt", {
              cache: "no-store",
              signal: controller.signal,
            });
            const reader = response.body!.getReader();
            const decoder = new TextDecoder("utf-8", { fatal: true });
            let text = "";
            let size = 0;
            let first = true;
            try {
              for (;;) {
                const part = await reader.read();
                if (part.done) break;
                if (part.value.byteLength > 256 - size)
                  throw new Error("Fixture limit exceeded");
                size += part.value.byteLength;
                text += decoder.decode(part.value, { stream: true });
                if (first) {
                  first = false;
                  if (outcome === "aborted") controller.abort();
                  void (window as any).releaseDirectoryObserverBytes();
                }
              }
              text += decoder.decode();
              return { ok: true, value: JSON.parse(text) };
            } finally {
              reader.releaseLock();
            }
          } catch {
            return { ok: false };
          }
        }, outcome);
        const received = await response;
        expect(received.status()).toBe(200);
        expect(received.headers()["cache-control"]).toBe("no-store");
        if (outcome === "complete") {
          const [observed, observedAgain] = await Promise.all([
            directoryV2ResponseJSON(received),
            directoryV2ResponseJSON(received),
          ]);
          expect(observedAgain).toEqual(observed);
          expect(observed).toEqual(JSON.parse(body));
          expect(await consumed).toEqual({ ok: true, value: observed });
        } else {
          expect(await consumed).toEqual({ ok: false });
          await expect(directoryV2ResponseJSON(received)).rejects.toThrow(
            "Directory browser body observation failed",
          );
        }
        expect(
          dataRequests,
          "Observation must not fetch or replay the endpoint",
        ).toBe(1);
        if (outcome === "complete") {
          const readAgain = async (inspect = true) => {
            const waiting = page.waitForResponse(
              (candidate) =>
                new URL(candidate.url()).pathname ===
                "/api/directory/v2/receipt",
            );
            const application = page.evaluate(async () => {
              const response = await fetch("/api/directory/v2/receipt", {
                cache: "no-store",
              });
              const reader = response.body!.getReader();
              let text = "";
              const decoder = new TextDecoder();
              try {
                for (;;) {
                  const part = await reader.read();
                  if (part.done) break;
                  text += decoder.decode(part.value, { stream: true });
                }
                return JSON.parse(text + decoder.decode());
              } finally {
                reader.releaseLock();
              }
            });
            const actual = await waiting;
            const applicationValue = await application;
            expect(applicationValue).toEqual(JSON.parse(body));
            if (inspect)
              expect(await directoryV2ResponseJSON(actual)).toEqual(
                applicationValue,
              );
            return actual;
          };
          await page.evaluate(() =>
            history.pushState({}, "", "#same-document"),
          );
          await readAgain();
          await page.evaluate(async () => {
            const controller = new AbortController();
            controller.abort();
            try {
              await fetch("/api/directory/v2/receipt", {
                signal: controller.signal,
              });
              throw new Error("Pre-aborted Fetch unexpectedly succeeded");
            } catch (error) {
              if (
                !(error instanceof DOMException) ||
                error.name !== "AbortError"
              )
                throw error;
            }
          });
          body = '{"task":{"sequence":1}}';
          await readAgain(false);
          body = '{"task":{"sequence":2}}';
          await readAgain();
          const retainedUnread = await readAgain(false);
          await page.reload();
          await readAgain();
          await expect(directoryV2ResponseJSON(retainedUnread)).rejects.toThrow(
            "Directory response document changed",
          );
          expect(dataRequests).toBe(6);
        }
      } finally {
        release();
        server.closeAllConnections();
        await new Promise<void>((resolve, reject) =>
          server.close((error) => (error ? reject(error) : resolve())),
        );
      }
    });
  }
}
