import { api } from "@/lib/api"
import type {
  AccountsResponse,
  HistoryResponse,
  KeysResponse,
  MetaResponse,
  ModelsResponse,
  SecurityResponse,
} from "@/types"

const SNAPSHOT_STORAGE = "cline-pass-switcher-snapshot"

/** How many history rows the first paint asks for. */
export const HISTORY_PAGE_SIZE = 50

/**
 * Everything the console renders on load. One snapshot keeps the panels
 * consistent - a model list from one revision and an account list from another
 * would show routing that never existed.
 */
export interface CachedSnapshot {
  models: ModelsResponse
  meta: MetaResponse
  history: HistoryResponse["history"]
  historyTotal?: number
  historyHasMore?: boolean
  accounts: AccountsResponse
  keys: KeysResponse
  security: SecurityResponse
}

export async function fetchSnapshot(key: string): Promise<CachedSnapshot> {
  const [meta, models, accounts, keys, security, history] = await Promise.all([
    api<MetaResponse>("/api/meta"),
    api<ModelsResponse>("/api/models", { key }),
    api<AccountsResponse>("/api/accounts", { key }),
    api<KeysResponse>("/api/keys", { key }),
    api<SecurityResponse>("/api/security", { key }),
    api<HistoryResponse>(`/api/history?limit=${HISTORY_PAGE_SIZE}`, { key }),
  ])
  return {
    meta,
    models,
    accounts,
    keys,
    security,
    history: history.history,
    historyTotal: history.total,
    historyHasMore: history.hasMore,
  }
}

/** readSnapshot restores the boot-time cache so a reload paints immediately. */
export function readSnapshot(): CachedSnapshot | null {
  try {
    const raw = sessionStorage.getItem(SNAPSHOT_STORAGE)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<CachedSnapshot>
    if (
      !parsed.models?.subscription ||
      !parsed.meta ||
      !Array.isArray(parsed.history) ||
      !parsed.accounts ||
      !parsed.keys ||
      !parsed.security
    ) {
      return null
    }
    return parsed as CachedSnapshot
  } catch {
    return null
  }
}

export function writeSnapshot(snapshot: CachedSnapshot) {
  try {
    sessionStorage.setItem(SNAPSHOT_STORAGE, JSON.stringify(snapshot))
  } catch {
    // Storage can be unavailable in restricted browser contexts.
  }
}
