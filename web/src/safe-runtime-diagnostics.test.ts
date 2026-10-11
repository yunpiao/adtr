// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const canary = "CANARY-secret-token-cookie-dsn-account-id";
let directory = "";
afterEach(() => {
  vi.unstubAllEnvs();
  if (directory) rmSync(directory, { recursive: true, force: true });
  directory = "";
});

async function setup() {
  vi.resetModules();
  directory = mkdtempSync(join(tmpdir(), "adtr-safe-diagnostics-"));
  const file = join(directory, "browser.json");
  vi.stubEnv("ADTR_E2E_SAFE_DIAGNOSTICS", file);
  return { file, ...(await import("../e2e/safe-runtime-diagnostics")) };
}

describe("safe browser diagnostic evidence", () => {
  it("retains only exact route templates and never query identifiers or unknown paths", async () => {
    const { file, recordSafeEvent, safeRoute } = await setup();
    recordSafeEvent(
      "read-json",
      `/api/directory-credential-use/v2/effective?accountId=${canary}`,
      "transport-error",
      11,
    );
    for (const path of [
      `/api/${canary}`,
      `https://${canary}/api/auth/me`,
      `/api/auth/me/${canary}`,
      `/${canary}?token=${canary}`,
    ]) {
      expect(safeRoute(path)).toBe("unknown");
      recordSafeEvent("read-json", path, "start");
    }
    const text = readFileSync(file, "utf8");
    expect(text).not.toContain(canary);
    expect(JSON.parse(text).events[0]).toMatchObject({
      route: "/api/directory-credential-use/v2/effective",
      method: "GET",
      outcome: "transport-error",
      elapsed_ms: 11,
      status: null,
    });
  });

  it("bounds record count and encoded bytes", async () => {
    const { file, recordSafeEvent } = await setup();
    for (let i = 0; i < 1000; i++)
      recordSafeEvent("read-json", "/api/tasks", "start");
    const text = readFileSync(file, "utf8");
    expect(JSON.parse(text).events).toHaveLength(256);
    expect(JSON.parse(text).dropped).toBe(744);
    expect(Buffer.byteLength(text)).toBeLessThanOrEqual(65_536);
    recordSafeEvent("read-json", "/api/auth/me", "transport-error");
    const finalRecords = JSON.parse(readFileSync(file, "utf8")).events;
    expect(finalRecords).toHaveLength(256);
    expect(finalRecords.at(-1)).toMatchObject({
      route: "/api/auth/me",
      outcome: "transport-error",
    });
  });

  it("does not replace transport outcomes if evidence writing is unavailable", async () => {
    const { recordSafeEvent } = await setup();
    vi.stubEnv("ADTR_E2E_SAFE_DIAGNOSTICS", directory);
    expect(() =>
      recordSafeEvent("totp-wait", "", "complete", 30000),
    ).not.toThrow();
    vi.stubEnv("ADTR_E2E_SAFE_DIAGNOSTICS", "");
    expect(() =>
      recordSafeEvent("read-json", canary, "transport-error"),
    ).not.toThrow();
  });
});
