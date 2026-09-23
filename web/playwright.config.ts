import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { defineConfig } from "@playwright/test"

// The console is exercised against the real server: the config boots the Go
// binary with its own temporary data directory and a fake-free credential set,
// so a failure here means the shipped binary and the shipped assets disagree.
const PORT = Number(process.env.E2E_PORT ?? 3133)
const ADMIN_KEY = "sk-e2e-admin"
const DATA_DIR = mkdtempSync(join(tmpdir(), "cline-pass-e2e-"))

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: "retain-on-failure",
  },
  webServer: {
    command: "go run ./cmd/cline-pass-switcher",
    cwd: "..",
    url: `http://127.0.0.1:${PORT}/healthz`,
    // A stale server would carry a different data directory and key.
    reuseExistingServer: false,
    timeout: 180_000,
    env: {
      PORT: String(PORT),
      BIND_HOST: "127.0.0.1",
      DATA_DIR,
      PROXY_KEY: "sk-e2e-client",
      ADMIN_KEY,
      // One knob from the environment on purpose: the access panel must report
      // it as env-sourced.
      COMPACTION_MIN_OUTPUT_TOKENS: "16384",
    },
  },
})
