import type { GatewayCatalog, GatewayProvider, ModelConfig } from "@/types"

export const gatewayCapabilities = [
  { value: "vision", label: "图片输入" },
  { value: "implicit-caching", label: "隐式缓存" },
  { value: "reasoning", label: "推理" },
  { value: "tool-use", label: "工具调用" },
  { value: "structured-output", label: "结构化输出" },
  { value: "free", label: "免费推理" },
] as const

export function normalizeHas(values: string[] = []) {
  return [...new Set(values.map(value => value.trim().toLowerCase()).filter(Boolean))].sort()
}

export function providerMatches(provider: GatewayProvider, has: string[]) {
  return has.every(condition => {
    if (condition.startsWith("!quantization:")) return provider.quantization !== condition.slice(14)
    if (condition.startsWith("quantization:")) return provider.quantization === condition.slice(13)
    return provider.has.includes(condition)
  })
}

export function recommendedHas(catalog?: GatewayCatalog) {
  if (!catalog) return []
  const author = catalog.model.split("/")[0]
  const providers = [...catalog.providers].sort((a, b) => Number(b.slug === author) - Number(a.slug === author))
  for (const provider of providers) {
    const result = ["vision", "implicit-caching"].filter(capability => provider.has.includes(capability))
    if (result.length) return normalizeHas(result)
  }
  return []
}

export function routingConfigKey(config: ModelConfig) {
  return JSON.stringify({ upstreams: config.upstreams, exclude: [...config.exclude].sort(), has: normalizeHas(config.gatewayHas) })
}
