import { Fragment, useState, type ReactNode } from "react"
import {
  ArrowDown,
  ArrowUp,
  Ban,
  Brain,
  ChevronDown,
  ChevronRight,
  CircleDot,
  FlaskConical,
  ImageIcon,
  ListPlus,
  Radar,
  Save,
  ShieldCheck,
  Timer,
  Trash2,
  TriangleAlert,
} from "lucide-react"
import { toast } from "sonner"

import { Field, IconAction } from "@/components/console-kit"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { TableCell, TableRow } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { ProviderName } from "@/components/provider-name"
import { StatusDot } from "@/components/status-dot"
import { TraceList } from "@/components/trace-list"
import { errorMessage } from "@/lib/api"
import { chipClass, modelColumnClass } from "@/lib/console-styles"
import {
  formatTime,
  isPinDisabled,
  normalizeModelConfig,
  pinReasonLabel,
  pipelineHint,
  pipelineLabel,
  providerLabel,
  shortDuration,
} from "@/lib/format"
import { orderedUpstreams } from "@/lib/upstream-order"
import { cn } from "@/lib/utils"
import type {
  ModelConfig,
  ProbeResponse,
  SubscriptionModel,
  TestResponse,
  ValidationResponse,
} from "@/types"

export interface ModelRowProps {
  model: SubscriptionModel
  onProbe: (modelID: string) => Promise<ProbeResponse>
  onValidate: (modelID: string) => Promise<ValidationResponse>
  onTest: (modelID: string, upstreams: string[], exclude: string[]) => Promise<TestResponse>
  onUpdateConfig: (modelID: string, config: ModelConfig) => Promise<void>
  onRemove: (modelID: string) => Promise<void>
  onRefresh: () => Promise<void>
}

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
    <IconAction
      label={label}
      variant="ghost"
      size="icon-sm"
      className="rounded-[5px]"
      disabled={disabled}
      onClick={onClick}
    >
      {children}
    </IconAction>
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

/**
 * One row of the subscription table plus the panel it expands into. State that
 * belongs to a single model - which action it is running, its last test result,
 * whether it is expanded, whether a removal is pending - lives here, so one
 * row's activity no longer re-renders every other row.
 */
