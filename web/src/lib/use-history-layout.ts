import { useLayoutEffect, useRef, useState } from "react"
import { historyCollapseOrder, type HistoryColumn } from "@/lib/history-columns"
import type { HistoryItem } from "@/types"

// Measure real cell widths rather than shortening values or estimating model
// names. Responsive omissions are local to this layout, never saved preferences.
export function useHistoryLayout(preferred: ReadonlySet<HistoryColumn>, history: HistoryItem[], narrow: boolean) {
  const viewportRef = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  const key = [...preferred].sort().join(",")
  const [layout, setLayout] = useState({ key: "", width: 0, collapsed: [] as HistoryColumn[], cards: false })
  const current = layout.key === key && layout.width === width
  const collapsedColumns = new Set(current ? layout.collapsed : [])
  const visibleColumns = new Set([...preferred].filter(column => !collapsedColumns.has(column)))
  const cards = narrow || (current && layout.cards)

  useLayoutEffect(() => {
    const viewport = viewportRef.current
    if (!viewport) return
    const measure = () => setWidth(viewport.clientWidth)
    measure()
    if (typeof ResizeObserver === "undefined") return
    const observer = new ResizeObserver(measure)
    observer.observe(viewport)
    return () => observer.disconnect()
  }, [])

  useLayoutEffect(() => {
    if (narrow || width <= 0 || cards) return
    const table = viewportRef.current?.querySelector("table")
    if (!table) return
    const next = current ? [...layout.collapsed] : []
    let required = table.scrollWidth
    for (const column of historyCollapseOrder) {
      if (required <= width + 1) break
      const header = table.querySelector<HTMLElement>(`th[data-history-column="${column}"]`)
      if (!header) continue
      next.push(column)
      required -= header.getBoundingClientRect().width
    }
    const useCards = required > width + 1
    if (!current || next.join(",") !== layout.collapsed.join(",") || useCards !== layout.cards) {
      setLayout({ key, width, collapsed: next, cards: useCards })
    }
  }, [preferred, history, narrow, width, key, current, layout, cards])

  return { viewportRef, visibleColumns, collapsedColumns, cards }
}
