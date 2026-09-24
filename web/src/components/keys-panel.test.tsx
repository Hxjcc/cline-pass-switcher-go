import { cleanup, fireEvent, render, screen, within } from "@testing-library/react"
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

test("reveals stored secrets on demand and masks them again", async () => {
  const stored = { id: "key_1", name: "ci", keyPreview: "sk-12…cdef", hasKey: true, enabled: true, requests: 0, spentUsd: 0 }
  const onReveal = vi.fn(async (): Promise<KeysResponse> => ({ keys: [{ ...stored, key: "sk-plain" }] }))
  render(
    <KeysPanel
      data={{ keys: [stored] }}
      accounts={accounts}
      onSave={vi.fn()}
      onReveal={onReveal}
      onReset={vi.fn()}
    />,
  )
  const input = screen.getByLabelText("客户端密钥") as HTMLInputElement
  expect(input.value).toBe("")

  fireEvent.click(screen.getByRole("button", { name: /显示密钥/ }))
  await vi.waitFor(() => expect(input.value).toBe("sk-plain"))

  fireEvent.click(screen.getByRole("button", { name: /隐藏密钥/ }))
  expect(input.value).toBe("")
})

test("shows spend against the limit with readable amounts", () => {
  renderPanel([
    { id: "key_small", name: "small", hasKey: true, enabled: true, spendLimitUsd: 0.5, requests: 2, spentUsd: 0.0018 },
  ])
  const card = within(screen.getByRole("article", { name: "small" }))
  expect(card.getByText("$0.0018")).toBeTruthy()
  expect(card.getByText(/\$0\.50/)).toBeTruthy()
  expect(card.getByRole("progressbar", { name: "额度用量" }).getAttribute("aria-valuenow")).toBe("0")
})

test("an empty list offers a single way to add a key", () => {
  renderPanel([])
  expect(screen.getByText("尚未签发客户端密钥")).toBeTruthy()
  expect(screen.getAllByRole("button", { name: /新增客户端密钥/ })).toHaveLength(1)
})
