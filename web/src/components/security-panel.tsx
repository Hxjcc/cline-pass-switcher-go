import { useState } from "react"
import { Check, Copy, Dices, Eye, EyeOff, Globe2, KeyRound, Save, ShieldCheck } from "lucide-react"
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
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { errorMessage } from "@/lib/api"
import { useDraft } from "@/lib/use-draft"
import { cn } from "@/lib/utils"
import type { EffectiveSetting, SecurityResponse } from "@/types"

// The badge tells the operator which layer supplied a value, which is the only
// way to tell "my .env change took effect" from "an old config.json entry or a
// built-in default is still in force".
const sourceLabels: Record<string, { label: string; className: string }> = {
  env: {
    label: "环境变量",
    className:
      "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-300",
  },
  config: {
    label: "config.json",
    className: "border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-900 dark:bg-sky-950 dark:text-sky-300",
  },
  default: {
    label: "内置默认",
    className: "",
  },
  builtin: {
    label: "代码常量",
    className: "",
  },
}

function SettingsTable({ settings }: { settings: EffectiveSetting[] }) {
  return (
    <div className="divide-y rounded-lg border">
      {settings.map((row) => {
        const source = sourceLabels[row.source] ?? { label: row.source, className: "" }
        return (
          <div key={row.key} className="flex items-center gap-3 px-3 py-2">
            <span className="min-w-0 flex-1 truncate text-sm">{row.label}</span>
            <span className="font-mono text-xs">{row.value}</span>
            <Badge variant="outline" className={cn("h-5 px-1.5 py-0 text-2xs", source.className)}>
              {source.label}
            </Badge>
          </div>
        )
      })}
    </div>
  )
}

interface SecurityPanelProps {
  data: SecurityResponse
  proxyBase: string
  onSave: (
    value: Pick<SecurityResponse, "proxyKey" | "adminKey" | "publicBaseUrl" | "exposeCatalog">,
  ) => Promise<SecurityResponse>
}

// Same shape the keys panel mints: sk- plus 48 hex characters, generated in the
// page.
function randomKey(): string {
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  return "sk-" + Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("")
}

