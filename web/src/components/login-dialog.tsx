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
            登录控制台
          </DialogTitle>
          <DialogDescription>
            请输入管理密钥。未单独设置管理密钥时，请使用代理主密钥。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="login-key" className="text-muted-foreground text-xs font-normal">
              管理密钥
            </Label>
            <Input
              id="login-key"
              data-secret="1"
              type="password"
              value={key}
              autoFocus
              autoComplete="current-password"
              aria-invalid={error ? true : undefined}
              onChange={(event) => setKey(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") void submit()
              }}
            />
            {error && <p className="text-destructive text-xs leading-5">{error}</p>}
          </div>
          <div className="space-y-1.5">
            <Label className="font-normal" htmlFor="login-remember">
              <Checkbox
                id="login-remember"
                checked={remember}
                onCheckedChange={(checked) => setRemember(checked === true)}
              />
              在这台设备上记住密钥
            </Label>
            <p className="text-muted-foreground pl-6 text-xs leading-5">
              {remember
                ? "密钥将保存在浏览器本地存储中，关闭浏览器后仍会保留。"
                : "密钥仅保存在当前会话中，关闭标签页后失效。"}
            </p>
          </div>
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
