import { useState } from "react"
import { Check, Copy, Dices, Eye, EyeOff, Globe2, RefreshCw, Save } from "lucide-react"
import { toast } from "sonner"

import { Field, IconAction, NoteList } from "@/components/console-kit"
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
import { errorMessage } from "@/lib/api"
import { cardActionClass, chipClass, successChipClass } from "@/lib/console-styles"
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
            <Badge variant="outline" className={cn(chipClass, "w-20 justify-center", source.className)}>
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
    value: Pick<SecurityResponse, "proxyKey" | "adminKey" | "publicBaseUrl">,
  ) => Promise<SecurityResponse>
}

// Same shape the keys panel mints: sk- plus 48 hex characters, generated in the
// page.
function randomKey(): string {
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  return "sk-" + Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("")
}

function StateChip({ on, onLabel, offLabel }: { on: boolean; onLabel: string; offLabel: string }) {
  return (
    <Badge variant="outline" className={on ? successChipClass : chipClass}>
      {on ? onLabel : offLabel}
    </Badge>
  )
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
      <div className="min-w-0 space-y-4">
        <Card>
          <CardHeader>
            <CardTitle>访问凭据</CardTitle>
            <CardDescription>设置控制台与客户端使用的密钥，以及服务对外公开的访问地址。</CardDescription>
            <CardAction className={cardActionClass}>
              <Button size="sm" onClick={save} disabled={saving}>
                {saving ? (
                  <RefreshCw className="animate-spin" data-icon="inline-start" />
                ) : (
                  <Save data-icon="inline-start" />
                )}
                保存设置
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent className="space-y-5">
            <Field
              label="管理密钥（控制台）"
              htmlFor="admin-key"
              aside={<StateChip on={adminKeyConfigured} onLabel="独立密钥" offLabel="沿用代理主密钥" />}
              hint="设置后仅该密钥可登录控制台；代理主密钥与客户端密钥只能调用模型接口，无法读取账号信息。"
            >
              <div className="flex gap-1.5">
                <Input
                  id="admin-key"
                  data-secret="1"
                  type={showAdminKey ? "text" : "password"}
                  value={draft.adminKey}
                  onChange={(event) => setDraft((current) => ({ ...current, adminKey: event.target.value }))}
                  placeholder="留空则使用代理主密钥登录"
                  className="font-mono"
                  autoComplete="new-password"
                />
                <IconAction
                  label={showAdminKey ? "隐藏管理密钥" : "显示管理密钥"}
                  onClick={() => setShowAdminKey((value) => !value)}
                >
                  {showAdminKey ? <EyeOff /> : <Eye />}
                </IconAction>
                <IconAction
                  label="随机生成管理密钥"
                  onClick={() => setDraft((current) => ({ ...current, adminKey: randomKey() }))}
                >
                  <Dices />
                </IconAction>
              </div>
            </Field>

            <Field
              label="代理主密钥（客户端）"
              htmlFor="proxy-key"
              aside={<StateChip on={draft.proxyKey.trim() !== ""} onLabel="已开启鉴权" offLabel="未开启鉴权" />}
              hint="客户端调用模型接口时使用的密钥；留空时仅允许本机免密访问。"
            >
              <div className="flex gap-1.5">
                <Input
                  id="proxy-key"
                  data-secret="1"
                  type={showKey ? "text" : "password"}
                  value={draft.proxyKey}
                  onChange={(event) => setDraft((current) => ({ ...current, proxyKey: event.target.value }))}
                  placeholder="留空表示不校验"
                  className="font-mono"
                  autoComplete="new-password"
                />
                <IconAction
                  label={showKey ? "隐藏代理密钥" : "显示代理密钥"}
                  onClick={() => setShowKey((value) => !value)}
                >
                  {showKey ? <EyeOff /> : <Eye />}
                </IconAction>
                <IconAction
                  label="随机生成代理主密钥"
                  onClick={() => setDraft((current) => ({ ...current, proxyKey: randomKey() }))}
                >
                  <Dices />
                </IconAction>
              </div>
            </Field>

            <Field
              label="公网访问地址"
              htmlFor="public-base"
              hint="服务部署在反向代理之后时填写；留空则使用本机地址。"
            >
              <div className="relative">
                <Globe2 className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
                <Input
                  id="public-base"
                  value={draft.publicBaseUrl}
                  onChange={(event) => setDraft((current) => ({ ...current, publicBaseUrl: event.target.value }))}
                  placeholder="https://api.example.com"
                  className="pl-8 font-mono"
                />
              </div>
            </Field>

            <NoteList title="注意事项">
              <li>设置保存后立即生效，无需重启服务。</li>
              <li>修改代理主密钥后，控制台会自动改用新密钥；已接入的客户端需同步更新。</li>
            </NoteList>
          </CardContent>
        </Card>

        {data.settings?.length ? (
          <Card>
            <CardHeader>
              <CardTitle>运行参数（当前生效值）</CardTitle>
              <CardDescription>来源优先级：环境变量 &gt; config.json &gt; 内置默认。</CardDescription>
            </CardHeader>
            <CardContent className="space-y-2">
              <SettingsTable settings={data.settings} />
              <p className="text-muted-foreground text-xs leading-5">
                修改 <code className="font-mono">.env</code> 后需重建容器（
                <code className="font-mono">docker compose up -d</code>）方可生效；同一数据也可通过{" "}
                <code className="font-mono">GET /api/settings</code> 读取。
              </p>
            </CardContent>
          </Card>
        ) : null}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>客户端接入</CardTitle>
          <CardDescription>在客户端中将 Base URL 设置为以下地址。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="bg-muted/40 space-y-1.5 rounded-lg border p-3">
            <div className="text-muted-foreground text-xs">Base URL</div>
            <code className="block font-mono text-xs leading-5 break-all">{proxyBase}</code>
          </div>
          <Button variant="outline" className="w-full" onClick={copyProxyBase}>
            {copied ? <Check data-icon="inline-start" /> : <Copy data-icon="inline-start" />}
            {copied ? "已复制" : "复制代理地址"}
          </Button>
          <NoteList title="接入参数">
            <li>API Key：代理主密钥，或在「代理密钥」中签发的客户端密钥。</li>
            <li>协议：OpenAI Chat Completions 与 Responses 接口。</li>
            <li>模型：「模型与上游」中已订阅的模型 ID。</li>
          </NoteList>
        </CardContent>
      </Card>
    </div>
  )
}
