import { expect, test, type Page } from "@playwright/test"

const ADMIN_KEY = "sk-e2e-admin"

// The console gets used from a phone; these checks are the guard against a
// desktop-only layout creeping back in.
test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })

async function signIn(page: Page) {
  await page.goto("/")
  await page.getByLabel("管理密钥").fill(ADMIN_KEY)
  await page.getByRole("button", { name: "进入控制台" }).click()
  await page.getByRole("tab", { name: "代理密钥" }).waitFor()
}

test("every tab fits a phone viewport", async ({ page }) => {
  await signIn(page)
  for (const tab of ["模型与上游", "账号池", "代理密钥", "访问与安全", "测试台", "请求历史"]) {
    await page.getByRole("tab", { name: tab }).click()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow, `${tab} pushed the page wider than the viewport`).toBeLessThanOrEqual(0)
  }
})

// The model table keeps its columns but scrolls inside the card, so the row
// actions stay reachable instead of being clipped off the right edge.
test("the model table scrolls to its row actions on a phone", async ({ page }) => {
  await signIn(page)
  await page.getByRole("tab", { name: "模型与上游" }).click()
  const row = page.getByRole("row").filter({ hasText: "cline-pass/glm-5.3-flash" }).first()
  await expect(row).toBeVisible()

  const scroller = page.locator('[data-slot="table-container"]').first()
  await scroller.evaluate((node) => {
    node.scrollLeft = node.scrollWidth
  })
  await expect(row.getByRole("button", { name: "移除模型" })).toBeVisible()
})
