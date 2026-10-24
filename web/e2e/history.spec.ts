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

test("column menu gives press feedback on the first interaction and later openings", async ({ page }) => {
  await page.route("**/api/history*", route => route.fulfill({ json: payload([success, failure]) }))
  await signIn(page)
  const trigger = page.getByRole("button", { name: "显示列", exact: true })
  const menu = page.getByRole("menu", { name: "选择显示列", exact: true })

  for (let opening = 0; opening < 2; opening += 1) {
    await trigger.hover()
    await page.mouse.down()
    try {
      await expect(menu).toBeVisible()
      await expect(trigger).toHaveAttribute("aria-expanded", "true")
      await expect.poll(() => trigger.evaluate(node => getComputedStyle(node).translate), {
        message: `press ${opening + 1} should have the same feedback while the pointer is held`,
      }).toBe("0px 1px")
    } finally {
      await page.mouse.up()
    }
    await expect.poll(() => trigger.evaluate(node => getComputedStyle(node).translate)).toBe("none")
    await page.keyboard.press("Escape")
    await expect(menu).toHaveCount(0)
    await expect(trigger).toBeFocused()
  }
  await trigger.press("Enter")
  await expect(menu).toBeVisible()
  await expect.poll(() => trigger.evaluate(node => getComputedStyle(node).translate)).toBe("none")
  await page.keyboard.press("Escape")
  await expect(menu).toHaveCount(0)
  await expect(trigger).toBeFocused()
})

test("column handles drag, cancel, persist, and reset the displayed order", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.route("**/api/history*", route => route.fulfill({ json: payload([success, failure]) }))
  await signIn(page)
  const trigger = page.getByRole("button", { name: "显示列", exact: true })
  const menu = page.getByRole("menu", { name: "选择显示列", exact: true })
  await trigger.click()
  const checked = await menu.getByRole("menuitemcheckbox").evaluateAll(nodes => nodes.map(node => [node.getAttribute("aria-label"), node.getAttribute("aria-checked")]))
  const drag = async (from: string, to: string) => {
    const source = await menu.locator(`[data-column-handle="${from}"]`).boundingBox()
    const target = await menu.locator(`[data-column-option="${to}"]`).boundingBox()
    await page.mouse.move(source!.x + source!.width / 2, source!.y + source!.height / 2)
    await page.mouse.down()
    await page.mouse.move(target!.x + 16, target!.y + 2, { steps: 12 })
  }
  await drag("cache", "time")
  await page.screenshot({ path: test.info().outputPath("history-column-drag.png"), fullPage: true })
  await page.mouse.up()
  await expect(menu.locator("[data-column-option]").first()).toHaveAttribute("data-column-option", "cache")
  const after = await menu.getByRole("menuitemcheckbox").evaluateAll(nodes => nodes.map(node => [node.getAttribute("aria-label"), node.getAttribute("aria-checked")]))
  expect(new Map(after as [string, string][])).toEqual(new Map(checked as [string, string][]))
  await page.keyboard.press("Escape")
  await expect(page.getByRole("columnheader").first()).toHaveText("缓存命中")
  const firstRow = page.getByRole("row").filter({ hasText: success.model })
  await expect(firstRow.locator("[data-history-column]").first()).toHaveAttribute("data-history-column", "cache")

  await trigger.click()
  await drag("model", "cache")
  await page.keyboard.press("Escape")
  await page.mouse.up()
  await expect(menu.locator("[data-column-option]").first()).toHaveAttribute("data-column-option", "cache")
  await page.keyboard.press("Escape")
  await page.reload()
  await expect(page.getByRole("columnheader").first()).toHaveText("缓存命中")
  await page.setViewportSize({ width: 390, height: 844 })
  const card = page.getByRole("article").filter({ hasText: success.model })
  await expect(card.locator("[data-history-card-column]").first()).toHaveAttribute("data-history-card-column", "cache")
  await trigger.click()
  await menu.getByRole("menuitem", { name: "调整原始请求模型顺序", exact: true }).focus()
  await page.keyboard.press("Alt+ArrowUp")
  await page.keyboard.press("Alt+ArrowUp")
  await expect(menu.locator("[data-column-option]").first()).toHaveAttribute("data-column-option", "model")
  await expect(menu.getByRole("menuitem", { name: "调整原始请求模型顺序", exact: true })).toBeFocused()
  await menu.getByRole("menuitem", { name: "恢复默认", exact: true }).click()
  await expect(card.locator("[data-history-card-column]").first()).toHaveAttribute("data-history-card-column", "time")
  await page.setViewportSize({ width: 1440, height: 1000 })
  await expect(page.getByRole("columnheader").first()).toHaveText("时间")
})