export function ModelRow({
  model,
  onProbe,
  onValidate,
  onTest,
  onUpdateConfig,
  onRemove,
  onRefresh,
}: ModelRowProps) {
  const [expanded, setExpanded] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<TestResponse | null>(null)
  const [confirmRemove, setConfirmRemove] = useState(false)

  const probe = async () => {
    setBusy("probe")
    try {
      const response = await onProbe(model.id)
      toast.success(`探测完成，发现 ${response.upstreams?.length ?? 0} 个渠道`)
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setBusy(null)
    }
  }

  const validate = async () => {
    setBusy("validate")
    try {
      const response = await onValidate(model.id)
      if (response.supported === false) {
        toast.warning(`已跳过校验：${pinReasonLabel(response.reason)}`)
      } else {
        toast.success("渠道校验已完成")
      }
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setBusy(null)
    }
  }

  const runTest = async () => {
    const config = normalizeModelConfig(model.config)
    setBusy("test")
    try {
      const response = await onTest(model.id, config.upstreams, config.exclude)
      setTestResult(response)
      if (!response.ok) {
        toast.error(response.error || "测试请求失败")
      } else if (!config.upstreams.length || response.actual === config.upstreams[0]) {
        toast.success(`实际命中 ${providerLabel(response.actual) || "未知渠道"}`)
      } else {
        const ignored = isPinDisabled(model.meta) ? "（网关已忽略钉住）" : ""
        toast.warning(`实际命中 ${providerLabel(response.actual) || "未知渠道"}，未命中首选渠道${ignored}`)
      }
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setBusy(null)
    }
  }

  const save = async (config: ModelConfig) => {
    setBusy("save")
    try {
      await onUpdateConfig(model.id, config)
    } catch (error) {
      toast.error(errorMessage(error))
      await onRefresh().catch(() => undefined)
    } finally {
      setBusy(null)
    }
  }

  const togglePriority = (upstreamSlug: string) => {
    const config = normalizeModelConfig(model.config)
    const selected = config.upstreams.includes(upstreamSlug)
    // Keep removing stale pins possible, but never add a new one while the
    // gateway ignores the preference.
    if (isPinDisabled(model.meta) && !selected) return
    const upstreams = selected
      ? config.upstreams.filter((value) => value !== upstreamSlug)
      : [...config.upstreams, upstreamSlug]
    const exclude = config.exclude.filter((value) => value !== upstreamSlug)
    void save({ ...config, upstreams, exclude })
  }

  const toggleExclude = (upstreamSlug: string) => {
    const config = normalizeModelConfig(model.config)
    const excluded = config.exclude.includes(upstreamSlug)
    // As with pins, allow cleanup of an old exclusion but not new ones.
    if (isPinDisabled(model.meta) && !excluded) return
    const exclude = excluded
      ? config.exclude.filter((value) => value !== upstreamSlug)
      : [...config.exclude, upstreamSlug]
    const upstreams = config.upstreams.filter((value) => value !== upstreamSlug)
    void save({ ...config, upstreams, exclude })
  }

  const movePriority = (upstreamSlug: string, offset: number) => {
    if (isPinDisabled(model.meta)) return
    const config = normalizeModelConfig(model.config)
    const upstreams = [...config.upstreams]
    const index = upstreams.indexOf(upstreamSlug)
    const nextIndex = index + offset
    if (index < 0 || nextIndex < 0 || nextIndex >= upstreams.length) return
    ;[upstreams[index], upstreams[nextIndex]] = [upstreams[nextIndex], upstreams[index]]
    void save({ ...config, upstreams })
  }

  const bulkAll = () => {
    if (isPinDisabled(model.meta)) return
    const config = normalizeModelConfig(model.config)
    const upstreams = orderedUpstreams(model).filter((value) => !config.exclude.includes(value))
    void save({ ...config, upstreams })
  }

  const clearConfig = () => {
    const config = normalizeModelConfig(model.config)
    void save({ ...config, upstreams: [], exclude: [] })
  }

  const removeRow = async () => {
    try {
      await onRemove(model.id)
      toast.success(`已移除 ${model.id}`)
    } catch (error) {
      toast.error(errorMessage(error))
      throw error
    }
  }

  const config = normalizeModelConfig(model.config)
  const action = busy
  const result = testResult
  const pinDisabled = isPinDisabled(model.meta)
  const pinnedFirst = config.upstreams[0]
  const actualProvider = model.meta?.lastProvider
  const pinMismatch = Boolean(
    pinnedFirst &&
      actualProvider &&
      pinnedFirst !== actualProvider &&
      (pinDisabled || config.pinMode === "strict"),
  )
  const upstreams = orderedUpstreams(model)
  const probed = Boolean(model.meta?.pipeline || model.meta?.probedAt)
  const controlId = (name: string) => `${model.id}-${name}`

  return (
    <Fragment>
      <TableRow data-state={expanded ? "selected" : undefined}>
        <TableCell>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => setExpanded(!expanded)}
            aria-label={expanded ? "收起模型" : "展开模型"}
          >
            {expanded ? <ChevronDown /> : <ChevronRight />}
          </Button>
        </TableCell>
        <TableCell>
          <div className="font-mono text-xs leading-5 font-medium">{model.id}</div>
          <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="text-muted-foreground font-mono text-2xs">
              {model.meta?.canonicalSlug || "尚未识别背后模型"}
            </span>
            {model.meta?.reasoning && (
              <Badge variant="secondary" className={chipClass}>
                <Brain data-icon="inline-start" />
                思考
                {model.meta.reasoningEfforts?.length ? (
                  <span className="font-mono tabular-nums">{model.meta.reasoningEfforts.join("/")}</span>
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
        <TableCell className={modelColumnClass.pipeline}>
          <Tooltip>
            <TooltipTrigger
              render={
                <Badge
                  variant={model.meta?.pipeline ? "secondary" : "outline"}
                  className={cn(chipClass, "cursor-help")}
                />
              }
            >
              {probed ? pipelineLabel(model.meta?.pipeline) : "未探测"}
            </TooltipTrigger>
            <TooltipContent className="max-w-72">
              {probed
                ? pipelineHint(model.meta?.pipeline, model.meta?.pinnable, model.meta?.pinReason)
                : "执行探测后即可识别该模型使用的路由线路。"}
            </TooltipContent>
          </Tooltip>
        </TableCell>
        <TableCell className={cn("text-right font-mono tabular-nums", modelColumnClass.channels)}>
          {model.meta?.upstreams?.length ?? 0}
        </TableCell>
        <TableCell className={modelColumnClass.lastHit}>
          {model.meta?.lastProvider ? (
            <div className="flex flex-wrap items-center gap-1">
              {pinMismatch && (
                <Badge
                  variant="destructive"
                  className={chipClass}
                  title={`首选 ${pinnedFirst}，最近实际命中 ${actualProvider}${
                    pinDisabled ? "；网关当前忽略钉住" : ""
                  }`}
                >
                  <TriangleAlert data-icon="inline-start" />
                  未命中
                </Badge>
              )}
              <Badge variant="outline" className={cn(chipClass, "bg-muted/40 gap-1.5 font-normal")}>
                <ProviderName slug={model.meta.lastProvider} className="font-medium" />
                <span aria-hidden className="bg-border h-3 w-px" />
                <Timer className="text-muted-foreground" />
                <span className="text-muted-foreground font-mono tabular-nums">
                  {shortDuration(model.meta.lastMs)}
                </span>
              </Badge>
            </div>
          ) : (
            <span className="text-muted-foreground text-xs">尚无请求</span>
          )}
        </TableCell>
        <TableCell>
          <div className="flex flex-wrap items-center gap-1">
            {config.upstreams.slice(0, 3).map((upstreamSlug, index) => (
              <Badge key={upstreamSlug} variant="secondary" className={cn(chipClass, "font-mono")}>
                {index + 1}. {upstreamSlug}
              </Badge>
            ))}
            {config.upstreams.length > 3 && (
              <Badge variant="outline" className={chipClass}>
                +{config.upstreams.length - 3}
              </Badge>
            )}
            {config.exclude.slice(0, 2).map((upstreamSlug) => (
              <Badge key={upstreamSlug} variant="destructive" className={cn(chipClass, "font-mono line-through")}>
                {upstreamSlug}
              </Badge>
            ))}
            {config.exclude.length > 2 && (
              <Badge variant="destructive" className={chipClass}>
                +{config.exclude.length - 2}
              </Badge>
            )}
            {!config.upstreams.length && !config.exclude.length && (
              <span className="text-muted-foreground text-xs">自动</span>
            )}
          </div>
        </TableCell>
        <TableCell className="text-right">
          <div className="bg-background inline-flex items-center rounded-md border p-0.5 align-middle">
            <RowAction label="探测渠道" disabled={action === "probe"} onClick={() => probe()}>
              <Radar className={action === "probe" ? "animate-pulse" : ""} />
            </RowAction>
            <RowAction label="发送测试请求" disabled={action === "test"} onClick={() => runTest()}>
              <FlaskConical className={action === "test" ? "animate-pulse" : ""} />
            </RowAction>
            <RowAction
              label={pinDisabled ? `不可校验：${pinReasonLabel(model.meta?.pinReason)}` : "校验全部渠道"}
              disabled={action === "validate" || !model.meta?.upstreams?.length || pinDisabled}
              onClick={() => validate()}
            >
              <ShieldCheck className={action === "validate" ? "animate-pulse" : ""} />
            </RowAction>
            <RowAction label="移除模型" disabled={Boolean(action)} onClick={() => setConfirmRemove(true)}>
              <Trash2 className="text-muted-foreground" />
            </RowAction>
          </div>
        </TableCell>
      </TableRow>

      {expanded && (
        <TableRow>
          <TableCell colSpan={7} className="bg-muted/25 p-0 whitespace-normal">
            <div className="space-y-4 p-4">
              {pinDisabled && (
                <Alert variant={model.meta?.pinReason === "single_provider" ? "default" : "destructive"}>
                  <TriangleAlert />
                  <AlertTitle>{pinReasonLabel(model.meta?.pinReason)}</AlertTitle>
                  <AlertDescription>
                    {model.meta?.pinReason === "single_provider"
                      ? "该模型仅有一个候选渠道，没有其他渠道可供钉住或校验。"
                      : "请求仍会正常发送，但实际渠道由 Cline 网关决定；以下钉住、排除、排序与校验设置均不会生效。"}
                  </AlertDescription>
                </Alert>
              )}

              <div className="flex flex-wrap items-end gap-3">
                <Field label="钉住模式" labelId={controlId("pin-mode")}>
                  <Select
                    value={config.pinMode}
                    items={pinModeItems}
                    onValueChange={(value) => void save({ ...config, pinMode: value as ModelConfig["pinMode"] })}
                  >
                    <SelectTrigger
                      size="sm"
                      className="bg-background w-36"
                      disabled={pinDisabled}
                      aria-labelledby={controlId("pin-mode")}
                    >
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
                </Field>
                <Field label="排序方式" labelId={controlId("sort")}>
                  <Select
                    value={config.sort ?? "auto"}
                    items={sortItems}
                    onValueChange={(value) =>
                      void save({ ...config, sort: value === "auto" ? null : (value as ModelConfig["sort"]) })
                    }
                  >
                    <SelectTrigger
                      size="sm"
                      className="bg-background w-36"
                      disabled={pinDisabled}
                      aria-labelledby={controlId("sort")}
                    >
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
                </Field>
                <div className="flex items-center gap-2">
                  <Button variant="outline" size="sm" disabled={pinDisabled} onClick={() => bulkAll()}>
                    <ListPlus data-icon="inline-start" />
                    全部设为优先
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => clearConfig()}>
                    恢复自动
                  </Button>
                </div>
                {action === "save" && (
                  <span className="text-muted-foreground flex h-7 items-center gap-1 text-xs">
                    <Save className="size-3.5 animate-pulse" />
                    保存中
                  </span>
                )}
              </div>

              {result && (
                <Alert variant={result.ok ? "default" : "destructive"}>
                  <CircleDot />
                  <AlertTitle>
                    {result.ok ? `实际命中 ${providerLabel(result.actual) || "未知渠道"}` : "测试请求失败"}
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

              {!upstreams.length ? (
                <div className="text-muted-foreground bg-card rounded-lg border border-dashed p-6 text-center text-sm">
                  尚无渠道数据，请先执行探测。
                </div>
              ) : (
                <div className="bg-card max-h-[520px] overflow-y-auto rounded-lg border">
                  {upstreams.map((upstreamSlug) => {
                    const status = model.meta?.upstreamStatus?.[upstreamSlug]
                    const detail = model.meta?.upstreamDetail?.[upstreamSlug]
                    const priorityIndex = config.upstreams.indexOf(upstreamSlug)
                    const pinned = priorityIndex >= 0
                    const excluded = config.exclude.includes(upstreamSlug)
                    const summary = [
                      detail ? `${detail.name}${detail.endpoints > 1 ? ` · ${detail.endpoints} 个端点` : ""}` : "",
                      status?.checkedAt ? `校验于 ${formatTime(status.checkedAt)}` : "",
                    ]
                      .filter(Boolean)
                      .join(" · ")
                    return (
                      <div
                        key={upstreamSlug}
                        className={cn(
                          "grid grid-cols-[28px_minmax(0,1fr)_auto] items-center gap-3 border-b px-3 py-2 last:border-b-0",
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
                            {summary || "尚未校验，暂无渠道详情"}
                          </div>
                        </div>
                        <div className="flex items-center gap-1">
                          {pinned && (
                            <>
                              <IconAction
                                label="提高优先级"
                                variant="ghost"
                                size="icon-sm"
                                disabled={priorityIndex === 0 || action === "save" || pinDisabled}
                                onClick={() => movePriority(upstreamSlug, -1)}
                              >
                                <ArrowUp />
                              </IconAction>
                              <IconAction
                                label="降低优先级"
                                variant="ghost"
                                size="icon-sm"
                                disabled={
                                  priorityIndex === config.upstreams.length - 1 || action === "save" || pinDisabled
                                }
                                onClick={() => movePriority(upstreamSlug, 1)}
                              >
                                <ArrowDown />
                              </IconAction>
                            </>
                          )}
                          <Button
                            variant={pinned ? "ghost" : "outline"}
                            size="sm"
                            className="w-20"
                            disabled={action === "save" || (pinDisabled && !pinned)}
                            onClick={() => togglePriority(upstreamSlug)}
                          >
                            {pinned ? "取消优先" : "设为优先"}
                          </Button>
                          <IconAction
                            label={excluded ? "取消排除" : "排除渠道"}
                            variant="ghost"
                            size="icon-sm"
                            className={excluded ? "text-destructive hover:text-destructive" : "text-muted-foreground"}
                            disabled={action === "save" || (pinDisabled && !excluded)}
                            onClick={() => toggleExclude(upstreamSlug)}
                          >
                            <Ban />
                          </IconAction>
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

      <ConfirmDialog
        open={confirmRemove}
        onOpenChange={(open) => {
          if (!open) setConfirmRemove(false)
        }}
        title="移除模型"
        description={`将从订阅列表中移除 ${model.id}，并删除其钉住配置与探测数据。\n「拉取官方模型」不会再将其加回；通过代理再次调用该模型时，它会重新加入订阅。`}
        confirmLabel="移除"
        destructive
        onConfirm={removeRow}
      />
    </Fragment>
  )
}
