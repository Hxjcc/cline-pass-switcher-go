import { useEffect, useRef, useState, type MouseEvent } from "react"
import { ChevronRight, CircleAlert, History, RefreshCw, Search, Trash2 } from "lucide-react"
import { toast } from "sonner"

import { EmptyState, ToggleChip } from "@/components/console-kit"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { HistoryColumnsMenu } from "@/components/history-columns-menu"
import { HistoryDetailsSheet } from "@/components/history-details-sheet"
import { ProviderName } from "@/components/provider-name"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { errorMessage } from "@/lib/api"
import { cardActionClass, chipClass, warningChipClass } from "@/lib/console-styles"
import { formatCompactTime, formatCost, formatTokenCount, shortDuration } from "@/lib/format"
import { historyColumns, useHistoryColumns, type HistoryColumn } from "@/lib/history-columns"
import { actualProvider, cacheStats, costTitle, finishLabel, formatCacheRate, fullHistoryTime, gatewayMultiple, historyNotices, historyStatus, latencyText, reasoningEffort, requestKindLabel, usageTitle } from "@/lib/history-display"
import { useHistoryLayout } from "@/lib/use-history-layout"
import { useNarrowViewport } from "@/lib/use-media-query"
import { cn } from "@/lib/utils"
import type { HistoryItem } from "@/types"

const AUTO_REFRESH_STORAGE = "cline-pass-switcher-history-auto-refresh"
const AUTO_REFRESH_MS = 5000
const columnLabels = new Map(historyColumns.map(column => [column.id, column.label]))
type OpenRequest = (item: HistoryItem, trigger: HTMLButtonElement | null) => void

function Dash() { return <span className="text-muted-foreground/50">—</span> }

function UsageCell({ item }: { item: HistoryItem }) {
  if (!item.usage) return <Dash />
  return <span className="font-mono text-xs whitespace-nowrap tabular-nums" title={usageTitle(item.usage, item.finishReason)}>
    {formatTokenCount(item.usage.promptTokens)}<span className="text-muted-foreground"> → </span>{formatTokenCount(item.usage.completionTokens)}
  </span>
}

function CacheCell({ item }: { item: HistoryItem }) {
  const rate = formatCacheRate(item.usage)
  const cache = cacheStats(item.usage)
  if (!rate) return <span title="未上报足够的缓存数据"><Dash /></span>
  return <span className={cn("font-mono text-xs whitespace-nowrap tabular-nums", cache.rate && cache.rate > 0 ? "text-emerald-700 dark:text-emerald-300" : "text-muted-foreground")}
    title={cache.tokens === undefined ? undefined : `缓存 Token ${cache.tokens.toLocaleString("zh-CN")} · ${rate}`}>{rate}</span>
}

function ModelBadges({ item }: { item: HistoryItem }) {
  const notices = historyNotices(item)
  if (!notices.length) return null
  return <div className="flex flex-wrap gap-1 whitespace-normal" aria-label="请求提示">{notices.map(notice =>
    <Badge key={notice.id} variant="outline" className={notice.error ? cn(chipClass, "border-destructive/20 bg-destructive/5 text-destructive dark:border-destructive/40 dark:bg-destructive/10") : warningChipClass} title={notice.title}>{notice.label}</Badge>,
  )}</div>
}

function EffortCell({ item }: { item: HistoryItem }) {
  const effort = reasoningEffort(item)
  return effort.text ? <span className="font-mono text-xs whitespace-nowrap" title={effort.title}>{effort.text}</span> : <Dash />
}

function ModeCell({ item }: { item: HistoryItem }) {
  return <Badge variant="secondary" className={cn(chipClass, "font-normal")} title={finishLabel(item.finishReason) || undefined}>
    {requestKindLabel(item.kind)} · {item.stream ? "流式" : "非流式"}
  </Badge>
}

