import { useEffect, useState } from "react"
import { Check, Copy, Eye, EyeOff, Globe2, KeyRound, Save, ShieldCheck } from "lucide-react"
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
import type { SecurityResponse } from "@/types"

interface SecurityPanelProps {
  data: SecurityResponse
  proxyBase: string
  onSave: (value: Pick<SecurityResponse, "proxyKey" | "publicBaseUrl" | "exposeCatalog">) => Promise<SecurityResponse>
}

export function SecurityPanel({ data, proxyBase, onSave }: SecurityPanelProps) {
  const [draft, setDraft] = useState(data)
  const [showKey, setShowKey] = useState(false)
  const [saving, setSaving] = useState(false)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    setDraft(data)
  }, [data])

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
              <Label htmlFor="proxy-key">代理密钥</Label>
              <Badge variant={draft.proxyKey ? "default" : "outline"}>
                {draft.proxyKey ? "鉴权已开启" : "鉴权已关闭"}
              </Badge>
            </div>
            <div className="flex gap-2">
              <Input
                id="proxy-key"
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
