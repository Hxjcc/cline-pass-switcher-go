import { StrictMode } from "react"
import { act, cleanup, renderHook, waitFor } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"

import { useAccountQuota } from "./use-account-quota"
import type { Account, QuotaResponse } from "@/types"

afterEach(() => { cleanup(); sessionStorage.clear() })

const accounts: Account[] = [{ id: "acc1", name: "账号1", key: "", keyPreview: "sk…old", hasKey: true, enabled: true }]
const response = (percentUsed = 35): QuotaResponse => ({accounts: [{
  accountId: "acc1", ok: true, fetchedAt: Date.now(), currentPeriodEnd: "2026-10-14T16:23:10Z",
  limits: [{type: "weekly", percentUsed, resetsAt: "2026-10-14T16:56:20Z"}],
}]})

test("StrictMode still completes the automatic quota read", async () => {
  const onQuota = vi.fn(async () => response())
  const { result } = renderHook(() => useAccountQuota(accounts, onQuota), {wrapper: StrictMode})
  await waitFor(() => expect(result.current.entries.acc1?.quota.limits?.[0].percentUsed).toBe(35))
  expect(result.current.loading).toBe(false)
})

test("reload restores the meters before the background request finishes", async () => {
  const firstLoader = async () => response()
  const first = renderHook(() => useAccountQuota(accounts, firstLoader))
  await waitFor(() => expect(first.result.current.entries.acc1).toBeDefined())
  first.unmount()
  let finish!: (value: QuotaResponse) => void
  const onQuota = vi.fn(() => new Promise<QuotaResponse>((resolve) => { finish = resolve }))
  const next = renderHook(() => useAccountQuota(accounts, onQuota))
  expect(next.result.current.entries.acc1.quota.limits?.[0].percentUsed).toBe(35)
  expect(next.result.current.loading).toBe(true)
  await act(async () => { finish(response(40)) })
  expect(next.result.current.entries.acc1.quota.limits?.[0].percentUsed).toBe(40)
  expect(next.result.current.loading).toBe(false)
})

test("a late automatic response cannot overwrite a manual refresh", async () => {
  const pending: Array<(value: QuotaResponse) => void> = []
  const onQuota = vi.fn(() => new Promise<QuotaResponse>((resolve) => { pending.push(resolve) }))
  const { result } = renderHook(() => useAccountQuota(accounts, onQuota))
  act(() => { void result.current.refresh() })
  await act(async () => { pending[1](response(60)) })
  await act(async () => { pending[0](response(35)) })
  expect(result.current.entries.acc1.quota.limits?.[0].percentUsed).toBe(60)
})

test("a changed key cannot display the previous key's cached quota", async () => {
  const onQuota = vi.fn<() => Promise<QuotaResponse>>()
    .mockResolvedValueOnce(response())
    .mockImplementation(() => new Promise(() => {}))
  const { result, rerender } = renderHook(({value}) => useAccountQuota(value, onQuota), {initialProps: {value: accounts}})
  await waitFor(() => expect(result.current.entries.acc1).toBeDefined())
  rerender({value: [{...accounts[0], keyPreview: "sk…new"}]})
  expect(result.current.entries.acc1).toBeUndefined()
})

test("a failed refresh keeps the existing meters and marks them as previous data", async () => {
  const onQuota = vi.fn().mockResolvedValueOnce(response()).mockResolvedValueOnce({accounts: [{accountId: "acc1", ok: false, error: "读取失败", fetchedAt: Date.now()}]})
  const { result } = renderHook(() => useAccountQuota(accounts, onQuota))
  await waitFor(() => expect(result.current.entries.acc1).toBeDefined())
  await act(async () => { await result.current.refresh() })
  expect(result.current.entries.acc1.quota.limits?.[0].percentUsed).toBe(35)
  expect(result.current.entries.acc1.refreshError).toBe("读取失败")
})
