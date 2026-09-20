import { normalizeModelConfig, upstreamRank } from "@/lib/format"
import type { SubscriptionModel, UpstreamState } from "@/types"

function statusOf(state?: UpstreamState): UpstreamState {
  return state ?? "unknown"
}

/**
 * Every channel a model is known to have: the pinned ones first, in the order
 * the operator arranged, then the rest sorted by health so a channel that just
 * failed sinks below the healthy ones instead of jumping around the list.
 *
 * Excluded channels are included: the row renders them struck through, and the
 * caller filters them out where an allow list is what it needs.
 */
export function orderedUpstreams(model: SubscriptionModel): string[] {
  const config = normalizeModelConfig(model.config)
  const meta = model.meta
  const all = new Set<string>([
    ...(meta?.upstreams ?? []),
    ...config.upstreams,
    ...config.exclude,
    ...Object.keys(meta?.upstreamDetail ?? {}),
  ])
  const selected = config.upstreams.filter((value) => all.has(value))
  const rest = [...all]
    .filter((value) => !selected.includes(value))
    .sort((left, right) => {
      const leftState = statusOf(meta?.upstreamStatus?.[left]?.status)
      const rightState = statusOf(meta?.upstreamStatus?.[right]?.status)
      return upstreamRank[leftState] - upstreamRank[rightState] || left.localeCompare(right)
    })
  return [...selected, ...rest]
}
