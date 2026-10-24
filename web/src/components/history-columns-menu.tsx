import { useEffect, useId, useState } from "react"
import { Menu } from "@base-ui/react/menu"
import { Check, Columns3, GripVertical, RotateCcw } from "lucide-react"

import { Button } from "@/components/ui/button"
import { historyColumns, type HistoryColumn } from "@/lib/history-columns"
import { useHistoryColumnDrag } from "@/lib/use-history-column-drag"
import { cn } from "@/lib/utils"

export function HistoryColumnsMenu({ columns, visibleColumns, collapsedColumns, onToggle, onReorder, onReset }: {
  columns: typeof historyColumns
  visibleColumns: ReadonlySet<HistoryColumn>
  collapsedColumns?: ReadonlySet<HistoryColumn>
  onToggle: (column: HistoryColumn) => void
  onReorder: (order: readonly HistoryColumn[]) => void
  onReset: () => void
}) {
  const [pressing, setPressing] = useState(false)
  const helpID = useId()
  const order = columns.map(column => column.id)
  const { popupRef, previewOrder, draggedColumn, startDrag, cancelDrag } = useHistoryColumnDrag(order, onReorder)
  const moveColumn = (column: HistoryColumn, offset: number) => {
    const next = [...order]
    const from = next.indexOf(column)
    const to = from + offset
    if (to < 0 || to >= next.length) return
    next.splice(from, 1)
    next.splice(to, 0, column)
    onReorder(next)
    requestAnimationFrame(() => popupRef.current?.querySelector<HTMLElement>(`[data-column-handle="${column}"]`)?.focus({ preventScroll: true }))
  }

  // Opening the menu transfers focus and clears the native :active state
  // before mouseup. Keep pointer feedback until the actual press ends.
  useEffect(() => {
    if (!pressing) return
    const release = () => setPressing(false)
    window.addEventListener("pointerup", release, true)
    window.addEventListener("pointercancel", release, true)
    window.addEventListener("blur", release)
    return () => {
      window.removeEventListener("pointerup", release, true)
      window.removeEventListener("pointercancel", release, true)
      window.removeEventListener("blur", release)
    }
  }, [pressing])

  return (
    <Menu.Root onOpenChange={open => { if (!open) cancelDrag() }}>
      <Menu.Trigger data-pressing={pressing || undefined} onPointerDown={event => { if (event.button === 0) setPressing(true) }}
        onPointerLeave={() => setPressing(false)}
        render={<Button variant="outline" className="text-muted-foreground font-normal active:translate-y-px data-pressing:translate-y-px aria-expanded:border-primary/40 aria-expanded:bg-primary/10 aria-expanded:text-primary aria-expanded:hover:bg-primary/15 aria-expanded:hover:text-primary dark:aria-expanded:bg-primary/20 dark:aria-expanded:hover:bg-primary/25" aria-label="显示列" />}>
        <Columns3 data-icon="inline-start" />
        显示列
        <span className="tabular-nums">{visibleColumns.size}/{historyColumns.length}</span>
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner side="bottom" align="end" sideOffset={6} className="z-50">
          <Menu.Popup ref={popupRef} aria-label="选择显示列" className="bg-popover text-popover-foreground max-h-(--available-height) w-72 max-w-[calc(100vw-2rem)] overflow-y-auto rounded-lg p-1.5 shadow-md ring-1 ring-foreground/10 outline-none">
            <Menu.Group>
              <Menu.GroupLabel className="text-muted-foreground px-2 py-1.5 text-xs">勾选需要显示的字段</Menu.GroupLabel>
              {previewOrder.map(id => {
                const column = columns.find(value => value.id === id)!
                return <div key={column.id} data-column-option={column.id} className={cn("flex items-center rounded-md", draggedColumn === column.id && "bg-primary/10 ring-1 ring-primary/40")}>
                <Menu.Item closeOnClick={false} aria-label={`调整${column.label}顺序`} aria-describedby={helpID} aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
                  data-column-handle={column.id} label={`${column.label}排序`}
                  onPointerDown={event => startDrag(event, column.id)}
                  onKeyDown={event => {
                    if (!event.altKey || !["ArrowUp", "ArrowDown"].includes(event.key)) return
                    event.preventDefault(); event.stopPropagation()
                    moveColumn(column.id, event.key === "ArrowUp" ? -1 : 1)
                  }}
                  className="text-muted-foreground data-highlighted:bg-accent data-highlighted:text-accent-foreground flex size-8 shrink-0 touch-none cursor-grab items-center justify-center rounded-md outline-none active:cursor-grabbing focus-visible:ring-2 focus-visible:ring-ring">
                  <GripVertical className="pointer-events-none size-3.5" />
                </Menu.Item>
                <Menu.CheckboxItem key={column.id} checked={visibleColumns.has(column.id)} disabled={column.id === "model"}
                  aria-label={column.label} closeOnClick={false} onCheckedChange={() => onToggle(column.id)}
                  className="data-highlighted:bg-accent data-highlighted:text-accent-foreground flex min-h-8 min-w-0 flex-1 cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-xs outline-none data-disabled:opacity-50">
                  <span className="border-input flex size-4 shrink-0 items-center justify-center rounded-sm border">
                    <Menu.CheckboxItemIndicator><Check className="size-3" /></Menu.CheckboxItemIndicator>
                  </span>
                  <span className="flex-1">{column.label}</span>
                  {column.id === "model" && <span className="text-muted-foreground text-2xs">始终显示</span>}
                  {column.id !== "model" && collapsedColumns?.has(column.id) && <span className="text-muted-foreground text-2xs">收进详情</span>}
                </Menu.CheckboxItem>
                </div>
              })}
            </Menu.Group>
            <p id={helpID} className="text-muted-foreground px-2 pt-1.5 text-2xs">拖动左侧手柄排序，也可用 Alt + ↑ / ↓。</p>
            <p className="text-muted-foreground px-2 py-1.5 text-2xs">空间不足时，次要字段会收进详情。</p>
            <Menu.Separator className="bg-border my-1 h-px" />
            <Menu.Item onClick={onReset} className="data-highlighted:bg-accent flex min-h-8 cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-xs outline-none">
              <RotateCcw className="size-3.5" />恢复默认
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  )
}