test.describe("touch column sorting", () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } })

  test("dragging a hidden field scrolls the menu and preserves its visibility", async ({ page, context }) => {
    await page.route("**/api/history*", route => route.fulfill({ json: payload([success, failure]) }))
    await signIn(page)
    await page.getByRole("button", { name: "显示列", exact: true }).tap()
    const menu = page.getByRole("menu", { name: "选择显示列", exact: true })
    const handle = menu.locator('[data-column-handle="gatewayCost"]')
    await handle.scrollIntoViewIfNeeded()
    const source = await handle.boundingBox()
    const popup = await menu.boundingBox()
    const cdp = await context.newCDPSession(page)
    const x = source!.x + source!.width / 2
    await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ id: 1, x, y: source!.y + source!.height / 2 }] })
    await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ id: 1, x, y: popup!.y + 8 }] })
    try {
      await expect.poll(() => menu.evaluate(node => node.scrollTop)).toBe(0)
      const first = await menu.locator('[data-column-option="time"]').boundingBox()
      await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ id: 1, x, y: first!.y + 2 }] })
    } finally {
      await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] })
      await cdp.detach()
    }
    await expect(menu.locator("[data-column-option]").first()).toHaveAttribute("data-column-option", "gatewayCost")
    const checkbox = menu.getByRole("menuitemcheckbox", { name: "网关市价", exact: true })
    await expect(checkbox).not.toBeChecked()
    await checkbox.tap()
    await page.keyboard.press("Escape")
    const card = page.getByRole("article").filter({ hasText: success.model })
    await expect(card.locator("[data-history-card-column]").first()).toHaveAttribute("data-history-card-column", "gatewayCost")
    expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
    await page.screenshot({ path: test.info().outputPath("history-column-order-mobile.png"), fullPage: true })
  })
})

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
    targetProviders: ["deepseek"], routingStatus: "matched", ttftMs: 1300, ms: 6500, kind: "responses", requestedEffort: "xhigh", effort: "max",
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
  await expect(page.getByRole("columnheader")).toHaveCount(10)
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
  await expect(page.getByRole("columnheader")).toHaveCount(10)
  await scroller.locator('xpath=ancestor::*[@data-slot="card"][1]').screenshot({ path: test.info().outputPath("history-panel.png") })
  await page.screenshot({ path: test.info().outputPath("history-columns-desktop.png"), fullPage: true })

  await page.reload()
  await expect(page.getByRole("columnheader")).toHaveCount(10)
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
  await expect(page.getByRole("columnheader")).toHaveCount(11)
})

