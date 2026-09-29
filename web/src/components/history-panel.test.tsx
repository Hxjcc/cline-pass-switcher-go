import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"

import { TooltipProvider } from "@/components/ui/tooltip"
import { HistoryPanel } from "./history-panel"
import type { HistoryItem } from "@/types"

afterEach(cleanup)

const noop = async () => {}

function renderPanel(
  history: HistoryItem[],
  options: { pending?: boolean; hasMore?: boolean } = {},
) {
  render(
    <TooltipProvider>
      <HistoryPanel
        history={history}
        total={history.length}
        hasMore={options.hasMore ?? false}
        pending={options.pending}
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
  // The tooltip names the failing status so a rate limit is distinguishable
  // from a server error.
  const badge = screen.getByText("网关 2 次")
  expect(badge.getAttribute("title")).toContain("deepseek 失败 429")
})

// A pinned channel that never shows up in the gateway's attempt list was not
// failed over — the preference was simply ignored, which is a different badge.
test("labels an ignored preference instead of a fallback", () => {
  renderPanel([
    {
      ...reroutedEntry,
      fallbackReason: "ignored",
      gatewayAttempts: [{ provider: "deepseek", status: 200, success: true, ms: 800 }],
    },
  ])
  expect(screen.getByText("忽略偏好")).toBeTruthy()
  expect(screen.queryByText("降级")).toBeNull()
})

test("shows the gateway total next to the ledger cost", () => {
  renderPanel([reroutedEntry])
  expect(screen.getByText("$0.0017")).toBeTruthy()
  // The multiplier is the honest headline: the gateway total is its own price
  // list, not a fee the ledger charges.
  expect(screen.getByText(/网关 \$0\.0085 ×5\.0/)).toBeTruthy()
})

test("shows the cache hit rate when the provider reports hit and miss", () => {
  renderPanel([reroutedEntry])
  expect(screen.getByText(/缓存 99\.9%/)).toBeTruthy()
})

// alibaba publishes no per-provider cache counters, but usage still carries the
// cached token count, so the rate is derived instead of showing a raw count.
test("derives the cache rate when the provider reports only the cached count", () => {
  renderPanel([
    {
      ...reroutedEntry,
      provider: "alibaba",
      resolved: "alibaba",
      fallback: false,
      usage: {
        promptTokens: 139_700,
        completionTokens: 401,
        reasoningTokens: 212,
        cachedTokens: 139_300,
        cost: 0.0007,
      },
      gatewayAttempts: undefined,
    },
  ])
  expect(screen.getByText(/缓存 99\.7%/)).toBeTruthy()
})

// A gateway tool turn runs two model legs: usage sums both, while the provider's
// hit/miss counters describe one. The rate has to come from the summed counts,
// and the per-leg split must not be shown next to them.
test("ignores per-leg cache counters on a multi-leg turn", () => {
  renderPanel([
    {
      ...reroutedEntry,
      gatewayAttempts: undefined,
      usage: {
        promptTokens: 148_300,
        completionTokens: 6_113,
        reasoningTokens: 5_740,
        cachedTokens: 139_400,
        totalTokens: 154_400,
        cost: 0.0054,
        cacheHitTokens: 69_800,
        cacheMissTokens: 8_728,
      },
    },
  ])
  expect(screen.getByText(/缓存 94\.0%/)).toBeTruthy()
  const row = screen.getByText(/缓存 94\.0%/)
  const title = row.closest("[title]")?.getAttribute("title") ?? ""
  expect(title).toContain("缓存 139.4k")
  expect(title).not.toContain("命中")
})

// A filter change still being confirmed by the server keeps the rows visible
// and only marks itself as pending, so the toggle never looks stuck.
test("marks a pending filter change and blocks paging until it lands", () => {
  renderPanel([reroutedEntry], { pending: true, hasMore: true })
  expect(screen.getByText("刷新中…")).toBeTruthy()
  const more = screen.getByRole("button", { name: /加载更多/ }) as HTMLButtonElement
  expect(more.disabled).toBe(true)
})

// A phone cannot show eleven columns: the panel renders cards there instead of
// clipping the table, and only one of the two layouts is in the DOM.
test("renders cards instead of the wide table on a narrow viewport", () => {
  const original = window.matchMedia
  window.matchMedia = ((query: string) => ({
    matches: true,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
  try {
    const { container } = render(
      <TooltipProvider>
        <HistoryPanel
          history={[reroutedEntry]}
          total={1}
          hasMore={false}
          query={{ q: "", onlyErrors: false }}
          onQueryChange={() => {}}
          onRefresh={noop}
          onLoadMore={noop}
          onClear={noop}
        />
      </TooltipProvider>,
    )
    expect(container.querySelectorAll("article")).toHaveLength(1)
    expect(container.querySelector("table")).toBeNull()
  } finally {
    window.matchMedia = original
  }
})
