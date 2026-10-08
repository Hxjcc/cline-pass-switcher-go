import { useRef, useState } from "react"
import { CalendarClock, Eye, EyeOff, Gauge, PlugZap, Plus, RefreshCw, Save, Trash2, Users } from "lucide-react"
import { toast } from "sonner"

import { EmptyState, Field, IconAction, SummaryBar, SummaryItem } from "@/components/console-kit"
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
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { errorMessage } from "@/lib/api"
import { cardActionClass, chipClass } from "@/lib/console-styles"
import { useDraft } from "@/lib/use-draft"
import { useAccountQuota } from "@/lib/use-account-quota"
import {
  formatClock,
  formatCompactTime,
  formatPlanExpiry,
  formatQuotaUSD,
  formatResetTime,
  formatTime,
} from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  Account,
  AccountQuota,
  AccountTestResponse,
  AccountsResponse,
  QuotaCaps,
  QuotaResponse,
} from "@/types"

// Unsaved rows get a client-side identity so the selection survives filtering
// and reordering before the account even exists on the server.
const draftIdPrefix = "draft_"
const isDraftId = (id: string) => id.startsWith(draftIdPrefix)

// Shown in a password field so a stored key looks like dots, not a truncated
// preview. The string never reaches account.key or the save payload.
const storedKeyMask = "00000000000000000000"

function keyFieldValue(
  account: Account,
  showKeys: boolean,
  revealed: Record<string, string>,
  focused: boolean,
): string {
  if (showKeys) return account.key || revealed[account.id] || ""
  if (account.key) return account.key
  if (account.hasKey && !focused) return storedKeyMask
  return ""
}

// selectionIndex maps the currently selected account onto a new list. Rows are
// matched by reference first and by identity second, so removing or filtering
// an earlier row never hands the choice to a different account.
function selectionIndex(accounts: Account[], selected: Account | undefined, fallback: number): number {
  if (selected) {
    const byReference = accounts.indexOf(selected)
    if (byReference >= 0) return byReference
    if (selected.id) {
      const byId = accounts.findIndex((account) => account.id === selected.id)
      if (byId >= 0) return byId
    }
  }
  return Math.min(Math.max(fallback, 0), Math.max(accounts.length - 1, 0))
}

interface AccountsPanelProps {
  data: AccountsResponse
  onSave: (value: AccountsResponse) => Promise<void>
  /** Tests a typed key, or the stored key of the account with this id. */
  onTest: (key: string, id?: string) => Promise<AccountTestResponse>
  onReveal: () => Promise<AccountsResponse>
  /** Reads plan utilization; refresh bypasses the short backend cache. */
  onQuota: (refresh?: boolean) => Promise<QuotaResponse>
}

// Base UI renders the raw value in the trigger unless it knows the labels.
const modeItems: Record<AccountsResponse["mode"], string> = {
  single: "单账号",
  roundrobin: "账号池轮询",
}

