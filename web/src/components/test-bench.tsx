import { useState } from "react"
import { FlaskConical, Play, RotateCcw } from "lucide-react"
import { toast } from "sonner"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import { ProviderName } from "@/components/provider-name"
import { TraceList } from "@/components/trace-list"
import { errorMessage } from "@/lib/api"
import {
  normalizeModelConfig,
  pipelineLabel,
  providerLabel,
  shortDuration,
  upstreamLabels,
  upstreamRank,
} from "@/lib/format"
import type { SubscriptionModel, TestResponse, UpstreamState } from "@/types"

interface TestBenchProps {
  models: SubscriptionModel[]
  onTest: (modelID: string, upstreams: string[], exclude: string[]) => Promise<TestResponse>
}

export function TestBench({ models, onTest }: TestBenchProps) {
  const [modelID, setModelID] = useState(models[0]?.id ?? "")
  const [upstream, setUpstream] = useState("auto")
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<TestResponse | null>(null)
  const [previousModelID, setPreviousModelID] = useState(modelID)
  if (!models.some((model) => model.id === modelID) && modelID !== (models[0]?.id ?? "")) {
    setModelID(models[0]?.id ?? "")
  }
  if (previousModelID !== modelID) {
    setPreviousModelID(modelID)
    setUpstream("auto")
    setResult(null)
  }

  const selected = models.find((model) => model.id === modelID)
  const config = normalizeModelConfig(selected?.config)
  const upstreams = [...new Set(selected?.meta?.upstreams ?? [])].sort((left, right) => {
    const leftState = selected?.meta?.upstreamStatus?.[left]?.status ?? "unknown"
    const rightState = selected?.meta?.upstreamStatus?.[right]?.status ?? "unknown"
    return upstreamRank[leftState as UpstreamState] - upstreamRank[rightState as UpstreamState]
  })
  // Base UI renders the raw value in the trigger unless it knows the labels.
  const upstreamItems: Record<string, string> = { auto: "跟随当前配置" }
  for (const value of upstreams) {
    const state = (selected?.meta?.upstreamStatus?.[value]?.status ?? "unknown") as UpstreamState
    upstreamItems[value] = `${value} · ${upstreamLabels[state]}`
  }

  const run = async () => {
    if (!modelID) {
      toast.error("没有可测试的模型")
      return
    }
    setRunning(true)
    try {
      // The API treats an explicit empty list as "drop the pins", so the
      // default must forward the saved pins for the hint below to hold true.
      const response = await onTest(
        modelID,
        upstream === "auto" ? config.upstreams : [upstream],
        config.exclude,
      )
      setResult(response)
      if (response.ok) {
        toast.success(`实际命中 ${providerLabel(response.actual) || "未知渠道"}`)
      } else {
        toast.error(response.error || "测试请求失败")
      }
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className="grid items-start gap-4 xl:grid-cols-[420px_minmax(0,1fr)]">
      <Card>
        <CardHeader>
          <CardTitle>测试台</CardTitle>
          <CardDescription>发送一条最小真实请求，检查上游是否被网关采纳。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-5">
          <div className="space-y-2">
            <label className="text-sm font-medium">模型</label>
            <Select
              value={modelID}
              onValueChange={(value) => {
                if (value) setModelID(value)
              }}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {models.map((model) => (
                  <SelectItem key={model.id} value={model.id}>
                    {model.id}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-2">
            <label className="text-sm font-medium">目标渠道</label>
            <Select
              value={upstream}
              items={upstreamItems}
              onValueChange={(value) => {
                if (value) setUpstream(value)
              }}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {Object.entries(upstreamItems).map(([value, label]) => (
                  <SelectItem key={value} value={value}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex gap-2">
            <Button className="flex-1" onClick={run} disabled={running || !modelID}>
              {running ? (
                <RotateCcw className="animate-spin" data-icon="inline-start" />
              ) : (
                <Play data-icon="inline-start" />
              )}
              {running ? "请求中" : "发送测试"}
            </Button>
            <Button variant="outline" onClick={() => setResult(null)} disabled={running}>
              清除结果
            </Button>
          </div>

          {config.upstreams.length > 0 && upstream === "auto" && (
            <div className="bg-muted/40 rounded-lg border p-3 text-sm">
              <div className="text-muted-foreground mb-2">当前配置会按以下顺序尝试</div>
              <div className="flex flex-wrap gap-1.5">
                {config.upstreams.map((value, index) => (
                  <Badge key={value} variant="secondary" className="font-mono">
                    {index + 1}. {value}
                  </Badge>
                ))}
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>响应与路由结果</CardTitle>
          <CardDescription>上游错误会继续尝试下一个候选渠道。</CardDescription>
        </CardHeader>
        <CardContent>
          {!result ? (
            <div className="text-muted-foreground flex min-h-64 flex-col items-center justify-center rounded-lg border border-dashed">
              <FlaskConical className="mb-3 size-6" />
              <p className="text-sm">尚未发送测试请求</p>
            </div>
          ) : (
            <div className="space-y-5">
              <Alert variant={result.ok ? "default" : "destructive"}>
                <FlaskConical />
                <AlertTitle>
                  {result.ok ? `命中 ${providerLabel(result.actual) || "未知渠道"}` : "请求失败"}
                </AlertTitle>
                <AlertDescription>
                  {result.ok
                    ? `耗时 ${shortDuration(result.ms)} · 线路 ${pipelineLabel(result.pipeline)} · 背后模型 ${result.canonicalSlug || "—"}`
                    : result.error}
                </AlertDescription>
              </Alert>

              {result.ok && (
                <>
                  <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                    <div>
                      <div className="text-muted-foreground text-xs">目标渠道</div>
                      <div className="mt-1 font-mono text-sm">
                        {result.targets?.length ? result.targets.join(" → ") : "自动"}
                      </div>
                    </div>
                    <div>
                      <div className="text-muted-foreground text-xs">实际上游</div>
                      <div className="mt-1 text-sm">
                        {result.actual ? <ProviderName slug={result.actual} /> : "未知"}
                      </div>
                    </div>
                    <div>
                      <div className="text-muted-foreground text-xs">账号</div>
                      <div className="mt-1 text-sm">{result.account || "—"}</div>
                    </div>
                    <div>
                      <div className="text-muted-foreground text-xs">耗时</div>
                      <div className="mt-1 font-mono text-sm">{shortDuration(result.ms)}</div>
                    </div>
                  </div>
                  <Separator />
                  <div>
                    <div className="text-muted-foreground mb-2 text-xs">尝试序列</div>
                    <TraceList trace={result.trace} />
                  </div>
                  <div>
                    <div className="text-muted-foreground mb-2 text-xs">模型回复</div>
                    <div className="bg-muted/50 rounded-lg border p-3 font-mono text-xs leading-5 whitespace-pre-wrap">
                      {result.content || "(空)"}
                    </div>
                  </div>
                </>
              )}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
