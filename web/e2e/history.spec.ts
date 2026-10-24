import { expect, test, type Locator, type Page, type Route } from "@playwright/test"

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

async function expectFullTextWithinCell(text: Locator) {
  const fits = await text.evaluate(node => {
    const range = document.createRange()
    range.selectNodeContents(node)
    const cell = node.closest("td")!.getBoundingClientRect()
    return [...range.getClientRects()].every(rect => rect.left >= cell.left - 1 && rect.right <= cell.right + 1 && rect.top >= cell.top - 1 && rect.bottom <= cell.bottom + 1)
  })
  expect(fits, "the full text should fit its cell instead of being clipped").toBe(true)
}

test("compact history keeps raw models and cache visible, opens row details, and remembers column choices", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  const routed = {
    ...success, id: "routed", model: "cline-pass/deepseek-v4.1-flash-1017", stream: true,
    account: "账号1", provider: "deepseek", resolved: "deepseek", canonical: "deepseek/deepseek-v4.1-flash-1017",
    gatewayHas: ["implicit-caching", "reasoning", "structured-output", "tool-use", "vision"],
    targetProviders: ["deepseek"], routingStatus: "matched", ttftMs: 1300, ms: 6500,
    usage: { promptTokens: 126600, completionTokens: 640, reasoningTokens: 523, cachedTokens: 126000, cost: 0.000002 },
  }
  await page.route("**/api/history*", route => route.fulfill({ json: payload([routed, success, failure]) }))
  await signIn(page)
  const row = page.getByRole("row").filter({ hasText: routed.model })
  const model = row.locator('[data-history-model]')
  await expectFullTextWithinCell(model)
  await expectFullTextWithinCell(row.getByText("99.5%", { exact: true }))
  await expect(page.getByText(routed.canonical, { exact: true })).toHaveCount(0)
  const color = await model.evaluate(node => getComputedStyle(node).color)
  const background = await row.evaluate(node => getComputedStyle(node).backgroundColor)
  await model.hover()
  expect(await model.evaluate(node => getComputedStyle(node).color)).toBe(color)
  expect(await row.evaluate(node => getComputedStyle(node).backgroundColor)).not.toBe(background)
  expect((await row.boundingBox())!.height).toBeLessThan(60)

  await page.getByRole("button", { name: "显示列", exact: true }).click()
  await expect(page.getByRole("menuitemcheckbox", { name: "原始请求模型", exact: true })).toBeDisabled()
  await expect(page.getByRole("menuitemcheckbox", { name: "缓存命中", exact: true })).toBeChecked()
  await page.getByRole("menuitemcheckbox", { name: "费用", exact: true }).click()
  await page.screenshot({ path: test.info().outputPath("history-columns-menu.png"), fullPage: true })
  await page.keyboard.press("Escape")
  await expect(page.getByRole("columnheader")).toHaveCount(8)
  await expect(page.getByRole("columnheader", { name: "背后模型", exact: true })).toHaveCount(0)
  const scroller = page.locator('[data-slot="table-container"]').last()
  await row.getByText("99.5%", { exact: true }).click()
  const details = page.getByRole("dialog", { name: "请求详情" })
  await expect(details.getByText(routed.canonical, { exact: true })).toBeVisible()
  await expect(details.getByText("126,000", { exact: true })).toBeVisible()
  await expect(details.getByText("523", { exact: true })).toBeVisible()
  await expect(details.getByText("$0.000002", { exact: true })).toBeVisible()
  await expect(details).toHaveCSS("opacity", "1")
  await page.screenshot({ path: test.info().outputPath("history-details-desktop.png"), fullPage: true })
  await page.keyboard.press("Escape")
  await expect(details).toHaveCount(0)
  await expect(row.getByRole("button", { name: "查看请求详情" })).toBeFocused()
  await row.locator('[data-history-column="time"]').click()
  await expect(details).toBeVisible()
  await row.locator('[data-history-column="time"]').click()
  await expect(details).toHaveCount(0)
  await expect(row).toHaveAttribute("aria-selected", "false")
  await row.getByRole("button", { name: "查看请求详情" }).click()
  await expect(details).toBeVisible()
  await row.getByRole("button", { name: "查看请求详情" }).press("Enter")
  await expect(details).toHaveCount(0)
  await row.locator('[data-history-column="time"]').click()
  await expect(details).toBeVisible()
  await page.locator('[data-history-model]').filter({ hasText: failure.model }).click()
  await expect(details.getByText("test failure", { exact: true })).toBeVisible()
  await details.getByRole("button", { name: "关闭详情" }).click()
  await expect(details).toHaveCount(0)

  for (const width of [1280, 1024, 820, 640]) {
    await page.setViewportSize({ width, height: 1000 })
    await expect.poll(() => scroller.evaluate(node => node.scrollWidth - node.clientWidth)).toBeLessThanOrEqual(1)
    await expect(page.getByRole("columnheader", { name: "缓存命中", exact: true })).toBeVisible()
    await expectFullTextWithinCell(model)
    await expectFullTextWithinCell(row.getByText("99.5%", { exact: true }))
  }
  await page.setViewportSize({ width: 1440, height: 1000 })
  await expect(page.getByRole("columnheader")).toHaveCount(8)
  await scroller.locator('xpath=ancestor::*[@data-slot="card"][1]').screenshot({ path: test.info().outputPath("history-panel.png") })
  await page.screenshot({ path: test.info().outputPath("history-columns-desktop.png"), fullPage: true })

  await page.reload()
  await expect(page.getByRole("columnheader")).toHaveCount(8)
  await expect(page.getByRole("columnheader", { name: "费用", exact: true })).toHaveCount(0)
  await page.setViewportSize({ width: 390, height: 844 })
  const card = page.getByRole("article").filter({ hasText: routed.model })
  await expect(card.getByText(routed.model, { exact: true })).toBeVisible()
  await expect(card.getByText(routed.canonical, { exact: true })).toHaveCount(0)
  await expect(card.getByText("99.5%", { exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
  await page.screenshot({ path: test.info().outputPath("history-columns-mobile.png"), fullPage: true })
  await card.getByText("99.5%", { exact: true }).click()
  await expect(details.getByText(routed.canonical, { exact: true })).toBeVisible()
  await expect(details).toHaveCSS("opacity", "1")
  const drawer = await details.boundingBox()
  expect(drawer!.x).toBeGreaterThanOrEqual(0)
  expect(drawer!.width).toBeLessThanOrEqual(390)
  await page.screenshot({ path: test.info().outputPath("history-details-mobile.png"), fullPage: true })
  await details.getByRole("button", { name: "关闭详情" }).click()
  await page.getByRole("button", { name: "显示列", exact: true }).click()
  await page.getByRole("menuitem", { name: "恢复默认", exact: true }).click()
  await expect(card.getByText("$0.000002", { exact: true })).toBeVisible()
  await page.setViewportSize({ width: 1440, height: 1000 })
  await expect(page.getByRole("columnheader")).toHaveCount(9)
})

test("unusually long raw models remain complete without forcing the page to scroll sideways", async ({ page }) => {
  const long = { ...success, id: "long", model: `cline-pass/${"long-model-".repeat(24)}`, usage: { promptTokens: 100, completionTokens: 1, cachedTokens: 99 } }
  await page.setViewportSize({ width: 1024, height: 900 })
  await page.route("**/api/history*", route => route.fulfill({ json: payload([long, success]) }))
  await signIn(page)
  await expect(page.getByRole("article").filter({ hasText: long.model })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
  await page.getByRole("article").filter({ hasText: long.model }).getByRole("button", { name: "查看请求详情" }).click()
  await expect(page.getByRole("dialog").getByText(long.model, { exact: true })).toBeVisible()
})
