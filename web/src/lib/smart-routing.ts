import type { SubscriptionModel } from "@/types"

export function canExploreGateway(model: SubscriptionModel) {
  if (model.config.gatewayHas?.length) return true
  const baseline = model.meta?.defaultRoute
  if (baseline) return baseline.pipeline === "planner" && !baseline.pinnable && (baseline.private || baseline.providers.length === 1 || baseline.reason === "single_provider")
  // Allow old single-channel metadata to be re-probed during migration.
  return model.meta?.pipeline !== "direct" && model.meta?.pinReason === "single_provider" && (model.meta?.upstreams?.length === 1 || model.meta.canonicalSlug?.startsWith("private/"))
}

export function discoveryLabel(model: SubscriptionModel) {
  const discovery = model.meta?.routingDiscovery
  if (!discovery) return ""
  if (!discovery.current) return "路由结果需重新确认"
  switch (discovery.status) {
    case "native": return "普通渠道可钉住"
    case "available": return model.config.gatewayHas?.length ? "已启用能力路由" : "能力路由可用"
    case "unavailable": return "当前能力条件未通过"
    case "unconfirmed": return "路由未能确认"
    default: return "保持普通路由"
  }
}
