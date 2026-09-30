import { TriangleAlert } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import type { StorageHealth } from "@/types"

export function StorageAlert({ health }: { health?: StorageHealth }) {
  if (!health || health.status === "ok") return null

  return (
    <Alert variant={health.status === "unavailable" ? "destructive" : "default"}>
      <TriangleAlert />
      <AlertTitle>
        {health.status === "unavailable" ? "费用记录异常" : "数据存储需要检查"}
      </AlertTitle>
      <AlertDescription>
        <p>{health.message}</p>
        {health.detail && (
          <details className="min-w-0">
            <summary className="cursor-pointer">故障详情</summary>
            <p className="mt-1 break-all font-mono text-xs">{health.detail}</p>
          </details>
        )}
      </AlertDescription>
    </Alert>
  )
}
