import { useState, type ReactNode } from "react"
import { RotateCcw } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = "确认",
  destructive = false,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description: ReactNode
  confirmLabel?: string
  destructive?: boolean
  onConfirm: () => Promise<void> | void
}) {
  const [pending, setPending] = useState(false)

  const confirm = async () => {
    setPending(true)
    try {
      await onConfirm()
      onOpenChange(false)
    } catch {
      // The caller has already reported the failure; keep the dialog open so
      // the user can retry or cancel.
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription className="whitespace-pre-line">{description}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose render={<Button variant="outline" size="sm" disabled={pending} />}>
            取消
          </DialogClose>
          <Button
            variant={destructive ? "destructive" : "default"}
            size="sm"
            disabled={pending}
            onClick={() => void confirm()}
          >
            {pending && <RotateCcw className="animate-spin" data-icon="inline-start" />}
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
