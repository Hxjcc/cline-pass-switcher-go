import { Menu } from "@base-ui/react/menu"
import { Check, Columns3, RotateCcw } from "lucide-react"

import { Button } from "@/components/ui/button"
import { historyColumns, type HistoryColumn } from "@/lib/history-columns"

export function HistoryColumnsMenu({ visibleColumns, collapsedColumns, onToggle, onReset }: {
  visibleColumns: ReadonlySet<HistoryColumn>
  collapsedColumns?: ReadonlySet<HistoryColumn>
  onToggle: (column: HistoryColumn) => void
  onReset: () => void
}) {
  return (
    <Menu.Root>
      <Menu.Trigger render={<Button variant="outline" className="text-muted-foreground font-normal" aria-label="显示列" />}>
        <Columns3 data-icon="inline-start" />
        显示列
        <span className="text-muted-foreground tabular-nums">{visibleColumns.size}/{historyColumns.length}</span>
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner side="bottom" align="end" sideOffset={6} className="z-50">
          <Menu.Popup aria-label="选择显示列" className="bg-popover text-popover-foreground max-h-(--available-height) w-52 max-w-[calc(100vw-2rem)] overflow-y-auto rounded-lg p-1.5 shadow-md ring-1 ring-foreground/10 outline-none">
            <Menu.Group>
              <Menu.GroupLabel className="text-muted-foreground px-2 py-1.5 text-xs">勾选需要显示的字段</Menu.GroupLabel>
              {historyColumns.map(column => (
                <Menu.CheckboxItem key={column.id} checked={visibleColumns.has(column.id)} disabled={column.id === "model"}
                  aria-label={column.label} closeOnClick={false} onCheckedChange={() => onToggle(column.id)}
                  className="data-highlighted:bg-accent data-highlighted:text-accent-foreground flex min-h-8 cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-xs outline-none data-disabled:opacity-50">
                  <span className="border-input flex size-4 shrink-0 items-center justify-center rounded-sm border">
                    <Menu.CheckboxItemIndicator><Check className="size-3" /></Menu.CheckboxItemIndicator>
                  </span>
                  <span className="flex-1">{column.label}</span>
                  {column.id === "model" && <span className="text-muted-foreground text-2xs">始终显示</span>}
                  {column.id !== "model" && collapsedColumns?.has(column.id) && <span className="text-muted-foreground text-2xs">收进详情</span>}
                </Menu.CheckboxItem>
              ))}
            </Menu.Group>
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
