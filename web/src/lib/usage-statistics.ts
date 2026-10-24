import type { UsageFilter } from "@/types"
import { formatTokenCount } from "@/lib/format"

export function formatUsageTokens(value: number) {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  return formatTokenCount(value)
}

export function usageDate(offsetDays = 0, now = Date.now()) {
  return new Date(now + 8 * 60 * 60 * 1000 + offsetDays * 24 * 60 * 60 * 1000).toISOString().slice(0, 10)
}

export function defaultUsageFilter(days = 7): UsageFilter {
  return { from: usageDate(1 - days), to: usageDate(), model: "", account: "", key: "" }
}

export function usagePath(filter: UsageFilter) {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(filter)) if (value) query.set(key, value)
  return `/api/usage?${query}`
}

export function validateUsageFilter(filter: UsageFilter) {
  if (!filter.from || !filter.to) return "请选择开始和结束日期"
  if (filter.from > filter.to) return "开始日期不能晚于结束日期"
  if (filter.to > usageDate()) return "结束日期不能晚于今天"
  if ((Date.parse(filter.to) - Date.parse(filter.from)) / 86400000 >= 365) return "每次最多查询 365 天"
  return ""
}
