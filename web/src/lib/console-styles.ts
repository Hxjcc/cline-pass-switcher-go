import { cn } from "@/lib/utils"

// Class names shared by every console panel. The components that use them
// live in components/console-kit.tsx.

export const chipClass = "h-5 px-1.5 py-0 text-2xs"

export const warningChipClass = cn(
  chipClass,
  "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300",
)

export const successChipClass = cn(
  chipClass,
  "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-300",
)

/** Card-level action row: every button in it uses size="sm". */
export const cardActionClass = "flex flex-wrap items-center justify-end gap-2"

// Secondary columns of the subscription table give way, one at a time, as its
// container narrows, so the model, its priorities and the row actions always
// fit without horizontal scrolling. Head and body cells share these.
// Each threshold is the width of the columns shown so far plus the one added,
// cell padding included.
export const modelColumnClass = {
  pipeline: "hidden @min-[1000px]/models:table-cell",
  channels: "hidden @min-[880px]/models:table-cell",
  lastHit: "hidden @min-[780px]/models:table-cell",
} as const
