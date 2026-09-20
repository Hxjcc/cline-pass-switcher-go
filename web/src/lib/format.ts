import type { UpstreamState } from "@/types"

export const upstreamLabels: Record<UpstreamState, string> = {
  ok: "可用",
  limited: "限流",
  bad: "不可钉",
  auth: "Key 异常",
  unknown: "未判定",
}

export const upstreamRank: Record<UpstreamState, number> = {
  ok: 0,
  limited: 1,
  unknown: 2,
  auth: 3,
  bad: 4,
}

export function formatTime(timestamp?: number): string {
  if (!timestamp) return "—"
  return new Date(timestamp).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  })
}

export function shortDuration(value?: number): string {
  if (value === undefined || value === null) return "—"
  if (value < 1000) return `${value} ms`
  return `${(value / 1000).toFixed(1)} s`
}

export function formatTokenCount(value?: number): string {
  if (value === undefined || value === null) return "—"
  if (value >= 10_000) return `${(value / 1000).toFixed(1)}k`
  return value.toLocaleString("zh-CN")
}

export function formatCost(value?: number): string {
  if (value === undefined || value === null) return ""
  if (value >= 0.01) return `$${value.toFixed(3)}`
  if (value >= 0.0001) return `$${value.toFixed(4)}`
  return `$${value.toFixed(6)}`
}

// Cline's quota API returns money as integer 1e-8 USD units. The scale was
// verified by matching a generation's gateway cost ($0.000006) with the usage
// ledger entry for the same id (costUsd=600).
export function formatQuotaUSD(units?: number): string {
  if (units === undefined || units === null) return "—"
  const usd = units / 1e8
  if (usd >= 1) return `$${usd.toFixed(2)}`
  if (usd >= 0.01) return `$${usd.toFixed(4)}`
  return `$${usd.toFixed(6)}`
}

// Compact "recently used" stamp: time only for today, date + time otherwise.
export function formatCompactTime(timestamp?: number): string {
  if (!timestamp) return "—"
  const date = new Date(timestamp)
  const sameDay = date.toDateString() === new Date().toDateString()
  return date.toLocaleString("zh-CN", sameDay
    ? { hour: "2-digit", minute: "2-digit", second: "2-digit" }
    : { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })
}

export function formatClock(timestamp?: number): string {
  if (!timestamp) return "—"
  return new Date(timestamp).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })
}

// Quota reset stamps arrive as ISO strings; show the clock for today and the
// date for anything further out.
export function formatResetTime(value?: string): string {
  if (!value) return "—"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "—"
  const sameDay = date.toDateString() === new Date().toDateString()
  return date.toLocaleString("zh-CN", sameDay
    ? { hour: "2-digit", minute: "2-digit" }
    : { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })
}

export function formatPlanExpiry(value?: string): string {
  if (!value) return "—"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "—"
  return date.toLocaleString("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", hour12: false,
  })
}

// The Cline gateway forwards each model through one of two aggregators; we
// tell them apart by the shape of the routing metadata in the response.
export function pipelineLabel(pipeline?: string): string {
  if (pipeline === "direct") return "OpenRouter"
  if (pipeline === "planner") return "Vercel 网关"
  return "未识别"
}

// Gateway provider slugs that are not a marketplace vendor but a private
// endpoint Cline wired into the gateway. The raw slug is too long for a table
// chip and says nothing to the reader; keep it for the tooltip.
const providerLabels: Record<string, { label: string; hint: string }> = {
  "openai-compatible-private": {
    label: "私有接口",
    hint: "Cline 在网关里私有注册的 OpenAI 兼容接口，通常是厂商自己的 API；这类模型没有其他渠道可选。",
  },
}

export function providerLabel(slug?: string): string {
  if (!slug) return ""
  return providerLabels[slug]?.label ?? slug
}

export function providerHint(slug?: string): string {
  if (!slug) return ""
  const entry = providerLabels[slug]
  return entry ? `${slug}\n${entry.hint}` : ""
}

// Pin support is discovered by probing the gateway, not by assuming that a
// pipeline name implies it works. Keep the backend reason codes readable.
export function pinReasonLabel(reason?: string): string {
  switch (reason) {
    case "gateway_ignores_provider_preferences":
      return "网关已忽略上游偏好"
    case "single_provider":
      return "只有一个候选渠道，无需钉住"
    case "probe_failed":
      return "无法确认网关是否支持钉住"
    case "no_channels":
      return "没有可用渠道"
    case "unsupported_pipeline":
      return "无法识别路由线路"
    default:
      return reason ? `当前不可钉（${reason}）` : "当前不可钉"
  }
}

export function pipelineHint(pipeline?: string, pinnable?: boolean, pinReason?: string): string {
  if (pinnable === false && pinReason === "single_provider") {
    return "这个模型只有一个候选渠道，无需钉住，也没有其他渠道可校验。"
  }
  if (pinnable === false) {
    const reason = pinReasonLabel(pinReason)
    switch (pipeline) {
      case "direct":
        return `Cline 经 OpenRouter 路由，但${reason}；钉住、排除、排序与校验不会生效。`
      case "planner":
        return `Cline 经 Vercel AI Gateway 路由，但${reason}；钉住、排除、排序与校验不会生效。`
      default:
        return `${reason}；无法确认钉住、排除、排序与校验是否生效。`
    }
  }
  if (pipeline === "direct") {
    return "Cline 网关经 OpenRouter 路由到各渠道，钉住与排序通过 provider 字段下发。"
  }
  if (pipeline === "planner") {
    return "Cline 网关经 Vercel AI Gateway 路由到各渠道，钉住与排序通过 providerOptions.gateway 字段下发。"
  }
  return "响应里没有可识别的路由信息，无法区分渠道，也无法钉住。"
}

export function normalizeModelConfig(
  config?: Partial<import("@/types").ModelConfig>,
): import("@/types").ModelConfig {
  return {
    upstream: config?.upstream,
    upstreams: config?.upstreams ?? [],
    exclude: config?.exclude ?? [],
    pinMode: config?.pinMode === "preferred" ? "preferred" : "strict",
    sort: config?.sort ?? null,
  }
}
