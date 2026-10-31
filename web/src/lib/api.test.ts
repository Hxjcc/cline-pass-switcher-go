// @vitest-environment node

import { afterEach, expect, test, vi } from "vitest"
import { api, UnauthorizedError } from "./api"

afterEach(() => vi.unstubAllGlobals())

test("sends the current key and JSON body for a settings change", async () => {
  const fetch = vi.fn(async (_path: string, _init?: RequestInit) => Response.json({ ok: true }))
  vi.stubGlobal("fetch", fetch)
  await api("/api/security", { key: "old-test-key", body: { proxyKey: "new-test-key" } })
  expect(fetch).toHaveBeenCalledWith("/api/security", {
    method: "POST",
    headers: { "X-Admin-Key": "old-test-key", "Content-Type": "application/json" },
    body: JSON.stringify({ proxyKey: "new-test-key" }),
    signal: expect.any(AbortSignal),
  })
  await api("/api/accounts", { key: "new-test-key" })
  expect(fetch.mock.lastCall?.[1]?.headers).toEqual({"X-Admin-Key":"new-test-key"})
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

test.each(["<html>gateway</html>", "null", "[]", '"ok"'])("rejects invalid successful payload %s", async (body) => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(body, {status:200,headers:{"Content-Type":"application/json"}})))
  await expect(api("/api/models")).rejects.toMatchObject({code:"invalid_json_response"})
})

test("times out an unresponsive management request", async () => {
  vi.stubGlobal("fetch", vi.fn((_path, init: RequestInit) => new Promise((_resolve,reject) => {
    init.signal!.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), {once:true})
  })))
  await expect(api("/api/models",{timeoutMs:10})).rejects.toMatchObject({code:"request_timeout"})
})

test("preserves caller cancellation", async () => {
  const controller=new AbortController()
  vi.stubGlobal("fetch", vi.fn((_path, init: RequestInit) => new Promise((_resolve,reject) => {
    init.signal!.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), {once:true})
  })))
  const pending=api("/api/models",{signal:controller.signal})
  controller.abort()
  await expect(pending).rejects.toMatchObject({name:"AbortError"})
})
