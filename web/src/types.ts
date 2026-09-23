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
  pinReason?: string
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
  /** Always an array from this backend; null is tolerated for older ones. */
  added?: string[] | null
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

export interface QuotaLimit {
  type: "five_hour" | "weekly" | "monthly" | string
  percentUsed: number
  resetsAt?: string
}

/** Raw inference cap thresholds. Values are 1e-8 USD units (1e8 = $1). */
export interface QuotaCaps {
  fiveHour: number
  weekly: number
  monthly: number
}

export interface AccountQuota {
  account?: string
  accountId?: string
  ok: boolean
  error?: string
  plan?: string
  active?: boolean
  currentPeriodEnd?: string
  caps?: QuotaCaps
  limits?: QuotaLimit[]
  fetchedAt: number
}

export interface QuotaResponse {
  accounts: AccountQuota[]
}

export interface AccountsResponse {
  accounts: Account[]
  mode: "single" | "roundrobin"
  active: number
  stats: Record<string, AccountStats>
}

export interface SecurityResponse {
  proxyKey: string
  /** Console credential. Empty falls back to proxyKey on the server. */
  adminKey: string
  publicBaseUrl: string
  authRequired: boolean
  exposeCatalog: boolean
  proxyBase?: string
  /** Values actually in force, with the layer that supplied each one. */
  settings?: EffectiveSetting[]
}

export interface EffectiveSetting {
  key: string
  label: string
  value: string
  /** env | config | default | builtin */
  source: string
  secret?: boolean
}

/** One console-issued client key, as listed by GET /api/keys. */
export interface ProxyKeyItem {
  id: string
  name?: string
  /** Only present when the list was fetched with reveal=1. */
  key?: string
  keyPreview?: string
  hasKey: boolean
  enabled: boolean
  accountId?: string
  spendLimitUsd?: number
  note?: string
  createdAt?: number
  requests: number
  spentUsd: number
  lastUsed?: number
  pinnedAccount?: string
}

export interface KeysResponse {
  keys: ProxyKeyItem[]
  maxKeys?: number
}

/** Editable row of the keys panel; counters are display-only copies. */
export interface ProxyKeyDraft {
  id: string
  name: string
  key: string
  enabled: boolean
  accountId: string
  spendLimitUsd: number
  note: string
  createdAt: number
  requests: number
  spentUsd: number
  lastUsed?: number
  keyPreview?: string
  hasKey: boolean
  dirty: boolean
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
  /** Anchored summary sections a completed compaction left out (advisory). */
  missingSummarySections?: string[]
  /** The compaction returned a fallback item because summarization failed. */
  degraded?: boolean
  degradeReason?: string
}

export interface HistoryResponse {
  history: HistoryItem[]
  total: number
  offset: number
  limit: number
  hasMore: boolean
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
  supported?: boolean
  reason?: string
  summary: Record<UpstreamState, number>
  results: Record<string, UpstreamStatus>
  upstreams: string[]
}

export interface OfficialResponse {
  ok: boolean
  sources: string[]
  found: number
  added?: string[] | null
  knownModels: string[]
  ts: number
  total: number
}
