import { useEffect, useMemo, useState } from "react"
import { Activity, ArrowDownToLine, ArrowUpFromLine, BarChart3, Boxes, Coins, Database, KeyRound, RefreshCw, RotateCcw, Search, Sigma, TriangleAlert, Users } from "lucide-react"
import { EmptyState, Field, ToggleChip } from "@/components/console-kit"
import { MetricCard } from "@/components/metric-card"
import { UsageChart } from "@/components/usage-chart"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api, errorMessage, UnauthorizedError } from "@/lib/api"
import { cardActionClass, chipClass } from "@/lib/console-styles"
import { formatCompactTime, formatCost } from "@/lib/format"
import { defaultUsageFilter, formatUsageTokens, usageDate, usagePath, validateUsageFilter } from "@/lib/usage-statistics"
import { cn } from "@/lib/utils"
import type { UsageOption, UsageReport } from "@/types"

function FilterSelect({ label, all, value, options, onChange }: { label: string; all: string; value: string; options: UsageOption[]; onChange: (value: string) => void }) {
  const items = { "": all, ...Object.fromEntries(options.map(option => [option.id, option.name])), ...(value && !options.some(option => option.id === value) ? { [value]: value } : {}) }
  return <Field label={label} labelId={`usage-${label}`}>
    <Select value={value} items={items} onValueChange={next => onChange(next ?? "")}>
      <SelectTrigger aria-labelledby={`usage-${label}`} className="w-full"><SelectValue /></SelectTrigger>
      <SelectContent>{Object.entries(items).map(([id, name]) => <SelectItem key={id} value={id}>{name}</SelectItem>)}</SelectContent>
    </Select>
  </Field>
}

