import { Fragment, useMemo, useState, type ReactNode } from "react"
import {
  ArrowDown,
  ArrowUp,
  Ban,
  Brain,
  ChevronDown,
  ChevronRight,
  CircleDot,
  Download,
  FlaskConical,
  ImageIcon,
  ListRestart,
  Radar,
  RefreshCw,
  Save,
  Search,
  ShieldCheck,
  Sparkles,
  Timer,
  Trash2,
} from "lucide-react"
import { toast } from "sonner"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { ProviderName } from "@/components/provider-name"
import { StatusDot } from "@/components/status-dot"
import { TraceList } from "@/components/trace-list"
import { errorMessage } from "@/lib/api"
import {
  formatTime,
  normalizeModelConfig,
  pipelineHint,
  pipelineLabel,
  providerLabel,
  shortDuration,
  upstreamRank,
} from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  ModelConfig,
  ModelsResponse,
  OfficialResponse,
  ProbeResponse,
  SubscriptionModel,
  TestResponse,
  UpstreamState,
} from "@/types"

interface ModelsPanelProps {
  data: ModelsResponse
  onRefresh: () => Promise<void>
  onProbe: (modelID: string) => Promise<ProbeResponse>
  onProbeAll: () => Promise<void>
  onValidate: (modelID: string) => Promise<void>
  onTest: (modelID: string, upstreams: string[], exclude: string[]) => Promise<TestResponse>
  onUpdateConfig: (modelID: string, config: ModelConfig) => Promise<void>
  onFetchOfficial: () => Promise<OfficialResponse>
  onRemove: (modelID: string) => Promise<void>
}

function getStatus(state?: UpstreamState): UpstreamState {
  return state ?? "unknown"
}

// One chip spec for every tag in the table: capability, pipeline, last hit,
// priority list. Keeps rows at two text lines.
const chipClass = "h-5 px-1.5 py-0 text-2xs"

