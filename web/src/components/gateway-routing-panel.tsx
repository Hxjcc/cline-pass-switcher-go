import { useState } from "react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Badge } from "@/components/ui/badge"
import { errorMessage } from "@/lib/api"
import { formatTime } from "@/lib/format"
import { gatewayCapabilities, normalizeHas, providerMatches, recommendationProviders, recommendedHas, routingConfigKey } from "@/lib/gateway-routing"
import type { GatewayRoutingActions, ModelConfig, RoutingVerifyResponse, SubscriptionModel } from "@/types"

interface Props extends GatewayRoutingActions {
  model: SubscriptionModel
  config: ModelConfig
  enabled: boolean
  busy: boolean
  onEnabledChange: (enabled: boolean) => void
  onDraftChange: (config: ModelConfig) => void
  onRestore: () => Promise<void>
}

export function GatewayRoutingPanel({ model, config, enabled, busy, accounts = [], onEnabledChange, onDraftChange, onRestore, onReadGateway, onVerifyGateway }: Props) {
  const [accountID, setAccountID] = useState("")
  const [working, setWorking] = useState(false)
  const [openedAt] = useState(() => Date.now())
  const [lastResult, setLastResult] = useState<{ key: string; result: RoutingVerifyResponse } | null>(null)
  const catalog = model.meta?.gatewayCatalog
  const has = config.gatewayHas ?? []
  const pending = busy || working
  const dirty = routingConfigKey(config) !== routingConfigKey(model.config)
  const candidates = catalog?.providers.filter(provider => providerMatches(provider, has)) ?? []
  const suggestedProviders = recommendationProviders(catalog, config.upstreams)
  const usableAccounts = accounts.filter(account => account.enabled && account.hasKey)
  const selectedAccounts = accountID === "all" ? usableAccounts.length : 1
  const requestCount = selectedAccounts * ((config.upstreams.length || 1) + 1)
  const eligible = config.upstreams.length > 0 && config.upstreams.every(target => candidates.some(provider => provider.slug === target))
  const checks = lastResult?.key === routingConfigKey(config) && (!lastResult.result.applied || dirty)
    ? lastResult.result.checks
    : !dirty ? Object.values(model.meta?.routingChecks ?? {}).filter(check => check.current) : []

  const read = async (useRecommendation = false) => {
    if (!onReadGateway) return
    setWorking(true)
    try {
      const response = await onReadGateway(model.id, true, config.upstreams)
      if (useRecommendation || (enabled && !has.length)) {
        const chosen = response.recommendedHas
        if (!chosen.length) {
          toast.info(config.upstreams.length > 1 ? "所选供应商没有共同的可推荐能力，请调整供应商或手动选择" : "目录没有所选供应商的可推荐能力，请调整供应商或手动选择")
          return
        }
        onEnabledChange(true)
        onDraftChange({ ...config, gatewayHas: chosen })
      }
    } catch (error) { toast.error(errorMessage(error)) } finally { setWorking(false) }
  }

  const toggle = (next: boolean) => {
    onEnabledChange(next)
    if (!next) { onDraftChange({ ...config, gatewayHas: [], upstreams: [], exclude: [] }); return }
    onDraftChange({ ...config, gatewayHas: has.length ? has : recommendedHas(catalog, config.upstreams) })
    if (!catalog && onReadGateway) void read(true)
  }

  const verify = async () => {
    if (!onVerifyGateway) return
    setWorking(true)
    const key = routingConfigKey(config)
    try {
      const response = await onVerifyGateway(model.id, config, accountID === "all" ? "" : accountID, accountID === "all")
      setLastResult({ key, result: response })
      if (response.applied) toast.success("路由对照通过，能力过滤与供应商设置已应用")
      else toast.error(response.checks.find(check => !check.ok)?.error || "验证未通过，原配置继续生效")
    } catch (error) { toast.error(errorMessage(error)) } finally { setWorking(false) }
  }

  return (
    <section className="bg-card space-y-3 rounded-lg border p-4" aria-label="供应商能力过滤">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="space-y-1">
          <label className="flex items-center gap-2 text-sm font-medium">
            <Switch checked={enabled} onCheckedChange={toggle} disabled={pending || !onReadGateway} aria-label="启用能力过滤" />
            能力过滤
            {dirty && <Badge variant="outline">待应用</Badge>}
          </label>
          <p className="text-muted-foreground text-xs">按供应商能力筛选路由；选择供应商并验证通过后应用。默认关闭。</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" disabled={pending || !onReadGateway} onClick={() => void read()}>读取供应商能力</Button>
          <Button variant="outline" size="sm" disabled={pending || !onReadGateway} onClick={() => void read(true)}>使用推荐</Button>
        </div>
      </div>

      {catalog && <p className="text-muted-foreground text-xs" title={catalog.source}>来源：Vercel · {catalog.model} · 更新于 {formatTime(catalog.fetchedAt)}{openedAt - catalog.fetchedAt > 6 * 60 * 60 * 1000 ? "（已过期，可刷新）" : ""}</p>}
      {suggestedProviders.length > 0 && <p className="text-muted-foreground text-xs">推荐依据：{suggestedProviders.map(provider => provider.name).join("、")}{config.upstreams.length > 1 ? "的共同能力" : "的目录能力"}{!config.upstreams.length && "（未选择供应商，优先使用模型原厂）"}。点击“使用推荐”填写草稿，验证通过后应用。</p>}

      {enabled && <>
        {!dirty && !checks.length && <p className="text-xs">当前账号与条件尚无有效验证记录，请重新验证后确认钉住状态。</p>}
        <div className="grid gap-x-6 gap-y-2 sm:grid-cols-2 xl:grid-cols-3">
          {gatewayCapabilities.map(capability => <label key={capability.value} className="flex items-center gap-2 text-xs">
            <Checkbox checked={has.includes(capability.value)} disabled={pending} onCheckedChange={checked => onDraftChange({ ...config, gatewayHas: normalizeHas(checked ? [...has, capability.value] : has.filter(value => value !== capability.value)) })} aria-label={`要求${capability.label}能力`} />
            要求{capability.label}能力
          </label>)}
        </div>
        <details className="text-xs">
          <summary className="text-muted-foreground cursor-pointer">高级：权重量化格式</summary>
          <Input className="mt-2 max-w-lg font-mono text-xs" aria-label="量化过滤条件" value={has.filter(value => value.includes("quantization:")).join(", ")} placeholder="quantization:fp8 或 !quantization:fp8，多项用逗号分隔" disabled={pending} onChange={event => onDraftChange({ ...config, gatewayHas: [...has.filter(value => !value.includes("quantization:")), ...event.target.value.split(",").map(value => value.trim()).filter(Boolean)] })} />
          <p className="text-muted-foreground mt-1">目录未提供量化格式时保持不选。免费推理标签与 Cline Pass 订阅扣费分别判断。</p>
        </details>
        <p className="text-muted-foreground text-xs" role="status">符合当前条件：{candidates.length} 个供应商。以下为目录候选，实际可用性以验证结果为准。</p>
        {!catalog && <p className="text-xs">请读取供应商能力，旧的私有渠道探测结果不影响本次筛选。</p>}
        {catalog && !eligible && <p className="text-xs">请在下面将符合条件的供应商设为优先，再验证并应用。</p>}
        <div className="flex flex-wrap items-center gap-2">
          <label className="text-muted-foreground text-xs" htmlFor={`verify-account-${model.id}`}>验证账号</label>
          <select id={`verify-account-${model.id}`} aria-label="验证账号" value={accountID} onChange={event => setAccountID(event.target.value)} disabled={pending} className="bg-background h-8 max-w-52 rounded-md border px-2 text-xs">
            <option value="">当前调度账号</option>
            {usableAccounts.map(account => <option key={account.id} value={account.id}>{account.name}</option>)}
            {usableAccounts.length > 1 && <option value="all">所有启用账号</option>}
          </select>
          <Button size="sm" disabled={pending || !onVerifyGateway || !has.length || !eligible} onClick={() => void verify()}>{working ? "处理中…" : "验证并应用"}</Button>
          <span className="text-muted-foreground text-xs">最多 {requestCount} 次最小模型请求，可能产生费用</span>
        </div>
        {checks.map(check => <div key={check.profile} className="bg-muted/30 rounded-md border px-3 py-2 text-xs">
          <div className="flex flex-wrap items-center gap-2"><Badge variant={check.ok ? "secondary" : "destructive"}>{check.ok ? "对照验证通过" : "无法确认钉住"}</Badge><span>{check.account} · {formatTime(check.checkedAt)}</span></div>
          <p className="mt-1">目标 {check.targets.join("、")} · 实际 {check.actual.filter(Boolean).join("、") || "未知"} · 无效上游{check.rejected ? "已拒绝" : "未确认拒绝"}</p>
          {check.error && <p className="text-destructive mt-1 break-all">{check.error}</p>}
        </div>)}
        <details className="text-xs"><summary className="text-muted-foreground cursor-pointer">查看最终路由参数</summary><pre className="bg-muted mt-2 overflow-x-auto rounded-md p-3">{JSON.stringify({ model: model.id, providerOptions: { gateway: { ...(config.upstreams[0] ? { only: [config.upstreams[0]] } : {}), has } } }, null, 2)}</pre><p className="text-muted-foreground mt-1">多个优先供应商按顺序逐个尝试，每次只允许当前一个。</p></details>
      </>}

      {!enabled && dirty && <Button size="sm" disabled={pending} onClick={() => void onRestore()}>应用默认路由</Button>}
    </section>
  )
}
