import type { ReactNode } from "react"
import type { LucideIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

// Building blocks shared by every console panel, so that labels, icon buttons
// and empty states look the same wherever they appear. Shared class names are
// in lib/console-styles.ts.

/** A single-line muted label above a control; controls below share h-8. */
export function Field({
  label,
  htmlFor,
  labelId,
  aside,
  hint,
  className,
  children,
}: {
  label: string
  htmlFor?: string
  labelId?: string
  /** Rendered at the right end of the label row, e.g. a status chip. */
  aside?: ReactNode
  /** One line of help text under the control. */
  hint?: ReactNode
  className?: string
  children: ReactNode
}) {
  // Flex gap rather than space-y: Base UI controls render a hidden input after
  // the visible one, and space-y would give the visible control a bottom
  // margin that pushes it out of line with its neighbours.
  return (
    <div className={cn("flex min-w-0 flex-col gap-1.5", className)}>
      <div className="flex min-h-4 items-center justify-between gap-2">
        <Label id={labelId} htmlFor={htmlFor} className="text-muted-foreground text-xs font-normal">
          {label}
        </Label>
        {aside}
      </div>
      {children}
      {hint && <p className="text-muted-foreground text-xs leading-5">{hint}</p>}
    </div>
  )
}

/**
 * A filter or mode that is either on or off, drawn as a button so it sits in
 * a toolbar at the same height as the inputs next to it.
 */
export function ToggleChip({
  pressed,
  onPressedChange,
  icon: Icon,
  children,
}: {
  pressed: boolean
  onPressedChange: (pressed: boolean) => void
  icon: LucideIcon
  children: ReactNode
}) {
  return (
    <Button
      variant="outline"
      aria-pressed={pressed}
      onClick={() => onPressedChange(!pressed)}
      className={cn(
        "text-muted-foreground font-normal",
        pressed &&
          "border-primary/40 bg-primary/10 text-primary hover:bg-primary/15 hover:text-primary dark:bg-primary/20 dark:hover:bg-primary/25",
      )}
    >
      <Icon data-icon="inline-start" />
      {children}
    </Button>
  )
}

/** Icon-only button with a tooltip that doubles as its accessible name. */
export function IconAction({
  label,
  onClick,
  children,
  className,
  disabled,
  variant = "outline",
  size = "icon",
}: {
  label: string
  onClick: () => void
  children: ReactNode
  className?: string
  disabled?: boolean
  variant?: "outline" | "ghost"
  size?: "icon" | "icon-sm" | "icon-xs"
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant={variant}
            size={size}
            aria-label={label}
            disabled={disabled}
            className={className}
            onClick={onClick}
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

export function EmptyState({
  icon: Icon,
  title,
  description,
  className,
}: {
  icon: LucideIcon
  title: string
  description?: ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-10 text-center",
        className,
      )}
    >
      <div className="bg-muted flex size-10 items-center justify-center rounded-full">
        <Icon className="text-muted-foreground size-5" />
      </div>
      <div className="space-y-1">
        <div className="text-sm font-medium">{title}</div>
        {description && <div className="text-muted-foreground text-sm">{description}</div>}
      </div>
    </div>
  )
}

/** The muted strip under a card header that summarises the list below. */
export function SummaryBar({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <div
      className={cn(
        "bg-muted/30 text-muted-foreground flex flex-wrap items-center gap-x-5 gap-y-1.5 rounded-lg border px-3 py-2.5 text-xs",
        className,
      )}
    >
      {children}
    </div>
  )
}

export function SummaryItem({ label, value, unit }: { label: string; value: ReactNode; unit?: string }) {
  return (
    <span className="whitespace-nowrap">
      {label} <span className="text-foreground font-medium tabular-nums">{value}</span>
      {unit && ` ${unit}`}
    </span>
  )
}

/** Short explanatory list at the bottom of a panel. */
export function NoteList({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="bg-muted/30 space-y-2 rounded-lg border px-4 py-3 text-xs">
      <h3 className="text-sm font-medium">{title}</h3>
      <ul className="text-muted-foreground list-disc space-y-1 pl-4 leading-5">{children}</ul>
    </section>
  )
}
