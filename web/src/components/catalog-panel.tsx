import { useMemo, useState } from "react"
import { Radar, Search } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { errorMessage } from "@/lib/api"
import { pinReasonLabel } from "@/lib/format"
import type { ModelsResponse, ProbeResponse } from "@/types"

export function CatalogPanel({
  data,
  onProbe,
}: {
  data: ModelsResponse
  onProbe: (modelID: string) => Promise<ProbeResponse>
}) {
  const [filter, setFilter] = useState("")
  const [probing, setProbing] = useState<string | null>(null)
  const models = useMemo(() => {
    const query = filter.trim().toLowerCase()
    const values = query
      ? data.catalog.filter((model) => model.toLowerCase().includes(query))
      : data.catalog
    return values.slice(0, 400)
  }, [data.catalog, filter])

  const probe = async (modelID: string) => {
    setProbing(modelID)
    try {
      const result = await onProbe(modelID)
      toast.success(`探测完成：${result.upstreams?.length ?? 0} 个渠道`)
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setProbing(null)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>完整模型目录</CardTitle>
        <CardDescription>
          共 {data.catalogCount} 个目录模型，当前显示 {models.length} 个。
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="relative max-w-sm">
          <Search className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
          <Input
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder="筛选模型，例如 free、glm、anthropic"
            className="pl-8"
          />
        </div>
        <div className="grid max-h-[680px] gap-2 overflow-y-auto pr-1 md:grid-cols-2 xl:grid-cols-3">
          {models.map((modelID) => {
            const model = data.subscription.find((item) => item.id === modelID)
            const pinnable = model?.meta?.pinnable === true
            const pinDisabled =
              model?.meta?.pinnable === false || Boolean(model?.meta?.pinReason)
            return (
              <div
                key={modelID}
                className="bg-card flex min-w-0 items-center gap-2 rounded-lg border px-3 py-2"
              >
                <div className="min-w-0 flex-1 truncate font-mono text-xs font-medium" title={modelID}>
                  {modelID}
                </div>
                {pinnable && (
                  <Badge variant="default" className="h-5 px-1.5 text-2xs">
                    可精确钉住
                  </Badge>
                )}
                {pinDisabled && (
                  <Badge
                    variant="outline"
                    className="h-5 px-1.5 text-2xs"
                    title={pinReasonLabel(model?.meta?.pinReason)}
                  >
                    钉住不可用
                  </Badge>
                )}
                {model?.meta?.lastProvider && (
                  <Badge variant="secondary" className="h-5 px-1.5 font-mono text-2xs">
                    {model.meta.lastProvider}
                  </Badge>
                )}
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={probing === modelID}
                  onClick={() => probe(modelID)}
                >
                  <Radar
                    className={probing === modelID ? "animate-pulse" : ""}
                    data-icon="inline-start"
                  />
                  探测
                </Button>
              </div>
            )
          })}
          {!models.length && (
            <div className="text-muted-foreground col-span-full rounded-lg border border-dashed py-16 text-center text-sm">
              没有匹配的目录模型
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
