import { useEffect, useRef, useState } from "react"
import { History, RefreshCw, Search, Trash2 } from "lucide-react"
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
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { ProviderName } from "@/components/provider-name"
import { TraceList } from "@/components/trace-list"
import { errorMessage } from "@/lib/api"
import { formatCost, formatTime, formatTokenCount, shortDuration } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { HistoryItem, UsageStats } from "@/types"

const AUTO_REFRESH_STORAGE = "cline-pass-switcher-history-auto-refresh"
const AUTO_REFRESH_MS = 5000

const finishLabels: Record<string, string> = {
  stop: "正常结束",
  tool_calls: "调用工具",
  function_call: "调用工具",
  length: "长度截断",
  content_filter: "内容过滤",
}

function finishLabel(reason?: string) {
  if (!reason) return ""
  return finishLabels[reason] || reason
}

// Quiet placeholder so a failed row (mostly empty cells) does not read as a
// wall of dashes next to the red error text.
function Dash() {
  return <span className="text-muted-foreground/50">—</span>
}

function EffortCell({ item }: { item: HistoryItem }) {
  const mapped = item.effort?.trim()
  const requested = item.requestedEffort?.trim()
  if (!mapped && !requested) {
    return <Dash />
  }
  if (requested && mapped && requested !== mapped) {
    return (
      <span
        className="font-mono text-xs"
        title={`客户端 ${requested} → 实际转给上游 ${mapped}`}
      >
        {requested}→{mapped}
      </span>
    )
  }
  return (
    <span className="font-mono text-xs" title="实际转给上游的思考强度">
      {mapped || requested}
    </span>
  )
}

function hasFailover(item: HistoryItem) {
  const trace = item.trace ?? []
  if (trace.length > 1) return true
  if (trace.some((attempt) => attempt.status !== 200)) return true
  return (item.attempts?.length ?? 0) > 1
}

function usageTitle(usage?: UsageStats, finishReason?: string) {
  if (!usage && !finishReason) return undefined
  const parts: string[] = []
  if (usage?.promptTokens) parts.push(`输入 ${formatTokenCount(usage.promptTokens)}`)
  if (usage?.completionTokens) parts.push(`输出 ${formatTokenCount(usage.completionTokens)}`)
  if (usage?.reasoningTokens) parts.push(`思考 ${formatTokenCount(usage.reasoningTokens)}`)
  if (usage?.cachedTokens) parts.push(`缓存 ${formatTokenCount(usage.cachedTokens)}`)
  if (usage?.totalTokens) parts.push(`合计 ${formatTokenCount(usage.totalTokens)}`)
  if (usage?.cost !== undefined) parts.push(`费用 ${formatCost(usage.cost)}`)
  if (finishReason) parts.push(finishLabel(finishReason))
  return parts.join(" · ")
}

function UsageCell({ item }: { item: HistoryItem }) {
  const usage = item.usage
  if (!usage) {
    return <Dash />
  }
  const extras = [
    usage.reasoningTokens ? `思考 ${formatTokenCount(usage.reasoningTokens)}` : "",
    usage.cachedTokens ? `缓存 ${formatTokenCount(usage.cachedTokens)}` : "",
  ].filter(Boolean)

  // Two short lines keep the column narrow enough for the table to fit at
  // 1400px without horizontal scrolling.
  return (
    <div className="whitespace-nowrap font-mono text-xs tabular-nums" title={usageTitle(usage, item.finishReason)}>
      <div>
        {formatTokenCount(usage.promptTokens)}
        <span className="text-muted-foreground"> → </span>
        {formatTokenCount(usage.completionTokens)}
      </div>
      {extras.length > 0 && (
        <div className="text-muted-foreground text-2xs">{extras.join(" · ")}</div>
      )}
    </div>
  )
}

function CostCell({ usage }: { usage?: UsageStats }) {
  if (usage?.cost === undefined) {
    return <Dash />
  }
  return <span className="font-mono text-xs tabular-nums">{formatCost(usage.cost)}</span>
}

// Cells mix text-sm, text-xs and badges with different line heights; only
// middle alignment puts a single-line row on one visual midline.
const cell = "align-middle"
const chipClass = "h-5 px-1.5 py-0 text-2xs"
// A compaction that succeeded with a thin summary is not an error, but it is
// worth spotting: the badge stays visible instead of hiding in a tooltip.
const warnChipClass =
  "h-5 border-amber-200 bg-amber-50 px-1.5 py-0 text-2xs text-amber-700 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300"

