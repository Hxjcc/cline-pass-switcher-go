import { afterEach, expect, test, vi } from "vitest"
import { api, UnauthorizedError } from "./api"

afterEach(() => vi.unstubAllGlobals())

test("sends the current key and JSON body for a settings change", async () => {
  const fetch = vi.fn().mockResolvedValue(Response.json({ ok: true }))
  vi.stubGlobal("fetch", fetch)
  await api("/api/security", { key: "old-test-key", body: { proxyKey: "new-test-key" } })
  expect(fetch).toHaveBeenCalledWith("/api/security", {
    method: "POST",
    headers: { "X-Admin-Key": "old-test-key", "Content-Type": "application/json" },
    body: JSON.stringify({ proxyKey: "new-test-key" }),
  })
  await api("/api/accounts", { key: "new-test-key" })
  expect(fetch.mock.lastCall?.[1].headers["X-Admin-Key"]).toBe("new-test-key")
})

test("treats unauthorized responses as a login request", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("", { status: 401 })))
  await expect(api("/api/accounts")).rejects.toBeInstanceOf(UnauthorizedError)
})

test.each([403, 413, 500])("shows the server error for HTTP %i", async (status) => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(Response.json({ error: { message: "test failure" } }, { status })))
  await expect(api("/api/config", { body: {} })).rejects.toThrow("test failure")
})

test("handles a non-JSON error response", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("unavailable", { status: 502 })))
  await expect(api("/api/models")).rejects.toThrow("502")
})