function RowAction({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string
  disabled?: boolean
  onClick: () => void
  children: ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            className="size-7 rounded-[5px]"
            disabled={disabled}
            onClick={onClick}
            aria-label={label}
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

// Base UI renders the raw value in the trigger unless it knows the labels.
const pinModeItems: Record<ModelConfig["pinMode"], string> = {
  strict: "严格钉住",
  preferred: "优先 + 回退",
}

const sortItems: Record<string, string> = {
  auto: "网关默认",
  cost: "最低成本",
  ttft: "最快首字",
  tps: "最高吞吐",
}

function orderedUpstreams(model: SubscriptionModel) {
  const config = normalizeModelConfig(model.config)
  const meta = model.meta
  const all = new Set<string>([
    ...(meta?.upstreams ?? []),
    ...config.upstreams,
    ...config.exclude,
    ...Object.keys(meta?.upstreamDetail ?? {}),
  ])
  const selected = config.upstreams.filter((value) => all.has(value))
  const rest = [...all]
    .filter((value) => !selected.includes(value))
    .sort((left, right) => {
      const leftState = getStatus(meta?.upstreamStatus?.[left]?.status)
      const rightState = getStatus(meta?.upstreamStatus?.[right]?.status)
      return upstreamRank[leftState] - upstreamRank[rightState] || left.localeCompare(right)
    })
  return [...selected, ...rest]
}

export function ModelsPanel({
  data,
  onRefresh,
  onProbe,
  onProbeAll,
  onValidate,
  onTest,
  onUpdateConfig,
  onFetchOfficial,
  onRemove,
}: ModelsPanelProps) {
  const [expanded, setExpanded] = useState<string | null>(null)
  const [filter, setFilter] = useState("")
  const [refreshing, setRefreshing] = useState(false)
  const [fetchingOfficial, setFetchingOfficial] = useState(false)
  const [probingAll, setProbingAll] = useState(false)
  const [busy, setBusy] = useState<Record<string, string>>({})
  const [testResults, setTestResults] = useState<Record<string, TestResponse>>({})
  const [removing, setRemoving] = useState<string | null>(null)

  const models = useMemo(() => {
    const query = filter.trim().toLowerCase()
    if (!query) return data.subscription
    return data.subscription.filter((model) => model.id.toLowerCase().includes(query))
  }, [data.subscription, filter])

  const setModelBusy = (modelID: string, action: string) => {
    setBusy((current) => ({ ...current, [modelID]: action }))
  }

  const clearModelBusy = (modelID: string) => {
    setBusy((current) => {
      const next = { ...current }
      delete next[modelID]
      return next
    })
  }

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

  const probe = async (modelID: string) => {
    setModelBusy(modelID, "probe")
    try {
      const result = await onProbe(modelID)
      toast.success(`探测完成：${result.upstreams?.length ?? 0} 个渠道`)
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      clearModelBusy(modelID)
    }
  }

  const probeAll = async () => {
    setProbingAll(true)
    try {
      await onProbeAll()
      toast.success("批量探测已完成")
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setProbingAll(false)
    }
  }

  const validate = async (modelID: string) => {
    setModelBusy(modelID, "validate")
    try {
      await onValidate(modelID)
      toast.success("渠道校验已完成")
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      clearModelBusy(modelID)
    }
  }

  const runTest = async (model: SubscriptionModel) => {
    const config = normalizeModelConfig(model.config)
    setModelBusy(model.id, "test")
    try {
      const result = await onTest(model.id, config.upstreams, config.exclude)
      setTestResults((current) => ({ ...current, [model.id]: result }))
      if (!result.ok) {
        toast.error(result.error || "测试请求失败")
      } else if (!config.upstreams.length || result.actual === config.upstreams[0]) {
        toast.success(`实际命中 ${providerLabel(result.actual) || "未知渠道"}`)
      } else {
        toast.warning(`实际命中 ${providerLabel(result.actual) || "未知渠道"}，未命中首选`)
      }
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      clearModelBusy(model.id)
    }
  }

  const fetchOfficial = async () => {
    setFetchingOfficial(true)
    try {
      const result = await onFetchOfficial()
      if (result.added.length) {
        toast.success(`已新增 ${result.added.length} 个模型`)
      } else {
        toast.success(`清单已是最新，共 ${result.total} 个模型`)
      }
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setFetchingOfficial(false)
    }
  }

  const updateConfig = async (modelID: string, config: ModelConfig) => {
    setModelBusy(modelID, "save")
    try {
      await onUpdateConfig(modelID, config)
    } catch (error) {
      toast.error(errorMessage(error))
      await onRefresh().catch(() => undefined)
    } finally {
      clearModelBusy(modelID)
    }
  }

  const togglePriority = (model: SubscriptionModel, upstreamSlug: string) => {
    const config = normalizeModelConfig(model.config)
    const selected = config.upstreams.includes(upstreamSlug)
    const upstreams = selected
      ? config.upstreams.filter((value) => value !== upstreamSlug)
      : [...config.upstreams, upstreamSlug]
    const exclude = config.exclude.filter((value) => value !== upstreamSlug)
    void updateConfig(model.id, { ...config, upstreams, exclude })
  }

  const toggleExclude = (model: SubscriptionModel, upstreamSlug: string) => {
    const config = normalizeModelConfig(model.config)
    const excluded = config.exclude.includes(upstreamSlug)
    const exclude = excluded
      ? config.exclude.filter((value) => value !== upstreamSlug)
      : [...config.exclude, upstreamSlug]
    const upstreams = config.upstreams.filter((value) => value !== upstreamSlug)
    void updateConfig(model.id, { ...config, upstreams, exclude })
  }

  const movePriority = (model: SubscriptionModel, upstreamSlug: string, offset: number) => {
    const config = normalizeModelConfig(model.config)
    const upstreams = [...config.upstreams]
    const index = upstreams.indexOf(upstreamSlug)
    const nextIndex = index + offset
    if (index < 0 || nextIndex < 0 || nextIndex >= upstreams.length) return
    ;[upstreams[index], upstreams[nextIndex]] = [upstreams[nextIndex], upstreams[index]]
    void updateConfig(model.id, { ...config, upstreams })
  }

  const bulkAll = (model: SubscriptionModel) => {
    const config = normalizeModelConfig(model.config)
    const upstreams = orderedUpstreams(model).filter((value) => !config.exclude.includes(value))
    void updateConfig(model.id, { ...config, upstreams })
  }

  const clearConfig = (model: SubscriptionModel) => {
    const config = normalizeModelConfig(model.config)
    void updateConfig(model.id, { ...config, upstreams: [], exclude: [] })
  }

  const remove = async (modelID: string) => {
    try {
      await onRemove(modelID)
      if (expanded === modelID) setExpanded(null)
      toast.success(`已移除 ${modelID}`)
    } catch (error) {
      toast.error(errorMessage(error))
      throw error
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
          <Button variant="outline" size="sm" onClick={probeAll} disabled={probingAll}>
            <Radar className={probingAll ? "animate-pulse" : ""} data-icon="inline-start" />
            {probingAll ? "批量探测中" : "批量探测"}
          </Button>
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
              {models.map((model) => {
                const config = normalizeModelConfig(model.config)
                const isExpanded = expanded === model.id
                const action = busy[model.id]
                const result = testResults[model.id]
                return (
                  <Fragment key={model.id}>
                    <TableRow data-state={isExpanded ? "selected" : undefined}>
                      <TableCell>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          onClick={() => setExpanded(isExpanded ? null : model.id)}
                          aria-label={isExpanded ? "收起模型" : "展开模型"}
                        >
                          {isExpanded ? <ChevronDown /> : <ChevronRight />}
                        </Button>
                      </TableCell>
                      <TableCell>
                        <div className="font-mono text-xs leading-5 font-medium">{model.id}</div>
                        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1">
                          <span className="text-muted-foreground font-mono text-2xs">
                            {model.meta?.canonicalSlug || "尚未读取背后模型"}
                          </span>
                          {model.meta?.reasoning && (
                            <Badge variant="secondary" className={chipClass}>
                              <Brain data-icon="inline-start" />
                              思考
                              {model.meta.reasoningEfforts?.length ? (
                                <span className="font-mono tabular-nums">
                                  {model.meta.reasoningEfforts.join("/")}
                                </span>
                              ) : null}
                            </Badge>
                          )}
                          {model.meta?.inputModalities?.includes("image") && (
                            <Badge variant="outline" className={chipClass}>
                              <ImageIcon data-icon="inline-start" />
                              视觉
                            </Badge>
                          )}
                        </div>
                      </TableCell>
                      <TableCell>
                        <Tooltip>
                          <TooltipTrigger
                            render={
                              <Badge
                                variant={model.meta?.pipeline ? "secondary" : "outline"}
                                className={cn(chipClass, "cursor-help")}
                              />
                            }
                          >
                            {model.meta?.pipeline || model.meta?.probedAt
                              ? pipelineLabel(model.meta?.pipeline)
                              : "未探测"}
                          </TooltipTrigger>
                          <TooltipContent className="max-w-72">
                            {model.meta?.pipeline || model.meta?.probedAt
                              ? pipelineHint(model.meta?.pipeline)
                              : "执行探测后才能识别这个模型走的线路。"}
                          </TooltipContent>
                        </Tooltip>
                      </TableCell>
                      <TableCell className="text-right font-mono tabular-nums">
                        {model.meta?.upstreams?.length ?? 0}
                      </TableCell>
                      <TableCell>
                        {model.meta?.lastProvider ? (
                          <Badge
                            variant="outline"
                            className={cn(chipClass, "bg-muted/40 gap-1.5 font-normal")}
                          >
                            <ProviderName slug={model.meta.lastProvider} className="font-medium" />
                            <span aria-hidden className="bg-border h-3 w-px" />
                            <Timer className="text-muted-foreground" />
                            <span className="text-muted-foreground font-mono tabular-nums">
                              {shortDuration(model.meta.lastMs)}
                            </span>
                          </Badge>
                        ) : (
                          <span className="text-muted-foreground">尚无请求</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap items-center gap-1">
                          {config.upstreams.slice(0, 3).map((upstreamSlug, index) => (
                            <Badge
                              key={upstreamSlug}
                              variant="secondary"
                              className={cn(chipClass, "font-mono")}
                            >
                              {index + 1}. {upstreamSlug}
                            </Badge>
                          ))}
                          {config.upstreams.length > 3 && (
                            <Badge variant="outline" className={chipClass}>
                              +{config.upstreams.length - 3}
                            </Badge>
                          )}
                          {config.exclude.slice(0, 2).map((upstreamSlug) => (
                            <Badge
                              key={upstreamSlug}
                              variant="destructive"
                              className={cn(chipClass, "font-mono line-through")}
                            >
                              {upstreamSlug}
                            </Badge>
                          ))}
                          {config.exclude.length > 2 && (
                            <Badge variant="destructive" className={chipClass}>
                              +{config.exclude.length - 2}
                            </Badge>
                          )}
                          {!config.upstreams.length && !config.exclude.length && (
                            <span className="text-muted-foreground">自动</span>
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="bg-background inline-flex items-center rounded-md border p-0.5 align-middle">
                          <RowAction
                            label="探测渠道"
                            disabled={action === "probe"}
                            onClick={() => probe(model.id)}
                          >
                            <Radar className={action === "probe" ? "animate-pulse" : ""} />
                          </RowAction>
                          <RowAction
                            label="发送测试请求"
                            disabled={action === "test"}
                            onClick={() => runTest(model)}
                          >
                            <FlaskConical className={action === "test" ? "animate-pulse" : ""} />
                          </RowAction>
                          <RowAction
                            label="校验全部渠道"
                            disabled={action === "validate" || !model.meta?.upstreams?.length}
                            onClick={() => validate(model.id)}
                          >
                            <ShieldCheck
                              className={action === "validate" ? "animate-pulse" : ""}
                            />
                          </RowAction>
                          <RowAction
                            label="移除模型"
                            disabled={Boolean(action)}
                            onClick={() => setRemoving(model.id)}
                          >
                            <Trash2 className="text-muted-foreground" />
                          </RowAction>
                        </div>
                      </TableCell>
                    </TableRow>
                    {isExpanded && (
                      <TableRow>
                        <TableCell colSpan={7} className="bg-muted/25 p-0 whitespace-normal">
                          <div className="space-y-4 p-4">
                            <div className="flex flex-wrap items-center gap-2">
                              <div className="flex items-center gap-2">
                                <span className="text-muted-foreground text-sm">模式</span>
                                <Select
                                  value={config.pinMode}
                                  items={pinModeItems}
                                  onValueChange={(value) =>
                                    void updateConfig(model.id, {
                                      ...config,
                                      pinMode: value as ModelConfig["pinMode"],
                                    })
                                  }
                                >
                                  <SelectTrigger size="sm" className="w-40">
                                    <SelectValue />
                                  </SelectTrigger>
                                  <SelectContent>
                                    {Object.entries(pinModeItems).map(([value, label]) => (
                                      <SelectItem key={value} value={value}>
                                        {label}
                                      </SelectItem>
                                    ))}
                                  </SelectContent>
                                </Select>
                              </div>
                              <div className="flex items-center gap-2">
                                <span className="text-muted-foreground text-sm">排序</span>
                                <Select
                                  value={config.sort ?? "auto"}
                                  items={sortItems}
                                  onValueChange={(value) =>
                                    void updateConfig(model.id, {
                                      ...config,
                                      sort: value === "auto" ? null : (value as ModelConfig["sort"]),
                                    })
                                  }
                                >
                                  <SelectTrigger size="sm" className="w-40">
                                    <SelectValue />
                                  </SelectTrigger>
                                  <SelectContent>
                                    {Object.entries(sortItems).map(([value, label]) => (
                                      <SelectItem key={value} value={value}>
                                        {label}
                                      </SelectItem>
                                    ))}
                                  </SelectContent>
                                </Select>
                              </div>
                              <Button variant="outline" size="sm" onClick={() => bulkAll(model)}>
                                <ListRestart data-icon="inline-start" />
                                全部加入
                              </Button>
                              <Button variant="ghost" size="sm" onClick={() => clearConfig(model)}>
                                恢复自动
                              </Button>
                              {action === "save" && (
                                <span className="text-muted-foreground flex items-center gap-1 text-sm">
                                  <Save className="size-3.5 animate-pulse" />
                                  保存中
                                </span>
                              )}
                            </div>

                            {result && (
                              <Alert variant={result.ok ? "default" : "destructive"}>
                                <CircleDot />
                                <AlertTitle>
                                  {result.ok
                                    ? `实际命中 ${providerLabel(result.actual) || "未知渠道"}`
                                    : "测试请求失败"}
                                </AlertTitle>
                                <AlertDescription className="space-y-2">
                                  <div>
                                    {result.ok
                                      ? `耗时 ${shortDuration(result.ms)} · 账号 ${result.account || "—"} · 背后模型 ${result.canonicalSlug || "—"}`
                                      : result.error}
                                  </div>
                                  <TraceList trace={result.trace} />
                                </AlertDescription>
                              </Alert>
                            )}

                            {!orderedUpstreams(model).length ? (
                              <div className="text-muted-foreground rounded-lg border border-dashed p-6 text-center text-sm">
                                尚无渠道数据，请先执行探测。
                              </div>
                            ) : (
                              <div className="bg-card max-h-[520px] overflow-y-auto rounded-lg border">
                                {orderedUpstreams(model).map((upstreamSlug) => {
                                  const status = model.meta?.upstreamStatus?.[upstreamSlug]
                                  const detail = model.meta?.upstreamDetail?.[upstreamSlug]
                                  const priorityIndex = config.upstreams.indexOf(upstreamSlug)
                                  const pinned = priorityIndex >= 0
                                  const excluded = config.exclude.includes(upstreamSlug)
                                  return (
                                    <div
                                      key={upstreamSlug}
                                      className={cn(
                                        "grid grid-cols-[32px_minmax(0,1fr)_auto] items-center gap-3 border-b px-3 py-2 last:border-b-0",
                                        pinned && "bg-primary/[0.04] dark:bg-primary/[0.07]",
                                        excluded && "bg-destructive/[0.04] dark:bg-destructive/[0.08]",
                                      )}
                                    >
                                      <div
                                        className={cn(
                                          "flex size-7 items-center justify-center rounded-md border font-mono text-xs tabular-nums",
                                          pinned
                                            ? "border-primary/30 bg-primary/10 text-primary font-semibold"
                                            : "bg-background text-muted-foreground",
                                        )}
                                      >
                                        {pinned ? priorityIndex + 1 : "—"}
                                      </div>
                                      <div className="min-w-0">
                                        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                                          <span
                                            className={cn(
                                              "truncate font-mono text-xs font-medium",
                                              excluded && "text-muted-foreground line-through",
                                            )}
                                          >
                                            {upstreamSlug}
                                          </span>
                                          <StatusDot status={status} />
                                          {excluded && (
                                            <Badge variant="destructive" className={chipClass}>
                                              已排除
                                            </Badge>
                                          )}
                                        </div>
                                        <div className="text-muted-foreground mt-0.5 truncate text-2xs">
                                          {[
                                            detail
                                              ? `${detail.name}${detail.endpoints > 1 ? ` · ${detail.endpoints} endpoints` : ""}`
                                              : "",
                                            status?.checkedAt
                                              ? `校验于 ${formatTime(status.checkedAt)}`
                                              : "",
                                          ]
                                            .filter(Boolean)
                                            .join(" · ") || "尚未校验，暂无渠道详情"}
                                        </div>
                                      </div>
                                      <div className="flex items-center gap-1">
                                        {priorityIndex >= 0 && (
                                          <>
                                            <Tooltip>
                                              <TooltipTrigger
                                                render={
                                                  <Button
                                                    variant="ghost"
                                                    size="icon-sm"
                                                    disabled={
                                                      priorityIndex === 0 || action === "save"
                                                    }
                                                    onClick={() =>
                                                      movePriority(model, upstreamSlug, -1)
                                                    }
                                                    aria-label="提高优先级"
                                                  />
                                                }
                                              >
                                                <ArrowUp />
                                              </TooltipTrigger>
                                              <TooltipContent>提高优先级</TooltipContent>
                                            </Tooltip>
                                            <Tooltip>
                                              <TooltipTrigger
                                                render={
                                                  <Button
                                                    variant="ghost"
                                                    size="icon-sm"
                                                    disabled={
                                                      priorityIndex ===
                                                        config.upstreams.length - 1 ||
                                                      action === "save"
                                                    }
                                                    onClick={() =>
                                                      movePriority(model, upstreamSlug, 1)
                                                    }
                                                    aria-label="降低优先级"
                                                  />
                                                }
                                              >
                                                <ArrowDown />
                                              </TooltipTrigger>
                                              <TooltipContent>降低优先级</TooltipContent>
                                            </Tooltip>
                                          </>
                                        )}
                                        <Button
                                          variant={pinned ? "ghost" : "outline"}
                                          size="sm"
                                          className="w-20"
                                          disabled={action === "save"}
                                          onClick={() => togglePriority(model, upstreamSlug)}
                                        >
                                          {pinned ? "取消优先" : "设为优先"}
                                        </Button>
                                        <Tooltip>
                                          <TooltipTrigger
                                            render={
                                              <Button
                                                variant="ghost"
                                                size="icon-sm"
                                                className={
                                                  excluded
                                                    ? "text-destructive hover:text-destructive"
                                                    : "text-muted-foreground"
                                                }
                                                disabled={action === "save"}
                                                onClick={() => toggleExclude(model, upstreamSlug)}
                                                aria-label={excluded ? "取消排除" : "排除渠道"}
                                              />
                                            }
                                          >
                                            <Ban />
                                          </TooltipTrigger>
                                          <TooltipContent>
                                            {excluded ? "取消排除" : "排除渠道"}
                                          </TooltipContent>
                                        </Tooltip>
                                      </div>
                                    </div>
                                  )
                                })}
                              </div>
                            )}
                          </div>
                        </TableCell>
                      </TableRow>
                    )}
                  </Fragment>
                )
              })}
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
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => {
          if (!open) setRemoving(null)
        }}
        title="移除模型"
        description={`将 ${removing ?? ""} 从订阅列表移除，并删除它的钉住配置和探测数据。\n拉取官方模型不会再把它加回来；通过代理再次调用该模型时会重新出现。`}
        confirmLabel="移除"
        destructive
        onConfirm={() => (removing ? remove(removing) : undefined)}
      />
    </Card>
  )
}
