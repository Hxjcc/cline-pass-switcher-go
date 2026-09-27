import { expect, test } from "vitest"

import { historyEntryMatches, historyQueryKey } from "@/lib/history-filter"
import type { HistoryItem } from "@/types"

function entry(overrides: Partial<HistoryItem>): HistoryItem {
  return {
    ts: 1,
    model: "cline-pass/deepseek-v4.1-flash",
    provider: "deepseek",
    ms: 100,
    stream: true,
    error: null,
    ...overrides,
  }
}

test("only-errors keeps failures and drops successes", () => {
  const failed = entry({ error: "upstream 429", provider: "baseten", resolved: "baseten" })
  const ok = entry({ error: null })
  expect(historyEntryMatches(failed, { q: "", onlyErrors: true })).toBe(true)
  expect(historyEntryMatches(ok, { q: "", onlyErrors: true })).toBe(false)
})

test("the text filter covers the fields the server matches", () => {
  const row = entry({ resolved: "baseten", generationId: "gen_01ABC", account: "账号1" })
  expect(historyEntryMatches(row, { q: "baseten", onlyErrors: false })).toBe(true)
  expect(historyEntryMatches(row, { q: "gen_01abc", onlyErrors: false })).toBe(true)
  expect(historyEntryMatches(row, { q: "账号1", onlyErrors: false })).toBe(true)
  expect(historyEntryMatches(row, { q: "fireworks", onlyErrors: false })).toBe(false)
})

test("cache keys separate the text and the error toggle", () => {
  expect(historyQueryKey({ q: "", onlyErrors: false })).not.toBe(historyQueryKey({ q: "", onlyErrors: true }))
  expect(historyQueryKey({ q: "abc", onlyErrors: true })).toBe(historyQueryKey({ q: " abc ", onlyErrors: true }))
})
