import { useMemo, useState } from "react"
import { Copy, Dices, Eye, EyeOff, KeyRound, Plus, RefreshCw, RotateCcw, Save, Trash2 } from "lucide-react"
import { toast } from "sonner"

import { EmptyState, Field, IconAction, NoteList, SummaryBar, SummaryItem } from "@/components/console-kit"
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
import { Switch } from "@/components/ui/switch"
import { errorMessage } from "@/lib/api"
import { cardActionClass, chipClass, warningChipClass } from "@/lib/console-styles"
import { formatCompactTime, formatTime } from "@/lib/format"
import { useDraft } from "@/lib/use-draft"
import { cn } from "@/lib/utils"
import type { AccountsResponse, KeysResponse, ProxyKeyDraft } from "@/types"

const ANY_ACCOUNT = "__any__"
const ANY_ACCOUNT_LABEL = "不限定（自动选择）"
const draftIdPrefix = "draft_"
const isDraftId = (id: string) => id.startsWith(draftIdPrefix)

// Both rows of a key share one column template, so every field lines up with
// the one above it.
const fieldGridClass =
  "grid gap-x-4 gap-y-3 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)_minmax(0,1fr)]"

interface KeysPanelProps {
  data: KeysResponse
  accounts: AccountsResponse
  proxyBase?: string
  onSave: (keys: ProxyKeyDraft[]) => Promise<KeysResponse>
  onReveal: () => Promise<KeysResponse>
  onReset: (id: string, all?: boolean) => Promise<KeysResponse>
}

// A fresh secret in the shape the operator asked for: sk- plus 48 hex
// characters, generated in the page so the value never has to travel to be
// minted.
function generateKey(): string {
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  return "sk-" + Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("")
}

function toDraft(item: KeysResponse["keys"][number], key: string): ProxyKeyDraft {
  return {
    id: item.id,
    name: item.name ?? "",
    key,
    enabled: item.enabled,
    accountId: item.accountId ?? ANY_ACCOUNT,
    spendLimitUsd: item.spendLimitUsd ?? 0,
    note: item.note ?? "",
    createdAt: item.createdAt ?? 0,
    // Counters ride along for display; the server owns their truth.
    requests: item.requests,
    spentUsd: item.spentUsd,
    lastUsed: item.lastUsed,
    keyPreview: item.keyPreview,
    hasKey: item.hasKey,
    dirty: false,
  }
}

// Cents once the amount reaches a dime, more digits below that so a handful of
// cheap requests does not read as $0.00; trailing zeros past the cents go.
function formatSpend(value: number): string {
  if (!value) return "$0.00"
  const digits = value >= 0.1 ? 2 : value >= 0.001 ? 4 : 6
  return "$" + value.toFixed(digits).replace(/(\.\d{2}\d*?)0+$/, "$1")
}

function SpendMeter({ spent, limit }: { spent: number; limit: number }) {
  if (limit <= 0) {
    return (
      <div className="flex h-8 items-center justify-between gap-2 text-sm tabular-nums">
        <span className="font-medium">{formatSpend(spent)}</span>
        <span className="text-muted-foreground text-xs">不限额</span>
      </div>
    )
  }
  const percent = Math.min(Math.round((spent / limit) * 100), 100)
  const tone =
    spent >= limit
      ? { bar: "bg-destructive", text: "text-destructive" }
      : percent >= 80
        ? { bar: "bg-amber-500", text: "text-amber-600 dark:text-amber-400" }
        : { bar: "bg-primary", text: "text-muted-foreground" }
  return (
    <div className="flex h-8 flex-col justify-center gap-1.5">
      <div className="flex items-baseline justify-between gap-2 text-xs tabular-nums">
        <span>
          <span className="text-foreground text-sm font-medium">{formatSpend(spent)}</span>
          <span className="text-muted-foreground"> / {formatSpend(limit)}</span>
        </span>
        <span className={tone.text}>{percent}%</span>
      </div>
      <span
        role="progressbar"
        aria-label="额度用量"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
        className="bg-muted-foreground/15 block h-1.5 w-full overflow-hidden rounded-full"
      >
        <span className={cn("block h-full rounded-full", tone.bar)} style={{ width: `${percent}%` }} />
      </span>
    </div>
  )
}

