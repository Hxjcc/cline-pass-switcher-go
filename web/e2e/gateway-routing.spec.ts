import { expect, test } from "@playwright/test"

test("batch probing combines ordinary discovery and single-channel plans without applying them", async ({ page }) => {
  const singleID = "cline-pass/deepseek-test"
  const nativeID = "cline-pass/native-test"
  const plan = { upstreams: ["deepseek"], exclude: [], gatewayHas: ["implicit-caching", "vision"] }
  const catalog = { model: "deepseek/test", source: "directory", fetchedAt: Date.now(), providers: [{ slug: "deepseek", name: "DeepSeek", has: ["implicit-caching", "vision"] }, { slug: "other", name: "Other", has: ["vision"] }] }
  const probed = new Set<string>()
  let adopted = false
  let adoptionCalls = 0
  const discovery = (id: string) => ({ profile: "baseline", sourceConfig: "original", accountId: "one", account: "账号一", checkedAt: Date.now(), current: true, status: id === singleID ? "available" : "native", config: id === singleID ? plan : {}, reason: id === singleID ? "正反对照通过，可采用该供应商路由" : "", checkProfile: "proof" })
  const baseline = (id: string) => ({ profile: "baseline", accountId: "one", account: "账号一", pipeline: "planner", canonical: id === singleID ? "private/test" : "vendor/test", providers: id === singleID ? ["openai-compatible-private"] : ["alpha", "beta"], private: id === singleID, pinnable: id !== singleID, current: true, checkedAt: Date.now() })
  await page.route("**/api/accounts", route => route.fulfill({ json: { accounts: [{ id: "one", name: "账号一", keyPreview: "", hasKey: true, enabled: true }], mode: "single", active: 0, stats: {} } }))
  await page.route("**/api/models", route => route.fulfill({ json: { proxyBase: "http://127.0.0.1:3133", officialFetch: null, subscription: [singleID, nativeID].map(id => ({ id, config: id === singleID && adopted ? plan : { upstreams: [], exclude: [] }, meta: {
    pipeline: "planner", pinnable: id !== singleID || adopted, pinReason: id === singleID && !adopted ? "single_provider" : "", upstreams: id === nativeID ? ["alpha", "beta"] : adopted ? ["deepseek", "other"] : ["openai-compatible-private"],
    canonicalSlug: id === singleID ? adopted ? "deepseek/test" : "private/test" : "vendor/test", gatewayCatalog: id === singleID && probed.has(id) ? catalog : undefined,
    defaultRoute: probed.has(id) ? baseline(id) : undefined, routingDiscovery: probed.has(id) ? discovery(id) : undefined,
  } })) } }))
  await page.route("**/api/probe", async route => {
    const body = route.request().postDataJSON()
    expect(body.force).toBe(false)
    probed.add(body.model)
    await route.fulfill({ json: { ok: true, ms: 10, defaultRoute: baseline(body.model), routingDiscovery: discovery(body.model) } })
  })
  await page.route("**/api/verify-routing", async route => {
    const body = route.request().postDataJSON()
    expect(body).toEqual({ model: singleID, config: plan, accountId: "one", allAccounts: false, apply: true, force: false, adopt: true })
    adoptionCalls++
    adopted = true
    await route.fulfill({ json: { ok: true, applied: true, checks: [] } })
  })
  await page.route("**/api/gateway-capabilities", () => { throw new Error("adoption must not reload the directory") })
  await page.goto("/")
  await page.getByLabel("管理密钥").fill("sk-e2e-admin")
  await page.getByRole("button", { name: "进入控制台" }).click()
  await page.getByRole("button", { name: "批量探测", exact: true }).click()
  await expect(page.getByText(/批量探测完成：普通可钉 1，能力路由可用 1/)).toBeVisible()
  expect(adopted).toBe(false)
  const nativeRow = page.getByRole("row").filter({ has: page.getByText(nativeID, { exact: true }) })
  await nativeRow.getByRole("button", { name: "展开模型" }).click()
  await expect(page.getByText("高级：能力过滤与供应商设置")).toHaveCount(0)
  const singleRow = page.getByRole("row").filter({ has: page.getByText(singleID, { exact: true }) })
  await singleRow.getByRole("button", { name: "展开模型" }).click()
  await expect(page.getByRole("button", { name: "采用此方案" })).toBeEnabled()
  await expect(page.getByRole("switch", { name: "启用能力过滤" })).toBeHidden()
  await page.getByRole("button", { name: "采用此方案" }).click()
  await expect(page.getByText("此方案正在使用", { exact: true })).toBeVisible()
  expect(adoptionCalls).toBe(1)
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
  const panel = await page.getByRole("region", { name: "供应商能力过滤" }).boundingBox()
  expect(panel).not.toBeNull()
  expect(panel!.x + panel!.width).toBeLessThanOrEqual(390)
})

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
  await page.getByText("高级：能力过滤与供应商设置").click()
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
  await page.getByRole("option", { name: "账号1", exact: true }).click()
  await expect(accountSelect.locator('[data-slot="select-value"]')).toHaveText("账号1")
  await page.getByRole("button", { name: "验证并应用" }).click()
  await expect(page.getByText("对照验证通过", { exact: true })).toBeVisible()
  await expect(page.getByText("待应用", { exact: true })).toHaveCount(0)
  await expect(page.getByText("Vercel", { exact: true })).toBeVisible()
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
})
