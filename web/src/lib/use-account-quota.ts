import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"

import { errorMessage } from "@/lib/api"
import type { Account, AccountQuota, QuotaResponse } from "@/types"

const storageKey = "cline-pass-switcher-account-quota-v1"
const cacheMaxAge = 5 * 60_000

interface QuotaEntry {
  keyPreview: string
  cachedAt: number
  quota: AccountQuota
  refreshError?: string
}

type QuotaCache = Record<string, QuotaEntry>

function readCache(): QuotaCache {
  try {
    const value = JSON.parse(sessionStorage.getItem(storageKey) || "{}") as QuotaCache
    return Object.fromEntries(Object.entries(value).filter(([id, entry]) =>
      entry?.quota?.accountId === id &&
      typeof entry.keyPreview === "string" &&
      typeof entry.cachedAt === "number" &&
      Date.now() - entry.cachedAt < cacheMaxAge &&
      (!entry.quota.limits || Array.isArray(entry.quota.limits)),
    ))
  } catch {
    return {}
  }
}

// Restore the readout on the first render, then refresh it in place. Only
// display data and redacted key previews are cached, never account keys.
export function useAccountQuota(accounts: Account[], onQuota: (refresh?: boolean) => Promise<QuotaResponse>) {
  const [cache, setCache] = useState(readCache)
  const [loading, setLoading] = useState(true)
  const accountsRef = useRef(accounts)
  const requestSequence = useRef(0)
  const identity = JSON.stringify(accounts.map(({ id, keyPreview, enabled }) => [id, keyPreview, enabled]))

  useEffect(() => { accountsRef.current = accounts }, [accounts])

  useEffect(() => {
    try { sessionStorage.setItem(storageKey, JSON.stringify(cache)) } catch { /* Storage is optional. */ }
  }, [cache])

  const request = useCallback(async (force: boolean) => {
    const sequence = ++requestSequence.current
    const probedAccounts = accountsRef.current
    try {
      const response = await onQuota(force)
      if (sequence !== requestSequence.current) return
      setCache((previous) => {
        const next: QuotaCache = {}
        for (const account of probedAccounts) {
          const entry = previous[account.id]
          if (entry?.keyPreview === account.keyPreview) next[account.id] = entry
        }
        for (const quota of response.accounts) {
          const account = probedAccounts.find(({ id }) => id === quota.accountId)
          if (!account) continue
          const previousEntry = next[account.id]
          next[account.id] = !quota.ok && previousEntry?.quota.ok
            ? { ...previousEntry, refreshError: quota.error || "配额更新失败" }
            : { keyPreview: account.keyPreview, cachedAt: Date.now(), quota }
        }
        return next
      })
    } catch (error) {
      if (sequence === requestSequence.current && force) toast.error(errorMessage(error))
    } finally {
      if (sequence === requestSequence.current) setLoading(false)
    }
  }, [onQuota])

  const refresh = useCallback(async () => {
    setLoading(true)
    await request(true)
  }, [request])

  useEffect(() => {
    void request(false)
    const sequence = requestSequence
    // StrictMode and a changed account snapshot must start a fresh request,
    // rather than permanently canceling a one-shot probe.
    return () => { sequence.current++ }
  }, [request, identity])

  const invalidate = (ids: string[]) => {
    requestSequence.current++
    setCache((previous) => Object.fromEntries(Object.entries(previous).filter(([id]) => !ids.includes(id))))
  }

  const entries = Object.fromEntries(accounts.flatMap((account) => {
    const entry = cache[account.id]
    return entry?.keyPreview === account.keyPreview ? [[account.id, entry]] : []
  })) as QuotaCache
  const updatedAt = Math.max(0, ...Object.values(entries).map(({ cachedAt }) => cachedAt))
  return { entries, loading, updatedAt, refresh, invalidate }
}
