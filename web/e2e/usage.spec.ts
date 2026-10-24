import { expect, test, type Page } from "@playwright/test"

const day = (offset = 0) => new Date(Date.now() + 8 * 3600000 + offset * 86400000).toISOString().slice(0, 10)
const total = { requests: 846, errors: 7, usageRequests: 839, costRequests: 839, inputTokens: 24500000, outputTokens: 1100000, reasoningTokens: 780000, cachedTokens: 19800000, totalTokens: 25600000, cost: 0.0246 }

function report(query: URLSearchParams) {
  const from = query.get("from") || day(-6), to = query.get("to") || day()
  const filtered = Boolean(query.get("model") || query.get("account") || query.get("key"))
  const totals = filtered ? { ...total, requests: 123, errors: 1, inputTokens: 6000000, outputTokens: 300000, totalTokens: 6300000, usageRequests: 122, costRequests: 122 } : total
  const models = ["cline-pass/deepseek-v4.1-flash-1017", "cline-pass/glm-5.3-flash", "cline-pass/deepseek-v4-pro"]
  const options = { models: models.map(id => ({ id, name: id })), accounts: [{ id: "account-a", name: "主账号" }, { id: "account-b", name: "备用账号" }], keys: [{ id: "key-codex", name: "Codex" }, { id: "key-cline", name: "Cline" }] }
  return { from, to, timezone: "UTC+08:00", retentionDays: 365, startedAt: Date.now(), importedRequests: 128, availableFrom: day(-29), generatedAt: Date.now(), summary: totals, filters: options,
    trend: Array.from({ length: 7 }, (_, i) => ({ ...total, day: day(i - 6), inputTokens: [2100000, 3400000, 2800000, 4600000, 3200000, 3900000, 4500000][i], outputTokens: 150000 + i * 17000 })),
    models: options.models.map((o, i) => ({ ...o, ...totals, totalTokens: totals.totalTokens * [0.62, 0.25, 0.13][i] })),
    accounts: options.accounts.map(o => ({ ...o, ...totals })), keys: options.keys.map(o => ({ ...o, ...totals })),
  }
}

async function signIn(page: Page) {
  await page.goto("/")
  await page.getByLabel("管理密钥").fill("sk-e2e-admin")
  await page.getByRole("button", { name: "进入控制台" }).click()
  await page.getByRole("tab", { name: "Token 统计", exact: true }).click()
}

test("statistics follow history, support combined filters, and stay within phone width", async ({ page }) => {
  let lastQuery = new URLSearchParams()
  await page.route("**/api/usage?*", route => { lastQuery = new URL(route.request().url()).searchParams; return route.fulfill({ json: report(lastQuery) }) })
  await page.setViewportSize({ width: 1600, height: 1150 })
  await signIn(page)
  const tabs = await page.getByRole("tab").allTextContents()
  expect(tabs.indexOf("Token 统计")).toBe(tabs.indexOf("请求历史") + 1)
  await expect(page.getByRole("img", { name: "每日输入与输出 Token 用量趋势" })).toBeVisible()
  await expect(page.getByText("25.6M", { exact: true }).first()).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("token-statistics-desktop.png"), fullPage: true })
  for (const [label, value] of [["模型", "cline-pass/deepseek-v4.1-flash-1017"], ["账号", "主账号"], ["代理密钥", "Codex"]]) {
    await page.getByRole("combobox", { name: label, exact: true }).click()
    await page.getByRole("option", { name: value, exact: true }).click()
  }
  await page.getByRole("button", { name: "应用筛选", exact: true }).click()
  await expect.poll(() => lastQuery.get("account")).toBe("account-a")
  expect(lastQuery.get("key")).toBe("key-codex")
  expect(lastQuery.get("model")).toBe("cline-pass/deepseek-v4.1-flash-1017")
  await expect(page.getByText("6.3M", { exact: true }).first()).toBeVisible()
  await page.getByRole("button", { name: "今天", exact: true }).click()
  await expect.poll(() => lastQuery.get("from")).toBe(day())
  expect(lastQuery.get("to")).toBe(day())
  await page.getByRole("button", { name: "按账号", exact: true }).click()
  await expect(page.getByRole("columnheader", { name: "账号", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "重置统计筛选", exact: true }).click()
  await expect.poll(() => lastQuery.get("model")).toBeNull()
  await page.getByRole("button", { name: "切换到暗色", exact: true }).click()
  await page.mouse.move(10, 1000)
  await page.screenshot({ path: test.info().outputPath("token-statistics-dark.png"), fullPage: true, animations: "disabled" })
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
  await expect(page.getByRole("button", { name: "应用筛选", exact: true })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("token-statistics-mobile.png"), fullPage: true })
})

test("invalid dates do not replace the statistics and empty results remain understandable", async ({ page }) => {
  let calls = 0
  await page.route("**/api/usage?*", route => { calls++; const data = report(new URL(route.request().url()).searchParams); return route.fulfill({ json: { ...data, summary: { ...total, requests: 0, usageRequests: 0, costRequests: 0 }, models: [], accounts: [], keys: [] } }) })
  await signIn(page)
  await expect(page.getByText("所选范围暂无记录", { exact: true })).toBeVisible()
  await page.getByLabel("开始日期", { exact: true }).fill("2026-10-25")
  await page.getByLabel("结束日期", { exact: true }).fill("2026-10-24")
  await page.getByRole("button", { name: "应用筛选", exact: true }).click()
  await expect(page.getByText("开始日期不能晚于结束日期", { exact: true })).toBeVisible()
  expect(calls).toBe(1)
})
