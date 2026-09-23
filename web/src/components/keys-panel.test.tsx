import { cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"

import { KeysPanel } from "./keys-panel"
import type { AccountsResponse, KeysResponse, ProxyKeyDraft } from "@/types"

afterEach(cleanup)

const accounts: AccountsResponse = {
  accounts: [
    { id: "acc_main", name: "main", key: "", keyPreview: "sk_liv…cdef", hasKey: true, enabled: true },
    { id: "acc_off", name: "spare", key: "", keyPreview: "", hasKey: true, enabled: false },
  ],
  mode: "single",
  active: 0,
  stats: {},
}

function renderPanel(keys: KeysResponse["keys"]) {
  const onSave = vi.fn(async (_value: ProxyKeyDraft[]): Promise<KeysResponse> => ({ keys }))
  const onReveal = vi.fn(async (): Promise<KeysResponse> => ({ keys }))
  const onReset = vi.fn(async (): Promise<KeysResponse> => ({ keys }))
  render(
    <KeysPanel data={{ keys }} accounts={accounts} onSave={onSave} onReveal={onReveal} onReset={onReset} />,
  )
  return { onSave, onReveal, onReset }
}

test("mints an sk- prefixed key when a row is added", () => {
  renderPanel([])
  fireEvent.click(screen.getByRole("button", { name: /新增客户端密钥/ }))
  const input = screen.getByLabelText("客户端密钥") as HTMLInputElement
  expect(input.value).toMatch(/^sk-[0-9a-f]{48}$/)
})

test("saves the draft in the shape the API expects", async () => {
  const { onSave } = renderPanel([
    {
      id: "key_1",
      name: "给小王",
      keyPreview: "sk-12…cdef",
      hasKey: true,
      enabled: true,
      accountId: "acc_main",
      spendLimitUsd: 5,
      requests: 3,
      spentUsd: 1.25,
    },
  ])
  fireEvent.click(screen.getByRole("button", { name: /^保存$/ }))
  await vi.waitFor(() => expect(onSave).toHaveBeenCalled())
  const payload = onSave.mock.calls[0][0]
  expect(payload).toHaveLength(1)
  // The secret stays empty: an empty key plus a known id means "keep it".
  expect(payload[0]).toMatchObject({
    id: "key_1",
    name: "给小王",
    key: "",
    enabled: true,
    accountId: "acc_main",
    spendLimitUsd: 5,
  })
})

test("flags a spent-out key and an unusable binding", () => {
  renderPanel([
    {
      id: "key_done",
      name: "用尽",
      keyPreview: "sk-aa…aaaa",
      hasKey: true,
      enabled: true,
      spendLimitUsd: 5,
      requests: 10,
      spentUsd: 5,
    },
    {
      id: "key_orphan",
      name: "孤儿",
      keyPreview: "sk-bb…bbbb",
      hasKey: true,
      enabled: true,
      accountId: "acc_off",
      requests: 1,
      spentUsd: 0.1,
    },
  ])
  expect(screen.getByText("额度已用尽")).toBeTruthy()
  expect(screen.getByText("绑定账号不可用")).toBeTruthy()
})
