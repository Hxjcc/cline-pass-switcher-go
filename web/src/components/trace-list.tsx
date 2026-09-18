import { ArrowRight } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"
import { shortDuration } from "@/lib/format"
import type { TraceAttempt } from "@/types"

export function TraceList({
  trace,
  className,
  compact = false,
}: {
  trace?: TraceAttempt[]
  className?: string
  // Compact mode keeps per-attempt timing in the tooltip so the badge fits a
  // narrow table column.
  compact?: boolean
}) {
  if (!trace?.length) {
    return <span className="text-muted-foreground">—</span>
  }

  return (
    <div className={cn("flex flex-wrap items-center gap-1.5", className)}>
      {trace.map((attempt, index) => {
        const duration = shortDuration(attempt.ms)
        const title = compact
          ? [duration, attempt.note].filter(Boolean).join(" · ")
          : attempt.note
        return (
          <div key={`${attempt.upstream ?? "auto"}-${index}`} className="flex items-center gap-1.5">
            {index > 0 && <ArrowRight className="size-3 text-muted-foreground" />}
            <Badge
              variant="outline"
              className={cn(
                "h-5 px-1.5 py-0 font-mono text-2xs",
                attempt.status === 200
                  ? "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-300"
                  : "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300",
              )}
              title={title}
            >
              {attempt.upstream || "auto"} · {attempt.status === 200 ? "OK" : attempt.status}
              {!compact && <> · {duration}</>}
            </Badge>
          </div>
        )
      })}
    </div>
  )
}
