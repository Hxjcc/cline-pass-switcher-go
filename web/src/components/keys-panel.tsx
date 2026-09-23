import { useMemo, useState } from "react"
import { Copy, Dices, KeyRound, Plus, RefreshCw, Save, Trash2 } from "lucide-react"
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
import { Switch } from "@/components/ui/switch"
import { errorMessage } from "@/lib/api"
import { formatCost, formatTime } from "@/lib/format"
import { useDraft } from "@/lib/use-draft"
import type { AccountsResponse, KeysResponse, ProxyKeyDraft } from "@/types"

const ANY_ACCOUNT = "__any__"
const draftIdPrefix = "draft_"

interface KeysPanelProps {
  data: KeysResponse
  accounts: AccountsResponse
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

export function KeysPanel({ data, accounts, onSave, onReveal, onReset }: KeysPanelProps) {
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
  const [resetting, setResetting] = useState<string | null>(null)

  const enabledAccounts = accounts.accounts.filter((account) => account.enabled)
  const accountName = (id: string) =>
    accounts.accounts.find((account) => account.id === id)?.name ?? "已删除的账号"

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

  const reveal = async () => {
    setRevealing(true)
    try {
      const response = await onReveal()
      setDraft(
        response.keys.map((item) => toDraft(item, item.key ?? "")),
      )
      toast.success("已显示所有密钥")
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
      toast.success("代理密钥已保存")
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setSaving(false)
    }
  }

  const resetUsage = async (row: ProxyKeyDraft) => {
    if (row.id.startsWith(draftIdPrefix)) return
    setResetting(row.id)
    try {
      const response = await onReset(row.id)
      setDraft(response.keys.map((item) => toDraft(item, "")))
      toast.success("已清零该密钥的用量")
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setResetting(null)
    }
  }

  const copyKey = async (value: string) => {
    if (!value) {
      toast.error("先显示或重新生成密钥")
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
          发给别人的客户端密钥。可以绑定某个账号（只用那一个，不做故障转移）并限制累计消费，超限直接拒绝。
        </CardDescription>
        <CardAction className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => void reveal()} disabled={revealing}>
            {revealing ? <RefreshCw className="animate-spin" data-icon="inline-start" /> : <KeyRound data-icon="inline-start" />}
            显示密钥
          </Button>
          <Button variant="outline" size="sm" onClick={addRow}>
            <Plus data-icon="inline-start" />
            新增客户端密钥
          </Button>
          <Button size="sm" onClick={() => void save()} disabled={saving}>
            {saving ? <RefreshCw className="animate-spin" data-icon="inline-start" /> : <Save data-icon="inline-start" />}
            保存
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="space-y-3">
        {draft.length === 0 && (
          <div className="text-muted-foreground rounded-lg border border-dashed p-6 text-center text-sm">
            还没有客户端密钥。点「新增客户端密钥」会直接生成一个 sk- 开头的随机密钥。
          </div>
        )}
        {draft.map((row, index) => {
          const spent = row.spentUsd
          const limit = row.spendLimitUsd
          const exhausted = limit > 0 && spent >= limit
          return (
            <div key={row.id} className="space-y-3 rounded-lg border p-3">
              <div className="flex flex-wrap items-end gap-3">
                <label className="w-40 space-y-1.5">
                  <span className="text-muted-foreground text-xs">名称</span>
                  <Input
                    value={row.name}
                    onChange={(event) => update(index, { name: event.target.value })}
                    placeholder="给谁用"
                    className="h-8"
                  />
                </label>
                <label className="min-w-[18rem] flex-1 space-y-1.5">
                  <span className="text-muted-foreground text-xs">密钥</span>
                  <div className="flex gap-1.5">
                    <Input
                      value={row.key}
                      onChange={(event) => update(index, { key: event.target.value })}
                      placeholder={row.hasKey ? row.keyPreview || "已保存" : "sk-..."}
                      aria-label="客户端密钥"
                      className="h-8 font-mono text-xs"
                    />
                    <Button
                      variant="outline"
                      size="sm"
                      className="h-8"
                      onClick={() => update(index, { key: generateKey() })}
                      title="随机生成一个 sk- 开头的密钥"
                    >
                      <Dices data-icon="inline-start" />
                      随机
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      className="h-8"
                      onClick={() => void copyKey(row.key)}
                      title="复制密钥"
                    >
                      <Copy data-icon="inline-start" />
                    </Button>
                  </div>
                </label>
              </div>

              <div className="flex flex-wrap items-end gap-3">
                <label className="w-56 space-y-1.5">
                  <span className="text-muted-foreground text-xs">绑定账号</span>
                  <Select
                    value={row.accountId}
                    onValueChange={(value) => update(index, { accountId: value ?? ANY_ACCOUNT })}
                  >
                    <SelectTrigger size="sm" className="h-8 w-full">
                      {/* The value is an id; the trigger must show the name. */}
                      <SelectValue placeholder="不限账号">
                        {row.accountId === ANY_ACCOUNT ? "不限账号（自动选）" : accountName(row.accountId)}
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={ANY_ACCOUNT}>不限账号（自动选）</SelectItem>
                      {enabledAccounts.map((account) => (
                        <SelectItem key={account.id} value={account.id}>
                          {account.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </label>
                <label className="w-40 space-y-1.5">
                  <span className="text-muted-foreground text-xs">额度上限（USD，0 = 不限）</span>
                  <Input
                    type="number"
                    min={0}
                    step="0.5"
                    value={row.spendLimitUsd === 0 ? "" : row.spendLimitUsd}
                    onChange={(event) =>
                      update(index, { spendLimitUsd: Number(event.target.value) || 0 })
                    }
                    placeholder="不限"
                    className="h-8"
                  />
                </label>
                <div className="text-muted-foreground space-y-1 text-xs">
                  <div>
                    已用 {formatCost(spent)}
                    {limit > 0 ? ` / ${formatCost(limit)}` : " / 不限"}
                  </div>
                  <div>
                    {row.requests} 次请求
                    {row.lastUsed ? ` · 最近 ${formatTime(row.lastUsed)}` : ""}
                  </div>
                </div>
                {exhausted && (
                  <Badge variant="outline" className="h-5 border-amber-200 bg-amber-50 px-1.5 text-2xs text-amber-700">
                    额度已用尽
                  </Badge>
                )}
                {row.accountId !== ANY_ACCOUNT && !accounts.accounts.some((account) => account.id === row.accountId && account.enabled) && (
                  <Badge variant="outline" className="h-5 border-amber-200 bg-amber-50 px-1.5 text-2xs text-amber-700">
                    绑定账号不可用
                  </Badge>
                )}
                <div className="ml-auto flex items-center gap-3">
                  <label className="flex items-center gap-1.5 text-xs">
                    <Switch
                      checked={row.enabled}
                      onCheckedChange={(checked) => update(index, { enabled: checked })}
                      aria-label="启用密钥"
                    />
                    启用
                  </label>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-8"
                    disabled={resetting === row.id || row.id.startsWith(draftIdPrefix) || spent <= 0}
                    onClick={() => void resetUsage(row)}
                  >
                    <RefreshCw
                      className={resetting === row.id ? "animate-spin" : undefined}
                      data-icon="inline-start"
                    />
                    清零用量
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-8"
                    aria-label="删除密钥"
                    onClick={() =>
                      setDraft((current) => current.filter((_, rowIndex) => rowIndex !== index))
                    }
                  >
                    <Trash2 />
                  </Button>
                </div>
              </div>

              <Input
                value={row.note}
                onChange={(event) => update(index, { note: event.target.value })}
                placeholder="备注（可选）"
                className="h-8"
              />
            </div>
          )
        })}

        <Alert>
          <KeyRound />
          <AlertTitle>客户端怎么用</AlertTitle>
          <AlertDescription>
            把这串密钥填进客户端即可：Base URL 不变，模型调用走 OpenAI 兼容接口。绑定账号的密钥只会用到那一个账号；
            额度按上游返回的实际费用累计（上游没报费用的请求不计入）。
          </AlertDescription>
        </Alert>
      </CardContent>
    </Card>
  )
}
