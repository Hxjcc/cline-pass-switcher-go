import { expect, test, type Page } from "@playwright/test"

const ADMIN_KEY = "sk-e2e-admin"

async function signIn(page: Page) {
  await page.goto("/")
  await page.getByLabel("管理密钥").fill(ADMIN_KEY)
  await page.getByRole("button", { name: "进入控制台" }).click()
  await expect(page.getByRole("tab", { name: "代理密钥" })).toBeVisible()
}

test("a fresh browser reaches the sign-in dialog instead of the auth throttle", async ({ page }) => {
  // Regression: the console used to fire five protected requests before the
  // login dialog, which counted as failed attempts and answered 429.
  const responses: number[] = []
  page.on("response", (response) => {
    if (response.url().includes("/api/")) responses.push(response.status())
  })

  await page.goto("/")
  await expect(page.getByLabel("管理密钥")).toBeVisible()
  expect(responses).not.toContain(429)
  expect(responses.filter((status) => status === 401).length).toBeLessThan(5)
})

test("issuing a client key keeps the secret out of the boot frame", async ({ page }) => {
  await signIn(page)
  await page.getByRole("tab", { name: "代理密钥" }).click()
  await page.getByRole("button", { name: /新增客户端密钥/ }).click()

  const field = page.getByLabel("客户端密钥")
  const minted = await field.inputValue()
  expect(minted).toMatch(/^sk-[0-9a-f]{48}$/)

  await page.getByPlaceholder("给谁用").fill("e2e")
  await page.getByRole("button", { name: /^保存$/ }).click()
  await expect(page.getByText("代理密钥已保存")).toBeVisible()

  // Reloading keeps the saved grant, and its secret stays masked until the
  // operator asks for it.
  await page.reload()
  await page.getByRole("tab", { name: "代理密钥" }).click()
  const stored = page.getByLabel("客户端密钥")
  await expect(stored).toHaveValue("")
  await expect(page.getByPlaceholder("给谁用")).toHaveValue("e2e")

  // Revealing it puts the plaintext into the DOM; the boot frame written on
  // pagehide must still not contain it.
  await page.getByRole("button", { name: /显示密钥/ }).click()
  await expect(stored).toHaveValue(minted)
  await page.evaluate(() => window.dispatchEvent(new Event("pagehide")))
  const snapshot = await page.evaluate(
    () => sessionStorage.getItem("cline-pass-switcher-root-snapshot-v1") ?? "",
  )
  expect(snapshot).not.toContain(minted)
})

test("the access panel reports where each runtime value came from", async ({ page }) => {
  await signIn(page)
  await page.getByRole("tab", { name: "访问与安全" }).click()
  await expect(page.getByText("运行参数（当前生效值）")).toBeVisible()

  const budget = page.getByText("压缩首轮输出预算下限").locator("..")
  await expect(budget).toContainText("16384")
  await expect(budget).toContainText("环境变量")

  const enforce = page.getByText("强制改写工具 shell 参数").locator("..")
  await expect(enforce).toContainText("false")
  await expect(enforce).toContainText("内置默认")
})
