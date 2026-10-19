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
  historyCursor?: string
  historyQuery?: { q: string; onlyErrors: boolean }
  accounts: AccountsResponse
  keys: KeysResponse
  security: SecurityResponse
}

export type ConsoleSnapshot = Omit<CachedSnapshot, "history" | "historyTotal" | "historyHasMore" | "historyCursor" | "historyQuery">

// History has its own request coordinator; a general refresh must not write
// unfiltered rows over a newer history query.
export async function fetchSnapshot(key: string): Promise<ConsoleSnapshot> {
  // Validate the credential with one protected request before fanning out.
  // A stale key must not burn the five-request auth-failure budget and lock
  // the operator out of the login dialog.
  const models = await api<ModelsResponse>("/api/models", { key })
  const [meta, accounts, keys, security] = await Promise.all([
    api<MetaResponse>("/api/status", { key }),
    api<AccountsResponse>("/api/accounts", { key }),
    api<KeysResponse>("/api/keys", { key }),
    api<SecurityResponse>("/api/security", { key }),
  ])
  return {
    meta,
    models,
    accounts,
    keys,
    security,
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
    const cached = parsed as CachedSnapshot
    return {
      ...cached,
      security: { ...cached.security, proxyKey: "", adminKey: "" },
    }
  } catch {
    return null
  }
}

export function writeSnapshot(snapshot: CachedSnapshot) {
  try {
    // The security panel needs the live values while the tab is open, but the
    // reload cache does not: a fresh fetch repopulates them after login. Keep
    // master/admin credentials out of sessionStorage so an XSS or a shared
    // browser profile has one less copy to read.
    const cached: CachedSnapshot = {
      ...snapshot,
      security: { ...snapshot.security, proxyKey: "", adminKey: "" },
    }
    sessionStorage.setItem(SNAPSHOT_STORAGE, JSON.stringify(cached))
  } catch {
    // Storage can be unavailable in restricted browser contexts.
  }
}