export function HistoryPanel({
  history,
  total,
  hasMore,
  query,
  onQueryChange,
  onRefresh,
  onLoadMore,
  onClear,
}: {
  history: HistoryItem[]
  total: number
  hasMore: boolean
  query: { q: string; onlyErrors: boolean }
  onQueryChange: (next: { q: string; onlyErrors: boolean }) => void
  onRefresh: () => Promise<void>
  onLoadMore: () => Promise<void>
  onClear: () => Promise<void>
}) {
  const [autoRefresh, setAutoRefresh] = useState(
    () => localStorage.getItem(AUTO_REFRESH_STORAGE) === "1",
  )
  const [refreshing, setRefreshing] = useState(false)
  const [clearing, setClearing] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [search, setSearch] = useState(query.q)

  // Typing filters as you go, but only after a pause: every keystroke would
  // otherwise reload the log from the server.
  useEffect(() => {
    if (search === query.q) return
    const timer = window.setTimeout(
      () => onQueryChange({ q: search, onlyErrors: query.onlyErrors }),
      300,
    )
    return () => window.clearTimeout(timer)
  }, [search, query.q, query.onlyErrors, onQueryChange])

  const refresh = async () => {
    setRefreshing(true)
    try {
      await onRefresh()
    } finally {
      setRefreshing(false)
    }
  }

  // The parent hands us a fresh callback on every render; keep the latest one
  // in a ref so toggling is the only thing that restarts the timer.
  const refreshRef = useRef(onRefresh)
  useEffect(() => {
    refreshRef.current = onRefresh
  }, [onRefresh])

  useEffect(() => {
    localStorage.setItem(AUTO_REFRESH_STORAGE, autoRefresh ? "1" : "0")
    if (!autoRefresh) return
    // The panel is only mounted while its tab is active, so polling stops on
    // its own when the user looks elsewhere; skip ticks in hidden windows.
    const timer = window.setInterval(() => {
      if (document.hidden) return
      void refreshRef.current()
    }, AUTO_REFRESH_MS)
    return () => window.clearInterval(timer)
  }, [autoRefresh])

  const clear = async () => {
    try {
      await onClear()
      toast.success("请求历史已清空")
    } catch (error) {
      toast.error(errorMessage(error))
      throw error
    }
  }

  const loadMore = async () => {
    setLoadingMore(true)
    try {
      await onLoadMore()
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setLoadingMore(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>请求历史</CardTitle>
        <CardDescription>
          保留最近 500 条代理请求，按页加载并可按模型、账号、渠道或错误筛选。首字是第一个思考/正文/工具调用到达的时间；强度是实际转给上游的思考档位，映射过会显示「客户端→上游」。
        </CardDescription>
        <CardAction className="flex flex-wrap items-center gap-3">
          <Label className="text-muted-foreground cursor-pointer gap-2 font-normal">
            <Switch
              size="sm"
              checked={autoRefresh}
              onCheckedChange={(checked) => setAutoRefresh(checked)}
              aria-label="自动刷新"
            />
            自动刷新
          </Label>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void refresh()} disabled={refreshing}>
              <RefreshCw className={refreshing ? "animate-spin" : ""} data-icon="inline-start" />
              刷新
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setClearing(true)}
              disabled={!history.length}
            >
              <Trash2 data-icon="inline-start" />
              清空
            </Button>
          </div>
        </CardAction>
      </CardHeader>
      <CardContent>
        <div className="mb-4 flex flex-wrap items-center gap-3">
          <div className="relative max-w-sm flex-1">
            <Search className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="筛选模型 / 账号 / 渠道 / 错误"
              aria-label="筛选请求历史"
              className="pl-8"
            />
          </div>
          <Label className="text-muted-foreground cursor-pointer gap-2 font-normal">
            <Switch
              size="sm"
              checked={query.onlyErrors}
              onCheckedChange={(checked) => onQueryChange({ ...query, onlyErrors: checked })}
              aria-label="只看失败"
            />
            只看失败
          </Label>
          <span className="text-muted-foreground text-xs tabular-nums">
            显示 {history.length} / {total} 条
          </span>
        </div>
        <div className="overflow-hidden rounded-lg ring-1 ring-foreground/10">
          {/* Eleven columns: slightly tighter cell padding keeps failover rows
              (trace badges, wrapped errors) inside 1400px without scrolling. */}
          <Table className="[&_td]:px-2.5 [&_th]:px-2.5">
            <TableHeader>
              <TableRow>
                <TableHead className="w-28">时间</TableHead>
                <TableHead className="min-w-48">模型</TableHead>
                <TableHead className="w-20">账号</TableHead>
                <TableHead className="w-32">实际上游</TableHead>
                <TableHead className="min-w-44">背后模型</TableHead>
                <TableHead className="w-20">首字</TableHead>
                <TableHead className="w-20">总耗时</TableHead>
                <TableHead className="min-w-28">Token</TableHead>
                <TableHead className="w-20 text-right">费用</TableHead>
                <TableHead className="w-20">强度</TableHead>
                <TableHead className="w-20">方式</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {history.map((item, index) => (
                <TableRow key={`${item.ts}-${item.model}-${index}`}>
                  <TableCell className={cn(cell, "text-muted-foreground text-xs")}>
                    {formatTime(item.ts)}
                  </TableCell>
                  <TableCell className={cell}>
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="font-mono text-xs">{item.model}</span>
                      {item.kind === "compact" && (
                        <Badge variant="secondary" className={chipClass}>
                          压缩
                        </Badge>
                      )}
                      {item.missingSummarySections?.length ? (
                        <Badge
                          variant="outline"
                          className={warnChipClass}
                          title={`摘要缺少段落：${item.missingSummarySections.join("、")}`}
                        >
                          摘要缺 {item.missingSummarySections.join("、")}
                        </Badge>
                      ) : null}
                    </div>
                    {item.error && (
                      // Upstream errors can be ~1k chars of JSON; unwrapped they
                      // stretch this column and push the rest of the table out
                      // of view.
                      <div
                        className="text-destructive mt-1 line-clamp-3 max-w-60 whitespace-normal text-2xs wrap-anywhere"
                        title={item.error}
                      >
                        {item.error}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className={cell}>{item.account || <Dash />}</TableCell>
                  <TableCell className={cn(cell, "whitespace-normal")}>
                    {item.provider && (
                      <Badge variant="outline" className={chipClass}>
                        <ProviderName slug={item.provider} />
                      </Badge>
                    )}
                    {/* A failed request has no winning provider; the attempt
                        badges already say which upstream was tried. */}
                    {hasFailover(item) ? (
                      <div className={item.provider ? "mt-1.5" : undefined}>
                        {item.trace?.length ? (
                          <TraceList trace={item.trace} compact />
                        ) : (
                          <span className="text-muted-foreground font-mono text-2xs">
                            {item.attempts?.join(" → ")}
                          </span>
                        )}
                      </div>
                    ) : (
                      !item.provider && <Dash />
                    )}
                  </TableCell>
                  <TableCell className={cn(cell, "text-muted-foreground font-mono text-xs")}>
                    {item.canonical || <Dash />}
                  </TableCell>
                  <TableCell className={cn(cell, "font-mono text-xs tabular-nums")}>
                    {item.ttftMs === undefined || item.ttftMs === null ? (
                      <Dash />
                    ) : (
                      shortDuration(item.ttftMs)
                    )}
                  </TableCell>
                  <TableCell className={cn(cell, "font-mono text-xs tabular-nums")}>
                    {shortDuration(item.ms)}
                  </TableCell>
                  <TableCell className={cell}>
                    <UsageCell item={item} />
                  </TableCell>
                  <TableCell className={cn(cell, "text-right")}>
                    <CostCell usage={item.usage} />
                  </TableCell>
                  <TableCell className={cell}>
                    <EffortCell item={item} />
                  </TableCell>
                  <TableCell className={cell}>
                    <Badge
                      variant={item.stream ? "secondary" : "outline"}
                      className={chipClass}
                      title={finishLabel(item.finishReason)}
                    >
                      {item.stream ? "流式" : "非流式"}
                    </Badge>
                  </TableCell>
                </TableRow>
              ))}
              {!history.length && (
                <TableRow>
                  <TableCell colSpan={11} className="text-muted-foreground h-40 text-center text-sm">
                    <History className="mx-auto mb-3 size-5" />
                    暂无请求记录
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        {hasMore && (
          <div className="mt-4 flex justify-center">
            <Button variant="outline" size="sm" onClick={() => void loadMore()} disabled={loadingMore}>
              {loadingMore ? "加载中…" : `加载更多（还有 ${Math.max(total - history.length, 0)} 条）`}
            </Button>
          </div>
        )}
      </CardContent>
      <ConfirmDialog
        open={clearing}
        onOpenChange={setClearing}
        title="清空请求历史"
        description="删除全部请求记录。账号的累计请求数不受影响。"
        confirmLabel="清空"
        destructive
        onConfirm={clear}
      />
    </Card>
  )
}
