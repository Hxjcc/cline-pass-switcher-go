export type UpstreamState = "ok" | "limited" | "bad" | "auth" | "unknown"

export interface Account {
  id: string
  name: string
  /** Empty means "keep the stored key"; only filled in when the user types one. */
  key: string
  keyPreview: string
  /** Characters in the stored key, for sizing the mask. Absent means unknown. */
  keyLength?: number
  hasKey: boolean
  enabled: boolean
}

export interface ModelConfig {
  upstream?: string
  upstreams: string[]
  exclude: string[]
  gatewayHas?: string[]
}

export interface GatewayProvider {
  slug: string
  name: string
  has: string[]
  quantization?: string
  context?: number
  output?: number
}

export interface GatewayCatalog {
  model: string
  source: string
  fetchedAt: number
  providers: GatewayProvider[]
}

export interface RoutingCheck {
  profile: string
  accountId: string
  account: string
  has: string[]
  targets: string[]
  actual: string[]
  matched: boolean
  rejected: boolean
  ok: boolean
  current: boolean
  error?: string
  code?: string
  checkedAt: number
  ms: number
}

export interface GatewayCapabilitiesResponse {
  ok: boolean
  catalog: GatewayCatalog
  recommendedHas: string[]
}

export interface RoutingVerifyResponse {
  ok: boolean
  applied: boolean
  checks: RoutingCheck[]
}

export interface GatewayRoutingActions {
  accounts?: Account[]
  onReadGateway?: (modelID: string, refresh: boolean, upstreams?: string[]) => Promise<GatewayCapabilitiesResponse>
  onVerifyGateway?: (modelID: string, config: ModelConfig, accountID: string, allAccounts: boolean, force?: boolean) => Promise<RoutingVerifyResponse>
}

export interface DefaultRouteProbe {
  profile: string
  accountId: string
  account: string
  pipeline: string
  canonical: string
  providers: string[]
  private: boolean
  pinnable: boolean
  reason?: string
  checkedAt: number
  current: boolean
}

export interface RoutingDiscovery {
  profile: string
  sourceConfig: string
  accountId: string
  account: string
  status: "native" | "available" | "unavailable" | "unconfirmed" | "not_applicable"
  reason?: string
  config?: Partial<ModelConfig>
  checkProfile?: string
  checkedAt: number
  current: boolean
  cached?: boolean
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
  gatewayCatalog?: GatewayCatalog
  routingChecks?: Record<string, RoutingCheck>
  gatewayStatusHas?: string[]
  defaultRoute?: DefaultRouteProbe
  routingDiscovery?: RoutingDiscovery
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
  /** Characters in the stored key, for sizing the mask. Absent means unknown. */
  keyLength?: number
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
  keyLength?: number
  hasKey: boolean
  dirty: boolean
}

export interface MetaResponse {
  authRequired: boolean
  /** Authenticated /api/status only. The public /api/meta omits it. */
  proxyBase?: string
  configured: boolean
  storage?: StorageHealth
}

export interface StorageHealth {
  status: "ok" | "degraded" | "unavailable"
  message?: string
  detail?: string
}

export interface TraceAttempt {
  upstream?: string
  status: number
  ms: number
  note?: string
}

/** One provider attempt the Cline gateway reports it made internally. */
export interface GatewayAttempt {
  provider?: string
  status?: number
  ms?: number
  success?: boolean
  requestId?: string
  responseId?: string
  /** Whatever the gateway said about a failed attempt. */
  error?: string
}

export interface UsageStats {
  promptTokens?: number
  completionTokens?: number
  reasoningTokens?: number
  cachedTokens?: number
  totalTokens?: number
  cost?: number
  /** Gateway cost split; display-only, spend limits use cost. */
  inputCost?: number
  outputCost?: number
  surchargeCost?: number
  gatewayCost?: number
  /** The provider's own prompt-cache counters. */
  cacheHitTokens?: number
  cacheMissTokens?: number
}

export interface HistoryItem {
  id?: string
  ts: number
  model: string
  provider?: string
  canonical?: string
  /** Provider the gateway reports it actually ran. */
  resolved?: string
  /** The request did not land on the pinned/affinity provider. */
  fallback?: boolean
  /** Why: "retry" = the wanted channel failed over, "ignored" = it was never tried. */
  fallbackReason?: "retry" | "ignored" | string
  /** The client's conversation id (prompt_cache_key) that produced this row. */
  session?: string
  gatewayAttempts?: GatewayAttempt[]
  generationId?: string
  gatewayHas?: string[]
  targetProviders?: string[]
  routingStatus?: string
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
  nextCursor?: string
}

export interface AccountTestResponse {
  ok: boolean
  ms: number
  model?: string
  note?: string
  error?: string
}

export interface ProbeResponse {
  defaultRoute?: DefaultRouteProbe
  routingDiscovery?: RoutingDiscovery
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
  gatewayHas?: string[]
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