function ReasoningCell({ item }: { item: HistoryItem }) {
  const tokens = item.usage?.reasoningTokens
  return tokens === undefined ? <Dash /> : <span className="font-mono text-xs tabular-nums" title={`${tokens.toLocaleString("zh-CN")} 个思考 Token，包含在输出 Token 中`}>{formatTokenCount(tokens)}</span>
}

function GatewayCostCell({ item }: { item: HistoryItem }) {
  const usage = item.usage
  return usage?.gatewayCost === undefined ? <Dash /> : <span className="font-mono text-xs whitespace-nowrap tabular-nums" title={costTitle(usage)}>
    {formatCost(usage.gatewayCost)}<span className="text-muted-foreground">{gatewayMultiple(usage)}</span>
  </span>
}

function OpenDetailsButton({ item, onOpen }: { item: HistoryItem; onOpen: OpenRequest }) {
  return <button type="button" data-history-open aria-label="查看请求详情"
    className="text-muted-foreground inline-flex size-6 shrink-0 items-center justify-center rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
    onClick={event => { event.stopPropagation(); onOpen(item, event.currentTarget) }}><ChevronRight className="size-3.5" /></button>
}

function openFromRow(event: MouseEvent<HTMLElement>, item: HistoryItem, onOpen: OpenRequest) {
  const selection = window.getSelection()
  if (selection && !selection.isCollapsed && event.currentTarget.contains(selection.anchorNode)) return
  const target = event.target as HTMLElement
  if (target.closest("a,input,select,textarea,[data-no-history-open]")) return
  onOpen(item, event.currentTarget.querySelector<HTMLButtonElement>("[data-history-open]"))
}

function historyCells(item: HistoryItem, card = false) {
  const status = historyStatus(item)
  return {
    time: <span className="text-muted-foreground text-xs" title={fullHistoryTime(item.ts)}>{formatCompactTime(item.ts)}</span>,
    model: <div className="space-y-1"><span className={cn("inline-flex items-center gap-1.5", card ? "min-w-0 max-w-full" : "whitespace-nowrap")}>
      {!card && status.tone === "error" && <span className="inline-flex shrink-0" title={item.error ?? status.label}><CircleAlert className="text-destructive size-3.5" role="img" aria-label={status.label} aria-hidden={false} /></span>}
      <span data-history-model className={cn("text-foreground font-mono text-xs", card && "min-w-0 wrap-anywhere")}>{item.model}</span>
    </span><ModelBadges item={item} /></div>,
    account: <span className="text-xs">{item.account || <Dash />}</span>,
    provider: actualProvider(item) ? <Badge variant="outline" className={chipClass}><ProviderName slug={actualProvider(item)} /></Badge> : <Dash />,
    canonical: item.canonical ? <span className="font-mono text-xs">{item.canonical}</span> : <Dash />,
    effort: <EffortCell item={item} />,
    mode: <ModeCell item={item} />,
    latency: <span className="font-mono text-xs tabular-nums" title={`首字 ${shortDuration(item.ttftMs)} · 总耗时 ${shortDuration(item.ms)}`}>{latencyText(item)}</span>,
    tokens: <UsageCell item={item} />,
    reasoning: <ReasoningCell item={item} />,
    cache: <CacheCell item={item} />,
    cost: item.usage?.cost === undefined ? <Dash /> : <span className="font-mono text-xs tabular-nums" title={costTitle(item.usage)}>{formatCost(item.usage.cost)}</span>,
    gatewayCost: <GatewayCostCell item={item} />,
  }
}

function HistoryTableRow({ item, columns, selected, onOpen }: { item: HistoryItem; columns: ReadonlySet<HistoryColumn>; selected: boolean; onOpen: OpenRequest }) {
  const cells = historyCells(item)
  return <TableRow data-history-record data-state={selected ? "selected" : undefined} aria-selected={selected}
    className="cursor-pointer hover:bg-primary/5 data-[state=selected]:bg-primary/5" onClick={event => openFromRow(event, item, onOpen)}>
    {[...columns].map(column => <TableCell key={column} data-history-column={column} className="align-middle whitespace-nowrap">{cells[column]}</TableCell>)}
    <TableCell className="w-8 px-1! text-right"><OpenDetailsButton item={item} onOpen={onOpen} /></TableCell>
  </TableRow>
}

