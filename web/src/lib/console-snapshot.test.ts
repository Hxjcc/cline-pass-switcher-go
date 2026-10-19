import { afterEach, expect, test, vi } from "vitest"

import { fetchSnapshot, readSnapshot, writeSnapshot } from "./console-snapshot"
import { UnauthorizedError } from "./api"

afterEach(() => {
  sessionStorage.clear()
  vi.unstubAllGlobals()
})

test("validates a stored credential with one request before fanning out", async () => {
  const fetch = vi.fn().mockResolvedValue(new Response("", { status: 401 }))
  vi.stubGlobal("fetch", fetch)

  await expect(fetchSnapshot("stale-key")).rejects.toBeInstanceOf(UnauthorizedError)
  expect(fetch).toHaveBeenCalledTimes(1)
  expect(fetch.mock.calls[0][0]).toBe("/api/models")
})

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
