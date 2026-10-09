// @vitest-environment node
import { request } from "@playwright/test";
import { createServer, type RequestListener } from "node:http";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { expect, it } from "vitest";
import { credentialUseRead } from "../e2e/credential-use-read";

async function withServer(
  handler: RequestListener,
  check: (baseURL: string) => Promise<void>,
) {
  const server = createServer(handler);
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("No test port");
  try {
    await check(`http://127.0.0.1:${address.port}`);
  } finally {
    server.closeAllConnections();
    await new Promise<void>((resolve, reject) =>
      server.close((error) => (error ? reject(error) : resolve())),
    );
  }
}

it("recovers one real connection reset using only GET and preserves the response", async () => {
  const methods: string[] = [];
  await withServer(
    (incoming, response) => {
      methods.push(incoming.method!);
      if (methods.length === 1) {
        incoming.socket.destroy();
        return;
      }
      response.setHeader("X-ADTR-User-ID", "7");
      response.setHeader("Cache-Control", "no-store");
      response.end(JSON.stringify({ List: [], consumerEnabled: true }));
    },
    async (baseURL) => {
      const api = await request.newContext({ baseURL });
      try {
        const response = await credentialUseRead(
          api,
          "/api/credential-use/accounts",
        );
        expect(response.status()).toBe(200);
        expect(response.headers()["x-adtr-user-id"]).toBe("7");
        expect(response.headers()["cache-control"]).toBe("no-store");
        expect(await response.json()).toEqual({
          List: [],
          consumerEnabled: true,
        });
      } finally {
        await api.dispose();
      }
    },
  );
  expect(methods).toEqual(["GET", "GET"]);
});

it.each([403, 500, 503])(
  "does not retry or replace HTTP %i",
  async (status) => {
    let calls = 0;
    await withServer(
      (_incoming, response) => {
        calls++;
        response.writeHead(status, { "Content-Type": "application/json" });
        response.end(JSON.stringify({ error: "synthetic_failure" }));
      },
      async (baseURL) => {
        const api = await request.newContext({ baseURL });
        try {
          const response = await credentialUseRead(
            api,
            "/api/credential-use/accounts",
          );
          expect(response.status()).toBe(status);
          expect(await response.json()).toEqual({ error: "synthetic_failure" });
          expect(() => expect(response.status()).toBe(200)).toThrow();
        } finally {
          await api.dispose();
        }
      },
    );
    expect(calls).toBe(1);
  },
);

it("fails after two connection resets without disclosing the cookie-bearing call log", async () => {
  let calls = 0;
  await withServer(
    (incoming) => {
      calls++;
      incoming.socket.destroy();
    },
    async (baseURL) => {
      const api = await request.newContext({
        baseURL,
        extraHTTPHeaders: {
          Cookie: "synthetic-session=do-not-log-this-fixture",
        },
      });
      try {
        const error = await credentialUseRead(
          api,
          "/api/credential-use/accounts",
        ).catch((error: unknown) => error);
        expect(error).toBeInstanceOf(Error);
        expect((error as Error).message).toBe(
          "Credential-use fixture GET failed before a complete HTTP response; request diagnostics withheld",
        );
        expect(String(error)).not.toMatch(
          /cookie|do-not-log-this-fixture|Call log/i,
        );
        expect((error as Error).cause).toBeUndefined();
      } finally {
        await api.dispose();
      }
    },
  );
  expect(calls).toBe(2);
});

it("does not retry a non-reset protocol failure", async () => {
  let calls = 0;
  await withServer(
    (incoming) => {
      calls++;
      incoming.socket.end("invalid HTTP response\r\n\r\n");
    },
    async (baseURL) => {
      const api = await request.newContext({ baseURL });
      try {
        await expect(
          credentialUseRead(api, "/api/credential-use/accounts"),
        ).rejects.toThrow(
          "Credential-use fixture GET failed before a complete HTTP response",
        );
      } finally {
        await api.dispose();
      }
    },
  );
  expect(calls).toBe(1);
});

it("keeps write interception single-attempt and limits the helper to the affected suite", () => {
  const fixtures = new URL("../e2e/", import.meta.url);
  expect(
    readdirSync(fixtures)
      .filter((name) => name.endsWith(".spec.ts"))
      .filter((name) =>
        readFileSync(new URL(name, fixtures), "utf8").includes(
          "credentialUseRead",
        ),
      ),
  ).toEqual(["credential-use.spec.ts"]);
  const fixture = readFileSync(
    fileURLToPath(new URL("../e2e/credential-use.spec.ts", import.meta.url)),
    "utf8",
  );
  expect(fixture.match(/route\.fetch\(\{ maxRetries: 0 \}\)/g)).toHaveLength(2);
  expect(fixture).not.toMatch(/maxRetries:\s*[1-9]/u);
  expect(fixture).toContain("expect(grantPosts).toBe(1)");
  expect(fixture).toContain("expect(revokePosts).toBe(1)");
  const helper = readFileSync(
    fileURLToPath(new URL("../e2e/credential-use-read.ts", import.meta.url)),
    "utf8",
  );
  expect(helper).toContain("request.get(path, { maxRetries: 1 })");
  expect(helper).not.toMatch(/request\.(?:post|put|patch|delete|fetch)\(/u);
});
