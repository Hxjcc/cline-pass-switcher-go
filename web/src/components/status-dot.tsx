import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { shortDuration } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { UpstreamState, UpstreamStatus } from "@/types"

const dotStyles: Record<UpstreamState, string> = {
  ok: "bg-emerald-500",
  limited: "bg-amber-500",
  bad: "bg-red-500",
  auth: "bg-rose-500",
  unknown: "bg-muted-foreground/40",
}

const textStyles: Record<UpstreamState, string> = {
  ok: "text-emerald-700 dark:text-emerald-300",
  limited: "text-amber-700 dark:text-amber-300",
  bad: "text-red-700 dark:text-red-300",
  auth: "text-rose-700 dark:text-rose-300",
  unknown: "text-muted-foreground",
}

// The gateway note is usually a raw JSON error; the row shows a short phrase
// and keeps the raw text for the tooltip.
function summarizeUpstreamStatus(status?: UpstreamStatus): string {
  const state = status?.status ?? "unknown"
  const note = status?.note ?? ""
  switch (state) {
    case "ok":
      // A healthy channel is best described by how fast the check came
      // back. Thinking models burn the 16-token check budget on reasoning
      // and the gateway calls the empty content an error; that note stays
      // in the tooltip but is not a problem worth a label.
      return status?.ms ? shortDuration(status.ms) : "可用"
    case "limited":
      return "限流"
    case "auth":
      return "鉴权失败"
    case "bad":
      if (/no (allowed|available) providers/i.test(note)) return "网关无此渠道"
      if (/context.?length|too many tokens|maximum context/i.test(note)) return "上下文超限"
      if (/content.?filter|moderation/i.test(note)) return "内容过滤"
      if (/modelid|unsupported|not found|does not exist/i.test(note)) return "不支持该模型"
      return "不可钉住"
    default: {
      const code = note.match(/\b(4\d\d|5\d\d)\b/)
      if (code) return `HTTP ${code[1]}`
      if (/timeout|deadline|timed out/i.test(note)) return "超时"
      return note ? "上游错误" : "未判定"
    }
  }
}

export function StatusDot({
  status,
  className,
}: {
  status?: UpstreamStatus
  className?: string
}) {
  const state = status?.status ?? "unknown"
  const label = summarizeUpstreamStatus(status)
  const detail = [
    state === "ok" && status?.ms ? `校验请求往返 ${shortDuration(status.ms)}` : "",
    status?.note ?? "",
  ]
    .filter(Boolean)
    .join("\n")
  const body = (
    <>
      <span aria-hidden className={cn("size-2 shrink-0 rounded-full", dotStyles[state])} />
      {label}
    </>
  )
  const baseClass = cn(
    "inline-flex items-center gap-1.5 text-xs font-medium whitespace-nowrap",
    textStyles[state],
    className,
  )
  if (!detail) {
    return <span className={baseClass}>{body}</span>
  }
  return (
    <Tooltip>
      <TooltipTrigger render={<span className={cn(baseClass, "cursor-help")} />}>
        {body}
      </TooltipTrigger>
      <TooltipContent className="max-w-md font-mono text-2xs break-all whitespace-pre-wrap">
        {detail}
      </TooltipContent>
    </Tooltip>
  )
}
