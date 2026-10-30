import { expect, test } from "@playwright/test"

test("all tab code warms during sign-in and switching needs no more scripts", async ({ page }) => {
  const loaded = new Set<string>()
  let usageRequests = 0
  const modules = ["models-panel", "accounts-panel", "keys-panel", "security-panel", "test-bench", "history-panel", "usage-panel"]
  page.on("response", response => {
    const pathname = new URL(response.url()).pathname
    if (!response.ok() || !pathname.endsWith(".js")) return
    for (const name of modules) {
      if (pathname.startsWith(`/assets/${name}-`)) loaded.add(name)
    }
  })
  page.on("request", request => { if (new URL(request.url()).pathname === "/api/usage") usageRequests++ })
  await page.goto("/")
  await expect(page.getByLabel("管理密钥")).toBeVisible()
  await expect.poll(() => modules.filter(name => !loaded.has(name)), { timeout: 15_000 }).toEqual([])
  expect(usageRequests).toBe(0)

  await page.getByLabel("管理密钥").fill("sk-e2e-admin")
  await page.getByRole("button", { name: "进入控制台" }).click()
  await expect(page.getByRole("textbox", { name: "筛选模型", exact: true })).toBeVisible()

  // Routing disables the HTTP cache. Success therefore demonstrates that the
  // already imported code is reused, rather than another download succeeding.
  const unexpected: string[] = []
  await page.route(/\/assets\/.*\.js(?:\?.*)?$/, route => {
    unexpected.push(route.request().url())
    return route.abort()
  })
  const tabs = [
    ["账号池", "添加账号"],
    ["代理密钥", "新增客户端密钥"],
    ["访问与安全", "保存设置"],
    ["测试台", "发送测试"],
    ["Token 统计", "刷新统计"],
  ]
  for (const [name, button] of tabs) {
    await page.getByRole("tab", { name, exact: true }).click()
    await expect(page.getByRole("button", { name: button, exact: true })).toBeVisible()
  }
  await page.getByRole("tab", { name: "请求历史", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "筛选请求历史" })).toBeVisible()
  await page.getByRole("tab", { name: "模型与上游", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "筛选模型", exact: true })).toBeVisible()
  expect(unexpected).toEqual([])
})

test("a very early tab switch keeps the current page until its code is ready", async ({ page }) => {
  let release!: () => void
  const waiting = new Promise<void>(resolve => { release = resolve })
  await page.route(/\/assets\/usage-panel-[^/]+\.js$/, async route => {
    await waiting
    await route.continue()
  })
  try {
    await page.goto("/")
    await page.getByLabel("管理密钥").fill("sk-e2e-admin")
    await page.getByRole("button", { name: "进入控制台" }).click()
    await expect(page.getByRole("textbox", { name: "筛选模型", exact: true })).toBeVisible()
    await page.getByRole("tab", { name: "Token 统计", exact: true }).click()
    await expect(page.getByRole("textbox", { name: "筛选模型", exact: true })).toBeVisible()
    await expect(page.getByText("正在加载页面…", { exact: true })).toHaveCount(0)
    release()
    await expect(page.getByRole("button", { name: "刷新统计", exact: true })).toBeVisible()
  } finally {
    release()
  }
})