function HistoryCard({ item, columns, selected, onOpen }: { item: HistoryItem; columns: ReadonlySet<HistoryColumn>; selected: boolean; onOpen: OpenRequest }) {
  const status = historyStatus(item)
  const cells = historyCells(item, true)
  return <article data-history-record className={cn("bg-card cursor-pointer rounded-lg p-3 ring-1 ring-foreground/10 hover:bg-primary/5", selected && "bg-primary/5")}
    onClick={event => openFromRow(event, item, onOpen)}>
    <div className="mb-1 flex items-center justify-between gap-2">
      <span className={cn("ml-auto text-2xs", status.tone === "error" ? "text-destructive" : status.tone === "warning" ? "text-amber-700 dark:text-amber-300" : "text-muted-foreground")}>{status.label}</span>
      <OpenDetailsButton item={item} onOpen={onOpen} />
    </div>
    <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-2">
      {[...columns].map(column => <div key={column} data-history-card-column={column} className={cn(["time", "model", "canonical"].includes(column) && "col-span-2")}>
        <dt className="text-muted-foreground text-2xs">{columnLabels.get(column)}</dt><dd className="mt-0.5 wrap-anywhere">{cells[column]}</dd>
      </div>)}
    </dl>
  </article>
}

export function HistoryPanel({ history, total, hasMore, pending, query, onQueryChange, onRefresh, onLoadMore, onClear }: {
  history: HistoryItem[]
  total: number
  hasMore: boolean
  pending?: boolean
  query: { q: string; onlyErrors: boolean }
  onQueryChange: (next: { q: string; onlyErrors: boolean }) => void
  onRefresh: () => Promise<void>
  onLoadMore: () => Promise<void>
  onClear: () => Promise<void>
}) {
  const [autoRefresh, setAutoRefresh] = useState(() => {
    try { return localStorage.getItem(AUTO_REFRESH_STORAGE) === "1" } catch { return false }
  })
  const [refreshing, setRefreshing] = useState(false)
  const [clearing, setClearing] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [search, setSearch] = useState(query.q)
  const [selected, setSelected] = useState<HistoryItem | null>(null)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const { visibleColumns, orderedColumns, toggleColumn, reorderColumns, resetColumns } = useHistoryColumns()
  const narrow = useNarrowViewport()
  const { viewportRef, visibleColumns: tableColumns, collapsedColumns, cards } = useHistoryLayout(visibleColumns, history, narrow)
  const refreshRef = useRef(onRefresh)

  const isSelected = (item: HistoryItem) => selected !== null && (item.id ? item.id === selected.id : item.ts === selected.ts && item.model === selected.model && item.account === selected.account)
  const openDetails: OpenRequest = (item, trigger) => { triggerRef.current = trigger; setSelected(isSelected(item) ? null : item) }

  useEffect(() => {
    if (search === query.q) return
    const timer = window.setTimeout(() => onQueryChange({ q: search, onlyErrors: query.onlyErrors }), 300)
    return () => window.clearTimeout(timer)
  }, [search, query.q, query.onlyErrors, onQueryChange])

  useEffect(() => { refreshRef.current = onRefresh }, [onRefresh])
  useEffect(() => {
    try { localStorage.setItem(AUTO_REFRESH_STORAGE, autoRefresh ? "1" : "0") } catch { /* Optional storage. */ }
    if (!autoRefresh) return
    const timer = window.setInterval(() => {
      if (!document.hidden) void refreshRef.current()
    }, AUTO_REFRESH_MS)
    return () => window.clearInterval(timer)
  }, [autoRefresh])

  const refresh = async () => {
    setRefreshing(true)
    try { await onRefresh() } finally { setRefreshing(false) }
  }
  const clear = async () => {
    try { await onClear(); toast.success("请求历史已清空") } catch (error) { toast.error(errorMessage(error)); throw error }
  }
  const loadMore = async () => {
    setLoadingMore(true)
    try { await onLoadMore() } catch (error) { toast.error(errorMessage(error)) } finally { setLoadingMore(false) }
  }

  return <Card>
    <CardHeader>
      <CardTitle>请求历史</CardTitle>
      <CardDescription>保留最近 500 条代理请求。点击记录查看完整模型、路由、用量与费用明细。</CardDescription>
      <CardAction className={cardActionClass}>
        <Button variant="outline" size="sm" onClick={() => void refresh()} disabled={refreshing}><RefreshCw className={refreshing ? "animate-spin" : ""} data-icon="inline-start" />刷新</Button>
        <Button variant="outline" size="sm" onClick={() => setClearing(true)} disabled={!history.length}><Trash2 data-icon="inline-start" />清空</Button>
      </CardAction>
    </CardHeader>
    <CardContent className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative mr-1 w-full max-w-xs">
          <Search className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
          <Input value={search} onChange={event => setSearch(event.target.value)} placeholder="筛选模型、账号、渠道或错误" aria-label="筛选请求历史" className="pl-8" />
        </div>
        <ToggleChip pressed={query.onlyErrors} onPressedChange={pressed => onQueryChange({ ...query, onlyErrors: pressed })} icon={CircleAlert}>只看失败</ToggleChip>
        <ToggleChip pressed={autoRefresh} onPressedChange={setAutoRefresh} icon={RefreshCw}>自动刷新</ToggleChip>
        <HistoryColumnsMenu columns={orderedColumns} visibleColumns={visibleColumns} collapsedColumns={collapsedColumns} onToggle={toggleColumn} onReorder={reorderColumns} onReset={resetColumns} />
        <span className="text-muted-foreground ml-auto text-xs tabular-nums">已显示 {history.length} / {total} 条{pending && <span className="ml-2">刷新中…</span>}</span>
      </div>
      <div ref={viewportRef} className="min-w-0">
        {!history.length ? <EmptyState icon={History} title={query.q || query.onlyErrors ? "没有符合条件的请求" : "暂无请求记录"}
          description={query.q || query.onlyErrors ? "请调整筛选条件后重试。" : "客户端通过代理发出的请求会记录在此处。"} />
          : cards ? <div className="space-y-2">{history.map((item, index) => <HistoryCard key={item.id ?? `${item.ts}-${item.model}-${index}`} item={item} columns={visibleColumns} selected={isSelected(item)} onOpen={openDetails} />)}</div>
          : <div className="overflow-hidden rounded-lg ring-1 ring-foreground/10">
            <Table className="[&_td]:px-2.5 [&_td]:py-3 [&_th]:px-2.5">
              <TableHeader><TableRow>
                {orderedColumns.filter(column => tableColumns.has(column.id)).map(column => <TableHead key={column.id} data-history-column={column.id} title={column.title}>{column.label}</TableHead>)}
                <TableHead className="w-8 px-1!"><span className="sr-only">详情</span></TableHead>
              </TableRow></TableHeader>
              <TableBody>{history.map((item, index) => <HistoryTableRow key={item.id ?? `${item.ts}-${item.model}-${index}`} item={item} columns={tableColumns} selected={isSelected(item)} onOpen={openDetails} />)}</TableBody>
            </Table>
          </div>}
      </div>
      {hasMore && <div className="flex justify-center"><Button variant="outline" size="sm" onClick={() => void loadMore()} disabled={loadingMore || pending}>
        {loadingMore ? "加载中…" : `加载更多（还有 ${Math.max(total - history.length, 0)} 条）`}
      </Button></div>}
    </CardContent>
    <HistoryDetailsSheet item={selected} triggerRef={triggerRef} onClose={() => setSelected(null)} />
    <ConfirmDialog open={clearing} onOpenChange={setClearing} title="清空请求历史" description="将删除全部请求记录，账号的累计请求数不受影响。" confirmLabel="清空" destructive onConfirm={clear} />
  </Card>
}
