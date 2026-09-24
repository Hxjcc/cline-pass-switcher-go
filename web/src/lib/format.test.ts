import { expect, test } from "vitest"
import {
  formatCost,
  formatQuotaUSD,
  formatTokenCount,
  isPinDisabled,
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
    pinMode: "strict",
    sort: null,
  })
  expect(normalizeModelConfig({ upstreams: ["a"], pinMode: "preferred" })).toEqual({
    upstream: undefined,
    upstreams: ["a"],
    exclude: [],
    pinMode: "preferred",
    sort: null,
  })
  // Anything that is not "preferred" means the channel is pinned strictly.
  expect(normalizeModelConfig({ pinMode: "other" as never }).pinMode).toBe("strict")
})

test("pipelineLabel names the two aggregators", () => {
  expect(pipelineLabel("direct")).toBe("OpenRouter")
  expect(pipelineLabel("planner")).toBe("Vercel 网关")
  expect(pipelineLabel(undefined)).toBe("未识别")
  expect(pipelineLabel("something-else")).toBe("未识别")
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
