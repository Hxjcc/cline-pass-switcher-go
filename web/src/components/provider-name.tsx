import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { providerHint, providerLabel } from "@/lib/format"
import { cn } from "@/lib/utils"

// Renders a gateway provider slug, swapping known private endpoints for a
// short label and keeping the raw slug in a tooltip.
export function ProviderName({ slug, className }: { slug?: string; className?: string }) {
  const label = providerLabel(slug)
  if (!label) return null
  const hint = providerHint(slug)
  if (!hint) {
    return <span className={cn("font-mono", className)}>{label}</span>
  }
  return (
    <Tooltip>
      <TooltipTrigger render={<span className={cn("cursor-help", className)} />}>
        {label}
      </TooltipTrigger>
      <TooltipContent className="max-w-72 whitespace-pre-wrap">{hint}</TooltipContent>
    </Tooltip>
  )
}
