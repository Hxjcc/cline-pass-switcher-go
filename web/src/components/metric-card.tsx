import type { LucideIcon } from "lucide-react"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"

export function MetricCard({
  label,
  value,
  detail,
  icon: Icon,
}: {
  label: string
  value: string | number
  detail?: string
  icon: LucideIcon
}) {
  return (
    <Card size="sm">
      <CardHeader className="grid-cols-[1fr_auto] items-center">
        <CardTitle className="text-muted-foreground text-xs font-medium">{label}</CardTitle>
        <div className="bg-muted text-muted-foreground flex size-7 items-center justify-center rounded-md">
          <Icon className="size-3.5" />
        </div>
      </CardHeader>
      <CardContent>
        <div className="font-mono text-2xl font-semibold tracking-tight tabular-nums">{value}</div>
        {detail && <div className="text-muted-foreground mt-1 truncate text-xs">{detail}</div>}
      </CardContent>
    </Card>
  )
}
