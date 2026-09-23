import { defineConfig } from "vitest/config"
import react from "@vitejs/plugin-react"
import path from "node:path"

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  test: {
    environment: "jsdom",
    restoreMocks: true,
    clearMocks: true,
    // Component tests live under src/. The browser end-to-end suite in e2e/
    // belongs to Playwright (npm run test:e2e); collecting it here would call
    // Playwright's test() inside vitest.
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
  },
})
