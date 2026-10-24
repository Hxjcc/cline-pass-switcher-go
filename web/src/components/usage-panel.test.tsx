import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"
import { UsagePanel } from "./usage-panel"
import { defaultUsageFilter } from "@/lib/usage-statistics"
import type { UsageReport, UsageTotals } from "@/types"

afterEach(() => { cleanup(); vi.restoreAllMocks() })
const totals: UsageTotals = { requests: 3, errors: 1, usageRequests: 2, costRequests: 2, inputTokens: 10000, outputTokens: 2000, reasoningTokens: 1200, cachedTokens: 8000, totalTokens: 12000, cost: 0.0012 }
const fixture: UsageReport = {
  from: defaultUsageFilter().from, to: defaultUsageFilter().to, timezone: "UTC+08:00", retentionDays: 365, startedAt: Date.now(), importedRequests: 2, availableFrom: defaultUsageFilter().from, generatedAt: Date.now(), summary: totals,
  trend: [{ day: defaultUsageFilter().to, ...totals }], models: [{ id: "cline-pass/alpha", name: "cline-pass/alpha", ...totals }], accounts: [{ id: "a", name: "Account A", ...totals }], keys: [{ id: "k", name: "Team key", ...totals }],
  filters: { models: [{ id: "cline-pass/alpha", name: "cline-pass/alpha" }], accounts: [{ id: "a", name: "Account A" }], keys: [{ id: "k", name: "Team key" }] },
}
const onError = vi.fn()
const response = (data = fixture) => new Response(JSON.stringify(data), { headers: { "Content-Type": "application/json" } })

test("statistics show reported totals without counting reasoning a second time", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(response())
  render(<UsagePanel authKey="placeholder" onError={onError} />)
  await screen.findByRole("img", { name: "每日输入与输出 Token 用量趋势" })
  expect(screen.getByText("12.0k", { selector: "div" })).toBeTruthy()
  expect(screen.getByText("其中思考 1,200")).toBeTruthy()
  expect(screen.getByText("成功 2 · 失败 1")).toBeTruthy()
  expect(screen.getByText(/首次已补入当时保留的 2 条/)).toBeTruthy()
})

test("date filters and model drill-down go to the statistics endpoint", async () => {
  const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async () => response())
  render(<UsagePanel authKey="placeholder" onError={onError} />)
  const model = await screen.findByRole("button", { name: "cline-pass/alpha" })
  fireEvent.click(model)
  await waitFor(() => expect(String(fetch.mock.calls.at(-1)?.[0])).toContain("model=cline-pass%2Falpha"))
  fireEvent.change(screen.getByLabelText("开始日期"), { target: { value: "2026-10-01" } })
  fireEvent.change(screen.getByLabelText("结束日期"), { target: { value: "2026-10-10" } })
  fireEvent.click(screen.getByRole("button", { name: "应用筛选" }))
  await waitFor(() => expect(String(fetch.mock.calls.at(-1)?.[0])).toContain("from=2026-10-01&to=2026-10-10"))
})

test("invalid dates do not issue a request and reset clears filters", async () => {
  const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async () => response())
  render(<UsagePanel authKey="placeholder" onError={onError} />)
  await screen.findByRole("button", { name: "cline-pass/alpha" })
  fireEvent.change(screen.getByLabelText("开始日期"), { target: { value: "2026-10-11" } })
  fireEvent.change(screen.getByLabelText("结束日期"), { target: { value: "2026-10-10" } })
  fireEvent.click(screen.getByRole("button", { name: "应用筛选" }))
  expect(await screen.findByText("开始日期不能晚于结束日期")).toBeTruthy()
  expect(fetch).toHaveBeenCalledTimes(1)
  fireEvent.click(screen.getByRole("button", { name: "重置统计筛选" }))
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
})

test("hidden statistics do not fetch until opened", async () => {
  const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(response())
  const { rerender } = render(<UsagePanel authKey="placeholder" active={false} onError={onError} />)
  expect(fetch).not.toHaveBeenCalled()
  rerender(<UsagePanel authKey="placeholder" active onError={onError} />)
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1))
})

test("failed refresh keeps existing numbers and displays the failure", async () => {
  const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(response()).mockResolvedValueOnce(new Response(JSON.stringify({ error: "stats unavailable" }), { status: 503 }))
  render(<UsagePanel authKey="placeholder" onError={onError} />)
  await screen.findByRole("button", { name: "cline-pass/alpha" })
  fireEvent.click(screen.getByRole("button", { name: "刷新统计" }))
  const alert = await screen.findByRole("alert")
  expect(within(alert).getByText("stats unavailable")).toBeTruthy()
  expect(screen.getByText("12.0k", { selector: "div" })).toBeTruthy()
  expect(fetch).toHaveBeenCalledTimes(2)
})

test("a late previous response cannot replace the newly selected date range", async () => {
  let resolveFirst!: (value: Response) => void
  vi.spyOn(globalThis, "fetch").mockReturnValueOnce(new Promise<Response>(resolve => { resolveFirst = resolve })).mockResolvedValueOnce(response({ ...fixture, summary: { ...totals, totalTokens: 24000 } }))
  render(<UsagePanel authKey="placeholder" onError={onError} />)
  fireEvent.click(screen.getByRole("button", { name: "近 30 天" }))
  await screen.findByText("24.0k", { selector: "div" })
  resolveFirst(response())
  await waitFor(() => expect(screen.queryByText("12.0k", { selector: "div" })).toBeNull())
  expect(screen.getByText("24.0k", { selector: "div" })).toBeTruthy()
})
