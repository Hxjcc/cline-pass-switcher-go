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
  const input = screen.getByLabelText("客户端密钥") as HTMLInputElement
  expect(input.type).toBe("password")
  expect(input.value).toMatch(/^0+$/)
  fireEvent.focus(input)
  expect(input.value).toBe("")
  fireEvent.blur(input)
  expect(input.value).toMatch(/^0+$/)
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
  expect(input.type).toBe("password")
  expect(input.value).toMatch(/^0+$/)

  fireEvent.click(screen.getByRole("button", { name: /显示密钥/ }))
  await vi.waitFor(() => expect(input.value).toBe("sk-plain"))
  expect(input.type).toBe("text")

  fireEvent.click(screen.getByRole("button", { name: /隐藏密钥/ }))
  expect(input.type).toBe("password")
  expect(input.value).toMatch(/^0+$/)
})

test("revealing keys preserves edits, new rows, deletions and typed secrets", async () => {
  const stored = { id: "key_1", name: "saved", hasKey: true, enabled: true, requests: 0, spentUsd: 0 }
  const removed = { ...stored, id: "key_2", name: "removed" }
  const onReveal = vi.fn(async (): Promise<KeysResponse> => ({
    keys: [{ ...stored, key: "sk-stored" }, { ...removed, key: "sk-removed" }],
  }))
  const onSave = vi.fn(async (_value: ProxyKeyDraft[]): Promise<KeysResponse> => ({ keys: [stored] }))
  render(<KeysPanel data={{ keys: [stored, removed] }} accounts={accounts} onSave={onSave} onReveal={onReveal} onReset={vi.fn()} />)

  fireEvent.change(screen.getAllByPlaceholderText("使用者或用途")[0], { target: { value: "renamed" } })
  fireEvent.change(screen.getAllByLabelText("额度上限（USD）")[0], { target: { value: "2.5" } })
  fireEvent.change(screen.getAllByPlaceholderText("可选")[0], { target: { value: "keep this note" } })
  fireEvent.change(screen.getAllByLabelText("客户端密钥")[0], { target: { value: "sk-replacement" } })
  fireEvent.click(within(screen.getByRole("article", { name: "removed" })).getByRole("button", { name: "删除密钥" }))
  fireEvent.click(screen.getByRole("button", { name: /新增客户端密钥/ }))
  fireEvent.change(screen.getAllByPlaceholderText("使用者或用途")[1], { target: { value: "new draft" } })
  const minted = (screen.getAllByLabelText("客户端密钥")[1] as HTMLInputElement).value

  fireEvent.click(screen.getByRole("button", { name: /显示密钥/ }))
  await vi.waitFor(() => expect(screen.getByRole("button", { name: /隐藏密钥/ })).toBeTruthy())
  expect(screen.getAllByRole("article")).toHaveLength(2)
  expect(screen.queryByRole("article", { name: "removed" })).toBeNull()
  expect((screen.getAllByLabelText("客户端密钥")[0] as HTMLInputElement).value).toBe("sk-replacement")
  fireEvent.click(screen.getByRole("button", { name: /隐藏密钥/ }))
  expect((screen.getAllByLabelText("客户端密钥")[1] as HTMLInputElement).value).toBe(minted)

  fireEvent.click(screen.getByRole("button", { name: /^保存$/ }))
  await vi.waitFor(() => expect(onSave).toHaveBeenCalled())
  const payload = onSave.mock.calls[0][0]
  expect(payload).toHaveLength(2)
  expect(payload[0]).toMatchObject({ id: "key_1", name: "renamed", note: "keep this note", spendLimitUsd: 2.5, key: "sk-replacement", dirty: true })
  expect(payload[1]).toMatchObject({ name: "new draft", key: minted, dirty: true })
})

test("hiding a revealed key preserves a name edit and keeps the stored secret out of the draft", async () => {
  const stored = { id: "key_1", name: "saved", hasKey: true, enabled: true, requests: 0, spentUsd: 0 }
  const onReveal = vi.fn(async (): Promise<KeysResponse> => ({ keys: [{ ...stored, key: "sk-stored" }] }))
  const onSave = vi.fn(async (_value: ProxyKeyDraft[]): Promise<KeysResponse> => ({ keys: [stored] }))
  render(<KeysPanel data={{ keys: [stored] }} accounts={accounts} onSave={onSave} onReveal={onReveal} onReset={vi.fn()} />)
  fireEvent.change(screen.getByPlaceholderText("使用者或用途"), { target: { value: "renamed" } })
  fireEvent.click(screen.getByRole("button", { name: /显示密钥/ }))
  await vi.waitFor(() => expect((screen.getByLabelText("客户端密钥") as HTMLInputElement).value).toBe("sk-stored"))
  fireEvent.click(screen.getByRole("button", { name: /隐藏密钥/ }))
  expect((screen.getByLabelText("客户端密钥") as HTMLInputElement).value).toMatch(/^0+$/)
  fireEvent.click(screen.getByRole("button", { name: /^保存$/ }))
  await vi.waitFor(() => expect(onSave).toHaveBeenCalled())
  expect(onSave.mock.calls[0][0][0]).toMatchObject({ name: "renamed", key: "" })
})

