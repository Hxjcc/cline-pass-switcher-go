import { cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"

import { SecurityPanel } from "./security-panel"
import type { SecurityResponse } from "@/types"

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const data: SecurityResponse = {
  proxyKey: "sk-master",
  adminKey: "sk-console",
  publicBaseUrl: "https://cline.example",
  authRequired: true,
  settings: [
    { key: "compactionMinOutputTokens", label: "压缩首轮输出预算下限", value: "16384", source: "env" },
    { key: "compactionRecentTokens", label: "压缩逐字尾部（估算 token）", value: "16000", source: "config" },
    { key: "shellCompat", label: "工具 shell 兼容值", value: "关闭", source: "default" },
    { key: "proxyKey", label: "代理主密钥（客户端）", value: "已设置", source: "env", secret: true },
  ],
}

function renderPanel() {
  const onSave = vi.fn(async (): Promise<SecurityResponse> => data)
  render(<SecurityPanel data={data} proxyBase="https://cline.example/v1" onSave={onSave} />)
  return { onSave }
}

// The point of the table is the source column: it answers "did my .env change
// take effect" without shell access.
test("shows every effective value with the layer that supplied it", () => {
  renderPanel()
  expect(screen.getByText("运行参数（当前生效值）")).toBeTruthy()
  expect(screen.getByText("压缩首轮输出预算下限")).toBeTruthy()
  expect(screen.getByText("16384")).toBeTruthy()
  expect(screen.getAllByText("环境变量").length).toBeGreaterThan(0)
  expect(screen.getAllByText("config.json").length).toBeGreaterThan(0)
  expect(screen.getAllByText("内置默认").length).toBeGreaterThan(0)
})

// Credentials are listed as presence only; the table must never echo a secret.
test("never echoes a secret value", () => {
  renderPanel()
  expect(screen.queryByText("sk-master")).toBeNull()
  expect(screen.queryByText("sk-console")).toBeNull()
  expect(screen.getByText("已设置")).toBeTruthy()
})

// Each key sits behind an eye, a dice and a copy button; copying must work
// while the field is still masked, exactly like the keys panel's rows.
test("copies either key while it is masked", async () => {
  const writeText = vi.fn(async () => {})
  vi.stubGlobal("navigator", { clipboard: { writeText } })
  renderPanel()

  fireEvent.click(screen.getByRole("button", { name: "复制管理密钥" }))
  await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith("sk-console"))
  fireEvent.click(screen.getByRole("button", { name: "复制代理主密钥" }))
  await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith("sk-master"))

  expect((screen.getByLabelText("管理密钥（控制台）") as HTMLInputElement).type).toBe("password")
  expect((screen.getByLabelText("代理主密钥（客户端）") as HTMLInputElement).type).toBe("password")
})
