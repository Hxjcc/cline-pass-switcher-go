import { act, cleanup, renderHook } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"

import { useHistory } from "./use-history"
import type { CachedSnapshot } from "./console-snapshot"
import type { HistoryItem, HistoryResponse } from "@/types"

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const row = (id: string, error: string | null = null): HistoryItem => ({ id, ts: 1000, model: "same", ms: 1, stream: false, error })
const a = row("a"), b = row("b", "failed"), c = row("c")
const page = (history: HistoryItem[], nextCursor = ""): HistoryResponse => ({ history, total: 3, offset: 0, limit: 50, hasMore: !!nextCursor, nextCursor })
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } })
const initial = { history: [a, b], historyTotal: 3, historyHasMore: true, historyCursor: "b" } as CachedSnapshot

function queuedFetch() {
  const pending: Array<(value: Response) => void> = []
  const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(() => new Promise<Response>((resolve) => { pending.push(resolve) }))
  return { fetch, pending }
}

test.each(["refresh", "loadMore"] as const)("late %s cannot replace a newer filter even if cancellation is ignored", async (operation) => {
  const { pending } = queuedFetch()
  const onError = vi.fn()
  const { result } = renderHook(() => useHistory("key", initial, onError))
  act(() => { void result.current[operation]() })
  act(() => { result.current.changeQuery({ q: "", onlyErrors: true }) })
  await act(async () => { pending[1](json(page([b]))) })
  await act(async () => { pending[0](json(page([a, c]))) })
  expect(result.current.history).toEqual([b])
  expect(result.current.query.onlyErrors).toBe(true)
  expect(result.current.pending).toBe(false)
  expect(onError).not.toHaveBeenCalled()
})

test("append uses the stable cursor and keeps distinct rows with identical timestamps", async () => {
  const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(json(page([b, c])))
  const { result } = renderHook(() => useHistory("key", initial, vi.fn()))
  await act(async () => { await result.current.loadMore() })
  const url = String(fetch.mock.calls[0][0])
  expect(url).toContain("cursor=b")
  expect(url).not.toContain("offset=")
  expect(result.current.history.map((entry) => entry.id)).toEqual(["a", "b", "c"])
  expect(result.current.historyHasMore).toBe(false)
})

test("refresh replaces an in-flight append and owns the continuation cursor", async () => {
  const { pending } = queuedFetch()
  const { result } = renderHook(() => useHistory("key", initial, vi.fn()))
  act(() => { void result.current.loadMore() })
  act(() => { void result.current.refresh() })
  const newest = row("new")
  await act(async () => { pending[1](json(page([newest, a], "a"))) })
  await act(async () => { pending[0](json(page([c]))) })
  expect(result.current.history).toEqual([newest, a])
  expect(result.current.historyCursor).toBe("a")
})

test("slow refresh ticks share a request instead of starving it", async () => {
  const { fetch, pending } = queuedFetch()
  const { result } = renderHook(() => useHistory("key", initial, vi.fn()))
  act(() => { void result.current.refresh(); void result.current.refresh() })
  expect(fetch).toHaveBeenCalledTimes(1)
  await act(async () => { pending[0](json(page([c]))) })
  expect(result.current.history).toEqual([c])
})

test("a completed clear invalidates pending reads and cached filter results", async () => {
  const { pending } = queuedFetch()
  const { result } = renderHook(() => useHistory("key", initial, vi.fn()))
  act(() => { result.current.changeQuery({ q: "", onlyErrors: true }) })
  await act(async () => { pending[0](json(page([b]))) })
  act(() => { void result.current.refresh() })
  act(() => { void result.current.clear() })
  await act(async () => { pending[2](json({ ok: true })) })
  await act(async () => { pending[1](json(page([b]))) })
  expect(result.current.history).toEqual([])
  act(() => { result.current.changeQuery({ q: "", onlyErrors: true }) })
  expect(result.current.history).toEqual([])
  await act(async () => { pending[3](json(page([]))) })
})

test("expired cursors replace the view instead of appending a fresh first page", async () => {
  const fetch = vi.spyOn(globalThis, "fetch")
    .mockResolvedValueOnce(json({ error: { message: "expired", code: "history_cursor_expired" } }, 409))
    .mockResolvedValueOnce(json(page([c])))
  const { result } = renderHook(() => useHistory("key", initial, vi.fn()))
  await act(async () => { await result.current.loadMore() })
  expect(String(fetch.mock.calls[1][0])).not.toContain("cursor=")
  expect(result.current.history).toEqual([c])
  expect(result.current.pending).toBe(false)
})

test("changing credentials invalidates requests from the previous login", async () => {
  const { pending } = queuedFetch()
  const { result, rerender } = renderHook(({ key }) => useHistory(key, initial, vi.fn()), { initialProps: { key: "old" } })
  act(() => { void result.current.refresh() })
  rerender({ key: "new" })
  act(() => { void result.current.refresh() })
  await act(async () => { pending[1](json(page([c]))) })
  await act(async () => { pending[0](json(page([a, b]))) })
  expect(result.current.history).toEqual([c])
})
