import { expect, test, type Page, type Route } from "@playwright/test"

const success = { id: "success", ts: 1700000000100, model: "cline-pass/history-success", ms: 100, stream: false, error: null }
const failure = { id: "failure", ts: 1700000000000, model: "cline-pass/history-failure", ms: 100, stream: false, error: "test failure" }
const payload = (history: Array<typeof success | typeof failure>, nextCursor = "") => ({ history, total: history.length, offset: 0, limit: 50, hasMore: !!nextCursor, nextCursor })

async function signIn(page: Page) {
  await page.goto("/")
  await page.getByLabel("管理密钥").fill("sk-e2e-admin")
  await page.getByRole("button", { name: "进入控制台" }).click()
  await page.getByRole("tab", { name: "请求历史" }).click()
  await expect(page.getByText(success.model, { exact: true })).toBeVisible()
}

for (const source of ["history panel", "global refresh"]) {
  test(`a late ${source} response cannot undo the failure filter`, async ({ page }) => {
    let holdNext = false
    let held!: Route
    let signal!: () => void
    const started = new Promise<void>((resolve) => { signal = resolve })
    await page.route("**/api/history*", async (route) => {
      const onlyErrors = new URL(route.request().url()).searchParams.get("result") === "error"
      if (holdNext && !onlyErrors) {
        holdNext = false
        held = route
        signal()
        return
      }
      await route.fulfill({ json: payload(onlyErrors ? [failure] : [success, failure]) })
    })
    await signIn(page)
    holdNext = true
    const buttons = page.getByRole("button", { name: "刷新", exact: true })
    const refresh = source === "history panel" ? buttons.last() : buttons.first()
    await refresh.click()
    await started
    const filtered = page.waitForResponse((response) => response.url().includes("/api/history?") && response.url().includes("result=error"))
    await page.getByRole("button", { name: "只看失败", exact: true }).click()
    await filtered
    await expect(page.getByText("刷新中…", { exact: true })).toHaveCount(0)
    await expect(page.getByText(success.model, { exact: true })).toHaveCount(0)
    await held.fulfill({ json: payload([success, failure]) })
    await expect(refresh).toBeEnabled()
    await expect(page.getByText(success.model, { exact: true })).toHaveCount(0)
    await expect(page.getByText(failure.model, { exact: true })).toBeVisible()
    // A later top-level refresh must keep using the selected query as well.
    await buttons.first().click()
    await expect(buttons.first()).toBeEnabled()
    await expect(page.getByText(success.model, { exact: true })).toHaveCount(0)
  })
}

test("load more sends the cursor and appends each row once", async ({ page }) => {
  const oldest = { ...success, id: "oldest", model: "cline-pass/history-oldest" }
  let cursor = ""
  await page.route("**/api/history*", async (route) => {
    const query = new URL(route.request().url()).searchParams
    cursor = query.get("cursor") ?? ""
    expect(query.has("offset")).toBe(false)
    await route.fulfill({ json: cursor
      ? { ...payload([oldest]), total: 3 }
      : { ...payload([success, failure], "failure"), total: 3 } })
  })
  await signIn(page)
  await page.getByRole("button", { name: /加载更多/ }).click()
  await expect(page.getByText(oldest.model, { exact: true })).toBeVisible()
  expect(cursor).toBe("failure")
  await expect(page.getByText(success.model, { exact: true })).toHaveCount(1)
  await expect(page.getByText(failure.model, { exact: true })).toHaveCount(1)
  await expect(page.getByRole("button", { name: /加载更多/ })).toHaveCount(0)
})

test("expanded routing details keep neighbouring columns stable on desktop and fit phone cards", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  const mismatch = {
    ...failure, id: "routing-mismatch", model: "cline-pass/deepseek-v4.1-flash", stream: true,
    account: "账号1", provider: "openai-compatible-private", resolved: "openai-compatible-private",
    canonical: "private/deepseek-v4p1-flash-contributor", kind: "routing-check",
    gatewayHas: ["implicit-caching", "reasoning", "structured-output", "tool-use", "vision"],
    targetProviders: ["deepseek"], routingStatus: "mismatch",
    error: "目标 deepseek，实际 openai-compatible-private",
    usage: { promptTokens: 33, completionTokens: 12, cost: 0.000002 },
  }
  const longEntry = { ...success, id: "long-model", model: `cline-pass/${"long-model-".repeat(20)}`, canonical: `private/${"long-canonical-".repeat(20)}`, account: "a very long account name" }
  await page.route("**/api/history*", route => route.fulfill({ json: payload([mismatch, success, longEntry]) }))
  await signIn(page)
  const row = page.getByRole("row").filter({ has: page.getByRole("button", { name: "路由对照 · 未命中目标" }) })
  const neighbour = page.getByRole("row").filter({ hasText: success.model })
  const widths = await neighbour.locator("td").evaluateAll(cells => cells.map(cell => cell.getBoundingClientRect().width))
  const height = (await row.boundingBox())!.height
  const scroller = page.locator('[data-slot="table-container"]').last()
  expect(await scroller.evaluate(node => node.scrollWidth - node.clientWidth)).toBeLessThanOrEqual(1)
  await row.getByRole("button", { name: "路由对照 · 未命中目标" }).click()
  const details = page.getByRole("region", { name: `请求详情 ${mismatch.model}` })
  await expect(details.getByText("openai-compatible-private", { exact: true })).toHaveCount(1)
  await expect(page.getByText(mismatch.error, { exact: true })).toHaveCount(0)
  expect(await neighbour.locator("td").evaluateAll(cells => cells.map(cell => cell.getBoundingClientRect().width))).toEqual(widths)
  expect((await row.boundingBox())!.height).toBe(height)
  expect(await scroller.evaluate(node => node.scrollWidth - node.clientWidth)).toBeLessThanOrEqual(1)
  await page.screenshot({ path: test.info().outputPath("history-routing-details.png"), fullPage: true })

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole("table")).toHaveCount(0)
  const card = page.getByRole("article").filter({ hasText: mismatch.model })
  await card.getByRole("button", { name: "路由对照 · 未命中目标" }).click()
  await expect(card.getByText("openai-compatible-private", { exact: true })).toHaveCount(1)
  await expect(page.getByText(mismatch.error, { exact: true })).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
  await page.screenshot({ path: test.info().outputPath("history-routing-details-mobile.png"), fullPage: true })
})
