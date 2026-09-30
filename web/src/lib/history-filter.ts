import type { HistoryItem } from "@/types"

/** Names one filter combination for the history response cache. */
export function historyQueryKey(query: { q: string; onlyErrors: boolean }) {
  return `${query.q.trim()}\u0000${query.onlyErrors ? "error" : "all"}`
}

/**
 * Mirrors the server's history filter so a pending change can show the rows the
 * client already holds instead of waiting for the round trip. The server stays
 * authoritative for the full result set.
 */
export function historyEntryMatches(
  entry: HistoryItem,
  query: { q: string; onlyErrors: boolean },
) {
  if (query.onlyErrors && entry.error == null) return false
  const needle = query.q.trim().toLowerCase()
  if (!needle) return true
  return [
    entry.model,
    entry.provider,
    entry.resolved,
    entry.canonical,
    entry.account,
    entry.kind,
    entry.effort,
    entry.generationId,
    entry.session,
    entry.error ?? "",
  ].some((field) => (field ?? "").toLowerCase().includes(needle))
}