export function KeysPanel({ data, accounts, proxyBase, onSave, onReveal, onReset }: KeysPanelProps) {
  // useDraft compares the source by identity, so the mapped rows have to be
  // stable across renders - otherwise every keystroke would be replaced by a
  // fresh copy of the server snapshot.
  const source = useMemo<ProxyKeyDraft[]>(
    () => data.keys.map((item) => toDraft(item, "")),
    [data],
  )
  const [draft, setDraft] = useDraft<ProxyKeyDraft[]>(source)
  const [saving, setSaving] = useState(false)
  const [revealing, setRevealing] = useState(false)
  const [revealed, setRevealed] = useState(false)
  const [resetting, setResetting] = useState<string | null>(null)

  const enabledAccounts = accounts.accounts.filter((account) => account.enabled)
  const accountName = (id: string) =>
    accounts.accounts.find((account) => account.id === id)?.name ?? "已删除的账号"
  const accountUsable = (id: string) =>
    accounts.accounts.some((account) => account.id === id && account.enabled)

  const unsaved =
    draft.length !== source.length ||
    draft.some((row, index) => row.dirty || row.id !== source[index]?.id)
  const enabledCount = draft.filter((row) => row.enabled).length
  const totalSpent = draft.reduce((total, row) => total + (row.spentUsd || 0), 0)
  const atCapacity = data.maxKeys !== undefined && draft.length >= data.maxKeys

  const update = (index: number, patch: Partial<ProxyKeyDraft>) => {
    setDraft((current) =>
      current.map((row, rowIndex) => (rowIndex === index ? { ...row, ...patch, dirty: true } : row)),
    )
  }

  const addRow = () => {
    setDraft((current) => [
      ...current,
      {
        id: draftIdPrefix + (current.length + 1) + "_" + Date.now().toString(36),
        name: "",
        key: generateKey(),
        enabled: true,
        accountId: ANY_ACCOUNT,
        spendLimitUsd: 0,
        note: "",
        createdAt: 0,
        requests: 0,
        spentUsd: 0,
        lastUsed: 0,
        keyPreview: "",
        hasKey: true,
        dirty: true,
      },
    ])
  }

  const removeRow = (index: number) => {
    setDraft((current) => current.filter((_, rowIndex) => rowIndex !== index))
  }

  const toggleReveal = async () => {
    if (revealed) {
      // Edited rows keep what the operator typed; the others go back to masked.
      setDraft((current) => current.map((row) => (row.dirty ? row : { ...row, key: "" })))
      setRevealed(false)
      return
    }
    setRevealing(true)
    try {
      const response = await onReveal()
      setDraft(response.keys.map((item) => toDraft(item, item.key ?? "")))
      setRevealed(true)
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setRevealing(false)
    }
  }

  const save = async () => {
    setSaving(true)
    try {
      const payload: ProxyKeyDraft[] = draft.map((row) => ({
        ...row,
        name: row.name.trim(),
        key: row.key.trim(),
        spendLimitUsd: Number.isFinite(row.spendLimitUsd) ? Math.max(row.spendLimitUsd, 0) : 0,
        accountId: row.accountId === ANY_ACCOUNT ? "" : row.accountId,
      }))
      const response = await onSave(payload)
      setDraft(response.keys.map((item) => toDraft(item, "")))
      setRevealed(false)
      toast.success("代理密钥已保存")
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setSaving(false)
    }
  }

  const resetUsage = async (row: ProxyKeyDraft) => {
    if (isDraftId(row.id)) return
    setResetting(row.id)
    try {
      const response = await onReset(row.id)
      setDraft(response.keys.map((item) => toDraft(item, "")))
      setRevealed(false)
      toast.success("已重置该密钥的用量")
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setResetting(null)
    }
  }

  const copyKey = async (value: string) => {
    if (!value) {
      toast.error("密钥已隐藏，请先点击「显示密钥」")
      return
    }
    try {
      await navigator.clipboard.writeText(value)
      toast.success("密钥已复制")
    } catch {
      toast.error("无法访问剪贴板")
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>代理密钥</CardTitle>
        <CardDescription>
          为下游客户端签发独立的访问密钥，可分别限定使用的账号与累计消费上限。
        </CardDescription>
        <CardAction className={cardActionClass}>
          <Button
            variant="outline"
            size="sm"
            onClick={() => void toggleReveal()}
            disabled={revealing || draft.length === 0}
          >
            {revealing ? (
              <RefreshCw className="animate-spin" data-icon="inline-start" />
            ) : revealed ? (
              <EyeOff data-icon="inline-start" />
            ) : (
              <Eye data-icon="inline-start" />
            )}
            {revealed ? "隐藏密钥" : "显示密钥"}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={addRow}
            disabled={atCapacity}
            title={atCapacity ? `最多可签发 ${data.maxKeys} 个密钥` : undefined}
          >
            <Plus data-icon="inline-start" />
            新增客户端密钥
          </Button>
          <Button size="sm" onClick={() => void save()} disabled={saving}>
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
        {draft.length > 0 && (
          <SummaryBar>
            <SummaryItem label="共" value={draft.length} unit="个密钥" />
            <SummaryItem label="已启用" value={enabledCount} unit="个" />
            <SummaryItem label="累计消费" value={formatSpend(totalSpent)} />
            {unsaved && (
              <span className="ml-auto text-amber-600 dark:text-amber-400">有未保存的更改</span>
            )}
          </SummaryBar>
        )}

        {draft.length === 0 ? (
          <EmptyState
            icon={KeyRound}
            title="尚未签发客户端密钥"
            description="新增后将自动生成以 sk- 开头的随机密钥，保存即可生效。"
          />
        ) : (
          <div className="space-y-3">
            {draft.map((row, index) => {
              const draftRow = isDraftId(row.id)
              const exhausted = row.spendLimitUsd > 0 && row.spentUsd >= row.spendLimitUsd
              const bindingBroken = row.accountId !== ANY_ACCOUNT && !accountUsable(row.accountId)
              const ids = {
                name: `${row.id}-name`,
                key: `${row.id}-key`,
                account: `${row.id}-account`,
                limit: `${row.id}-limit`,
                note: `${row.id}-note`,
              }
              return (
                <article
                  key={row.id}
                  aria-label={row.name || `密钥 ${index + 1}`}
                  className={cn(
                    "bg-card min-w-0 rounded-xl border transition-colors",
                    !row.enabled && "bg-muted/20",
                  )}
                >
                  <div className="space-y-3 p-4">
                    <div className={fieldGridClass}>
                      <Field label="名称" htmlFor={ids.name}>
                        <Input
                          id={ids.name}
                          value={row.name}
                          onChange={(event) => update(index, { name: event.target.value })}
                          placeholder="使用者或用途"
                        />
                      </Field>
                      <Field label="密钥" htmlFor={ids.key}>
                        <div className="flex gap-1.5">
                          <Input
                            id={ids.key}
                            value={row.key}
                            onChange={(event) => update(index, { key: event.target.value })}
                            placeholder={row.hasKey ? row.keyPreview || "已保存" : "sk-..."}
                            aria-label="客户端密钥"
                            data-secret="1"
                            autoComplete="off"
                            spellCheck={false}
                            className="font-mono text-xs"
                          />
                          <IconAction
                            label="生成新的随机密钥"
                            onClick={() => update(index, { key: generateKey() })}
                          >
                            <Dices />
                          </IconAction>
                          <IconAction label="复制密钥" onClick={() => void copyKey(row.key)}>
                            <Copy />
                          </IconAction>
                        </div>
                      </Field>
                      <Field label="绑定账号" labelId={ids.account}>
                        <Select
                          value={row.accountId}
                          onValueChange={(value) => update(index, { accountId: value ?? ANY_ACCOUNT })}
                        >
                          <SelectTrigger aria-labelledby={ids.account} className="w-full">
                            {/* The value is an id; the trigger must show the name. */}
                            <SelectValue placeholder={ANY_ACCOUNT_LABEL}>
                              {row.accountId === ANY_ACCOUNT ? ANY_ACCOUNT_LABEL : accountName(row.accountId)}
                            </SelectValue>
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value={ANY_ACCOUNT}>{ANY_ACCOUNT_LABEL}</SelectItem>
                            {enabledAccounts.map((account) => (
                              <SelectItem key={account.id} value={account.id}>
                                {account.name}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Field>
                    </div>

                    <div className={fieldGridClass}>
                      <Field label="额度上限（USD）" htmlFor={ids.limit}>
                        <div className="relative">
                          <span className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-sm">
                            $
                          </span>
                          <Input
                            id={ids.limit}
                            type="number"
                            inputMode="decimal"
                            min={0}
                            step="0.5"
                            value={row.spendLimitUsd === 0 ? "" : row.spendLimitUsd}
                            onChange={(event) =>
                              update(index, { spendLimitUsd: Number(event.target.value) || 0 })
                            }
                            placeholder="不限"
                            className="pl-6 tabular-nums"
                          />
                        </div>
                      </Field>
                      <Field label="累计消费">
                        <SpendMeter spent={row.spentUsd} limit={row.spendLimitUsd} />
                      </Field>
                      <Field label="备注" htmlFor={ids.note}>
                        <Input
                          id={ids.note}
                          value={row.note}
                          onChange={(event) => update(index, { note: event.target.value })}
                          placeholder="可选"
                        />
                      </Field>
                    </div>
                  </div>

                  <div className="bg-muted/25 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-b-xl border-t px-4 py-2">
                    <div className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 text-xs">
                      {draftRow ? (
                        <span>尚未保存</span>
                      ) : (
                        <>
                          <span>
                            累计请求{" "}
                            <span className="text-foreground font-medium tabular-nums">
                              {row.requests.toLocaleString("zh-CN")}
                            </span>{" "}
                            次
                          </span>
                          <span title={row.lastUsed ? `最近使用 ${formatTime(row.lastUsed)}` : undefined}>
                            {row.lastUsed ? `最近使用 ${formatCompactTime(row.lastUsed)}` : "尚未使用"}
                          </span>
                        </>
                      )}
                      {row.dirty && !draftRow && (
                        <Badge variant="secondary" className={chipClass}>
                          已修改
                        </Badge>
                      )}
                      {exhausted && (
                        <Badge variant="outline" className={warningChipClass}>
                          额度已用尽
                        </Badge>
                      )}
                      {bindingBroken && (
                        <Badge variant="outline" className={warningChipClass}>
                          绑定账号不可用
                        </Badge>
                      )}
                    </div>
                    <div className="ml-auto flex h-7 items-center gap-1.5">
                      <label className="flex cursor-pointer items-center gap-1.5 pr-1 text-xs">
                        <Switch
                          checked={row.enabled}
                          onCheckedChange={(checked) => update(index, { enabled: checked })}
                          aria-label="启用密钥"
                        />
                        <span className={cn(!row.enabled && "text-muted-foreground")}>
                          {row.enabled ? "已启用" : "已停用"}
                        </span>
                      </label>
                      <Button
                        variant="ghost"
                        size="xs"
                        className="text-muted-foreground"
                        disabled={
                          draftRow || resetting === row.id || (row.spentUsd <= 0 && row.requests === 0)
                        }
                        onClick={() => void resetUsage(row)}
                      >
                        {resetting === row.id ? (
                          <RefreshCw className="animate-spin" data-icon="inline-start" />
                        ) : (
                          <RotateCcw data-icon="inline-start" />
                        )}
                        重置用量
                      </Button>
                      <IconAction
                        label="删除密钥"
                        variant="ghost"
                        size="icon-xs"
                        className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                        onClick={() => removeRow(index)}
                      >
                        <Trash2 />
                      </IconAction>
                    </div>
                  </div>
                </article>
              )
            })}
          </div>
        )}

        <NoteList title="使用说明">
            <li>
              客户端的 Base URL 填写
              {proxyBase ? (
                <code className="text-foreground bg-background mx-1 rounded border px-1 py-0.5 font-mono">
                  {proxyBase}
                </code>
              ) : (
                "本代理的地址"
              )}
              ，API Key 填写此处的密钥，使用 OpenAI 兼容接口调用模型。
            </li>
            <li>绑定账号后，该密钥仅使用指定账号；账号不可用时请求直接失败，不会切换到其他账号。</li>
            <li>累计消费以上游返回的实际费用为准，达到额度上限后请求返回 HTTP 429；留空表示不限额。</li>
            <li>保存后的密钥仅显示首尾几位，点击「显示密钥」可查看完整内容；删除或停用需保存后生效。</li>
        </NoteList>
      </CardContent>
    </Card>
  )
}
