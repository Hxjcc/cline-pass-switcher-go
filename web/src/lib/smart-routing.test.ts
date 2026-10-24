import { expect, test } from "vitest"
import { canExploreGateway, discoveryLabel } from "./smart-routing"
import type { DefaultRouteProbe, RoutingDiscovery, SubscriptionModel } from "@/types"

const baseline: DefaultRouteProbe = { profile: "p", accountId: "one", account: "one", pipeline: "planner", canonical: "private/test", providers: ["private"], private: true, pinnable: false, checkedAt: 1, current: true }
const model: SubscriptionModel = { id: "cline-pass/test", config: { upstreams: [], exclude: [] }, meta: { defaultRoute: baseline } }

test("only single/private planner routes expose capability discovery", () => {
  expect(canExploreGateway(model)).toBe(true)
  expect(canExploreGateway({ ...model, meta: { defaultRoute: { ...baseline, private: false, providers: ["a", "b"] } } })).toBe(false)
  expect(canExploreGateway({ ...model, meta: { defaultRoute: { ...baseline, pinnable: true } } })).toBe(false)
  expect(canExploreGateway({ ...model, meta: { defaultRoute: { ...baseline, pipeline: "direct" } } })).toBe(false)
  expect(canExploreGateway({ ...model, meta: { defaultRoute: { ...baseline, private: false, providers: ["a", "b"], reason: "single_provider" } } })).toBe(true)
})

test("unfiltered evidence takes precedence over a one-entry pinned plan", () => {
  expect(canExploreGateway({ ...model, meta: { pipeline: "planner", pinReason: "single_provider", upstreams: ["a"], defaultRoute: { ...baseline, private: false, pinnable: true, providers: ["a", "b"] } } })).toBe(false)
})

test("existing capability settings remain manageable and legacy singles can be re-probed", () => {
  expect(canExploreGateway({ ...model, config: { ...model.config, gatewayHas: ["vision"] }, meta: { defaultRoute: { ...baseline, providers: ["a", "b"], private: false } } })).toBe(true)
  expect(canExploreGateway({ ...model, meta: { pipeline: "planner", pinReason: "single_provider", upstreams: ["private"] } })).toBe(true)
  expect(canExploreGateway({ ...model, meta: null })).toBe(false)
})

test.each([
  ["native", "普通渠道可钉住"], ["available", "能力路由可用"], ["unavailable", "当前能力条件未通过"], ["unconfirmed", "路由未能确认"], ["not_applicable", "保持普通路由"],
] as const)("discovery state %s has a distinct label", (status, label) => {
  const discovery: RoutingDiscovery = { profile: "p", sourceConfig: "s", accountId: "one", account: "one", status, current: true, checkedAt: 1 }
  expect(discoveryLabel({ ...model, meta: { routingDiscovery: discovery } })).toBe(label)
  expect(discoveryLabel({ ...model, meta: { routingDiscovery: { ...discovery, current: false } } })).toBe("路由结果需重新确认")
})
