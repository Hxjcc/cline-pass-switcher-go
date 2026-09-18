import { useEffect, useState } from "react"
import { KeyRound, LogIn, RefreshCw } from "lucide-react"

import { Button } from "@/components/ui/button"
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
  onLogin: (key: string) => Promise<void>
}) {
  const [key, setKey] = useState("")
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    if (!open) {
      setKey("")
      setError("")
    }
  }, [open])

  const submit = async () => {
    if (!key.trim()) return
    setLoading(true)
    setError("")
    try {
      await onLogin(key.trim())
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
