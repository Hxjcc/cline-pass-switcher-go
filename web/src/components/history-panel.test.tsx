import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"
import { TooltipProvider } from "@/components/ui/tooltip"
import { HistoryPanel } from "./history-panel"
import { HISTORY_COLUMNS_STORAGE, HISTORY_COLUMN_ORDER_STORAGE, LEGACY_HISTORY_COLUMNS_STORAGE, PREVIOUS_HISTORY_COLUMNS_STORAGE } from "@/lib/history-columns"
import type { HistoryItem } from "@/types"

afterEach(() => {
  cleanup()
  localStorage.removeItem(HISTORY_COLUMNS_STORAGE)
  localStorage.removeItem(LEGACY_HISTORY_COLUMNS_STORAGE)
  localStorage.removeItem(PREVIOUS_HISTORY_COLUMNS_STORAGE)
  localStorage.removeItem(HISTORY_COLUMN_ORDER_STORAGE)
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

test("the main row groups reasoning and gateway price beneath their parent values", () => {
  renderPanel()
  expect(screen.getByRole("columnheader", { name: "原始请求模型" })).toBeTruthy()
  expect(screen.getByRole("columnheader", { name: "首字 / 总耗时" })).toBeTruthy()
  expect(within(row()).getByText("94.0%")).toBeTruthy()
  expect(within(row()).getByText("4.2s / 18.2s")).toBeTruthy()
  expect(screen.queryByText(entry.canonical!)).toBeNull()
  expect(row().querySelector('[data-history-column="tokens"] [data-history-reasoning]')?.textContent).toBe("思考 5,740")
  expect(row().querySelector('[data-history-column="cost"] [data-history-gateway-cost]')?.textContent).toBe("↳ 网关 $0.0085")
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

test("column choices persist, keep the model, and reset to the ten common fields", async () => {
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
  expect(screen.getAllByRole("columnheader")).toHaveLength(11)
})

test.each(["not-json", JSON.stringify({ cost: true })])("invalid preferences restore the common columns: %s", saved => {
  localStorage.setItem(HISTORY_COLUMNS_STORAGE, saved)
  renderPanel()
  expect(screen.getAllByRole("columnheader")).toHaveLength(11)
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

test("restored common fields show requested-to-upstream effort and the request type", () => {
  renderPanel([{ ...entry, kind: "responses", requestedEffort: "xhigh", effort: "max" }])
  expect(screen.getByRole("columnheader", { name: "思考强度" })).toBeTruthy()
  expect(screen.getByRole("columnheader", { name: "类型 / 方式" })).toBeTruthy()
  expect(within(row()).getByText("xhigh → max").title).toContain("实际转发给上游 max")
  expect(within(row()).getByText("Responses · 流式")).toBeTruthy()
  expect(screen.queryByRole("columnheader", { name: "思考 Token" })).toBeNull()
  expect(screen.queryByRole("columnheader", { name: "背后模型" })).toBeNull()
  expect(screen.queryByRole("columnheader", { name: "网关市价" })).toBeNull()
})

test("the full underlying model remains an optional column and persists", async () => {
  renderPanel()
  fireEvent.click(screen.getByRole("button", { name: "显示列" }))
  fireEvent.click(await screen.findByRole("menuitemcheckbox", { name: "背后模型" }))
  expect(screen.getByRole("columnheader", { name: "背后模型" })).toBeTruthy()
  expect(screen.queryByRole("menuitemcheckbox", { name: "思考 Token" })).toBeNull()
  expect(screen.queryByRole("menuitemcheckbox", { name: "网关市价" })).toBeNull()
  expect(within(row()).getByText(entry.canonical!)).toBeTruthy()
  await waitFor(() => expect(JSON.parse(localStorage.getItem(HISTORY_COLUMNS_STORAGE)!)).toContain("canonical"))
  cleanup()
  renderPanel()
  expect(screen.getByRole("columnheader", { name: "背后模型" })).toBeTruthy()
  expect(within(row()).getByText(entry.canonical!)).toBeTruthy()
})

test("existing column choices migrate while new common fields become available", () => {
  localStorage.setItem(PREVIOUS_HISTORY_COLUMNS_STORAGE, JSON.stringify(["model", "cache", "tokens"]))
  renderPanel()
  for (const label of ["原始请求模型", "缓存命中", "输入 → 输出", "思考强度", "类型 / 方式"]) {
    expect(screen.getByRole("columnheader", { name: label })).toBeTruthy()
  }
  expect(screen.queryByRole("columnheader", { name: "费用" })).toBeNull()
  expect(screen.queryByRole("columnheader", { name: "网关市价" })).toBeNull()
  cleanup()
  localStorage.setItem(HISTORY_COLUMNS_STORAGE, JSON.stringify(["model", "cache"]))
  renderPanel()
  expect(screen.queryByRole("columnheader", { name: "思考强度" })).toBeNull()
  expect(screen.queryByRole("columnheader", { name: "类型 / 方式" })).toBeNull()
})

test("saved order applies to headers and values while ignoring duplicate and unknown fields", () => {
  localStorage.setItem(HISTORY_COLUMNS_STORAGE, JSON.stringify(["model", "cache", "cost"]))
  localStorage.setItem(HISTORY_COLUMN_ORDER_STORAGE, JSON.stringify(["cost", "cache", "cost", "unknown", "model"]))
  renderPanel()
  expect(screen.getAllByRole("columnheader").map(node => node.textContent)).toEqual(["费用", "缓存命中", "原始请求模型", "详情"])
  expect([...row().querySelectorAll("[data-history-column]")].map(node => node.getAttribute("data-history-column"))).toEqual(["cost", "cache", "model"])
  const saved = JSON.parse(localStorage.getItem(HISTORY_COLUMN_ORDER_STORAGE)!)
  expect(saved).toHaveLength(11)
  expect(new Set(saved).size).toBe(11)
  expect(saved).not.toContain("unknown")
})

test("keyboard sorting persists and reset restores both order and visibility", async () => {
  renderPanel()
  fireEvent.click(screen.getByRole("button", { name: "显示列" }))
  const handle = await screen.findByRole("menuitem", { name: "调整原始请求模型顺序" })
  fireEvent.keyDown(handle, { key: "ArrowUp", altKey: true })
  expect(screen.getAllByRole("columnheader")[0].textContent).toBe("原始请求模型")
  expect(screen.getByRole("menuitemcheckbox", { name: "原始请求模型" }).getAttribute("aria-disabled")).toBe("true")
  await waitFor(() => expect(JSON.parse(localStorage.getItem(HISTORY_COLUMN_ORDER_STORAGE)!)[0]).toBe("model"))
  cleanup()
  renderPanel()
  expect(screen.getAllByRole("columnheader")[0].textContent).toBe("原始请求模型")
  fireEvent.click(screen.getByRole("button", { name: "显示列" }))
  fireEvent.click(await screen.findByRole("menuitem", { name: "恢复默认" }))
  expect(screen.getAllByRole("columnheader")[0].textContent).toBe("时间")
  expect(screen.getAllByRole("columnheader")).toHaveLength(11)
})

test("invalid saved order keeps the existing visibility choices", () => {
  localStorage.setItem(HISTORY_COLUMNS_STORAGE, JSON.stringify(["model", "cache"]))
  localStorage.setItem(HISTORY_COLUMN_ORDER_STORAGE, "not-json")
  renderPanel()
  expect(screen.getAllByRole("columnheader").map(node => node.textContent)).toEqual(["原始请求模型", "缓存命中", "详情"])
})

test.each([
  ["length", "长度截断"],
  ["content_filter", "内容过滤"],
])("an abnormal finish stays visible in the main row: %s", (finishReason, label) => {
  renderPanel([{ ...entry, finishReason }])
  expect(within(row()).getByText(label)).toBeTruthy()
})

test("routing warnings and retry counts stay visible with the provider column hidden", () => {
  localStorage.setItem(HISTORY_COLUMNS_STORAGE, JSON.stringify(["model", "cache"]))
  renderPanel([{ ...entry, routingStatus: "mismatch", fallback: true, fallbackReason: "ignored",
    trace: [{ upstream: "deepseek", status: 503, ms: 10 }, { upstream: "baseten", status: 200, ms: 10 }],
    gatewayAttempts: [{ provider: "deepseek", status: 429, success: false }, { provider: "deepseek", status: 503, success: false }, { provider: "baseten", status: 200, success: true }],
  }])
  for (const label of ["未命中目标", "忽略偏好", "重试 1 次", "网关重试 2 次"]) {
    expect(within(row()).getByText(label)).toBeTruthy()
  }
  expect(screen.queryByRole("columnheader", { name: "实际上游" })).toBeNull()
})

test("successful gateway legs and ordinary tool calls do not look like retries or failures", () => {
  renderPanel([{ ...entry, finishReason: "tool_calls", gatewayAttempts: [{ provider: "deepseek", status: 200, success: true }, { provider: "deepseek", status: 200, success: true }] }])
  expect(within(row()).queryByText(/重试|长度截断|内容过滤/)).toBeNull()
  expect(row().querySelector('[data-history-column="mode"] [title]')?.getAttribute("title")).toBe("调用工具")
})

test("former detail-column preferences merge into parent columns without losing order", () => {
  localStorage.setItem(HISTORY_COLUMNS_STORAGE, JSON.stringify(["model", "reasoning", "gatewayCost"]))
  localStorage.setItem(HISTORY_COLUMN_ORDER_STORAGE, JSON.stringify(["gatewayCost", "time", "reasoning", "model", "cost", "tokens"]))
  renderPanel()
  expect(screen.getAllByRole("columnheader").map(node => node.textContent)).toEqual(["费用", "输入 → 输出", "原始请求模型", "详情"])
  expect(within(row()).getByText("思考 5,740")).toBeTruthy()
  expect(within(row()).getByText("↳ 网关 $0.0085")).toBeTruthy()
  expect(JSON.parse(localStorage.getItem(HISTORY_COLUMNS_STORAGE)!)).toEqual(["cost", "tokens", "model"])
  const order = JSON.parse(localStorage.getItem(HISTORY_COLUMN_ORDER_STORAGE)!)
  expect(order).not.toContain("reasoning")
  expect(order).not.toContain("gatewayCost")
})

test("reported zero reasoning and gateway price differ from missing values", () => {
  localStorage.setItem(HISTORY_COLUMNS_STORAGE, JSON.stringify(["model", "reasoning", "gatewayCost"]))
  renderPanel([{ ...entry, usage: { reasoningTokens: 0, gatewayCost: 0 } }])
  expect(row().querySelector('[data-history-reasoning]')?.textContent).toBe("思考 0")
  expect(row().querySelector('[data-history-gateway-cost]')?.textContent).toBe("↳ 网关 $0.000000")
  cleanup()
  renderPanel([{ ...entry, usage: undefined }])
  expect(row().querySelector('[data-history-column="tokens"]')?.textContent).toBe("—")
  expect(row().querySelector('[data-history-column="cost"]')?.textContent).toBe("—")
  expect(row().querySelector('[data-history-reasoning]')).toBeNull()
  expect(row().querySelector('[data-history-gateway-cost]')).toBeNull()
})

test("an identical gateway price is not repeated beneath the actual charge", () => {
  renderPanel([{ ...entry, usage: { cost: 0, gatewayCost: 0 } }])
  expect(row().querySelector('[data-history-column="cost"]')?.textContent).toBe("$0.000000")
  expect(row().querySelector('[data-history-gateway-cost]')).toBeNull()
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
  expect(within(row()).getByText("压缩 · 流式")).toBeTruthy()
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
  localStorage.setItem(HISTORY_COLUMN_ORDER_STORAGE, JSON.stringify(["cache", "model"]))
  try {
    renderPanel()
    const card = screen.getByRole("article")
    expect(within(card).getByText("94.0%")).toBeTruthy()
    expect(within(card).queryByText("账号1")).toBeNull()
    expect([...card.querySelectorAll("[data-history-card-column]")].map(node => node.getAttribute("data-history-card-column"))).toEqual(["cache", "model"])
    expect(screen.queryByRole("table")).toBeNull()
    fireEvent.click(within(card).getByText("94.0%"))
    expect(within(await screen.findByRole("dialog")).getByText(entry.canonical!)).toBeTruthy()
  } finally { window.matchMedia = previous }
})