test("restored history fields and warnings stay usable on desktop and phone", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" })
  await page.setViewportSize({ width: 1920, height: 1100 })
  const restored = {
    ...success, id: "restored", model: "cline-pass/deepseek-v4.1-flash-1017", kind: "responses", stream: true,
    account: "账号1", provider: "deepseek", canonical: "deepseek/deepseek-v4.1-flash-1017", effort: "max", requestedEffort: "xhigh",
    ttftMs: 1100, ms: 6200, usage: { promptTokens: 126600, completionTokens: 640, reasoningTokens: 523, cachedTokens: 126000, cost: 0.000002, gatewayCost: 0.00001 },
  }
  const rerouted = { ...restored, id: "rerouted", model: "cline-pass/retried", resolved: "baseten", routingStatus: "mismatch", targetProviders: ["deepseek"], fallback: true, fallbackReason: "ignored", error: "目标 deepseek，实际 baseten",
    trace: [{ upstream: "deepseek", status: 503, ms: 100 }, { upstream: "baseten", status: 200, ms: 150 }],
    gatewayAttempts: [{ provider: "deepseek", status: 429, success: false }, { provider: "baseten", status: 200, success: true }],
  }
  const truncated = { ...restored, id: "truncated", model: "cline-pass/truncated", finishReason: "length" }
  const filtered = { ...failure, id: "filtered", model: "cline-pass/filtered", kind: "test", finishReason: "content_filter" }
  await page.route("**/api/history*", route => route.fulfill({ json: payload([restored, rerouted, truncated, filtered, success]) }))
  await signIn(page)
  const row = page.getByRole("row").filter({ hasText: restored.model })
  await expect(row.getByText("xhigh → max", { exact: true })).toBeVisible()
  await expect(row.getByText("Responses · 流式", { exact: true })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("history-restored-default.png"), fullPage: true })
  await page.getByRole("button", { name: "显示列", exact: true }).click()
  // Make room for the optional details instead of relying on a wider screen:
  // the console deliberately caps its content width on desktop.
  for (const label of ["时间", "账号", "实际上游", "首字 / 总耗时", "输入 → 输出", "费用"]) {
    await page.getByRole("menuitemcheckbox", { name: label, exact: true }).click()
  }
  for (const label of ["背后模型", "思考 Token", "网关市价"]) {
    await page.getByRole("menuitemcheckbox", { name: label, exact: true }).click()
  }
  await page.keyboard.press("Escape")
  for (const label of ["背后模型", "思考 Token", "网关市价"]) {
    await expect(page.getByRole("columnheader", { name: label, exact: true })).toBeVisible()
  }
  await expect(row.getByText(restored.canonical, { exact: true })).toBeVisible()
  await expect(row.locator('[data-history-column="reasoning"]')).toHaveText("523")
  await expect(row.locator('[data-history-column="gatewayCost"]')).toHaveText("$0.000010 ×5.0")
  for (const label of ["未命中目标", "忽略偏好", "重试 1 次", "网关重试 1 次", "长度截断", "内容过滤"]) {
    await expect(page.getByText(label, { exact: true })).toBeVisible()
  }
  await page.screenshot({ path: test.info().outputPath("history-restored-desktop.png"), fullPage: true })
  await page.getByRole("button", { name: "切换到暗色", exact: true }).click()
  await expect(page.locator("html")).toHaveClass(/dark/)
  await page.mouse.move(10, 1000)
  await page.screenshot({ path: test.info().outputPath("history-restored-dark.png"), fullPage: true, animations: "disabled" })
  const mismatchColor = await page.getByRole("img", { name: "未命中目标", exact: true }).evaluate(node => getComputedStyle(node).color)
  await expect.poll(() => page.getByText("未命中目标", { exact: true }).evaluate(node => getComputedStyle(node).color)).toBe(mismatchColor)
  await page.setViewportSize({ width: 390, height: 844 })
  const card = page.getByRole("article").filter({ hasText: restored.model })
  await expect(card.getByText(restored.canonical, { exact: true })).toBeVisible()
  await expect(card.getByText("523", { exact: true })).toBeVisible()
  await expect(card.getByText("xhigh → max", { exact: true })).toBeVisible()
  await expect(card.getByText("Responses · 流式", { exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
  await page.screenshot({ path: test.info().outputPath("history-restored-mobile.png"), fullPage: true, animations: "disabled" })
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
