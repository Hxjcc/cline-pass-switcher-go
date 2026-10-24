import { formatCost } from "@/lib/format"
import { formatUsageTokens } from "@/lib/usage-statistics"
import { useNarrowViewport } from "@/lib/use-media-query"
import type { UsageDay } from "@/types"

export function UsageChart({ days }: { days: UsageDay[] }) {
  const narrow = useNarrowViewport()
  const width = narrow ? 360 : 960
  const left = 48, top = 16, bottom = 206
  const plotWidth = width - left - 12
  const max = Math.max(1, ...days.map(day => day.inputTokens + day.outputTokens))
  const step = plotWidth / Math.max(1, days.length)
  const barWidth = Math.max(1, Math.min(40, step * 0.64))
  const labelStep = Math.max(1, Math.ceil(days.length / (narrow ? 4 : 8)))
  return <svg role="img" aria-label="每日输入与输出 Token 用量趋势" viewBox={`0 0 ${width} 240`} className="h-60 w-full overflow-visible">
    {[0, 0.5, 1].map(ratio => {
      const y = bottom - ratio * (bottom - top)
      return <g key={ratio}>
        <line x1={left} x2={width - 12} y1={y} y2={y} className="stroke-border" strokeDasharray="3 5" />
        <text x={left - 8} y={y + 4} textAnchor="end" className="fill-muted-foreground text-[10px]">{formatUsageTokens(Math.round(max * ratio))}</text>
      </g>
    })}
    {days.map((day, index) => {
      const x = left + step * (index + 0.5)
      const inputHeight = day.inputTokens / max * (bottom - top)
      const outputHeight = day.outputTokens / max * (bottom - top)
      return <g key={day.day}>
        <title>{`${day.day} · ${day.requests.toLocaleString("zh-CN")} 次请求\n输入 ${day.inputTokens.toLocaleString("zh-CN")} · 输出 ${day.outputTokens.toLocaleString("zh-CN")}\n缓存 ${day.cachedTokens.toLocaleString("zh-CN")} · 实际扣费 ${formatCost(day.cost)}`}</title>
        <rect x={x - barWidth / 2} y={bottom - inputHeight} width={barWidth} height={inputHeight} rx={2} className="fill-blue-500/80" />
        <rect x={x - barWidth / 2} y={bottom - inputHeight - outputHeight} width={barWidth} height={outputHeight} rx={2} className="fill-violet-500/80" />
        {index % labelStep === 0 && <text x={x} y={228} textAnchor="middle" className="fill-muted-foreground text-[10px]">{day.day.slice(5).replace("-", "/")}</text>}
      </g>
    })}
  </svg>
}
