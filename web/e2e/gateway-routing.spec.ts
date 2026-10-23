import { expect, test } from "@playwright/test"

test("a private-only model discovers suppliers and applies verified capability routing", async ({ page }) => {
  const modelID = "cline-pass/deepseek-v4.1-flash-1017"
  const has = ["implicit-caching", "reasoning", "structured-output", "tool-use", "vision"]
  const catalog = { model: "deepseek/deepseek-v4.1-flash-1017", source: "https://example.test/endpoints", fetchedAt: Date.now(), providers: [
    { slug: "deepseek", name: "DeepSeek", has: ["vision", "implicit-caching", "reasoning", "tool-use", "structured-output"] },
    { slug: "baseten", name: "Baseten", has: ["vision", "implicit-caching", "tool-use"] },
  ] }
  let loaded = false
  let applied = false
  let check: object | null = null
  await page.route("**/api/accounts", route => route.fulfill({ json: {
    accounts: [
      { id: "one", name: "账号1", key: "", keyPreview: "", hasKey: true, enabled: true },
      { id: "two", name: "账号2", key: "", keyPreview: "", hasKey: true, enabled: true },
    ],
    mode: "single", active: 0, stats: {},
  } }))
  await page.route("**/api/models", route => route.fulfill({ json: { proxyBase: "http://127.0.0.1:3133", officialFetch: null, subscription: [{ id: modelID, config: { upstreams: applied ? ["deepseek"] : [], exclude: [], gatewayHas: applied ? has : [] }, meta: {
    pipeline: "planner", pinnable: applied, pinReason: applied ? "" : "single_provider", canonicalSlug: applied ? catalog.model : "private/deepseek-v4p1-flash-1017-contributor", upstreams: applied ? ["deepseek", "baseten"] : ["openai-compatible-private"], lastProvider: applied ? "deepseek" : "openai-compatible-private", gatewayCatalog: loaded ? catalog : undefined, routingChecks: check ? { profile: check } : {},
  } }] } }))
  await page.route("**/api/gateway-capabilities", async route => {
    expect(route.request().postDataJSON().model).toBe(modelID)
    expect(route.request().postDataJSON().upstreams).toEqual(loaded ? ["deepseek"] : [])
    loaded = true
    await route.fulfill({ json: { ok: true, catalog, recommendedHas: has } })
  })
  await page.route("**/api/verify-routing", async route => {
    const body = route.request().postDataJSON()
    expect(body.model).toBe(modelID)
    expect(body.config.gatewayHas).toEqual(has)
    expect(body.config.upstreams).toEqual(["deepseek"])
    expect(body.apply).toBe(true)
    expect(body.accountId).toBe("one")
    expect(body.allAccounts).toBe(false)
    applied = true
    check = { profile: "profile", accountId: "one", account: "验证账号", has, targets: ["deepseek"], actual: ["deepseek"], matched: true, rejected: true, ok: true, current: true, checkedAt: Date.now(), ms: 1 }
    await route.fulfill({ json: { ok: true, applied: true, checks: [check] } })
  })
  await page.goto("/")
  await page.getByLabel("管理密钥").fill("sk-e2e-admin")
  await page.getByRole("button", { name: "进入控制台" }).click()
  await page.getByRole("button", { name: "展开模型" }).click()
  await expect(page.getByText("当前探测仅发现一个渠道", { exact: true })).toBeVisible()
  await page.getByRole("switch", { name: "启用能力过滤" }).click()
  const deepseek = page.getByRole("group", { name: "供应商 deepseek" })
  await expect(deepseek).toBeVisible()
  await expect(page.getByRole("group", { name: "供应商 baseten" })).toHaveCount(0)
  for (const capability of ["图片输入", "隐式缓存", "推理", "工具调用", "结构化输出"]) {
    await expect(page.getByRole("checkbox", { name: `要求${capability}能力` })).toBeChecked()
  }
  await expect(page.getByRole("checkbox", { name: "要求免费推理能力" })).not.toBeChecked()
  await deepseek.getByRole("button", { name: "设为优先" }).click()
  await expect(page.getByText("待应用", { exact: true })).toBeVisible()
  const recommendation = page.waitForResponse("**/api/gateway-capabilities")
  await page.getByRole("button", { name: "使用推荐" }).click()
  await recommendation
  await expect(page.getByRole("button", { name: "验证并应用" })).toBeEnabled()
  const accountSelect = page.getByRole("combobox", { name: "验证账号" })
  await expect(accountSelect.locator('[data-slot="select-value"]')).toHaveText("当前调度账号")
  await accountSelect.click()
  await expect(page.getByRole("option", { name: "所有启用账号", exact: true })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("gateway-account-menu.png"), fullPage: true })
  await page.getByRole("option", { name: "账号1", exact: true }).click()
  await expect(accountSelect.locator('[data-slot="select-value"]')).toHaveText("账号1")
  await page.getByRole("button", { name: "验证并应用" }).click()
  await expect(page.getByText("对照验证通过", { exact: true })).toBeVisible()
  await expect(page.getByText("待应用", { exact: true })).toHaveCount(0)
  await expect(page.getByText("Vercel", { exact: true })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("gateway-routing.png"), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
  await page.screenshot({ path: test.info().outputPath("gateway-routing-mobile.png"), fullPage: true })
})
