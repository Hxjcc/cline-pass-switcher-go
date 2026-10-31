import { expect, test } from "@playwright/test"

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("cline-pass-switcher-admin-key", "sk-e2e-admin"))
})

for (const reducedMotion of ["no-preference", "reduce"] as const) {
  test(`a cold home waits for its data and code before the entrance (${reducedMotion})`, async ({ page }) => {
    await page.emulateMedia({ reducedMotion })
    await page.clock.install()
    let releaseData!: () => void
    let releaseCode!: () => void
    const dataGate = new Promise<void>(resolve => { releaseData = resolve })
    const codeGate = new Promise<void>(resolve => { releaseCode = resolve })
    await page.route("**/api/accounts", async route => { await dataGate; await route.continue() })
    const modelCode = /\/assets\/models-panel-[^/]+\.js$/
    await page.route(modelCode, async route => { await codeGate; await route.continue() })
    try {
      await page.goto("/", { waitUntil: "domcontentloaded" })
      const cover = page.locator("#app-entry-cover")
      await expect(cover).toBeVisible()
      // The previous fixed 60 ms + 520 ms entrance expired while these
      // requests were still pending, revealing placeholder cards and Suspense.
      await page.clock.runFor(1000)
      await expect(cover).toBeVisible()
      await expect(page.getByRole("textbox", { name: "筛选模型", exact: true })).toBeHidden()
      const codeLoaded = page.waitForResponse(response => modelCode.test(new URL(response.url()).pathname))
      releaseCode()
      await codeLoaded
      await page.clock.runFor(1000)
      await expect(cover).toBeVisible()
      releaseData()
      await expect(cover).toHaveCount(0)
      await expect(page.getByRole("textbox", { name: "筛选模型", exact: true })).toBeVisible()
      await expect(page.getByText("正在加载页面…", { exact: true })).toHaveCount(0)
      await expect(page.locator('#root [data-slot="card"]').filter({ has: page.getByText("订阅模型", { exact: true }) }).first().getByText("1", { exact: true })).toBeVisible()
    } finally {
      releaseData()
      releaseCode()
    }
  })
}

test("reload keeps the cached home visible until React's lazy panel commits", async ({ page }) => {
  await page.clock.install()
  await page.goto("/")
  await expect(page.getByRole("textbox", { name: "筛选模型", exact: true })).toBeVisible()
  await expect(page.locator("#app-entry-cover")).toHaveCount(0)
  await expect.poll(() => page.evaluate(() => sessionStorage.getItem("cline-pass-switcher-snapshot") ?? "")).toContain('"models"')
  await page.evaluate(() => window.dispatchEvent(new Event("pagehide")))
  const completeSnapshot = await page.evaluate(() => sessionStorage.getItem("cline-pass-switcher-root-snapshot-v1"))
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  await page.route(/\/assets\/models-panel-[^/]+\.js$/, async route => { await gate; await route.continue() })
  try {
    await page.reload({ waitUntil: "domcontentloaded" })
    const cached = page.locator("#app-boot-snapshot")
    await expect(cached).toBeVisible()
    await expect(cached.locator('input[aria-label="筛选模型"]')).toBeVisible()
    await expect(cached).toHaveAttribute("inert", "")
    await expect(page.locator("#root")).toHaveAttribute("inert", "")
    await page.clock.runFor(1000)
    await expect(cached.locator('input[aria-label="筛选模型"]')).toBeVisible()
    // A second refresh during this wait must retain the complete old frame.
    await page.evaluate(() => window.dispatchEvent(new Event("pagehide")))
    expect(await page.evaluate(() => sessionStorage.getItem("cline-pass-switcher-root-snapshot-v1"))).toBe(completeSnapshot)
    release()
    await expect(cached).toHaveCount(0)
    await expect(page.getByRole("textbox", { name: "筛选模型", exact: true })).toBeVisible()
    await expect(page.locator("#root")).not.toHaveAttribute("inert", "")
  } finally {
    release()
  }
})

test("a panel render failure releases the boot cover and exposes recovery", async ({ page }) => {
  await page.route(/\/assets\/models-panel-[^/]+\.js$/, route => route.fulfill({
    contentType: "application/javascript", body: 'throw new Error("boot panel fixture failed")',
  }))
  await page.goto("/")
  await expect(page.getByRole("button", { name: "重新加载", exact: true })).toBeVisible()
  await expect(page.locator("#app-entry-cover")).toHaveCount(0)
  await expect(page.locator("#root")).not.toHaveAttribute("inert", "")
})
