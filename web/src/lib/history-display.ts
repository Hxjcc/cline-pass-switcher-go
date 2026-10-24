import { formatCost, formatTokenCount, shortDuration } from "@/lib/format"
import type { HistoryItem, UsageStats } from "@/types"

const finishLabels: Record<string, string> = {
  stop: "正常结束", tool_calls: "调用工具", function_call: "调用工具", length: "长度截断", content_filter: "内容过滤",
}

export function finishLabel(reason?: string) {
  return reason ? finishLabels[reason] || reason : ""
}

export function actualProvider(item: HistoryItem) { return item.resolved || item.provider }

export function hasFailover(item: HistoryItem) {
  return (item.trace?.length ?? 0) > 1 || Boolean(item.trace?.some(attempt => attempt.status !== 200)) || (item.attempts?.length ?? 0) > 1
}

export function fallbackTitle(item: HistoryItem) {
  return item.fallbackReason === "ignored" ? "网关从未尝试你钉的渠道：渠道偏好被忽略" : "实际渠道与网关会话亲和或首选渠道不一致（网关自动降级）"
}

export function providerCountersCoverThePrompt(usage: UsageStats) {
  const counted = (usage.cacheHitTokens ?? 0) + (usage.cacheMissTokens ?? 0)
  return counted > 0 && (!usage.promptTokens || counted >= usage.promptTokens * 0.9)
}

export function cacheStats(usage?: UsageStats): { rate?: number; tokens?: number } {
  if (!usage) return {}
  if (usage.cachedTokens !== undefined && usage.cachedTokens >= 0) {
    return { tokens: usage.cachedTokens, rate: usage.promptTokens && usage.promptTokens > 0 ? usage.cachedTokens / usage.promptTokens * 100 : undefined }
  }
  if (providerCountersCoverThePrompt(usage)) {
    const hit = usage.cacheHitTokens ?? 0
    return { tokens: hit, rate: hit / (hit + (usage.cacheMissTokens ?? 0)) * 100 }
  }
  return {}
}

export function formatCacheRate(usage?: UsageStats) {
  const { rate } = cacheStats(usage)
  return rate === undefined ? "" : `${rate.toFixed(rate === 0 || rate === 100 ? 0 : 1)}%`
}

export function usageTitle(usage?: UsageStats, finishReason?: string) {
  if (!usage && !finishReason) return undefined
  const parts: string[] = []
  if (usage?.promptTokens !== undefined) parts.push(`输入 ${formatTokenCount(usage.promptTokens)}`)
  if (usage?.completionTokens !== undefined) parts.push(`输出 ${formatTokenCount(usage.completionTokens)}`)
  if (usage?.reasoningTokens !== undefined) parts.push(`思考 ${formatTokenCount(usage.reasoningTokens)}`)
  const cache = cacheStats(usage)
  if (cache.tokens !== undefined) parts.push(`缓存 ${formatTokenCount(cache.tokens)}${formatCacheRate(usage) ? `（${formatCacheRate(usage)}）` : ""}`)
  if (usage && providerCountersCoverThePrompt(usage) && usage.cacheHitTokens !== undefined) parts.push(`命中 ${formatTokenCount(usage.cacheHitTokens)} / 未命中 ${formatTokenCount(usage.cacheMissTokens ?? 0)}`)
  if (usage?.totalTokens !== undefined) parts.push(`合计 ${formatTokenCount(usage.totalTokens)}`)
  if (usage?.cost !== undefined) parts.push(`费用 ${formatCost(usage.cost)}`)
  if (finishReason) parts.push(finishLabel(finishReason))
  return parts.join(" · ")
}

export function gatewayMultiple(usage: UsageStats) {
  if (usage.gatewayCost === undefined || !usage.cost) return ""
  const multiple = usage.gatewayCost / usage.cost
  return Math.abs(multiple - 1) < 0.005 ? "" : ` ×${multiple.toFixed(multiple >= 10 ? 0 : 1)}`
}

export function costTitle(usage: UsageStats) {
  const parts = [`账本 ${formatCost(usage.cost)}（实际扣费，计入密钥限额）`]
  if (usage.gatewayCost !== undefined && Math.abs(usage.gatewayCost - (usage.cost ?? 0)) > 1e-9) parts.push(`网关 ${formatCost(usage.gatewayCost)}（网关市价口径${gatewayMultiple(usage)}：含渠道差价与按次工具费，不影响扣费）`)
  if (usage.inputCost !== undefined) parts.push(`输入 ${formatCost(usage.inputCost)}`)
  if (usage.outputCost !== undefined) parts.push(`输出 ${formatCost(usage.outputCost)}`)
  if (usage.surchargeCost !== undefined) parts.push(`附加 ${formatCost(usage.surchargeCost)}`)
  return parts.join("\n")
}

export function historyError(item: HistoryItem) {
  const actual = actualProvider(item)
  if (item.gatewayHas?.length && item.routingStatus === "mismatch" && actual && item.targetProviders?.length) {
    const repeated = [`实际供应商不满足本次路由要求：${actual}`, `实际供应商 ${actual} 不在本次允许列表中`, ...(item.targetProviders.length === 1 ? [`目标 ${item.targetProviders[0]}，实际 ${actual}`] : [])]
    if (item.error && repeated.includes(item.error.trim())) return null
  }
  return item.error
}

export function historyStatus(item: HistoryItem) {
  if (item.routingStatus === "rejected") return { label: "无效上游已拒绝", tone: "success" as const }
  if (item.routingStatus === "mismatch") return { label: "未命中目标", tone: "error" as const }
  if (item.error) return { label: "请求失败", tone: "error" as const }
  if (item.degraded) return { label: "压缩降级", tone: "warning" as const }
  if (item.routingStatus === "matched") return { label: "已命中目标", tone: "success" as const }
  if (item.gatewayHas?.length) return { label: "路由未确认", tone: "muted" as const }
  return { label: "请求成功", tone: "success" as const }
}

export function fullHistoryTime(ts: number) {
  return ts ? new Date(ts).toLocaleString("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "—"
}

export function latencyText(item: HistoryItem) {
  return `${shortDuration(item.ttftMs).replace(/\s/g, "")} / ${shortDuration(item.ms).replace(/\s/g, "")}`
}
