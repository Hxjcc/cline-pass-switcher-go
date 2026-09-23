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
  ListRestart,
  Radar,
  Save,
  ShieldCheck,
  Timer,
  Trash2,
  TriangleAlert,
} from "lucide-react"
import { toast } from "sonner"

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
      toast.success(`探测完成：${response.upstreams?.length ?? 0} 个渠道`)
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
        toast.warning(`跳过校验：${pinReasonLabel(response.reason)}`)
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
        toast.warning(`实际命中 ${providerLabel(response.actual) || "未知渠道"}，未命中首选${ignored}`)
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
  const isExpanded = expanded
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

  return (
    <>
                  <Fragment key={model.id}>
                    <TableRow data-state={isExpanded ? "selected" : undefined}>
                      <TableCell>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          onClick={() => setExpanded(!isExpanded)}
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
                              ? pipelineHint(
                                  model.meta?.pipeline,
                                  model.meta?.pinnable,
                                  model.meta?.pinReason,
                                )
                              : "执行探测后才能识别这个模型走的线路。"}
                          </TooltipContent>
                        </Tooltip>
                      </TableCell>
                      <TableCell className="text-right font-mono tabular-nums">
                        {model.meta?.upstreams?.length ?? 0}
                      </TableCell>
                      <TableCell>
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
                          </div>
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
                            onClick={() => probe()}
                          >
                            <Radar className={action === "probe" ? "animate-pulse" : ""} />
                          </RowAction>
                          <RowAction
                            label="发送测试请求"
                            disabled={action === "test"}
                            onClick={() => runTest()}
                          >
                            <FlaskConical className={action === "test" ? "animate-pulse" : ""} />
                          </RowAction>
                          <RowAction
                            label={
                              pinDisabled
                                ? `不可校验：${pinReasonLabel(model.meta?.pinReason)}`
                                : "校验全部渠道"
                            }
                            disabled={
                              action === "validate" ||
                              !model.meta?.upstreams?.length ||
                              pinDisabled
                            }
                            onClick={() => validate()}
                          >
                            <ShieldCheck
                              className={action === "validate" ? "animate-pulse" : ""}
                            />
                          </RowAction>
                          <RowAction
                            label="移除模型"
                            disabled={Boolean(action)}
                            onClick={() => setConfirmRemove(true)}
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
                            {pinDisabled && (
                              <Alert
                                variant={
                                  model.meta?.pinReason === "single_provider"
                                    ? "default"
                                    : "destructive"
                                }
                              >
                                <TriangleAlert />
                                <AlertTitle>{pinReasonLabel(model.meta?.pinReason)}</AlertTitle>
                                <AlertDescription>
                                  {model.meta?.pinReason === "single_provider"
                                    ? "这个模型只有一个候选渠道，没有可钉或可校验的其他渠道。"
                                    : "请求仍会正常发送，但实际渠道由 Cline 决定；下面的钉住、排除、排序和校验都不会生效。"}
                                </AlertDescription>
                              </Alert>
                            )}
                            <div className="flex flex-wrap items-center gap-2">
                              <div className="flex items-center gap-2">
                                <span className="text-muted-foreground text-sm">模式</span>
                                <Select
                                  value={config.pinMode}
                                  items={pinModeItems}
                                  onValueChange={(value) =>
                                    void save({
                                      ...config,
                                      pinMode: value as ModelConfig["pinMode"],
                                    })
                                  }
                                >
                                  <SelectTrigger
                                    size="sm"
                                    className="w-40"
                                    disabled={pinDisabled}
                                    aria-label="钉住模式"
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
                              </div>
                              <div className="flex items-center gap-2">
                                <span className="text-muted-foreground text-sm">排序</span>
                                <Select
                                  value={config.sort ?? "auto"}
                                  items={sortItems}
                                  onValueChange={(value) =>
                                    void save({
                                      ...config,
                                      sort: value === "auto" ? null : (value as ModelConfig["sort"]),
                                    })
                                  }
                                >
                                  <SelectTrigger
                                    size="sm"
                                    className="w-40"
                                    disabled={pinDisabled}
                                    aria-label="排序"
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
                              </div>
                              <Button
                                variant="outline"
                                size="sm"
                                disabled={pinDisabled}
                                onClick={() => bulkAll()}
                              >
                                <ListRestart data-icon="inline-start" />
                                全部加入
                              </Button>
                              <Button variant="ghost" size="sm" onClick={() => clearConfig()}>
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
                                                      priorityIndex === 0 ||
                                                      action === "save" ||
                                                      pinDisabled
                                                    }
                                                    onClick={() =>
                                                      movePriority(upstreamSlug, -1)
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
                                                      action === "save" ||
                                                      pinDisabled
                                                    }
                                                    onClick={() =>
                                                      movePriority(upstreamSlug, 1)
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
                                          disabled={action === "save" || (pinDisabled && !pinned)}
                                          onClick={() => togglePriority(upstreamSlug)}
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
                                                disabled={action === "save" || (pinDisabled && !excluded)}
                                                onClick={() => toggleExclude(upstreamSlug)}
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
      <ConfirmDialog
        open={confirmRemove}
        onOpenChange={(open) => {
          if (!open) setConfirmRemove(false)
        }}
        title="移除模型"
        description={`将 ${model.id} 从订阅列表移除，并删除它的钉住配置和探测数据。\n拉取官方模型不会再把它加回来；通过代理再次调用该模型时会重新出现。`}
        confirmLabel="移除"
        destructive
        onConfirm={removeRow}
      />
    </>
  )
}
