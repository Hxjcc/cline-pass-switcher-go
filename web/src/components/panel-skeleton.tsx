import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { cardActionClass } from "@/lib/console-styles"

// A fixed key list keeps the placeholders stable across renders without
// indexing into an array the way a map over Array.from would.
const skeletonKeys = ["one", "two", "three", "four", "five", "six", "seven", "eight"] as const

interface PanelSkeletonProps {
  /** The real panel heading, so the frame is readable before any data lands. */
  title: string
  description?: string
  /** Body rows to reserve: table rows or labelled fields. */
  rows?: number
  body?: "table" | "form"
  /** Placeholder buttons in the card action row. */
  actions?: number
}

/**
 * Reserves a panel's frame while its first response is in flight. A browser
 * with no cached snapshot used to show nothing at all and then pop the whole
 * card in at once; the heading and the first rows are static, so they can be
 * on screen from the first frame and only the values need to fade in.
 */
export function PanelSkeleton({
  title,
  description,
  rows = 4,
  body = "table",
  actions = 3,
}: PanelSkeletonProps) {
  return (
    <Card aria-busy="true" data-panel-skeleton="true">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
        {actions > 0 ? (
          <CardAction className={cardActionClass}>
            {skeletonKeys.slice(0, actions).map((key) => (
              <Skeleton key={key} className="h-8 w-24" />
            ))}
          </CardAction>
        ) : null}
      </CardHeader>
      <CardContent className="space-y-4">
        {body === "form" ? (
          <div className="grid gap-4 sm:grid-cols-2">
            {skeletonKeys.slice(0, rows).map((key) => (
              <div key={key} className="space-y-2">
                <Skeleton className="h-4 w-24" />
                <Skeleton className="h-9 w-full" />
              </div>
            ))}
          </div>
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
              <Skeleton className="h-9 w-full max-w-xs" />
              <Skeleton className="h-4 w-44" />
            </div>
            <div className="space-y-2 rounded-lg p-3 ring-1 ring-foreground/10">
              {skeletonKeys.slice(0, rows).map((key) => (
                <Skeleton key={key} className="h-11 w-full" />
              ))}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
