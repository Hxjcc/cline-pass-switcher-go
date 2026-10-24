import { useEffect, useRef, useState, type PointerEvent } from "react"
import type { HistoryColumn } from "@/lib/history-columns"

// Pointer events keep the small reorder list usable with mouse, pen and touch.
// Only commit on drop: Escape, cancellation and dropping outside leave the
// saved order and column visibility untouched.
export function useHistoryColumnDrag(order: readonly HistoryColumn[], onReorder: (order: HistoryColumn[]) => void) {
  const popupRef = useRef<HTMLDivElement>(null)
  const [preview, setPreview] = useState<{ column: HistoryColumn; order: HistoryColumn[] } | null>(null)
  const cancelRef = useRef<() => void>(() => {})
  useEffect(() => () => cancelRef.current(), [])

  const startDrag = (event: PointerEvent<HTMLElement>, column: HistoryColumn) => {
    if (event.button !== 0) return
    event.preventDefault()
    event.stopPropagation()
    cancelRef.current()
    const popup = popupRef.current
    if (!popup) return
    const pointerID = event.pointerId
    const startY = event.clientY
    let x = event.clientX
    let y = startY
    let next = [...order]
    let dragging = false
    let frame = 0

    const inside = () => {
      const rect = popup.getBoundingClientRect()
      return x >= rect.left && x <= rect.right && y >= rect.top && y <= rect.bottom
    }
    const update = () => {
      if (!inside()) return
      const rows = popup.querySelectorAll<HTMLElement>("[data-column-option]")
      for (const row of rows) {
        const rect = row.getBoundingClientRect()
        if (y < rect.top || y > rect.bottom) continue
        const target = row.dataset.columnOption as HistoryColumn
        const from = next.indexOf(column)
        const to = next.indexOf(target)
        if (from === to || to < 0) return
        const middle = (rect.top + rect.bottom) / 2
        if (from < to ? y < middle : y > middle) return
        next = [...next]
        next.splice(from, 1)
        next.splice(to, 0, column)
        setPreview({ column, order: next })
        return
      }
    }
    const autoScroll = () => {
      if (inside()) {
        const rect = popup.getBoundingClientRect()
        const delta = y < rect.top + 28 ? -8 : y > rect.bottom - 28 ? 8 : 0
        if (delta) { popup.scrollTop += delta; update() }
      }
      frame = requestAnimationFrame(autoScroll)
    }
    const finish = (commit: boolean) => {
      window.removeEventListener("pointermove", move, true)
      window.removeEventListener("pointerup", drop, true)
      window.removeEventListener("pointercancel", cancel, true)
      window.removeEventListener("keydown", keyDown, true)
      window.removeEventListener("blur", cancel)
      cancelAnimationFrame(frame)
      cancelRef.current = () => {}
      setPreview(null)
      if (commit && dragging && inside()) onReorder(next)
    }
    const move = (e: globalThis.PointerEvent) => {
      if (e.pointerId !== pointerID) return
      if (e.buttons === 0) { finish(false); return }
      x = e.clientX; y = e.clientY
      if (!dragging && Math.abs(y - startY) < 4) return
      e.preventDefault()
      if (!dragging) {
        dragging = true
        setPreview({ column, order: next })
        frame = requestAnimationFrame(autoScroll)
      }
      update()
    }
    const drop = (e: globalThis.PointerEvent) => {
      if (e.pointerId !== pointerID) return
      x = e.clientX; y = e.clientY
      if (dragging) { e.preventDefault(); e.stopPropagation() }
      finish(true)
    }
    const cancel = () => finish(false)
    const keyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); cancel() }
    }
    cancelRef.current = cancel
    window.addEventListener("pointermove", move, { capture: true, passive: false })
    window.addEventListener("pointerup", drop, true)
    window.addEventListener("pointercancel", cancel, true)
    window.addEventListener("keydown", keyDown, true)
    window.addEventListener("blur", cancel)
  }

  return { popupRef, previewOrder: preview?.order ?? order, draggedColumn: preview?.column, startDrag, cancelDrag: () => cancelRef.current() }
}
