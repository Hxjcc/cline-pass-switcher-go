import { defineConfig } from "vitest/config"
import react from "@vitejs/plugin-react"
import path from "node:path"

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  test: {
    // vmThreads was benchmarked; forks was faster on the current suite.
    // Recheck with --pool=vmThreads when the suite or runtime changes.
    pool: "forks",
    // Pure logic tests opt into node per file; components and browser helpers
    // retain jsdom without sharing globals or turning off isolation.
    environment: "jsdom",
    restoreMocks: true,
    clearMocks: true,
    // Component tests live under src/. The browser end-to-end suite in e2e/
    // belongs to Playwright (npm run test:e2e); collecting it here would call
    // Playwright's test() inside vitest.
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
  },
})
