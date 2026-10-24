import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"
import { TooltipProvider } from "@/components/ui/tooltip"
import { HistoryPanel } from "./history-panel"
import { HISTORY_COLUMNS_STORAGE, LEGACY_HISTORY_COLUMNS_STORAGE } from "@/lib/history-columns"
import type { HistoryItem } from "@/types"

afterEach(() => {
  cleanup()
  localStorage.removeItem(HISTORY_COLUMNS_STORAGE)
  localStorage.removeItem(LEGACY_HISTORY_COLUMNS_STORAGE)
})

const noop = async () => {}
const entry: HistoryItem = {
  id: "one", ts: 1700000000000, model: "cline-pass/deepseek-v4.1-flash-1017",
  canonical: "deepseek/deepseek-v4.1-flash-1017", resolved: "deepseek", account: "账号1",
  ms: 18200, ttftMs: 4200, stream: true, kind: "chat", error: null,
  usage: { promptTokens: 148300, completionTokens: 6113, reasoningTokens: 5740, cachedTokens: 139400, totalTokens: 154413, cost: 0.0017, gatewayCost: 0.0085, cacheHitTokens: 69800, cacheMissTokens: 8728 },
  gatewayHas: ["vision", "implicit-caching"], targetProviders: ["deepseek"], routingStatus: "matched",
}

function renderPanel(history: HistoryItem[] = [entry], options: { pending?: boolean; hasMore?: boolean } = {}) {
  return render(<TooltipProvider><HistoryPanel history={history} total={history.length} hasMore={options.hasMore ?? false} pending={options.pending}
    query={{ q: "", onlyErrors: false }} onQueryChange={() => {}} onRefresh={noop} onLoadMore={noop} onClear={noop} /></TooltipProvider>)
}

function row() { return screen.getByText(entry.model, { selector: "[data-history-model]" }).closest("tr")! }
async function openDetails() {
  fireEvent.click(within(row()).getByRole("button", { name: "查看请求详情" }))
  return screen.findByRole("dialog", { name: "请求详情" })
}

test("the main row keeps the raw model, one-line token pair, and a dedicated cache percentage", () => {
  renderPanel()
  expect(screen.getByRole("columnheader", { name: "原始请求模型" })).toBeTruthy()
  expect(screen.getByRole("columnheader", { name: "首字 / 总耗时" })).toBeTruthy()
  expect(within(row()).getByText("94.0%")).toBeTruthy()
  expect(within(row()).getByText("4.2s / 18.2s")).toBeTruthy()
  expect(screen.queryByText(entry.canonical!)).toBeNull()
  expect(screen.queryByText("思考 5.7k")).toBeNull()
  expect(screen.queryByText(/网关 \$/)).toBeNull()
})

test("clicking a cache cell opens full details, and Escape closes them", async () => {
  renderPanel()
  fireEvent.click(within(row()).getByText("94.0%"))
  const dialog = await screen.findByRole("dialog", { name: "请求详情" })
  expect(within(dialog).getByText(entry.canonical!)).toBeTruthy()
  expect(within(dialog).getByText("139,400")).toBeTruthy()
  expect(within(dialog).getByText("5,740")).toBeTruthy()
  expect(within(dialog).getByText(/\$0\.0085 ×5\.0/)).toBeTruthy()
  fireEvent.keyDown(within(dialog).getByRole("button", { name: "关闭详情" }), { key: "Escape", code: "Escape" })
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
})

test("opening an existing record performs no network request", async () => {
  const fetch = vi.spyOn(globalThis, "fetch")
  renderPanel()
  await openDetails()
  expect(fetch).not.toHaveBeenCalled()
})

test("selecting model text for copying does not open the drawer", () => {
  renderPanel()
  const model = screen.getByText(entry.model, { selector: "[data-history-model]" })
  vi.spyOn(window, "getSelection").mockReturnValue({ isCollapsed: false, anchorNode: model.firstChild } as Selection)
  fireEvent.click(model)
  expect(screen.queryByRole("dialog")).toBeNull()
})

test("column choices persist, keep the model, and reset to all eight common fields", async () => {
  renderPanel()
  fireEvent.click(screen.getByRole("button", { name: "显示列" }))
  fireEvent.click(await screen.findByRole("menuitemcheckbox", { name: "费用" }))
  expect(screen.queryByRole("columnheader", { name: "费用" })).toBeNull()
  expect(screen.getByRole("menuitemcheckbox", { name: "原始请求模型" }).getAttribute("aria-disabled")).toBe("true")
  await waitFor(() => expect(JSON.parse(localStorage.getItem(HISTORY_COLUMNS_STORAGE)!)).not.toContain("cost"))
  cleanup()
  renderPanel()
  expect(screen.queryByRole("columnheader", { name: "费用" })).toBeNull()
  fireEvent.click(screen.getByRole("button", { name: "显示列" }))
  fireEvent.click(await screen.findByRole("menuitem", { name: "恢复默认" }))
  expect(screen.getByRole("columnheader", { name: "费用" })).toBeTruthy()
  expect(screen.getByRole("columnheader", { name: "缓存命中" })).toBeTruthy()
  expect(screen.getAllByRole("columnheader")).toHaveLength(9)
})