test("a late reveal cannot expose a credential from an earlier server snapshot", async () => {
  const stored = { id: "key_1", name: "saved", hasKey: true, enabled: true, requests: 0, spentUsd: 0 }
  let finish!: (value: KeysResponse) => void
  const onReveal = vi.fn(() => new Promise<KeysResponse>((resolve) => { finish = resolve }))
  const props = { accounts, onSave: vi.fn(), onReveal, onReset: vi.fn() }
  const panel = render(<KeysPanel {...props} data={{ keys: [stored] }} />)
  fireEvent.click(screen.getByRole("button", { name: /显示密钥/ }))
  panel.rerender(<KeysPanel {...props} data={{ keys: [{ ...stored, keyPreview: "sk-new…key" }] }} />)
  finish({ keys: [{ ...stored, key: "sk-old-secret" }] })
  await vi.waitFor(() => expect(screen.getByRole("button", { name: /显示密钥/ }).hasAttribute("disabled")).toBe(false))
  expect((screen.getByLabelText("客户端密钥") as HTMLInputElement).value).toMatch(/^0+$/)
  expect(screen.queryByRole("button", { name: /隐藏密钥/ })).toBeNull()
})

test("resetting usage updates counters without discarding any unsaved rows", async () => {
  const stored = { id: "key_1", name: "saved", hasKey: true, enabled: true, requests: 4, spentUsd: 1.25 }
  const removed = { ...stored, id: "key_2", name: "removed" }
  const reset: KeysResponse = { keys: [{ ...stored, requests: 0, spentUsd: 0 }, removed] }
  const onSave = vi.fn(async (_value: ProxyKeyDraft[]): Promise<KeysResponse> => reset)
  let updateSnapshot!: () => void
  const onReset = vi.fn(async () => { updateSnapshot(); return reset })
  const props = { accounts, onSave, onReveal: vi.fn(), onReset }
  const panel = render(<KeysPanel {...props} data={{ keys: [stored, removed] }} />)
  updateSnapshot = () => panel.rerender(<KeysPanel {...props} data={reset} />)
  fireEvent.change(screen.getAllByPlaceholderText("使用者或用途")[0], { target: { value: "renamed" } })
  fireEvent.change(screen.getAllByLabelText("客户端密钥")[0], { target: { value: "sk-edited" } })
  fireEvent.change(screen.getAllByLabelText("额度上限（USD）")[0], { target: { value: "2.5" } })
  fireEvent.click(within(screen.getByRole("article", { name: "removed" })).getByRole("button", { name: "删除密钥" }))
  fireEvent.click(screen.getByRole("button", { name: /新增客户端密钥/ }))
  fireEvent.change(screen.getAllByPlaceholderText("使用者或用途")[1], { target: { value: "new draft" } })
  const minted = (screen.getAllByLabelText("客户端密钥")[1] as HTMLInputElement).value
  fireEvent.click(within(screen.getByRole("article", { name: "renamed" })).getByRole("button", { name: "重置用量" }))
  await vi.waitFor(() => expect(onReset).toHaveBeenCalled())
  await vi.waitFor(() => expect(screen.getByRole("article", { name: "renamed" })).toBeTruthy())
  expect(screen.queryByRole("article", { name: "removed" })).toBeNull()
  expect(screen.getByRole("article", { name: "new draft" })).toBeTruthy()
  expect(within(screen.getByRole("article", { name: "renamed" })).getByText("0")).toBeTruthy()
  fireEvent.click(screen.getByRole("button", { name: /^保存$/ }))
  await vi.waitFor(() => expect(onSave).toHaveBeenCalled())
  expect(onSave.mock.calls[0][0]).toHaveLength(2)
  expect(onSave.mock.calls[0][0][0]).toMatchObject({ id: "key_1", name: "renamed", key: "sk-edited", spendLimitUsd: 2.5, requests: 0, spentUsd: 0 })
  expect(onSave.mock.calls[0][0][1]).toMatchObject({ name: "new draft", key: minted })
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
