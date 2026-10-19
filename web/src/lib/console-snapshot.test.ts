import { afterEach, expect, test } from "vitest"

import { readSnapshot, writeSnapshot } from "./console-snapshot"

afterEach(() => sessionStorage.clear())

test("keeps master credentials out of the reload cache", () => {
  writeSnapshot({
    models: { subscription: [], proxyBase: "", officialFetch: null },
    meta: { authRequired: true, proxyBase: "", configured: true },
    history: [],
    accounts: { accounts: [], mode: "single", active: 0, stats: {} },
    keys: { keys: [] },
    security: {
      proxyKey: "sk-master-secret",
      adminKey: "admin-secret",
      publicBaseUrl: "",
      authRequired: true,
    },
  })

  const raw = sessionStorage.getItem("cline-pass-switcher-snapshot") ?? ""
  expect(raw).not.toContain("sk-master-secret")
  expect(raw).not.toContain("admin-secret")
  const cached = JSON.parse(raw) as { security: { proxyKey: string; adminKey: string } }
  expect(cached.security.proxyKey).toBe("")
  expect(cached.security.adminKey).toBe("")
  expect(readSnapshot()?.security.proxyKey).toBe("")
  expect(readSnapshot()?.security.adminKey).toBe("")
})
