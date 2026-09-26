import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"

import { TooltipProvider } from "@/components/ui/tooltip"
import { HistoryPanel } from "./history-panel"
import type { HistoryItem } from "@/types"

afterEach(cleanup)

const noop = async () => {}

function renderPanel(history: HistoryItem[]) {
  render(
    <TooltipProvider>
      <HistoryPanel
        history={history}
        total={history.length}
        hasMore={false}
        query={{ q: "", onlyErrors: false }}
        onQueryChange={() => {}}
        onRefresh={noop}
        onLoadMore={noop}
        onClear={noop}
      />
    </TooltipProvider>,
  )
}

const compactEntry: HistoryItem = {
  ts: 1_700_000_000_000,
  model: "cline-pass/deepseek-v4.1-flash",
  ms: 1000,
  stream: true,
  kind: "compact",
  account: "main",
  error: null,
}

// A compaction can succeed with a summary that skipped anchored sections. That
// is advisory rather than an error, but it has to be visible in the table.
test("flags a compaction whose summary skipped anchored sections", () => {
  renderPanel([{ ...compactEntry, missingSummarySections: ["Next Move"] }])
  expect(screen.getByText("摘要缺 Next Move")).toBeTruthy()
})

test("leaves complete compactions unflagged", () => {
  renderPanel([compactEntry])
  expect(screen.queryByText(/摘要缺/)).toBeNull()
  expect(screen.getByText("压缩")).toBeTruthy()
})

// A degraded compaction still answered the client with a valid item, so only
// the badge tells the operator that this turn is not a real summary.
test("flags a compaction that fell back to a placeholder", () => {
  renderPanel([{ ...compactEntry, degraded: true, degradeReason: "upstream 503" }])
  expect(screen.getByText("压缩降级")).toBeTruthy()
})

test("does not fetch anything on render", () => {
  const fetchSpy = vi.spyOn(globalThis, "fetch")
  renderPanel([compactEntry])
  expect(fetchSpy).not.toHaveBeenCalled()
  fetchSpy.mockRestore()
})

// One turn the gateway rerouted to baseten after deepseek answered 429. The
// numbers are from a live search turn: the ledger charged cost while the
// gateway total also carried the tool fee.
const reroutedEntry: HistoryItem = {
  ts: 1_700_000_000_000,
  model: "cline-pass/deepseek-v4.1-flash",
  provider: "baseten",
  resolved: "baseten",
  fallback: true,
  ms: 18_200,
  ttftMs: 4_200,
  stream: true,
  kind: "chat",
  account: "main",
  error: null,
  usage: {
    promptTokens: 186_300,
    completionTokens: 3_128,
    reasoningTokens: 2_979,
    totalTokens: 189_428,
    cost: 0.0017,
    gatewayCost: 0.0085,
    inputCost: 0.0006,
    outputCost: 0.0011,
    surchargeCost: 0.0068,
    cacheHitTokens: 186_100,
    cacheMissTokens: 200,
  },
  gatewayAttempts: [
    { provider: "deepseek", status: 429, success: false, ms: 500 },
    { provider: "baseten", status: 200, success: true, ms: 1_000 },
  ],
}

test("flags a request the gateway rerouted", () => {
  renderPanel([reroutedEntry])
  expect(screen.getByText("降级")).toBeTruthy()
  expect(screen.getByText("网关 2 次")).toBeTruthy()
})

test("shows the gateway total next to the ledger cost", () => {
  renderPanel([reroutedEntry])
  expect(screen.getByText("$0.0017")).toBeTruthy()
  expect(screen.getByText(/网关 \$0\.0085/)).toBeTruthy()
})

test("shows the cache hit rate when the provider reports hit and miss", () => {
  renderPanel([reroutedEntry])
  expect(screen.getByText(/缓存 99\.9%/)).toBeTruthy()
})
