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

  await page.getByPlaceholder("使用者或用途").fill("e2e")
  await page.getByRole("button", { name: /^保存$/ }).click()
  await expect(page.getByText("代理密钥已保存")).toBeVisible()

  // Reloading keeps the saved grant, and its secret stays masked until the
  // operator asks for it.
  await page.reload()
  await page.getByRole("tab", { name: "代理密钥" }).click()
  const stored = page.getByLabel("客户端密钥")
  await expect(stored).toHaveAttribute("type", "password")
  await expect(stored).toHaveValue(/^0+$/)
  await stored.focus()
  await expect(stored).toHaveValue("")
  await stored.blur()
  await expect(stored).toHaveValue(/^0+$/)
  await expect(page.getByPlaceholder("使用者或用途")).toHaveValue("e2e")

  // Revealing it puts the plaintext into the DOM; the boot frame written on
  // pagehide must still not contain it.
  await page.getByRole("button", { name: /显示密钥/ }).click()
  await expect(stored).toHaveAttribute("type", "text")
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

test("revealing client keys keeps an unsaved rename and a new row", async ({ page }) => {
  await signIn(page)
  await page.getByRole("tab", { name: "代理密钥", exact: true }).click()
  await page.getByRole("button", { name: /新增客户端密钥/ }).click()
  await page.getByPlaceholder("使用者或用途").last().fill("reveal draft fixture")
  await page.getByRole("button", { name: /^保存$/ }).click()
  await expect(page.getByText("代理密钥已保存")).toBeVisible()
  const saved = page.getByRole("article", { name: "reveal draft fixture", exact: true })
  const nameId = await saved.getByPlaceholder("使用者或用途").getAttribute("id")
  const keyId = await saved.getByLabel("客户端密钥", { exact: true }).getAttribute("id")
  const existing = page.locator(`[id=${JSON.stringify(nameId)}]`)
  await existing.fill("unsaved rename")
  await page.getByRole("button", { name: /新增客户端密钥/ }).click()
  await page.getByPlaceholder("使用者或用途").last().fill("unsaved new key")
  const minted = await page.getByLabel("客户端密钥", { exact: true }).last().inputValue()
  await page.getByRole("button", { name: /显示密钥/ }).click()
  await expect(page.getByRole("button", { name: /隐藏密钥/ })).toBeVisible()
  await expect(existing).toHaveValue("unsaved rename")
  await expect(page.getByPlaceholder("使用者或用途").last()).toHaveValue("unsaved new key")
  await expect(page.getByLabel("客户端密钥", { exact: true }).last()).toHaveValue(minted)
  await page.getByRole("button", { name: /隐藏密钥/ }).click()
  await expect(page.locator(`[id=${JSON.stringify(keyId)}]`)).toHaveValue(/^0+$/)
  await expect(existing).toHaveValue("unsaved rename")
  await expect(page.getByLabel("客户端密钥", { exact: true }).last()).toHaveValue(minted)
})

test("storage faults refresh after login and clear after recovery", async ({ page }) => {
  await page.clock.install()
  let storage = { status: "ok", message: "", detail: "" }
  await page.route("**/api/meta", async (route) => {
    const response = await route.fetch()
    const payload = await response.json()
    if (route.request().headers()["x-admin-key"] === ADMIN_KEY) payload.storage = storage
    await route.fulfill({ response, json: payload })
  })
  await signIn(page)
  await expect(page.getByText("费用记录异常", { exact: true })).toHaveCount(0)
  storage = { status: "unavailable", message: "已暂停有额度上限的密钥的新请求", detail: "test journal write failed" }
  await page.clock.fastForward(16_000)
  await expect(page.getByText("费用记录异常", { exact: true })).toBeVisible()
  await expect(page.getByText(storage.message, { exact: true })).toBeVisible()
  await page.getByText("故障详情", { exact: true }).click()
  await expect(page.getByText(storage.detail, { exact: true })).toBeVisible()
  storage = { status: "degraded", message: "日志已保存，快照合并失败", detail: "test snapshot failed" }
  await page.clock.fastForward(16_000)
  await expect(page.getByText("数据存储需要检查", { exact: true })).toBeVisible()
  await expect(page.getByText("费用记录异常", { exact: true })).toHaveCount(0)
  storage = { status: "ok", message: "", detail: "" }
  await page.clock.fastForward(16_000)
  await expect(page.getByText("数据存储需要检查", { exact: true })).toHaveCount(0)
})

