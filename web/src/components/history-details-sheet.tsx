import type { ReactNode, RefObject } from "react"
import { CircleCheck, CircleAlert, X } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { chipClass, successChipClass, warningChipClass } from "@/lib/console-styles"
import { formatCost, shortDuration } from "@/lib/format"
import { actualProvider, cacheStats, fallbackTitle, finishLabel, formatCacheRate, fullHistoryTime, gatewayMultiple, historyError, historyStatus } from "@/lib/history-display"
import { cn } from "@/lib/utils"
import type { HistoryItem } from "@/types"

function Field({ label, children, className }: { label: string; children: ReactNode; className?: string }) {
  return <div className={cn("min-w-0", className)}><dt className="text-muted-foreground text-2xs">{label}</dt><dd className="mt-1 font-mono text-xs whitespace-pre-wrap wrap-anywhere">{children ?? "—"}</dd></div>
}

const count = (value?: number) => value === undefined ? "—" : value.toLocaleString("zh-CN")

export function HistoryDetailsSheet({ item, triggerRef, onClose }: {
  item: HistoryItem | null
  triggerRef: RefObject<HTMLButtonElement | null>
  onClose: () => void
}) {
  const status = item ? historyStatus(item) : null
  const usage = item?.usage
  const cache = cacheStats(usage)
  const error = item ? historyError(item) : null
  return (
    <Sheet open={Boolean(item)} modal={false} onOpenChange={(open, details) => {
      if (open) return
      if (details.reason === "outside-press" || details.reason === "focus-out") {
        const target = details.event instanceof FocusEvent ? details.event.relatedTarget : details.event.target
        // Let the row click toggle or switch the selection before dismissing.
        if (target instanceof Element && target.closest("[data-history-record]")) {
          details.cancel()
          return
        }
      }
      onClose()
    }}>
      <SheetContent showCloseButton={false} overlayClassName="pointer-events-none bg-transparent supports-backdrop-filter:backdrop-blur-none"
        style={{ width: "min(440px, 100%)", maxWidth: "100%" }} className="gap-0"
        finalFocus={() => triggerRef.current?.isConnected ? triggerRef.current : false}>
        <SheetHeader className="shrink-0 flex-row items-center gap-2 border-b p-5">
          <SheetTitle>请求详情</SheetTitle>
          {status && <Badge variant="outline" className={cn(chipClass, status.tone === "success" ? successChipClass : status.tone === "warning" ? warningChipClass : status.tone === "error" && "border-destructive/20 text-destructive")}>{status.label}</Badge>}
          <SheetClose render={<Button variant="ghost" size="icon-sm" className="ml-auto" aria-label="关闭详情" />}><X /></SheetClose>
          <SheetDescription className="sr-only">完整请求模型、路由、用量与费用明细</SheetDescription>
        </SheetHeader>
        {item && <div className="min-h-0 flex-1 space-y-4 overflow-y-auto overscroll-contain p-5 text-xs">
          <dl className="space-y-4">
            <Field label="请求时间">{fullHistoryTime(item.ts)}</Field>
            <div className="space-y-3 border-t pt-4">
              <Field label="原始请求模型">{item.model}</Field>
              <Field label="背后模型">{item.canonical}</Field>
            </div>
            <div className="grid grid-cols-2 gap-4 border-t pt-4">
              <Field label="账号">{item.account}</Field>
              <Field label="实际供应商">{actualProvider(item) || "未知"}</Field>
              <Field label="首字">{shortDuration(item.ttftMs)}</Field>
              <Field label="总耗时">{shortDuration(item.ms)}</Field>
            </div>
            <div className="grid grid-cols-2 gap-4 border-t pt-4">
              <Field label="输入 Token">{count(usage?.promptTokens)}</Field>
              <Field label="输出 Token">{count(usage?.completionTokens)}</Field>
              <Field label="思考 Token">{count(usage?.reasoningTokens)}</Field>
              <Field label="缓存 Token">{count(cache.tokens)}</Field>
              <Field label="缓存命中率" className={cache.rate && cache.rate > 0 ? "text-emerald-700 dark:text-emerald-300" : undefined}>{formatCacheRate(usage) || "未上报"}</Field>
              {usage?.totalTokens !== undefined && <Field label="合计 Token">{count(usage.totalTokens)}</Field>}
            </div>
            <div className="grid grid-cols-2 gap-4 border-t pt-4">
              <Field label="实际扣费">{usage?.cost === undefined ? "—" : formatCost(usage.cost)}</Field>
              {usage?.gatewayCost !== undefined && <Field label="网关市价">{formatCost(usage.gatewayCost)}{gatewayMultiple(usage)}</Field>}
              {usage?.inputCost !== undefined && <Field label="输入费用">{formatCost(usage.inputCost)}</Field>}
              {usage?.outputCost !== undefined && <Field label="输出费用">{formatCost(usage.outputCost)}</Field>}
              {usage?.surchargeCost !== undefined && <Field label="附加费用">{formatCost(usage.surchargeCost)}</Field>}
              <Field label="响应方式">{item.stream ? "流式" : "非流式"}</Field>
              {item.finishReason && <Field label="结束原因">{finishLabel(item.finishReason)}</Field>}
              {item.effort && <Field label="上游思考强度">{item.effort}</Field>}
              {item.requestedEffort && <Field label="请求思考强度">{item.requestedEffort}</Field>}
            </div>
          </dl>
          {(item.gatewayHas?.length || item.targetProviders?.length || item.fallback) ? <section className="space-y-3 border-t pt-4" aria-label="路由条件">
            <dl><Field label="目标供应商">{item.targetProviders?.join("、") || "按能力自动选择"}</Field></dl>
            {item.gatewayHas?.length ? <div><p className="text-muted-foreground text-2xs">能力过滤（has）</p><div className="mt-1.5 flex flex-wrap gap-1.5">{item.gatewayHas.map(value => <Badge key={value} variant="secondary" className={cn(chipClass, "max-w-full font-mono whitespace-normal wrap-anywhere")}>{value}</Badge>)}</div></div> : null}
            <p className={cn("flex items-center gap-1.5", status?.tone === "error" ? "text-destructive" : "text-muted-foreground")}>
              {status?.tone === "success" ? <CircleCheck className="size-3.5 shrink-0" /> : <CircleAlert className="size-3.5 shrink-0" />}
              {item.kind === "routing-check" ? "路由对照" : item.gatewayHas?.length ? "能力过滤" : "路由"} · {status?.label}
            </p>
            {item.fallback && <p className="text-muted-foreground whitespace-pre-wrap wrap-anywhere">{fallbackTitle(item)}</p>}
          </section> : null}
          {error && <section className="space-y-1.5 border-t pt-4" aria-label="错误原因"><h3 className="text-muted-foreground text-2xs">错误原因</h3><p className="text-destructive whitespace-pre-wrap wrap-anywhere">{error}</p></section>}
          {(item.degraded || item.missingSummarySections?.length) ? <section className="space-y-2 border-t pt-4" aria-label="压缩说明">
            <h3 className="text-muted-foreground text-2xs">压缩说明</h3>
            {item.degraded && <p className="whitespace-pre-wrap wrap-anywhere">压缩降级{item.degradeReason ? `：${item.degradeReason}` : ""}</p>}
            {item.missingSummarySections?.length ? <p className="wrap-anywhere">摘要缺 {item.missingSummarySections.join("、")}</p> : null}
          </section> : null}
          {item.gatewayAttempts?.length ? <section className="space-y-2 border-t pt-4" aria-label="网关内部尝试">
            <h3 className="text-muted-foreground text-2xs">网关内部尝试 · {item.gatewayAttempts.length} 次</h3>
            <ol className="space-y-2">{item.gatewayAttempts.map((attempt, index) => <li key={index} className="space-y-1 rounded-md bg-muted/40 p-2">
              <p className="font-mono wrap-anywhere">{attempt.provider || "未知渠道"} · {attempt.success === false || (attempt.status ?? 0) >= 400 ? `失败 ${attempt.status ?? ""}` : "成功"} · {shortDuration(attempt.ms)}</p>
              {attempt.error && <p className="text-destructive whitespace-pre-wrap wrap-anywhere">{attempt.error}</p>}
            </li>)}</ol>
          </section> : null}
          {item.trace?.length ? <section className="space-y-2 border-t pt-4" aria-label="请求尝试记录">
            <h3 className="text-muted-foreground text-2xs">请求尝试记录</h3>
            <ol className="space-y-2">{item.trace.map((attempt, index) => <li key={index} className="rounded-md bg-muted/40 p-2">
              <p className="font-mono wrap-anywhere">{attempt.upstream || "自动路由"} · HTTP {attempt.status} · {shortDuration(attempt.ms)}</p>
              {attempt.note && <p className="text-muted-foreground mt-1 whitespace-pre-wrap wrap-anywhere">{attempt.note}</p>}
            </li>)}</ol>
          </section> : item.attempts?.length ? <dl className="border-t pt-4"><Field label="请求尝试记录">{item.attempts.join(" → ")}</Field></dl> : null}
          {item.session || item.generationId ? <dl className="space-y-3 border-t pt-4">
            {item.session && <Field label="会话 ID">{item.session}</Field>}
            {item.generationId && <Field label="生成 ID">{item.generationId}</Field>}
          </dl> : null}
        </div>}
      </SheetContent>
    </Sheet>
  )
}
