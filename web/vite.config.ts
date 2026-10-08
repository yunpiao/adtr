import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  // Rollup 4.64.0 tree-shaking takes >4 minutes on this React graph in this executor.
  // Preserve all code, keep production minification, and test the complete bundle.
  build: { rollupOptions: { treeshake: false } },
  server: { host: "127.0.0.1", proxy: { "/api": "http://127.0.0.1:8080" } },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["./src/test-setup.ts"],
    restoreMocks: true,
  },
});
