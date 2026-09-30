import { useCallback, useEffect, useRef, useState } from "react"

import { api, APIError } from "@/lib/api"
import { HISTORY_PAGE_SIZE, type CachedSnapshot } from "@/lib/console-snapshot"
import { historyEntryMatches, historyQueryKey } from "@/lib/history-filter"
import type { HistoryItem, HistoryResponse } from "@/types"

type Query = { q: string; onlyErrors: boolean }
type View = { history: HistoryItem[]; historyTotal: number; historyHasMore: boolean; historyCursor: string }
type Flight = { kind: "replace" | "append"; query: string; controller: AbortController; promise: Promise<void> }
const emptyView = (): View => ({ history: [], historyTotal: 0, historyHasMore: false, historyCursor: "" })

// Every history entry point shares this coordinator, including the top-level
// refresh. Replacing a query invalidates all older responses; an append can
// only attach to the exact view and cursor that started it.
export function useHistory(key: string, initial: CachedSnapshot | null, onError: (error: unknown) => void) {
  const [view, setView] = useState<View>(() => ({
    history: initial?.history ?? [],
    historyTotal: initial?.historyTotal ?? initial?.history.length ?? 0,
    historyHasMore: initial?.historyHasMore ?? false,
    historyCursor: initial?.historyCursor ?? "",
  }))
  const [query, setQuery] = useState<Query>(initial?.historyQuery ?? { q: "", onlyErrors: false })
  const [pending, setPending] = useState(false)
  const viewRef = useRef(view)
  const queryRef = useRef(query)
  const active = useRef<Flight | null>(null)
  const clearing = useRef(false)
  const cache = useRef(new Map<string, View>())

  const publish = useCallback((next: View) => {
    viewRef.current = next
    setView(next)
  }, [])

  const invalidate = useCallback(() => {
    active.current?.controller.abort()
    active.current = null
  }, [])

  useEffect(() => () => {
    invalidate()
    cache.current.clear()
  }, [key, invalidate])

  const requestPage = useCallback(function run(kind: "replace" | "append"): Promise<void> {
    if (clearing.current) return Promise.resolve()
    const query = queryRef.current
    const queryKey = historyQueryKey(query)
    // Coalesce refresh ticks on a slow connection instead of continually
    // cancelling a response that has not had time to arrive.
    if (active.current && active.current.query === queryKey &&
      (kind === "append" || active.current.kind === "replace")) return active.current.promise
    const base = viewRef.current
    if (kind === "append" && !base.historyHasMore) return Promise.resolve()
    if (kind === "append" && !base.historyCursor) return run("replace")
    invalidate()
    const flight: Flight = { kind, query: queryKey, controller: new AbortController(), promise: Promise.resolve() }
    active.current = flight
    setPending(true)
    flight.promise = (async () => {
      const params = new URLSearchParams({
        limit: String(kind === "append" ? HISTORY_PAGE_SIZE : Math.max(HISTORY_PAGE_SIZE, Math.min(base.history.length, 200))),
      })
      if (kind === "append") params.set("cursor", base.historyCursor)
      if (query.q.trim()) params.set("q", query.q.trim())
      if (query.onlyErrors) params.set("result", "error")
      try {
        const response = await api<HistoryResponse>(`/api/history?${params}`, { key, signal: flight.controller.signal })
        if (active.current !== flight) return
        let rows = response.history
        if (kind === "append") {
          const seen = new Set(base.history.map((entry) => entry.id).filter(Boolean))
          rows = [...base.history, ...rows.filter((entry) => !entry.id || !seen.has(entry.id))]
        }
        const next: View = {
          history: rows, historyTotal: response.total,
          historyHasMore: response.hasMore, historyCursor: response.nextCursor ?? "",
        }
        publish(next)
        if (kind === "replace") cache.current.set(queryKey, next)
      } catch (error) {
        if (active.current !== flight) return
        if (kind === "append" && error instanceof APIError && error.code === "history_cursor_expired") {
          // Retention or a clear in another browser removed the anchor. Start
          // a new view, never append the first page to an obsolete list.
          active.current = null
          cache.current.clear()
          publish(emptyView())
          await run("replace")
        } else {
          throw error
        }
      } finally {
        if (active.current === flight) {
          active.current = null
          setPending(false)
        }
      }
    })()
    return flight.promise
  }, [key, invalidate, publish])

  const changeQuery = useCallback((next: Query) => {
    queryRef.current = next
    setQuery(next)
    const cached = cache.current.get(historyQueryKey(next))
    const rows = viewRef.current.history.filter((entry) => historyEntryMatches(entry, next))
    publish(cached ?? { ...emptyView(), history: rows, historyTotal: rows.length })
    void requestPage("replace").catch(onError)
  }, [onError, publish, requestPage])

  const refresh = useCallback(() => requestPage("replace"), [requestPage])
  const loadMore = useCallback(() => requestPage("append"), [requestPage])
  const clear = useCallback(async () => {
    clearing.current = true
    invalidate()
    setPending(true)
    try {
      await api("/api/history/clear", { key, body: {} })
      cache.current.clear()
      publish(emptyView())
    } finally {
      clearing.current = false
      setPending(false)
    }
  }, [key, invalidate, publish])

  return { ...view, query, pending, changeQuery, refresh, loadMore, clear }
}