export function UsagePanel({ authKey, active = true, refreshKey = 0, onError }: { authKey: string; active?: boolean; refreshKey?: number; onError: (error: unknown) => void }) {
  const [draft, setDraft] = useState(defaultUsageFilter)
  const [applied, setApplied] = useState(defaultUsageFilter)
  const [preset, setPreset] = useState("7")
  const [report, setReport] = useState<UsageReport | null>(null)
  const [completedRequest, setCompletedRequest] = useState<object | null>(null)
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const [group, setGroup] = useState<"models" | "accounts" | "keys">("models")
  const path = usagePath(applied)
  const request = useMemo(() => ({ path, authKey, active, revision, refreshKey }), [path, authKey, active, revision, refreshKey])
  const pending = active && completedRequest !== request

  useEffect(() => {
    if (!request.active) return
    const controller = new AbortController()
    let current = true
    void api<UsageReport>(request.path, { key: request.authKey, signal: controller.signal })
      .then(next => { if (current) { setReport(next); setError("") } })
      .catch((reason: unknown) => {
        if (!current) return
        setError(errorMessage(reason))
        if (reason instanceof UnauthorizedError) onError(reason)
      })
      .finally(() => { if (current) setCompletedRequest(request) })
    return () => { current = false; controller.abort() }
  }, [request, onError])

  const apply = (next = draft) => {
    const invalid = validateUsageFilter(next)
    if (invalid) { setError(invalid); return }
    setApplied(next); setRevision(value => value + 1)
  }
  const choosePreset = (value: string) => {
    const next = value === "yesterday" ? { ...draft, from: usageDate(-1), to: usageDate(-1) } : { ...draft, from: usageDate(1 - Number(value)), to: usageDate() }
    setPreset(value); setDraft(next); apply(next)
  }
  const reset = () => { const next = defaultUsageFilter(); setDraft(next); setPreset("7"); apply(next) }
  const rows = report?.[group] ?? []
  const totals = report?.summary
  const tokenValue = (value?: number) => totals && totals.usageRequests > 0 && value !== undefined ? formatUsageTokens(value) : "—"
  const dirty = JSON.stringify(draft) !== JSON.stringify(applied)
  const drillDown = (id: string) => { const field = group === "models" ? "model" : group === "accounts" ? "account" : "key"; const next = { ...applied, [field]: id }; setDraft(next); apply(next) }

  return <section className="space-y-4" aria-label="Token 使用统计" aria-busy={pending}>
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><BarChart3 className="text-primary size-4" />Token 统计{dirty && <Badge variant="outline" className={chipClass}>待应用</Badge>}</CardTitle>
        <CardDescription>查看请求实际用量，支持时间、模型、账号与代理密钥筛选。按北京时间（UTC+8）统计。</CardDescription>
        <CardAction className={cardActionClass}><Button variant="outline" size="sm" disabled={pending} onClick={() => setRevision(value => value + 1)}><RefreshCw className={pending ? "animate-spin" : ""} data-icon="inline-start" />刷新统计</Button></CardAction>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-center gap-1.5" aria-label="统计时间范围">{[["1", "今天"], ["yesterday", "昨天"], ["7", "近 7 天"], ["30", "近 30 天"]].map(([value, label]) =>
          <Button key={value} variant="outline" size="sm" aria-pressed={preset === value} className={cn(preset === value && "border-primary/40 bg-primary/10 text-primary")} onClick={() => choosePreset(value)}>{label}</Button>,
        )}<span className="text-muted-foreground ml-2 text-2xs">也可选择自定义日期</span></div>
        <form noValidate className="grid items-end gap-3 sm:grid-cols-2 lg:grid-cols-[1fr_1fr_1.5fr_1fr_1fr_auto]" onSubmit={event => { event.preventDefault(); apply() }}>
          <Field label="开始日期" htmlFor="usage-from"><Input id="usage-from" type="date" value={draft.from} max={usageDate()} onChange={event => { setPreset("custom"); setDraft({ ...draft, from: event.target.value }) }} /></Field>
          <Field label="结束日期" htmlFor="usage-to"><Input id="usage-to" type="date" value={draft.to} max={usageDate()} onChange={event => { setPreset("custom"); setDraft({ ...draft, to: event.target.value }) }} /></Field>
          <FilterSelect label="模型" all="全部模型" value={draft.model} options={report?.filters.models ?? []} onChange={model => setDraft({ ...draft, model })} />
          <FilterSelect label="账号" all="全部账号" value={draft.account} options={report?.filters.accounts ?? []} onChange={account => setDraft({ ...draft, account })} />
          <FilterSelect label="代理密钥" all="全部密钥" value={draft.key} options={report?.filters.keys ?? []} onChange={key => setDraft({ ...draft, key })} />
          <div className="flex gap-2"><Button type="submit"><Search data-icon="inline-start" />应用筛选</Button><Button type="button" variant="outline" size="icon" aria-label="重置统计筛选" onClick={reset}><RotateCcw /></Button></div>
        </form>
        <div className="text-muted-foreground flex flex-wrap items-center justify-between gap-2 text-2xs">
          <span>{report ? `${report.from} — ${report.to}${report.availableFrom ? ` · 已有数据自 ${report.availableFrom}` : ""}` : "读取统计记录中…"}</span>
          <span>{pending ? "更新中…" : report ? `更新于 ${formatCompactTime(report.generatedAt)}` : ""}</span>
        </div>
      </CardContent>
    </Card>
    {error && !pending && <Alert variant="destructive"><TriangleAlert /><AlertTitle>统计暂未更新</AlertTitle><AlertDescription>{error}</AlertDescription></Alert>}
    <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
      <MetricCard label="总 Token" value={tokenValue(totals?.totalTokens)} detail={totals ? `${totals.usageRequests.toLocaleString("zh-CN")} 次请求上报用量` : "输入与输出合计"} icon={Sigma} />
      <MetricCard label="输入 Token" value={tokenValue(totals?.inputTokens)} detail="包含命中的缓存输入" icon={ArrowDownToLine} />
      <MetricCard label="输出 Token" value={tokenValue(totals?.outputTokens)} detail={`其中思考 ${tokenValue(totals?.reasoningTokens)}`} icon={ArrowUpFromLine} />
      <MetricCard label="缓存 Token" value={tokenValue(totals?.cachedTokens)} detail="上游报告的缓存用量" icon={Database} />
      <MetricCard label="实际扣费" value={totals?.costRequests ? formatCost(totals.cost) : "—"} detail={totals ? `${totals.costRequests.toLocaleString("zh-CN")} 次请求上报费用` : "仅累计上游报告的费用"} icon={Coins} />
      <MetricCard label="请求次数" value={totals ? totals.requests.toLocaleString("zh-CN") : "—"} detail={totals ? `成功 ${totals.requests - totals.errors} · 失败 ${totals.errors}` : "当前筛选范围内"} icon={Activity} />
    </div>
    <Card>
      <CardHeader><CardTitle>每日 Token 用量</CardTitle><CardDescription>输入与输出按日汇总，思考 Token 包含在输出中。</CardDescription><CardAction className="text-muted-foreground flex items-center gap-4 text-2xs"><span className="flex items-center gap-1.5"><i className="size-2 rounded-sm bg-blue-500" />输入</span><span className="flex items-center gap-1.5"><i className="size-2 rounded-sm bg-violet-500" />输出</span></CardAction></CardHeader>
      <CardContent>{totals?.requests ? <UsageChart days={report!.trend} /> : <EmptyState icon={BarChart3} title={pending ? "正在读取用量…" : "所选范围暂无记录"} description="调整日期或筛选条件，查看对应的 Token 用量。" />}</CardContent>
    </Card>
    <Card>
      <CardHeader><CardTitle>用量明细</CardTitle><CardDescription>点击名称可继续筛选。所有分组使用同一组时间和筛选条件。</CardDescription><CardAction className="flex flex-wrap gap-1.5">{([["models", "按模型", Boxes], ["accounts", "按账号", Users], ["keys", "按密钥", KeyRound]] as const).map(([value, label, Icon]) => <ToggleChip key={value} pressed={group === value} onPressedChange={() => setGroup(value)} icon={Icon}>{label}</ToggleChip>)}</CardAction></CardHeader>
      <CardContent>{rows.length ? <div className="overflow-x-auto rounded-lg ring-1 ring-foreground/10"><Table className="min-w-[800px]">
        <TableHeader><TableRow><TableHead>{group === "models" ? "模型" : group === "accounts" ? "账号" : "代理密钥"}</TableHead><TableHead className="text-right">请求</TableHead><TableHead className="text-right">输入</TableHead><TableHead className="text-right">输出</TableHead><TableHead className="text-right">思考</TableHead><TableHead className="text-right">缓存</TableHead><TableHead className="text-right">实际扣费</TableHead></TableRow></TableHeader>
        <TableBody>{rows.map(row => <TableRow key={row.id}>
          <TableCell className="min-w-48 max-w-80"><button type="button" className="text-foreground text-left font-mono text-xs wrap-anywhere hover:text-primary" onClick={() => drillDown(row.id)}>{row.name || "未标记"}</button><div className="bg-muted mt-1.5 h-1 w-32 overflow-hidden rounded-full" aria-hidden><div className="h-full rounded-full bg-primary/65" style={{ width: `${totals?.totalTokens ? row.totalTokens / totals.totalTokens * 100 : 0}%` }} /></div></TableCell>
          {[row.requests, row.inputTokens, row.outputTokens, row.reasoningTokens, row.cachedTokens].map((value, index) => <TableCell key={index} className="text-right font-mono text-xs tabular-nums" title={value.toLocaleString("zh-CN")}>{index === 0 ? value.toLocaleString("zh-CN") : row.usageRequests ? formatUsageTokens(value) : "—"}</TableCell>)}
          <TableCell className="text-right font-mono text-xs tabular-nums">{row.costRequests ? formatCost(row.cost) : "—"}</TableCell>
        </TableRow>)}</TableBody>
      </Table></div> : <p className="text-muted-foreground py-6 text-center text-xs">暂无符合条件的明细</p>}</CardContent>
    </Card>
    <p className="text-muted-foreground px-1 text-2xs leading-5">按日汇总保留近 365 天，清空请求历史不会清空统计。{report?.importedRequests ? `首次已补入当时保留的 ${report.importedRequests} 条历史记录，更早已删除的记录无法补算。` : "统计从启用此版本后开始累计。"}费用仅累计上游已报告的实际扣费，未上报的用量与费用不作估算。</p>
  </section>
}
