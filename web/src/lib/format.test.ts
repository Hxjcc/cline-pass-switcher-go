import { expect, test } from "vitest"
import {
  formatCost,
  formatQuotaUSD,
  formatTokenCount,
  isPinDisabled,
  keyMask,
  normalizeModelConfig,
  pinReasonLabel,
  pipelineLabel,
  pipelineHint,
  providerLabel,
  shortDuration,
} from "./format"

test("shortDuration switches to seconds at 1000 ms", () => {
  expect(shortDuration(0)).toBe("0 ms")
  expect(shortDuration(999)).toBe("999 ms")
  expect(shortDuration(1000)).toBe("1.0 s")
  expect(shortDuration(12_345)).toBe("12.3 s")
  expect(shortDuration(undefined)).toBe("—")
})

test("formatTokenCount only compacts from ten thousand", () => {
  expect(formatTokenCount(9999)).toBe("9,999")
  expect(formatTokenCount(10_000)).toBe("10.0k")
  expect(formatTokenCount(1_234_567)).toBe("1234.6k")
  expect(formatTokenCount(undefined)).toBe("—")
})

// The ladder matters: a $0.000006 generation has to stay readable instead of
// rounding to "$0.0000".
test("formatCost keeps enough digits for tiny amounts", () => {
  expect(formatCost(0.02)).toBe("$0.020")
  expect(formatCost(0.01)).toBe("$0.010")
  expect(formatCost(0.005)).toBe("$0.0050")
  expect(formatCost(0.0001)).toBe("$0.0001")
  expect(formatCost(0.000006)).toBe("$0.000006")
  expect(formatCost(0)).toBe("$0.000000")
  expect(formatCost(undefined)).toBe("")
})

// Cline reports plan money as integer 1e-8 USD units.
test("formatQuotaUSD converts 1e-8 units", () => {
  expect(formatQuotaUSD(5_000_000_000)).toBe("$50.00")
  expect(formatQuotaUSD(100_000_000)).toBe("$1.00")
  expect(formatQuotaUSD(1_000_000)).toBe("$0.0100")
  expect(formatQuotaUSD(600)).toBe("$0.000006")
  expect(formatQuotaUSD(undefined)).toBe("—")
})

test("normalizeModelConfig fills in the missing halves", () => {
  expect(normalizeModelConfig(undefined)).toEqual({
    upstream: undefined,
    upstreams: [],
    exclude: [],
  })
  expect(normalizeModelConfig({ upstreams: ["a"] })).toEqual({
    upstream: undefined,
    upstreams: ["a"],
    exclude: [],
  })
})

test("pipelineLabel names the two aggregators", () => {
  expect(pipelineLabel("direct")).toBe("OpenRouter")
  expect(pipelineLabel("planner")).toBe("Vercel")
  expect(pipelineLabel(undefined)).toBe("未识别")
  expect(pipelineLabel("something-else")).toBe("未识别")
})

test("pipelineLabel gives a privately served model its own line", () => {
  // The provider filter never reaches OpenRouter or Vercel for these, so naming
  // either one on the row would be a lie.
  expect(pipelineLabel("planner", { canonicalSlug: "private/glm-5p3-contributor" })).toBe("Private")
  expect(pipelineLabel("planner", { upstreams: ["openai-compatible-private"] })).toBe("Private")
  expect(pipelineLabel("planner", { upstreams: ["deepseek", "azure"] })).toBe("Vercel")
  expect(pipelineLabel("planner")).toBe("Vercel")
})

test("providerLabel shortens the private-endpoint slug and passes others through", () => {
  expect(providerLabel("openai-compatible-private")).toBe("私有接口")
  expect(providerLabel("z-ai")).toBe("z-ai")
  expect(providerLabel(undefined)).toBe("")
})

test("pinReasonLabel maps the backend probe reasons", () => {
  expect(pinReasonLabel("gateway_ignores_provider_preferences")).toBe("网关已忽略上游偏好")
  expect(pinReasonLabel("single_provider")).toBe("仅有一个候选渠道，无需钉住")
  expect(pinReasonLabel("probe_failed")).toBe("无法确认网关是否支持钉住")
  expect(pinReasonLabel(undefined)).toBe("当前不可钉住")
})

test("keyMask is as long as the stored key", () => {
  // The mask is painted text, not a password field's own bullets, so it has to
  // carry both the key's length and a glyph the field's monospace font draws at
  // the same width as a character of the key itself.
  expect(keyMask(3)).toBe("•••")
  expect(keyMask(67)).toHaveLength(67)
  expect(keyMask(5)).toHaveLength(5)
  expect(keyMask(undefined)).toHaveLength(20)
  expect(keyMask(0)).toHaveLength(20)
})

test("isPinDisabled accepts either the explicit false or the legacy reason", () => {
  expect(isPinDisabled({ pinnable: false })).toBe(true)
  expect(isPinDisabled({ pinReason: "gateway_ignores_provider_preferences" })).toBe(true)
  expect(isPinDisabled({ pinnable: true })).toBe(false)
  expect(isPinDisabled(undefined)).toBe(false)
})

test("pipelineHint explains when pinning is unavailable", () => {
  expect(pipelineHint("planner", false, "gateway_ignores_provider_preferences")).toContain(
    "网关已忽略上游偏好",
  )
  // Older metadata can carry the reason without the boolean false.
  expect(pipelineHint("planner", undefined, "gateway_ignores_provider_preferences")).toContain(
    "网关已忽略上游偏好",
  )
  expect(pipelineHint("planner", false, "single_provider")).toContain("仅有一个候选渠道")
  expect(pipelineHint("planner", true)).toContain("providerOptions.gateway")
  expect(pipelineHint("direct", true)).toContain("provider 字段")
  expect(pipelineHint(undefined)).toContain("无法区分渠道")
})

test("pipelineHint explains a privately served model", () => {
  const hint = pipelineHint("planner", false, "single_provider", { canonicalSlug: "private/glm-5p3-contributor" })
  expect(hint).toContain("不经过 OpenRouter / Vercel")
  expect(hint).toContain("不能钉住")
})