export function SecurityPanel({ data, proxyBase, onSave }: SecurityPanelProps) {
  const [draft, setDraft] = useDraft(data)
  const [showKey, setShowKey] = useState(false)
  const [showAdminKey, setShowAdminKey] = useState(false)
  const [saving, setSaving] = useState(false)
  const [copied, setCopied] = useState(false)
  const adminKeyConfigured = draft.adminKey.trim() !== ""

  const copyProxyBase = async () => {
    try {
      await navigator.clipboard.writeText(proxyBase)
      setCopied(true)
      toast.success("代理地址已复制")
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      toast.error("无法访问剪贴板")
    }
  }

  const save = async () => {
    setSaving(true)
    try {
      const saved = await onSave({
        proxyKey: draft.proxyKey.trim(),
        adminKey: draft.adminKey.trim(),
        publicBaseUrl: draft.publicBaseUrl.trim(),
        exposeCatalog: draft.exposeCatalog,
      })
      setDraft(saved)
      toast.success(saved.authRequired ? "访问设置已保存，鉴权已开启" : "访问设置已保存，鉴权已关闭")
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_360px]">
      <Card>
        <CardHeader>
          <CardTitle>访问与安全</CardTitle>
          <CardDescription>控制下游客户端使用的代理密钥与公网地址。</CardDescription>
          <CardAction>
            <Button size="sm" onClick={save} disabled={saving}>
              <Save data-icon="inline-start" />
              {saving ? "保存中" : "保存设置"}
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="space-y-5">
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label htmlFor="admin-key">管理密钥（本控制台）</Label>
              <Badge variant={adminKeyConfigured ? "default" : "outline"}>
                {adminKeyConfigured ? "独立密钥" : "沿用代理主密钥"}
              </Badge>
            </div>
            <div className="flex gap-2">
              <Input
                id="admin-key"
                data-secret="1"
                type={showAdminKey ? "text" : "password"}
                value={draft.adminKey}
                onChange={(event) =>
                  setDraft((current) => ({ ...current, adminKey: event.target.value }))
                }
                placeholder="留空 = 用下面的代理主密钥登录"
                className="font-mono"
                autoComplete="new-password"
              />
              <Button
                variant="outline"
                size="icon"
                onClick={() => setShowAdminKey((value) => !value)}
                aria-label={showAdminKey ? "隐藏管理密钥" : "显示管理密钥"}
              >
                {showAdminKey ? <EyeOff /> : <Eye />}
              </Button>
              <Button
                variant="outline"
                size="icon"
                onClick={() => setDraft((current) => ({ ...current, adminKey: randomKey() }))}
                aria-label="随机生成管理密钥"
                title="随机生成一个 sk- 开头的密钥"
              >
                <Dices />
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              设了它以后，只有这个密钥能打开控制台；代理主密钥和代理密钥只能调用模型，拿不到账号列表。
            </p>
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label htmlFor="proxy-key">代理主密钥（客户端）</Label>
              <Badge variant={draft.proxyKey ? "default" : "outline"}>
                {draft.proxyKey ? "鉴权已开启" : "鉴权已关闭"}
              </Badge>
            </div>
            <div className="flex gap-2">
              <Input
                id="proxy-key"
                data-secret="1"
                type={showKey ? "text" : "password"}
                value={draft.proxyKey}
                onChange={(event) =>
                  setDraft((current) => ({ ...current, proxyKey: event.target.value }))
                }
                placeholder="留空表示不鉴权"
                className="font-mono"
                autoComplete="new-password"
              />
              <Button
                variant="outline"
                size="icon"
                onClick={() => setShowKey((value) => !value)}
                aria-label={showKey ? "隐藏代理密钥" : "显示代理密钥"}
              >
                {showKey ? <EyeOff /> : <Eye />}
              </Button>
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="public-base">公网代理地址</Label>
            <div className="relative">
              <Globe2 className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
              <Input
                id="public-base"
                value={draft.publicBaseUrl}
                onChange={(event) =>
                  setDraft((current) => ({ ...current, publicBaseUrl: event.target.value }))
                }
                placeholder="https://pass.example.com"
                className="pl-8 font-mono"
              />
            </div>
          </div>

          <div className="flex items-center justify-between rounded-lg border p-3">
            <div className="space-y-0.5">
              <Label>在 /v1/models 暴露完整目录</Label>
              <p className="text-muted-foreground text-sm">
                关闭时只返回订阅模型与已配置模型。
              </p>
            </div>
            <Switch
              checked={draft.exposeCatalog}
              onCheckedChange={(checked) =>
                setDraft((current) => ({ ...current, exposeCatalog: checked }))
              }
            />
          </div>

          <Alert>
            <ShieldCheck />
            <AlertTitle>客户端凭据</AlertTitle>
            <AlertDescription>
              修改代理密钥后，本地控制台会同步使用新密钥，下游客户端需更新为同一密钥。
            </AlertDescription>
          </Alert>

          {data.settings?.length ? (
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <Label>运行参数（当前生效值）</Label>
                <span className="text-muted-foreground text-xs">
                  来源优先级：环境变量 &gt; config.json &gt; 内置默认
                </span>
              </div>
              <SettingsTable settings={data.settings} />
              <p className="text-muted-foreground text-xs">
                修改 <code className="font-mono">.env</code> 后需重建容器（
                <code className="font-mono">docker compose up -d</code>）才会反映到这里；
                同一份数据也可通过 <code className="font-mono">GET /api/settings</code> 读取。
              </p>
            </div>
          ) : null}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>OpenAI 兼容入口</CardTitle>
          <CardDescription>Base URL 指向此地址。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="bg-muted/50 rounded-lg border p-3">
            <div className="text-muted-foreground mb-2 flex items-center gap-2 text-xs">
              <KeyRound className="size-3.5" />
              Base URL
            </div>
            <code className="block break-all font-mono text-xs leading-5">{proxyBase}</code>
          </div>
          <Button variant="outline" className="w-full" onClick={copyProxyBase}>
            {copied ? <Check data-icon="inline-start" /> : <Copy data-icon="inline-start" />}
            {copied ? "已复制" : "复制代理地址"}
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}