export function AccountsPanel({ data, onSave, onTest, onReveal, onQuota }: AccountsPanelProps) {
  const [draft, setDraft] = useDraft(data)
  // Revealed keys are cached together with the snapshot they came from. A new
  // snapshot (save or refresh) makes the cache stale by construction, so a
  // test can never send the key a previous snapshot had.
  const [reveal, setReveal] = useState<{
    snapshot: AccountsResponse
    keys: Record<string, string>
  } | null>(null)
  const showKeys = reveal !== null && reveal.snapshot === data
  const revealed = showKeys ? reveal.keys : {}
  const [revealing, setRevealing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState<number | null>(null)
  const {
    entries: quotas, loading: quotaLoading, updatedAt: quotaAt,
    refresh: refreshQuota, invalidate: invalidateQuotas,
  } = useAccountQuota(data.accounts, onQuota)
  const [keyFocused, setKeyFocused] = useState<string | null>(null)
  const draftSequence = useRef(0)

  const updateAccount = (index: number, patch: Partial<Account>) => {
    setDraft((current) => ({
      ...current,
      accounts: current.accounts.map((account, accountIndex) =>
        accountIndex === index ? { ...account, ...patch } : account,
      ),
    }))
  }

  const addAccount = () => {
    draftSequence.current += 1
    const draftID = `${draftIdPrefix}${draftSequence.current}`
    setDraft((current) => ({
      ...current,
      accounts: [
        ...current.accounts,
        {
          id: draftID,
          name: `账号${current.accounts.length + 1}`,
          key: "",
          keyPreview: "",
          hasKey: false,
          enabled: true,
        },
      ],
    }))
  }

  const removeAccount = (index: number) => {
    setDraft((current) => {
      const selected = current.accounts[current.active]
      const accounts = current.accounts.filter((_, accountIndex) => accountIndex !== index)
      return {
        ...current,
        accounts,
        active: selectionIndex(accounts, selected, current.active),
      }
    })
  }

  const save = async () => {
    // Rows the user left untouched keep their stored key, so only accounts
    // that never had one are dropped here.
    const kept = draft.accounts.filter((account) => account.key.trim() || account.hasKey)
    if (!kept.length) {
      toast.error("至少需要一个已填写 API Key 的账号")
      return
    }
    // Filtering out an empty row above the selection must not move the choice;
    // the index is recomputed against the list that is actually submitted.
    const selected = draft.accounts[draft.active]
    const active = selectionIndex(kept, selected, draft.active)
    // Draft identities are client-side only: the server treats an empty id as
    // a brand new account (or a legacy name match).
    const accounts = kept.map((account) => (isDraftId(account.id) ? { ...account, id: "" } : account))
    setSaving(true)
    try {
      await onSave({ ...draft, accounts, active })
      // Ordinary saves keep the meters visible. Changed keys cannot reuse
      // the previous account's plan, even if its redacted preview matches.
      invalidateQuotas(draft.accounts.filter((account) => account.key.trim()).map(({ id }) => id))
      void refreshQuota()
      toast.success("账号池已保存")
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setSaving(false)
    }
  }

  const testAccount = async (index: number) => {
    const account = draft.accounts[index]
    const typed = account.key.trim()
    const savedID = account.id.trim()
    if (!typed && (!savedID || !account.hasKey)) {
      toast.error("请先填写 API Key，或保存后再测试已保存的密钥")
      return
    }
    setTesting(index)
    try {
      // A typed key tests the unsaved edit. Otherwise the backend reads the
      // stored key by account id, which cannot go stale in the console.
      const result = await onTest(typed, typed ? undefined : savedID)
      if (result.ok) {
        toast.success(
          `${account.name || `账号${index + 1}`} 可用，耗时 ${result.ms} ms${result.note ? ` · ${result.note}` : ""}`,
        )
      } else {
        toast.error(result.error || "账号不可用")
      }
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setTesting(null)
    }
  }

  const toggleKeys = async () => {
    if (showKeys) {
      setReveal(null)
      return
    }
    setRevealing(true)
    try {
      const response = await onReveal()
      const keys: Record<string, string> = {}
      for (const account of response.accounts) {
        keys[account.id] = account.key
      }
      setReveal({ snapshot: data, keys })
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setRevealing(false)
    }
  }

  return (
    <Card className="@container/accounts">
      <CardHeader>
        <CardTitle>账号池</CardTitle>
        <CardDescription>
          管理 Cline Pass 账号与调度方式，并查看各周期的套餐用量；用量已满的账号在有其他可用账号时会被跳过。
        </CardDescription>
        <CardAction className={cardActionClass}>
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  variant="outline"
                  size="sm"
                  disabled={quotaLoading}
                  onClick={() => void refreshQuota()}
                />
              }
            >
              {quotaLoading ? (
                <RefreshCw className="animate-spin" data-icon="inline-start" />
              ) : (
                <Gauge data-icon="inline-start" />
              )}
              查询配额
            </TooltipTrigger>
            <TooltipContent>
              {quotaAt > 0 ? `上次查询 ${formatClock(quotaAt)}` : "读取各账号的套餐用量"}
            </TooltipContent>
          </Tooltip>
          <Button
            variant="outline"
            size="sm"
            disabled={revealing}
            onClick={() => void toggleKeys()}
          >
            {showKeys ? <EyeOff data-icon="inline-start" /> : <Eye data-icon="inline-start" />}
            {showKeys ? "隐藏密钥" : "显示密钥"}
          </Button>
          <Button variant="outline" size="sm" onClick={addAccount}>
            <Plus data-icon="inline-start" />
            添加账号
          </Button>
          <Button size="sm" onClick={save} disabled={saving}>
            {saving ? (
              <RefreshCw className="animate-spin" data-icon="inline-start" />
            ) : (
              <Save data-icon="inline-start" />
            )}
            保存
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="space-y-4">
        <SummaryBar className="py-2">
          <div className="flex items-center gap-2.5">
            <Label id="account-mode-label" className="text-muted-foreground text-xs font-normal">
              调度模式
            </Label>
            <Select
              value={draft.mode}
              items={modeItems}
              onValueChange={(value) =>
                setDraft((current) => ({
                  ...current,
                  mode: value as AccountsResponse["mode"],
                }))
              }
            >
              <SelectTrigger size="sm" aria-labelledby="account-mode-label" className="bg-card w-32">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {Object.entries(modeItems).map(([value, label]) => (
                  <SelectItem key={value} value={value}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <SummaryItem
            label="已配置"
            value={draft.accounts.filter((account) => account.key || account.hasKey).length}
            unit="个账号"
          />
          <SummaryItem
            label="已启用"
            value={draft.accounts.filter((account) => account.enabled).length}
            unit="个"
          />
          <span>
            {draft.mode === "roundrobin"
              ? "请求在所有已启用账号之间轮流分配。"
              : "请求优先使用当前账号，不可用时按列表顺序改用下一个。"}
          </span>
        </SummaryBar>

        {draft.accounts.length ? (
          <div className="grid grid-cols-1 items-start gap-3 @min-[1100px]/accounts:grid-cols-2">
            {draft.accounts.map((account, index) => {
              const stats = draft.stats[account.id]
              const isActive = draft.mode === "single" && draft.active === index
              const inPool = draft.mode === "roundrobin" && account.enabled
              const rowKey = account.id || `new-${index}`
              return (
                <article
                  key={account.id || `new-${index}`}
                  aria-label={account.name || `账号${index + 1}`}
                  className={cn(
                    "@container/account bg-card min-w-0 rounded-xl border transition-colors",
                    isActive && "border-primary/40 ring-1 ring-primary/10",
                    !account.enabled && "bg-muted/20",
                  )}
                >
                  <div className="grid gap-3 p-4 @min-[520px]/account:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
                    <Field label="账号名称" htmlFor={`${rowKey}-name`}>
                      <Input
                        id={`${rowKey}-name`}
                        value={account.name}
                        onChange={(event) => updateAccount(index, { name: event.target.value })}
                        placeholder="例如：主账号"
                        aria-label="账号名称"
                      />
                    </Field>
                    <Field label="API Key" htmlFor={`${rowKey}-key`}>
                      <div className="flex gap-1.5">
                        <Input
                          id={`${rowKey}-key`}
                          value={keyFieldValue(account, showKeys, revealed, keyFocused === rowKey)}
                          type={showKeys ? "text" : "password"}
                          autoComplete="off"
                          spellCheck={false}
                          placeholder={account.hasKey ? "" : "sk_..."}
                          aria-label="API Key"
                          data-secret="1"
                          onFocus={() => setKeyFocused(rowKey)}
                          onBlur={() => setKeyFocused((current) => (current === rowKey ? null : current))}
                          onChange={(event) => updateAccount(index, { key: event.target.value })}
                          className={cn("font-mono text-xs", !showKeys && "tracking-[0.18em]")}
                        />
                        <Button
                          variant="outline"
                          className="shrink-0"
                          disabled={testing === index}
                          onClick={() => testAccount(index)}
                        >
                          {testing === index ? (
                            <RefreshCw className="animate-spin" data-icon="inline-start" />
                          ) : (
                            <PlugZap data-icon="inline-start" />
                          )}
                          测试
                        </Button>
                      </div>
                    </Field>
                  </div>
                  {!isDraftId(account.id) && account.hasKey && (
                    <div className="border-t px-4 py-3">
                      <QuotaReadout quota={quotas[account.id]?.quota} loading={quotaLoading} refreshError={quotas[account.id]?.refreshError} />
                    </div>
                  )}
                  <div className="bg-muted/25 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-b-xl border-t px-4 py-2">
                    <div className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 text-xs">
                      {draft.mode === "single" && (
                        <label className={cn("flex cursor-pointer items-center gap-2", isActive && "text-primary font-medium")}>
                          <Checkbox
                            checked={isActive}
                            onCheckedChange={(checked) => {
                              if (checked) setDraft((current) => ({ ...current, active: index }))
                            }}
                            aria-label="设为当前账号"
                          />
                          {isActive ? "当前账号" : "设为当前"}
                        </label>
                      )}
                      {inPool && (
                        <Badge variant="secondary" className={chipClass}>参与轮询</Badge>
                      )}
                      <span>
                        累计请求 <span className="text-foreground font-medium tabular-nums">{(stats?.requests ?? 0).toLocaleString("zh-CN")}</span> 次
                      </span>
                      <span title={stats?.lastUsed ? `最近使用 ${formatTime(stats.lastUsed)}` : undefined}>
                        {stats?.lastUsed ? `最近使用 ${formatCompactTime(stats.lastUsed)}` : "尚未使用"}
                      </span>
                      {stats?.lastError && (
                        <Tooltip>
                          <TooltipTrigger render={<Badge variant="destructive" className={cn(chipClass, "cursor-help")} />}>
                            最近请求出错
                          </TooltipTrigger>
                          <TooltipContent className="max-w-md font-mono text-2xs break-all whitespace-pre-wrap">
                            {stats.lastError}
                          </TooltipContent>
                        </Tooltip>
                      )}
                    </div>
                    <div className="ml-auto flex h-7 items-center gap-1.5">
                      <label className="flex cursor-pointer items-center gap-1.5 pr-1 text-xs">
                        <Switch
                          checked={account.enabled}
                          onCheckedChange={(checked) => updateAccount(index, { enabled: checked })}
                          aria-label="启用账号"
                        />
                        <span className={cn(!account.enabled && "text-muted-foreground")}>
                          {account.enabled ? "已启用" : "已停用"}
                        </span>
                      </label>
                      <IconAction
                        label="删除账号"
                        variant="ghost"
                        size="icon-xs"
                        className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                        onClick={() => removeAccount(index)}
                      >
                        <Trash2 />
                      </IconAction>
                    </div>
                  </div>
                </article>
              )
            })}
          </div>
        ) : (
          <EmptyState
            icon={Users}
            title="尚未添加账号"
            description="添加 Cline Pass API Key 并保存后，代理即可开始转发请求。"
          />
        )}
      </CardContent>
    </Card>
  )
}

const quotaWindows = [
  { type: "five_hour", title: "5 小时", cap: (caps?: QuotaCaps) => caps?.fiveHour },
  { type: "weekly", title: "7 天", cap: (caps?: QuotaCaps) => caps?.weekly },
  { type: "monthly", title: "30 天", cap: (caps?: QuotaCaps) => caps?.monthly },
] as const

// Utilization colors: quiet below 70%, amber from 70%, red from 90%.
function quotaTone(percent: number) {
  if (percent >= 90) return { bar: "bg-destructive", text: "text-destructive" }
  if (percent >= 70) return { bar: "bg-amber-500", text: "text-amber-600 dark:text-amber-400" }
  return { bar: "bg-primary", text: "text-foreground" }
}

// Equal-width meters keep each percentage next to its window. Reset times are
// visible without hovering; the tooltip carries plan and cap details.
function QuotaReadout({ quota, loading, refreshError }: { quota?: AccountQuota; loading: boolean; refreshError?: string }) {
  return (
    <div className="space-y-2.5">
      <div className="text-muted-foreground flex min-h-4 flex-wrap items-center justify-between gap-x-2 gap-y-1 text-2xs">
        <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
          <CalendarClock className="size-3" aria-hidden="true" />
          套餐到期
          <span className="text-foreground tabular-nums">{formatPlanExpiry(quota?.currentPeriodEnd)}</span>
        </span>
        {refreshError || (quota && !quota.ok) ? (
          <Tooltip>
            <TooltipTrigger render={<span tabIndex={0} className="text-destructive cursor-help" />}>
              {refreshError ? "更新失败，显示上次数据" : "配额不可用"}
            </TooltipTrigger>
            <TooltipContent className="max-w-md whitespace-pre-wrap">
              {refreshError || quota?.error || "探测失败"}
            </TooltipContent>
          </Tooltip>
        ) : quota?.ok && quota.limits?.some((limit) => limit.percentUsed >= 100) ? (
          <span className="text-destructive">已用满，存在其他可用账号时将跳过</span>
        ) : (
          <span>{loading ? "更新中…" : !quota ? "尚未查询配额" : ""}</span>
        )}
      </div>
      <div className="grid min-w-0 grid-cols-1 gap-3 @min-[360px]/account:grid-cols-3">
      {quotaWindows.map((window) => {
        const limit = quota?.limits?.find((value) => value.type === window.type)
        const percent = limit?.percentUsed ?? 0
        const tone = quotaTone(percent)
        const details = [
          quota?.plan,
          `${window.title}上限 ${formatQuotaUSD(window.cap(quota?.caps))}`,
          limit?.resetsAt ? `${formatResetTime(limit.resetsAt)} 重置` : undefined,
        ].filter(Boolean).join(" · ")
        return (
          <Tooltip key={window.type}>
            <TooltipTrigger
              render={<div tabIndex={0} className="min-w-0 cursor-help space-y-1.5 rounded-sm outline-offset-4 focus-visible:outline-2 focus-visible:outline-ring" />}
            >
              <div className="flex items-baseline justify-between gap-2 text-xs">
                <span className="text-muted-foreground">{window.title}用量</span>
                <span className={cn("font-medium tabular-nums", tone.text)}>
                  {limit ? `${percent}%` : "—"}
                </span>
              </div>
              <span
                role="progressbar"
                aria-label={`${window.title}配额用量`}
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={limit ? Math.min(Math.max(percent, 0), 100) : undefined}
                aria-valuetext={limit ? `已用 ${percent}%` : "暂无用量数据"}
                className="bg-muted-foreground/15 block h-1.5 w-full overflow-hidden rounded-full"
              >
                <span
                  className={cn("block h-full rounded-full", tone.bar)}
                  style={{ width: `${Math.min(Math.max(percent, 0), 100)}%` }}
                />
              </span>
              <span className="text-muted-foreground block truncate text-2xs tabular-nums">
                {limit?.resetsAt ? `${formatResetTime(limit.resetsAt)} 重置` : "重置时间未知"}
              </span>
            </TooltipTrigger>
            <TooltipContent className="max-w-[calc(100vw-2rem)] whitespace-nowrap">
              <span className="min-w-0 truncate">{details}</span>
            </TooltipContent>
          </Tooltip>
        )
      })}
      </div>
    </div>
  )
}
