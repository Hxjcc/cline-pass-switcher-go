// @vitest-environment node

import { expect, test } from "vitest"
import { normalizeHas, providerMatches, recommendationProviders, recommendedHas, routingConfigKey } from "./gateway-routing"
import type { GatewayCatalog } from "@/types"

const catalog: GatewayCatalog = { model: "deepseek/test", source: "directory", fetchedAt: 1, providers: [
  { slug: "other", name: "Other", has: ["tool-use", "reasoning", "free"] },
  { slug: "deepseek", name: "DeepSeek", has: ["vision", "implicit-caching", "reasoning", "tool-use", "structured-output", "open-weights", "quantization:fp8"] },
  { slug: "vision-only", name: "Vision", has: ["vision"] },
] }

test.each([
  { name: "author supplies all supported capability tags", targets: [], want: ["implicit-caching", "reasoning", "structured-output", "tool-use", "vision"] },
  { name: "selected supplier overrides author", targets: ["other"], want: ["free", "reasoning", "tool-use"] },
  { name: "multiple suppliers share requirements", targets: ["deepseek", "other"], want: ["reasoning", "tool-use"] },
  { name: "priority order does not change common capabilities", targets: ["other", "deepseek"], want: ["reasoning", "tool-use"] },
  { name: "suppliers with no shared capability produce no recommendation", targets: ["other", "vision-only"], want: [] },
  { name: "missing suppliers do not fall back to author", targets: ["deepseek", "missing"], want: [] },
])("$name", ({ targets, want }) => {
  expect(recommendedHas(catalog, targets)).toEqual(want)
})

test("fallback and displayed recommendation sources follow the same directory choice", () => {
  expect(recommendationProviders(catalog).map(provider => provider.slug)).toEqual(["deepseek"])
  expect(recommendationProviders(catalog, ["deepseek", "other"]).map(provider => provider.slug)).toEqual(["deepseek", "other"])
  expect(recommendedHas({ ...catalog, model: "missing/test" })).toEqual(["free", "reasoning", "tool-use"])
  expect(recommendedHas({ ...catalog, providers: [{ slug: "deepseek", name: "DeepSeek", has: ["open-weights"] }, catalog.providers[0]] })).toEqual(["free", "reasoning", "tool-use"])
  expect(recommendedHas()).toEqual([])
  expect(recommendedHas({ ...catalog, providers: [] })).toEqual([])
})

test("directory capabilities select providers without guessing unknown quantization", () => {
  const provider = { slug: "deepseek", name: "DeepSeek", has: ["vision", "implicit-caching"] }
  expect(providerMatches(provider, ["vision", "implicit-caching"])).toBe(true)
  expect(providerMatches(provider, ["free"])).toBe(false)
  expect(providerMatches(provider, ["quantization:fp8"])).toBe(false)
  expect(providerMatches(provider, ["!quantization:fp8"])).toBe(true)
  expect(recommendedHas({ model: "deepseek/test", source: "directory", fetchedAt: 1, providers: [provider] })).toEqual(["implicit-caching", "vision"])
  expect(normalizeHas([" Vision ", "vision"])).toEqual(["vision"])
})

test("configuration identity ignores capability and exclusion ordering, retaining priority order", () => {
  const first = { upstreams: ["a", "b"], exclude: ["x", "y"], gatewayHas: ["vision", "implicit-caching"] }
  expect(routingConfigKey(first)).toBe(routingConfigKey({ ...first, exclude: ["y", "x"], gatewayHas: ["implicit-caching", "vision"] }))
  expect(routingConfigKey(first)).not.toBe(routingConfigKey({ ...first, upstreams: ["b", "a"] }))
})
