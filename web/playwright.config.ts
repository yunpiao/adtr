import { defineConfig } from "@playwright/test";
// Playwright can emit error-context DOM snapshots independently of trace and
// screenshot settings. Never copy proof inputs into those failure artifacts.
process.env.PLAYWRIGHT_NO_COPY_PROMPT = "1";
// Harness-owned exact suite selector. It carries no secrets and is not inferred
// from a prefix, spec substring, or controller URL supplied by a caller.
const v4Budgets = new Map([
  ["directory-v4-controls", 630_000],
  ["directory-v4-cancel", 900_000],
  ["directory-v4-readers", 630_000],
]);
const v4Budget = v4Budgets.get(process.env.ADTR_E2E_V4_CONTROLS_SUITE ?? "");
const v4Controls = v4Budget !== undefined;
// Apply at the actual Chromium launch, not at page/context creation. Node
// retains its private inputs; a new copy prevents mutation of process.env.
function chromiumEnvironment(): Record<string, string> {
  return Object.fromEntries(Object.entries(process.env).filter(
    (entry): entry is [string, string] =>
      entry[1] !== undefined && !/^(ADTR_|POSTGRES_|PG)/i.test(entry[0]),
  ));
}
export default defineConfig({
  testDir: "./e2e",
  timeout: v4Budget ?? 120_000,
  ...(v4Controls ? { globalTimeout: v4Budget } : {}),
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [["list"]],
  outputDir: "test-results",
  use: {
    baseURL: process.env.ADTR_E2E_BASE_URL ?? "http://127.0.0.1:18080",
    // The isolated harness passes --headed for session-invalidation and
    // directory-readers so their real tab visibility/focus fallback can run.
    headless: true,
    trace: v4Controls ? "off" : "retain-on-failure",
    screenshot: v4Controls ? "off" : "only-on-failure",
    ...(v4Controls ? { video: "off" as const } : {}),
    launchOptions: {
      ...(v4Controls ? { env: chromiumEnvironment() } : {}),
      ...(process.env.ADTR_E2E_CHROMIUM_PATH
        ? { executablePath: process.env.ADTR_E2E_CHROMIUM_PATH }
        : {}),
    },
  },
});
