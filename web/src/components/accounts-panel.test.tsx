import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"

import { AccountsPanel } from "./accounts-panel"
import type { AccountTestResponse, AccountsResponse } from "@/types"

afterEach(cleanup)

const data: AccountsResponse = {
  accounts: [
    { id: "acc_1", name: "main", key: "", keyPreview: "sk_liv…cdef", hasKey: true, enabled: true },
  ],
  mode: "single",
  active: 0,
  stats: {},
}

function renderPanel(options: { reveal?: AccountsResponse } = {}) {
  const onSave = vi.fn(async (_value: AccountsResponse) => {})
  const onTest = vi.fn(async (_key: string): Promise<AccountTestResponse> => ({ ok: true, ms: 1 }))
  const onReveal = vi.fn(async (): Promise<AccountsResponse> => options.reveal ?? data)
  render(<AccountsPanel data={data} onSave={onSave} onTest={onTest} onReveal={onReveal} />)
  return { onSave, onTest, onReveal }
}

test("keeps the stored key out of the page until it is revealed", () => {
  renderPanel()
  const input = screen.getByLabelText("API Key") as HTMLInputElement
  expect(input.value).toBe("")
  expect(input.placeholder).toContain("sk_liv")
})

test("reveals stored keys only while the eye is on", async () => {
  const revealed: AccountsResponse = {
    ...data,
    accounts: [{ ...data.accounts[0], key: "sk_live_secret" }],
  }
  const { onReveal } = renderPanel({ reveal: revealed })
  fireEvent.click(screen.getByRole("button", { name: "显示密钥" }))
  await waitFor(() =>
    expect((screen.getByLabelText("API Key") as HTMLInputElement).value).toBe("sk_live_secret"),
  )
  expect(onReveal).toHaveBeenCalledTimes(1)
  fireEvent.click(screen.getByRole("button", { name: "隐藏密钥" }))
  expect((screen.getByLabelText("API Key") as HTMLInputElement).value).toBe("")
})

test("saves an untouched account with an empty key so the stored one is kept", async () => {
  const { onSave } = renderPanel()
  fireEvent.change(screen.getByLabelText("账号名称"), { target: { value: "renamed" } })
  fireEvent.click(screen.getByRole("button", { name: "保存" }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  const payload = onSave.mock.calls[0][0]
  expect(payload.accounts).toEqual([
    expect.objectContaining({ id: "acc_1", name: "renamed", key: "", hasKey: true }),
  ])
})
