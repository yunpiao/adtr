import { defineConfig } from "@playwright/test";
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
