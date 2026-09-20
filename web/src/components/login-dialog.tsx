import { useState } from "react"
import { KeyRound, LogIn, RefreshCw } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { errorMessage } from "@/lib/api"

export function LoginDialog({
  open,
  onOpenChange,
  onLogin,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onLogin: (key: string, remember: boolean) => Promise<void>
}) {
  const [key, setKey] = useState("")
  const [remember, setRemember] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [previousOpen, setPreviousOpen] = useState(open)
  if (previousOpen !== open) {
    setPreviousOpen(open)
    setKey("")
    setRemember(false)
    setError("")
  }

  const submit = async () => {
    if (!key.trim()) return
    setLoading(true)
    setError("")
    try {
      await onLogin(key.trim(), remember)
      onOpenChange(false)
    } catch (loginError) {
      setError(errorMessage(loginError))
    } finally {
      setLoading(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <KeyRound className="size-4" />
            控制台鉴权
          </DialogTitle>
          <DialogDescription>输入当前代理密钥以读取和管理服务配置。</DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <Label htmlFor="login-key">代理密钥</Label>
          <Input
            id="login-key"
            type="password"
            value={key}
            autoFocus
            autoComplete="current-password"
            onChange={(event) => setKey(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") void submit()
            }}
          />
          <Label className="mt-2 font-normal" htmlFor="login-remember">
            <Checkbox
              id="login-remember"
              checked={remember}
              onCheckedChange={(checked) => setRemember(checked === true)}
            />
            在这台设备上记住密钥
          </Label>
          <p className="text-muted-foreground text-xs">
            {remember
              ? "密钥会写入浏览器 localStorage，关闭浏览器后仍然保留。"
              : "密钥只保存在本次会话（sessionStorage），关闭标签页即失效。"}
          </p>
          {error && <p className="text-destructive text-sm">{error}</p>}
        </div>
        <DialogFooter>
          <Button onClick={submit} disabled={loading || !key.trim()}>
            {loading ? (
              <RefreshCw className="animate-spin" data-icon="inline-start" />
            ) : (
              <LogIn data-icon="inline-start" />
            )}
            进入控制台
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
