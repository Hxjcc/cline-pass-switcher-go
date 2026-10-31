// @vitest-environment node

import { expect, test } from "vitest"
import { cacheStats, formatCacheRate, usageTitle } from "./history-display"

test("cache ratio uses whole-request cached tokens instead of per-leg gateway counters", () => {
  const usage = { promptTokens: 148300, cachedTokens: 139400, cacheHitTokens: 69800, cacheMissTokens: 8728 }
  expect(formatCacheRate(usage)).toBe("94.0%")
  expect(cacheStats(usage).tokens).toBe(139400)
  expect(usageTitle(usage)).not.toContain("命中")
})

test("provider counters are a fallback only when they cover the whole prompt", () => {
  expect(formatCacheRate({ promptTokens: 186300, cacheHitTokens: 186100, cacheMissTokens: 200 })).toBe("99.9%")
  expect(formatCacheRate({ promptTokens: 148300, cacheHitTokens: 69800, cacheMissTokens: 8728 })).toBe("")
})

test("zero, full, and unknown cache use distinct values", () => {
  expect(formatCacheRate({ promptTokens: 35, cachedTokens: 0 })).toBe("0%")
  expect(formatCacheRate({ promptTokens: 35, cachedTokens: 35 })).toBe("100%")
  expect(formatCacheRate({ promptTokens: 0, cachedTokens: 0 })).toBe("")
  expect(formatCacheRate({ promptTokens: 35 })).toBe("")
  expect(formatCacheRate()).toBe("")
})
