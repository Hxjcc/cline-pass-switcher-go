import { useState } from "react"
import { Eye, EyeOff, KeyRound, Plus, RefreshCw, Save, Trash2 } from "lucide-react"
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
import { useDraft } from "@/lib/use-draft"
import { formatTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Account, AccountTestResponse, AccountsResponse } from "@/types"

const chipClass = "h-5 px-1.5 py-0 text-2xs"

interface AccountsPanelProps {
  data: AccountsResponse
  onSave: (value: AccountsResponse) => Promise<void>
  onTest: (key: string) => Promise<AccountTestResponse>
  onReveal: () => Promise<AccountsResponse>
}

// Base UI renders the raw value in the trigger unless it knows the labels.
const modeItems: Record<AccountsResponse["mode"], string> = {
  single: "单账号",
  roundrobin: "账号池轮询",
}

export function AccountsPanel({ data, onSave, onTest, onReveal }: AccountsPanelProps) {
  const [draft, setDraft] = useDraft(data)
  const [showKeys, setShowKeys] = useState(false)
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  const [revealing, setRevealing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState<number | null>(null)

  const updateAccount = (index: number, patch: Partial<Account>) => {
    setDraft((current) => ({
      ...current,
      accounts: current.accounts.map((account, accountIndex) =>
        accountIndex === index ? { ...account, ...patch } : account,
      ),
    }))
  }

  const addAccount = () => {
    setDraft((current) => ({
      ...current,
      accounts: [
        ...current.accounts,
        {
          id: "",
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
      const accounts = current.accounts.filter((_, accountIndex) => accountIndex !== index)
      return {
        ...current,
        accounts,
        active: Math.min(current.active, Math.max(0, accounts.length - 1)),
      }
    })
  }

  const save = async () => {
    // Rows the user left untouched keep their stored key, so only accounts
    // that never had one are dropped here.
    const accounts = draft.accounts.filter((account) => account.key.trim() || account.hasKey)
    if (!accounts.length) {
      toast.error("至少需要一个填写了 key 的账号")
      return
    }
    setSaving(true)
    try {
      await onSave({ ...draft, accounts })
      toast.success("账号池已保存")
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setSaving(false)
    }
  }

  const testAccount = async (index: number) => {
    const account = draft.accounts[index]
    const key = account.key.trim() || revealed[account.id] || ""
    if (!key) {
      toast.error("请先显示该账号的密钥，或填写一个新的 Key")
      return
    }
    setTesting(index)
    try {
      const result = await onTest(key)
      if (result.ok) {
        toast.success(
          `${account.name || `账号${index + 1}`} 可用 · ${result.ms} ms${result.note ? ` · ${result.note}` : ""}`,
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
      setShowKeys(false)
      setRevealed({})
      return
    }
    setRevealing(true)
    try {
      const response = await onReveal()
      const keys: Record<string, string> = {}
      for (const account of response.accounts) {
        keys[account.id] = account.key
      }
      setRevealed(keys)
      setShowKeys(true)
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setRevealing(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>账号池</CardTitle>
        <CardDescription>单账号或轮询模式，所有请求实时读取当前配置。</CardDescription>
        <CardAction className="flex items-center gap-2">
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
        <div className="flex flex-wrap items-end gap-4">
          <div className="space-y-1.5">
            <Label>调度模式</Label>
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
              <SelectTrigger className="w-48">
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
          <div className="text-muted-foreground flex h-8 -translate-y-1.5 items-center gap-2 text-sm">
            <KeyRound className="size-3.5" />
            {draft.accounts.filter((account) => account.key || account.hasKey).length} 个已配置账号
          </div>
        </div>

        {draft.accounts.length ? (
          <div className="space-y-2">
            {draft.accounts.map((account, index) => {
              const stats = draft.stats[account.name]
              const isActive = draft.mode === "single" && draft.active === index
              const inPool = draft.mode === "roundrobin" && account.enabled
              return (
                <div
                  key={account.id || `new-${index}`}
                  className={cn(
                    "flex items-center gap-3 rounded-lg border px-3 py-2.5",
                    isActive && "border-primary/30 bg-primary/[0.04] dark:bg-primary/[0.07]",
                    !account.enabled && "bg-muted/40",
                  )}
                >
                  {draft.mode === "single" && (
                    <label className="flex shrink-0 items-center gap-2 text-sm">
                      <Checkbox
                        checked={isActive}
                        onCheckedChange={(checked) => {
                          if (checked) {
                            setDraft((current) => ({ ...current, active: index }))
                          }
                        }}
                        aria-label="设为当前账号"
                      />
                      当前
                    </label>
                  )}
                  <div className="min-w-0 flex-1 space-y-1.5">
                    <div className="flex flex-wrap items-center gap-2">
                      <Input
                        value={account.name}
                        onChange={(event) => updateAccount(index, { name: event.target.value })}
                        placeholder="名称"
                        aria-label="账号名称"
                        className="h-8 w-36"
                      />
                      <Input
                        value={showKeys ? account.key || revealed[account.id] || "" : account.key}
                        type={showKeys ? "text" : "password"}
                        autoComplete="off"
                        placeholder={account.hasKey ? `已保存 ${account.keyPreview}` : "Cline API Key"}
                        aria-label="API Key"
                        onChange={(event) => updateAccount(index, { key: event.target.value })}
                        className="h-8 min-w-64 flex-1 font-mono text-xs"
                      />
                      <label className="flex h-8 items-center gap-2 pl-1 text-sm">
                        <Switch
                          checked={account.enabled}
                          onCheckedChange={(checked) => updateAccount(index, { enabled: checked })}
                          aria-label="启用账号"
                        />
                        启用
                      </label>
                      <div className="flex items-center gap-1">
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={testing === index}
                          onClick={() => testAccount(index)}
                        >
                          {testing === index && (
                            <RefreshCw className="animate-spin" data-icon="inline-start" />
                          )}
                          测试
                        </Button>
                        <Tooltip>
                          <TooltipTrigger
                            render={
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                className="text-muted-foreground hover:text-destructive"
                                aria-label="删除账号"
                                onClick={() => removeAccount(index)}
                              />
                            }
                          >
                            <Trash2 />
                          </TooltipTrigger>
                          <TooltipContent>删除账号</TooltipContent>
                        </Tooltip>
                      </div>
                    </div>
                    <div className="text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
                      {isActive && (
                        <Badge className={chipClass}>当前使用</Badge>
                      )}
                      {inPool && (
                        <Badge variant="secondary" className={chipClass}>
                          参与轮询
                        </Badge>
                      )}
                      {!account.enabled && (
                        <Badge variant="outline" className={chipClass}>
                          已停用
                        </Badge>
                      )}
                      <span>
                        <span className="font-mono tabular-nums">{stats?.requests ?? 0}</span> 次请求
                      </span>
                      <span aria-hidden>·</span>
                      <span>最近使用 {formatTime(stats?.lastUsed)}</span>
                      {stats?.lastError && (
                        <Tooltip>
                          <TooltipTrigger
                            render={
                              <Badge
                                variant="destructive"
                                className={cn(chipClass, "cursor-help")}
                              />
                            }
                          >
                            最近一次出错
                          </TooltipTrigger>
                          <TooltipContent className="max-w-md font-mono text-2xs break-all whitespace-pre-wrap">
                            {stats.lastError}
                          </TooltipContent>
                        </Tooltip>
                      )}
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        ) : (
          <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-10 text-center">
            <div className="bg-muted flex size-10 items-center justify-center rounded-full">
              <KeyRound className="text-muted-foreground size-5" />
            </div>
            <div className="space-y-1">
              <div className="text-sm font-medium">还没有账号</div>
              <div className="text-muted-foreground text-sm">
                添加一个 Cline API Key 并保存后，代理即可开始转发请求。
              </div>
            </div>
            <Button variant="outline" size="sm" onClick={addAccount}>
              <Plus data-icon="inline-start" />
              添加账号
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
