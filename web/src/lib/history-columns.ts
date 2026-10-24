import { useEffect, useMemo, useState } from "react"

export const HISTORY_COLUMNS_STORAGE = "cline-pass-switcher-history-columns-v3"
export const PREVIOUS_HISTORY_COLUMNS_STORAGE = "cline-pass-switcher-history-columns-v2"
export const LEGACY_HISTORY_COLUMNS_STORAGE = "cline-pass-switcher-history-hidden-columns-v1"
export const HISTORY_COLUMN_ORDER_STORAGE = "cline-pass-switcher-history-column-order-v1"

export type HistoryColumn = "time" | "model" | "account" | "provider" | "latency" | "tokens" | "cache" | "cost" | "effort" | "mode" | "reasoning" | "canonical" | "gatewayCost"

export const historyColumns: ReadonlyArray<{ id: HistoryColumn; label: string; title?: string; defaultVisible?: boolean }> = [
  { id: "time", label: "时间", title: "完整日期和时间可在请求详情中查看" },
  { id: "model", label: "原始请求模型" },
  { id: "account", label: "账号" },
  { id: "provider", label: "实际上游" },
  { id: "canonical", label: "背后模型", defaultVisible: false },
  { id: "effort", label: "思考强度", title: "客户端 → 上游表示思考强度经过映射" },
  { id: "mode", label: "类型 / 方式", title: "请求类型及流式或非流式响应" },
  { id: "latency", label: "首字 / 总耗时" },
  { id: "tokens", label: "输入 → 输出", title: "输入和输出 Token；完整计数和思考明细可在请求详情中查看" },
  { id: "reasoning", label: "思考 Token", title: "输出 Token 中的思考部分，不与输出重复相加", defaultVisible: false },
  { id: "cache", label: "缓存命中", title: "缓存命中率；— 表示缺少计算所需的数据" },
  { id: "cost", label: "费用", title: "实际扣费；网关市价和费用明细可在请求详情中查看" },
  { id: "gatewayCost", label: "网关市价", title: "网关报告的参考价格，不计入密钥限额", defaultVisible: false },
]

export const historyCollapseOrder: HistoryColumn[] = ["canonical", "gatewayCost", "reasoning", "account", "cost", "provider", "time", "effort", "mode", "latency", "tokens"]
const defaults = () => new Set(historyColumns.filter(column => column.defaultVisible !== false).map(column => column.id))
const defaultOrder = historyColumns.map(column => column.id)

function normalizeOrder(value: unknown): HistoryColumn[] {
  const stored = Array.isArray(value) ? value : []
  return [...new Set<HistoryColumn>([...stored.filter((id): id is HistoryColumn => defaultOrder.includes(id)), ...defaultOrder])]
}

function readOrder(): HistoryColumn[] {
  try { return normalizeOrder(JSON.parse(localStorage.getItem(HISTORY_COLUMN_ORDER_STORAGE) ?? "null")) } catch { return [...defaultOrder] }
}

function readColumns(): Set<HistoryColumn> {
  try {
    const raw = localStorage.getItem(HISTORY_COLUMNS_STORAGE)
    if (raw !== null) {
      const stored: unknown = JSON.parse(raw)
      if (!Array.isArray(stored)) return defaults()
      return new Set(historyColumns.filter(column => column.id === "model" || stored.includes(column.id)).map(column => column.id))
    }
    const previous: unknown = JSON.parse(localStorage.getItem(PREVIOUS_HISTORY_COLUMNS_STORAGE) ?? "null")
    if (Array.isArray(previous)) {
      // Keep existing choices while enabling the two newly restored common
      // fields. Optional detail columns stay off until explicitly selected.
      return new Set(historyColumns.filter(column => column.id === "model" || column.id === "effort" || column.id === "mode" || previous.includes(column.id)).map(column => column.id))
    }
    const legacy: unknown = JSON.parse(localStorage.getItem(LEGACY_HISTORY_COLUMNS_STORAGE) ?? "null")
    if (Array.isArray(legacy)) return new Set(historyColumns.filter(column => column.defaultVisible !== false && (column.id === "model" || column.id === "cache" || (column.id === "latency" ? !(legacy.includes("ttft") && legacy.includes("duration")) : !legacy.includes(column.id)))).map(column => column.id))
  } catch {
    // Ignore invalid or unavailable browser storage.
  }
  return defaults()
}

export function useHistoryColumns() {
  const [selectedColumns, setColumns] = useState(readColumns)
  const [columnOrder, setColumnOrder] = useState(readOrder)
  const orderedColumns = useMemo(() => columnOrder.map(id => historyColumns.find(column => column.id === id)!), [columnOrder])
  const visibleColumns = useMemo(() => new Set(columnOrder.filter(id => selectedColumns.has(id))), [columnOrder, selectedColumns])
  useEffect(() => {
    try {
      localStorage.setItem(HISTORY_COLUMNS_STORAGE, JSON.stringify([...visibleColumns]))
    } catch {
      // Column selection still works when browser storage is unavailable.
    }
  }, [visibleColumns])
  useEffect(() => {
    try { localStorage.setItem(HISTORY_COLUMN_ORDER_STORAGE, JSON.stringify(columnOrder)) } catch { /* Sorting also works without storage. */ }
  }, [columnOrder])

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
    orderedColumns,
    toggleColumn,
    reorderColumns: (order: readonly HistoryColumn[]) => setColumnOrder(normalizeOrder(order)),
    resetColumns: () => { setColumns(defaults()); setColumnOrder([...defaultOrder]) },
  }
}
