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

test("does not fetch anything on render", () => {
  const fetchSpy = vi.spyOn(globalThis, "fetch")
  renderPanel([compactEntry])
  expect(fetchSpy).not.toHaveBeenCalled()
  fetchSpy.mockRestore()
})
