export type PinMode = "strict" | "preferred"
export type SortMode = "cost" | "ttft" | "tps"
export type UpstreamState = "ok" | "limited" | "bad" | "auth" | "unknown"

export interface Account {
  id: string
  name: string
  /** Empty means "keep the stored key"; only filled in when the user types one. */
  key: string
  keyPreview: string
  hasKey: boolean
  enabled: boolean
}

export interface ModelConfig {
  upstream?: string
  upstreams: string[]
  exclude: string[]
  pinMode: PinMode
  sort: SortMode | null
}

export interface UpstreamDetail {
  slug: string
  name: string
  endpoints: number
  context: number
  uptime: number
}

export interface UpstreamStatus {
  status: UpstreamState
  note: string
  checkedAt: number
  ms?: number
}

export interface ModelMeta {
  ok?: boolean
  displayName?: string
  description?: string
  family?: string
  capabilitiesKnown?: boolean
  reasoning?: boolean
  reasoningEfforts?: string[]
  inputModalities?: string[]
  outputModalities?: string[]
  attachment?: boolean
  toolCall?: boolean
  structuredOutput?: boolean
  temperature?: boolean
  contextWindow?: number
  outputLimit?: number
  capabilityUpdatedAt?: number
  pipeline?: "direct" | "planner"
  pinnable?: boolean
  availableProviders?: string[]
  canonicalSlug?: string
  openrouterSlug?: string
  upstreamDetail?: Record<string, UpstreamDetail>
  upstreams?: string[]
  tier0?: string[]
  lastProvider?: string
  lastMs?: number
  probedAt?: number
  upstreamStatus?: Record<string, UpstreamStatus>
  validatedAt?: number
}

export interface SubscriptionModel {
  id: string
  config: ModelConfig
  meta: ModelMeta | null
}

export interface OfficialFetch {
  ts: number
  sources: string[]
  found: number
  added: string[]
  total: number
}

export interface ModelsResponse {
  subscription: SubscriptionModel[]
  catalogCount: number
  catalog: string[]
  proxyBase: string
  officialFetch: OfficialFetch | null
}

export interface AccountStats {
  requests: number
  lastUsed: number
  lastError: string | null
}

export interface AccountsResponse {
  accounts: Account[]
  mode: "single" | "roundrobin"
  active: number
  stats: Record<string, AccountStats>
}

export interface SecurityResponse {
  proxyKey: string
  publicBaseUrl: string
  authRequired: boolean
  exposeCatalog: boolean
  proxyBase?: string
}

export interface MetaResponse {
  authRequired: boolean
  proxyBase: string
  configured: boolean
}

export interface TraceAttempt {
  upstream?: string
  status: number
  ms: number
  note?: string
}

export interface UsageStats {
  promptTokens?: number
  completionTokens?: number
  reasoningTokens?: number
  cachedTokens?: number
  totalTokens?: number
  cost?: number
}

export interface HistoryItem {
  ts: number
  model: string
  provider?: string
  canonical?: string
  ms: number
  ttftMs?: number
  stream: boolean
  kind?: "chat" | "responses" | "compact" | "test" | string
  effort?: string
  requestedEffort?: string
  finishReason?: string
  usage?: UsageStats
  error: string | null
  account?: string
  attempts?: string[]
  trace?: TraceAttempt[]
}

export interface HistoryResponse {
  history: HistoryItem[]
}

export interface AccountTestResponse {
  ok: boolean
  ms: number
  model?: string
  note?: string
  error?: string
}

export interface ProbeResponse {
  ok: boolean
  ms: number
  error?: string
  upstreams?: string[]
  lastProvider?: string
}

export interface TestResponse {
  ok: boolean
  error?: string
  ms?: number
  targets?: string[]
  exclude?: string[]
  actual?: string
  actualName?: string
  pipeline?: "direct" | "planner"
  pinnable?: boolean
  canonicalSlug?: string
  fallbacks?: string[]
  content?: string
  account?: string
  trace?: TraceAttempt[]
}

export interface ValidationResponse {
  ok: boolean
  summary: Record<UpstreamState, number>
  results: Record<string, UpstreamStatus>
  upstreams: string[]
}

export interface OfficialResponse {
  ok: boolean
  sources: string[]
  found: number
  added: string[]
  knownModels: string[]
  ts: number
  total: number
}
