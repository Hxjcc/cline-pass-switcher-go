import { useState, type ReactNode } from "react"
import { FlaskConical, Play, RotateCcw, TriangleAlert } from "lucide-react"
import { toast } from "sonner"

import { EmptyState, Field } from "@/components/console-kit"
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
import { chipClass } from "@/lib/console-styles"
import { cn } from "@/lib/utils"
import {
  isPinDisabled,
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
  const pinDisabled = isPinDisabled(selected?.meta)
  if (pinDisabled && upstream !== "auto") {
    setUpstream("auto")
  }
  const config = normalizeModelConfig(selected?.config)
  const upstreams = [...new Set(selected?.meta?.upstreams ?? [])].sort((left, right) => {
    const leftState = selected?.meta?.upstreamStatus?.[left]?.status ?? "unknown"
    const rightState = selected?.meta?.upstreamStatus?.[right]?.status ?? "unknown"
    return upstreamRank[leftState as UpstreamState] - upstreamRank[rightState as UpstreamState]
  })
  // Base UI renders the raw value in the trigger unless it knows the labels.
  const upstreamItems: Record<string, string> = { auto: "沿用当前配置" }
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
        pinDisabled || upstream === "auto" ? config.upstreams : [upstream],
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
    <div className="grid items-start gap-4 xl:grid-cols-[400px_minmax(0,1fr)]">
      <Card>
        <CardHeader>
          <CardTitle>测试台</CardTitle>
          <CardDescription>向指定模型发送一条最小请求，确认实际命中的上游渠道。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <Field label="模型" labelId="test-model-label">
            <Select
              value={modelID}
              onValueChange={(value) => {
                if (value) setModelID(value)
              }}
            >
              <SelectTrigger className="w-full" aria-labelledby="test-model-label">
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
          </Field>

          <Field label="目标渠道" labelId="test-upstream-label">
            <Select
              value={upstream}
              items={upstreamItems}
              onValueChange={(value) => {
                if (value) setUpstream(value)
              }}
            >
              <SelectTrigger className="w-full" disabled={pinDisabled} aria-labelledby="test-upstream-label">
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
          </Field>

          {pinDisabled && (
            <Alert>
              <TriangleAlert />
              <AlertTitle>当前模型不可指定目标渠道</AlertTitle>
              <AlertDescription>
                网关已忽略上游偏好，测试将沿用当前配置，实际渠道由 Cline 网关决定。
              </AlertDescription>
            </Alert>
          )}

          {config.upstreams.length > 0 && upstream === "auto" && (
            <div className="bg-muted/30 space-y-2 rounded-lg border px-3 py-2.5">
              <div className="text-muted-foreground text-xs">将按以下顺序尝试渠道</div>
              <div className="flex flex-wrap gap-1.5">
                {config.upstreams.map((value, index) => (
                  <Badge key={value} variant="secondary" className={cn(chipClass, "font-mono")}>
                    {index + 1}. {value}
                  </Badge>
                ))}
              </div>
            </div>
          )}

          <div className="flex gap-2">
            <Button className="flex-1" onClick={run} disabled={running || !modelID}>
              {running ? (
                <RotateCcw className="animate-spin" data-icon="inline-start" />
              ) : (
                <Play data-icon="inline-start" />
              )}
              {running ? "请求中" : "发送测试"}
            </Button>
            <Button variant="outline" onClick={() => setResult(null)} disabled={running || !result}>
              清除结果
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>响应与路由结果</CardTitle>
          <CardDescription>上游出错时，网关会依次尝试下一个候选渠道。</CardDescription>
        </CardHeader>
        <CardContent>
          {!result ? (
            <EmptyState
              icon={FlaskConical}
              title="尚未发送测试请求"
              description="选择模型与目标渠道后点击「发送测试」，结果将显示在此处。"
              className="min-h-64 justify-center"
            />
          ) : (
            <div className="space-y-5">
              <Alert variant={result.ok ? "default" : "destructive"}>
                <FlaskConical />
                <AlertTitle>
                  {result.ok ? `实际命中 ${providerLabel(result.actual) || "未知渠道"}` : "测试请求失败"}
                </AlertTitle>
                <AlertDescription>
                  {result.ok
                    ? `耗时 ${shortDuration(result.ms)} · 线路 ${pipelineLabel(result.pipeline)} · 背后模型 ${result.canonicalSlug || "—"}`
                    : result.error}
                </AlertDescription>
              </Alert>

              {result.ok && (
                <>
                  <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                    <ResultItem label="目标渠道" mono>
                      {result.targets?.length ? result.targets.join(" → ") : "自动"}
                    </ResultItem>
                    <ResultItem label="实际上游">
                      {result.actual ? <ProviderName slug={result.actual} /> : "未知"}
                    </ResultItem>
                    <ResultItem label="账号">{result.account || "—"}</ResultItem>
                    <ResultItem label="耗时" mono>
                      {shortDuration(result.ms)}
                    </ResultItem>
                  </div>
                  <Separator />
                  <ResultItem label="尝试序列">
                    <TraceList trace={result.trace} />
                  </ResultItem>
                  <ResultItem label="模型回复">
                    <div className="bg-muted/40 rounded-lg border p-3 font-mono text-xs leading-5 whitespace-pre-wrap">
                      {result.content || "（空）"}
                    </div>
                  </ResultItem>
                </>
              )}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function ResultItem({ label, mono, children }: { label: string; mono?: boolean; children: ReactNode }) {
  return (
    <div className="min-w-0 space-y-1.5">
      <div className="text-muted-foreground text-xs">{label}</div>
      <div className={cn("text-sm", mono && "font-mono")}>{children}</div>
    </div>
  )
}
