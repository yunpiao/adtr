import { defineConfig } from "@playwright/test";
import base from "./playwright.config";

// Separate acceptance entry point: never expands the frozen engineering-ci
// browser inventory. The dedicated workflow explicitly runs this config.
export default defineConfig(base, {
  testDir: "./simulation-rule-catalog-e2e",
  testMatch: "rule-catalog.spec.ts",
  timeout: 120_000,
  globalTimeout: 180_000,
  outputDir: "simulation-rule-catalog-test-results",
  use: { trace: "off", screenshot: "off", video: "off" },
});
