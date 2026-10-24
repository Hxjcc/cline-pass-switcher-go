import { useEffect, useState } from "react"

export const HISTORY_COLUMNS_STORAGE = "cline-pass-switcher-history-columns-v2"
export const LEGACY_HISTORY_COLUMNS_STORAGE = "cline-pass-switcher-history-hidden-columns-v1"

export type HistoryColumn = "time" | "model" | "account" | "provider" | "latency" | "tokens" | "cache" | "cost"

export const historyColumns: ReadonlyArray<{ id: HistoryColumn; label: string; title?: string }> = [
  { id: "time", label: "时间", title: "完整日期和时间可在请求详情中查看" },
  { id: "model", label: "原始请求模型" },
  { id: "account", label: "账号" },
  { id: "provider", label: "实际上游" },
  { id: "latency", label: "首字 / 总耗时" },
  { id: "tokens", label: "输入 → 输出", title: "输入和输出 Token；完整计数和思考明细可在请求详情中查看" },
  { id: "cache", label: "缓存命中", title: "缓存命中率；— 表示缺少计算所需的数据" },
  { id: "cost", label: "费用", title: "实际扣费；网关市价和费用明细可在请求详情中查看" },
]

export const historyCollapseOrder: HistoryColumn[] = ["account", "cost", "provider", "time", "latency", "tokens"]
const defaults = () => new Set(historyColumns.map(column => column.id))

function readColumns(): Set<HistoryColumn> {
  try {
    const raw = localStorage.getItem(HISTORY_COLUMNS_STORAGE)
    if (raw !== null) {
      const stored: unknown = JSON.parse(raw)
      if (!Array.isArray(stored)) return defaults()
      return new Set(historyColumns.filter(column => column.id === "model" || stored.includes(column.id)).map(column => column.id))
    }
    const legacy: unknown = JSON.parse(localStorage.getItem(LEGACY_HISTORY_COLUMNS_STORAGE) ?? "null")
    if (Array.isArray(legacy)) return new Set(historyColumns.filter(column => column.id === "model" || column.id === "cache" || (column.id === "latency" ? !(legacy.includes("ttft") && legacy.includes("duration")) : !legacy.includes(column.id))).map(column => column.id))
  } catch {
    // Ignore invalid or unavailable browser storage.
  }
  return defaults()
}

export function useHistoryColumns() {
  const [visibleColumns, setColumns] = useState(readColumns)
  useEffect(() => {
    try {
      localStorage.setItem(HISTORY_COLUMNS_STORAGE, JSON.stringify([...visibleColumns]))
    } catch {
      // Column selection still works when browser storage is unavailable.
    }
  }, [visibleColumns])

  const toggleColumn = (column: HistoryColumn) => {
    if (column === "model") return
    setColumns(current => {
      const next = new Set(current)
      if (next.has(column)) next.delete(column)
      else next.add(column)
      return next
    })
  }
  return {
    visibleColumns,
    toggleColumn,
    resetColumns: () => setColumns(defaults()),
  }
}
