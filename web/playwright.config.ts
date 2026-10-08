import { defineConfig } from "@playwright/test";
// Playwright can emit error-context DOM snapshots independently of trace and
// screenshot settings. Never copy proof inputs into those failure artifacts.
process.env.PLAYWRIGHT_NO_COPY_PROMPT = "1";
export default defineConfig({
  testDir: "./e2e",
  timeout: 120_000,
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
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    launchOptions: {
      ...(process.env.ADTR_E2E_CHROMIUM_PATH
        ? { executablePath: process.env.ADTR_E2E_CHROMIUM_PATH }
        : {}),
    },
  },
});
