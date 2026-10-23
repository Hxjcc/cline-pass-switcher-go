import { expect, test } from "vitest"
import { normalizeHas, providerMatches, recommendedHas, routingConfigKey } from "./gateway-routing"

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
