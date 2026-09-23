import { useMemo, useState } from "react"
import { Download, Radar, RefreshCw, Search, Sparkles, X } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ModelRow } from "@/components/model-row"
import { errorMessage } from "@/lib/api"
import type { ProbeBatchResult } from "@/lib/probe-batch"
import type {
  ModelConfig,
  ModelsResponse,
  OfficialResponse,
  ProbeResponse,
  TestResponse,
  ValidationResponse,
} from "@/types"

interface ModelsPanelProps {
  data: ModelsResponse
  onRefresh: () => Promise<void>
  onProbe: (modelID: string) => Promise<ProbeResponse>
  onProbeAll: () => Promise<ProbeBatchResult>
  /** Non-null while a batch is running, so the button can show progress. */
  probeAllProgress: { done: number; total: number } | null
  onCancelProbeAll: () => void
  onValidate: (modelID: string) => Promise<ValidationResponse>
  onTest: (modelID: string, upstreams: string[], exclude: string[]) => Promise<TestResponse>
  onUpdateConfig: (modelID: string, config: ModelConfig) => Promise<void>
  onFetchOfficial: () => Promise<OfficialResponse>
  onRemove: (modelID: string) => Promise<void>
}

export function ModelsPanel({
  data,
  onRefresh,
  onProbe,
  onProbeAll,
  probeAllProgress,
  onCancelProbeAll,
  onValidate,
  onTest,
  onUpdateConfig,
  onFetchOfficial,
  onRemove,
}: ModelsPanelProps) {
  const [filter, setFilter] = useState("")
  const [refreshing, setRefreshing] = useState(false)
  const [fetchingOfficial, setFetchingOfficial] = useState(false)

  const models = useMemo(() => {
    const query = filter.trim().toLowerCase()
    if (!query) return data.subscription
    return data.subscription.filter((model) => model.id.toLowerCase().includes(query))
  }, [data.subscription, filter])



  const refresh = async () => {
    setRefreshing(true)
    try {
      await onRefresh()
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setRefreshing(false)
    }
  }


  const probeAll = async () => {
    try {
      const result = await onProbeAll()
      if (result.aborted) {
        toast.info(`批量探测已停止，已完成 ${result.ok} 个模型`)
      } else if (result.failed > 0) {
        toast.warning(`批量探测完成：成功 ${result.ok} 个，失败 ${result.failed} 个`)
      } else {
        toast.success(`批量探测完成：${result.ok} 个模型`)
      }
    } catch (error) {
      toast.error(errorMessage(error))
    }
  }



  const fetchOfficial = async () => {
    setFetchingOfficial(true)
    try {
      const result = await onFetchOfficial()
      // The API always returns an array, but an older or third-party backend
      // may still answer null; never crash the panel on it.
      const added = result.added?.length ?? 0
      if (added) {
        toast.success(`已新增 ${added} 个模型`)
      } else {
        toast.success(`清单已是最新，共 ${result.total} 个模型`)
      }
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setFetchingOfficial(false)
    }
  }








  return (
    <Card>
      <CardHeader>
        <CardTitle>订阅模型与上游优先级</CardTitle>
        <CardDescription>
          {data.subscription.length} 个订阅模型 · {data.catalogCount} 个目录模型
        </CardDescription>
        <CardAction className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={refresh} disabled={refreshing}>
            <RefreshCw className={refreshing ? "animate-spin" : ""} data-icon="inline-start" />
            刷新
          </Button>
          <Button variant="outline" size="sm" onClick={probeAll} disabled={probeAllProgress !== null}>
            <Radar
              className={probeAllProgress ? "animate-pulse" : ""}
              data-icon="inline-start"
            />
            {probeAllProgress
              ? `批量探测 ${probeAllProgress.done}/${probeAllProgress.total}`
              : "批量探测"}
          </Button>
          {probeAllProgress && (
            <Button variant="ghost" size="sm" onClick={onCancelProbeAll}>
              <X data-icon="inline-start" />
              停止
            </Button>
          )}
          <Button size="sm" onClick={fetchOfficial} disabled={fetchingOfficial}>
            <Download className={fetchingOfficial ? "animate-pulse" : ""} data-icon="inline-start" />
            拉取官方模型
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="relative max-w-sm">
          <Search className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
          <Input
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder="筛选模型"
            className="pl-8"
          />
        </div>

        {data.officialFetch && (
          <div className="text-muted-foreground flex flex-wrap items-center gap-2 text-sm">
            <Sparkles className="size-3.5" />
            官方清单 {data.officialFetch.found} 个，当前 {data.officialFetch.total} 个
            {data.officialFetch.sources.length > 0 && (
              <Badge variant="outline">{data.officialFetch.sources.join(" + ")}</Badge>
            )}
          </div>
        )}

        <div className="overflow-hidden rounded-lg ring-1 ring-foreground/10">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10" />
                <TableHead className="min-w-72">模型</TableHead>
                <TableHead className="w-28">线路</TableHead>
                <TableHead className="w-20 text-right">渠道数</TableHead>
                <TableHead className="w-44">最近命中</TableHead>
                <TableHead className="min-w-48">优先级 / 排除</TableHead>
                <TableHead className="w-36 text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {models.map((model) => (
                <ModelRow
                  key={model.id}
                  model={model}
                  onProbe={onProbe}
                  onValidate={onValidate}
                  onTest={onTest}
                  onUpdateConfig={onUpdateConfig}
                  onRemove={onRemove}
                  onRefresh={onRefresh}
                />
              ))}
              {!models.length && (
                <TableRow>
                  <TableCell colSpan={7} className="text-muted-foreground h-28 text-center">
                    没有匹配的模型
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      </CardContent>
    </Card>
  )
}