test("switching tabs keeps unfinished management drafts", async ({ page }) => {
  await signIn(page)
  await page.getByRole("tab", { name: "代理密钥", exact: true }).click()
  await page.getByRole("button", { name: /新增客户端密钥/ }).click()
  const nameId = await page.getByPlaceholder("使用者或用途").last().getAttribute("id")
  const keyId = await page.getByLabel("客户端密钥", { exact: true }).last().getAttribute("id")
  const keyName = page.locator(`[id=${JSON.stringify(nameId)}]`)
  await keyName.fill("unfinished key")
  const secret = await page.locator(`[id=${JSON.stringify(keyId)}]`).inputValue()
  await page.getByRole("tab", { name: "账号池", exact: true }).click()
  await page.getByRole("button", { name: "添加账号", exact: true }).click()
  await page.getByLabel("账号名称", { exact: true }).fill("unfinished account")
  await page.getByRole("tab", { name: "访问与安全", exact: true }).click()
  await page.getByLabel("公网访问地址", { exact: true }).fill("https://unfinished.example")
  await page.getByRole("tab", { name: "测试台", exact: true }).click()
  await expect(page.getByRole("button", { name: "发送测试", exact: true })).toBeVisible()
  await page.getByRole("tab", { name: "代理密钥", exact: true }).click()
  await expect(keyName).toHaveValue("unfinished key")
  await expect(page.locator(`[id=${JSON.stringify(keyId)}]`)).toHaveValue(secret)
  await page.getByRole("tab", { name: "账号池", exact: true }).click()
  await expect(page.getByLabel("账号名称", { exact: true })).toHaveValue("unfinished account")
  await page.getByRole("tab", { name: "访问与安全", exact: true }).click()
  await expect(page.getByLabel("公网访问地址", { exact: true })).toHaveValue("https://unfinished.example")
  await page.evaluate(() => window.dispatchEvent(new Event("pagehide")))
  const snapshot = await page.evaluate(() => sessionStorage.getItem("cline-pass-switcher-root-snapshot-v1") ?? "")
  expect(snapshot).not.toContain(secret)
})

test("resetting usage preserves unfinished key edits and additions", async ({ page }) => {
  await signIn(page)
  await page.getByRole("tab", { name: "代理密钥", exact: true }).click()
  await page.getByRole("button", { name: /新增客户端密钥/ }).click()
  const secret = await page.getByLabel("客户端密钥", { exact: true }).last().inputValue()
  await page.getByPlaceholder("使用者或用途").last().fill("usage reset fixture")
  await page.getByRole("button", { name: /^保存$/ }).click()
  await expect(page.getByText("代理密钥已保存", { exact: true })).toBeVisible()
  // The isolated server has no upstream accounts. A refused generation is a
  // real recorded request, so resetting its counter exercises the API for free.
  const generated = await page.request.post("/v1/chat/completions", {
    headers: { Authorization: `Bearer ${secret}` },
    data: { model: "cline-pass/glm-5.3-flash", messages: [{ role: "user", content: "test" }] },
  })
  expect(generated.status()).toBe(503)
  expect((await generated.json()).error.code).toBe("no_account")
  await page.getByRole("button", { name: "刷新", exact: true }).click()
  const saved = page.getByRole("article", { name: "usage reset fixture", exact: true })
  const nameId = await saved.getByPlaceholder("使用者或用途").getAttribute("id")
  const name = page.locator(`[id=${JSON.stringify(nameId)}]`)
  await expect(saved.getByRole("button", { name: "重置用量", exact: true })).toBeEnabled()
  await name.fill("unsaved reset rename")
  await page.getByRole("button", { name: /新增客户端密钥/ }).click()
  await page.getByPlaceholder("使用者或用途").last().fill("unsaved reset addition")
  const minted = await page.getByLabel("客户端密钥", { exact: true }).last().inputValue()
  await page.getByRole("article", { name: "unsaved reset rename", exact: true }).getByRole("button", { name: "重置用量", exact: true }).click()
  await expect(page.getByText("已重置该密钥的用量", { exact: true })).toBeVisible()
  await expect(name).toHaveValue("unsaved reset rename")
  await expect(page.getByPlaceholder("使用者或用途").last()).toHaveValue("unsaved reset addition")
  await expect(page.getByLabel("客户端密钥", { exact: true }).last()).toHaveValue(minted)
  await expect(page.getByRole("article", { name: "unsaved reset rename", exact: true }).getByRole("button", { name: "重置用量", exact: true })).toBeDisabled()
})

// The legacy "优先 + 回退" pin mode was removed: pinning is always strict now,
// so the expanded panel must not offer a mode selector.
test("expanded model keeps the strict-only routing controls", async ({ page }) => {
  await signIn(page)
  const modelID = "cline-pass/glm-5.3-flash"
  const modelRow = () => page.getByRole("row").filter({ hasText: modelID }).first()

  await expect(modelRow()).toBeVisible()
  await modelRow().getByRole("button", { name: "展开模型" }).click()

  // The expanded panel is its own table row and does not repeat the model id,
  // so locate it by the controls it contains.
  const panel = page.getByRole("row").filter({ hasText: "全部设为优先" })
  await expect(page.getByLabel("钉住模式")).toHaveCount(0)
  await expect(panel.getByRole("button", { name: "全部设为优先" })).toBeVisible()
  await expect(panel.getByRole("button", { name: "恢复自动" })).toBeVisible()
})