test.each(["not-json", JSON.stringify({ cost: true })])("invalid preferences restore the common columns: %s", saved => {
  localStorage.setItem(HISTORY_COLUMNS_STORAGE, saved)
  renderPanel()
  expect(screen.getAllByRole("columnheader")).toHaveLength(9)
})

test("legacy preferences migrate to combined latency and enable the new cache column", () => {
  localStorage.setItem(LEGACY_HISTORY_COLUMNS_STORAGE, JSON.stringify(["model", "cost", "ttft", "canonical"]))
  renderPanel()
  expect(screen.getByRole("columnheader", { name: "原始请求模型" })).toBeTruthy()
  expect(screen.getByRole("columnheader", { name: "首字 / 总耗时" })).toBeTruthy()
  expect(screen.getByRole("columnheader", { name: "缓存命中" })).toBeTruthy()
  expect(screen.queryByRole("columnheader", { name: "费用" })).toBeNull()
})

test("unknown column keys are ignored and the model cannot be removed by saved preferences", () => {
  localStorage.setItem(HISTORY_COLUMNS_STORAGE, JSON.stringify(["unknown", "cache"]))
  renderPanel()
  expect(screen.getByRole("columnheader", { name: "原始请求模型" })).toBeTruthy()
  expect(screen.getByRole("columnheader", { name: "缓存命中" })).toBeTruthy()
  expect(screen.getAllByRole("columnheader")).toHaveLength(3)
})

test("a reported zero cache rate differs from missing cache data", () => {
  renderPanel([{ ...entry, usage: { promptTokens: 100, completionTokens: 1, cachedTokens: 0 } }])
  expect(within(row()).getByText("0%")).toBeTruthy()
  cleanup()
  renderPanel([{ ...entry, usage: { promptTokens: 100, completionTokens: 1 } }])
  expect(within(row()).queryByText("0%")).toBeNull()
  expect(row().querySelector('[data-history-column="cache"]')?.textContent).toBe("—")
})

test("structured routing details replace only the duplicate generated mismatch error", async () => {
  const mismatch = { ...entry, routingStatus: "mismatch", resolved: "openai-compatible-private", error: "目标 deepseek，实际 openai-compatible-private" }
  renderPanel([mismatch])
  const dialog = await openDetails()
  expect(within(dialog).getAllByText("openai-compatible-private", { exact: true })).toHaveLength(1)
  expect(within(dialog).getByText("deepseek", { exact: true })).toBeTruthy()
  expect(within(dialog).queryByText(mismatch.error, { exact: true })).toBeNull()
})

test("additional upstream error information stays readable in the details", async () => {
  const error = "目标 deepseek，实际 openai-compatible-private；上游返回额度不足"
  renderPanel([{ ...entry, routingStatus: "mismatch", resolved: "openai-compatible-private", error }])
  expect(within(row()).queryByText(error)).toBeNull()
  expect(within(await openDetails()).getByText(error)).toBeTruthy()
})

test("gateway retry statuses and ignored-preference explanations remain in the drawer", async () => {
  renderPanel([{ ...entry, fallback: true, fallbackReason: "ignored", gatewayAttempts: [{ provider: "deepseek", status: 429, success: false, error: "rate limited" }, { provider: "baseten", status: 200, success: true }] }])
  const dialog = await openDetails()
  expect(within(dialog).getByText(/deepseek.*失败 429/)).toBeTruthy()
  expect(within(dialog).getByText("rate limited")).toBeTruthy()
  expect(within(dialog).getByText(/网关从未尝试你钉的渠道/)).toBeTruthy()
})

test("compaction warnings keep their full reasons in the details", async () => {
  renderPanel([{ ...entry, kind: "compact", degraded: true, degradeReason: "upstream 503", missingSummarySections: ["Next Move"] }])
  expect(within(row()).getByText("压缩")).toBeTruthy()
  expect(within(row()).getByText("压缩降级")).toBeTruthy()
  expect(within(row()).getByText("摘要缺段")).toBeTruthy()
  const dialog = await openDetails()
  expect(within(dialog).getByText("摘要缺 Next Move")).toBeTruthy()
  expect(within(dialog).getByText("压缩降级：upstream 503")).toBeTruthy()
})

test("pending filtering blocks paging until the new query lands", () => {
  renderPanel([entry], { pending: true, hasMore: true })
  expect(screen.getByText("刷新中…")).toBeTruthy()
  expect((screen.getByRole("button", { name: /加载更多/ }) as HTMLButtonElement).disabled).toBe(true)
})

test("phone cards respect selected fields and open the same details", async () => {
  const previous = window.matchMedia
  window.matchMedia = vi.fn((query: string) => ({ matches: true, media: query, onchange: null, addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {}, dispatchEvent: () => false }))
  localStorage.setItem(HISTORY_COLUMNS_STORAGE, JSON.stringify(["model", "cache"]))
  try {
    renderPanel()
    const card = screen.getByRole("article")
    expect(within(card).getByText("94.0%")).toBeTruthy()
    expect(within(card).queryByText("账号1")).toBeNull()
    expect(screen.queryByRole("table")).toBeNull()
    fireEvent.click(within(card).getByText("94.0%"))
    expect(within(await screen.findByRole("dialog")).getByText(entry.canonical!)).toBeTruthy()
  } finally { window.matchMedia = previous }
})
