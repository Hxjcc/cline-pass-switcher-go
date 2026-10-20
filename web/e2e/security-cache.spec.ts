import { expect, test } from "@playwright/test"

const ADMIN_KEY = "sk-e2e-admin"
const PROXY_KEY = "sk-e2e-client"

for (const snapshot of ["delayed", "failed"] as const) {
  test(`a ${snapshot} security snapshot cannot turn cached placeholders into saved keys`, async ({ page }) => {
    await page.goto("/")
    await page.getByLabel("管理密钥").fill(ADMIN_KEY)
    await page.getByRole("button", { name: "进入控制台" }).click()
    await page.getByRole("tab", { name: "访问与安全" }).click()
    await expect(page.locator("#admin-key")).toHaveValue(ADMIN_KEY)
    await expect(page.locator("#proxy-key")).toHaveValue(PROXY_KEY)
    await expect.poll(() => page.evaluate(
      () => sessionStorage.getItem("cline-pass-switcher-snapshot") ?? "",
    )).toContain('"security"')

    let releaseSnapshot!: () => void
    const gate = new Promise<void>((resolve) => { releaseSnapshot = resolve })
    await page.route("**/api/security", async (route) => {
      if (route.request().method() !== "GET") {
        await route.continue()
      } else if (snapshot === "failed") {
        await route.abort()
      } else {
        await gate
        await route.continue()
      }
    })

    try {
      await page.reload()
      await expect(page.getByText("访问设置尚未加载", { exact: false })).toBeVisible()
      await expect(page.locator("#admin-key")).toHaveCount(0)
      await expect(page.locator("#public-base")).toHaveCount(0)
      await expect(page.getByRole("button", { name: "保存设置", exact: true })).toHaveCount(0)
      expect((await page.request.get("/api/security")).status()).toBe(401)

      if (snapshot === "failed") {
        await page.unroute("**/api/security")
        await page.getByRole("button", { name: "刷新", exact: true }).click()
      } else {
        releaseSnapshot()
      }
      await expect(page.locator("#admin-key")).toHaveValue(ADMIN_KEY)
      await expect(page.locator("#proxy-key")).toHaveValue(PROXY_KEY)

      await page.locator("#public-base").fill(new URL(page.url()).origin)
      const savedRequest = page.waitForRequest(
        (request) => request.url().endsWith("/api/security") && request.method() === "POST",
      )
      const savedResponse = page.waitForResponse(
        (response) => response.url().endsWith("/api/security") && response.request().method() === "POST",
      )
      await page.getByRole("button", { name: "保存设置", exact: true }).click()
      expect((await savedRequest).postDataJSON()).toMatchObject({ proxyKey: PROXY_KEY, adminKey: ADMIN_KEY })
      expect((await savedResponse).status()).toBe(200)
      expect((await page.request.get("/api/security")).status()).toBe(401)
      const live = await page.request.get("/api/security", { headers: { "X-Admin-Key": ADMIN_KEY } })
      expect(await live.json()).toMatchObject({ proxyKey: PROXY_KEY, adminKey: ADMIN_KEY })
    } finally {
      releaseSnapshot()
      await page.request.post("/api/security", {
        headers: { "X-Admin-Key": ADMIN_KEY }, data: { publicBaseUrl: "" },
      })
    }
  })
}
