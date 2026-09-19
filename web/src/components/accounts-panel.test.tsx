import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"
import { useState } from "react"

import { AccountsPanel } from "./accounts-panel"
import { formatPlanExpiry } from "@/lib/format"
import type { AccountTestResponse, AccountsResponse, QuotaResponse } from "@/types"

afterEach(() => { cleanup(); sessionStorage.clear() })

const data: AccountsResponse = {
  accounts: [
    { id: "acc_1", name: "main", key: "", keyPreview: "sk_liv…cdef", hasKey: true, enabled: true },
  ],
  mode: "single",
  active: 0,
  stats: {},
}

const quota: QuotaResponse = {
  accounts: [
    {
      account: "main",
      accountId: "acc_1",
      ok: true,
      plan: "Cline Pass (Monthly)",
      active: true,
      currentPeriodEnd: "2026-10-14T16:23:10Z",
      caps: { fiveHour: 1_000_000_000, weekly: 2_500_000_000, monthly: 5_000_000_000 },
      limits: [
        { type: "five_hour", percentUsed: 1, resetsAt: "2026-09-19T17:07:47Z" },
        { type: "weekly", percentUsed: 35, resetsAt: "2026-09-21T16:56:20Z" },
        { type: "monthly", percentUsed: 17, resetsAt: "2026-10-14T16:56:20Z" },
      ],
      fetchedAt: 1_789_900_000_000,
    },
  ],
}

function renderPanel(options: { reveal?: AccountsResponse; quota?: QuotaResponse } = {}) {
  const onSave = vi.fn(async (_value: AccountsResponse) => {})
  const onTest = vi.fn(
    async (_key: string, _id?: string): Promise<AccountTestResponse> => ({ ok: true, ms: 1 }),
  )
  const onReveal = vi.fn(async (): Promise<AccountsResponse> => options.reveal ?? data)
  const onQuota = vi.fn(async (_refresh?: boolean): Promise<QuotaResponse> => options.quota ?? quota)
  render(
    <AccountsPanel
      data={data}
      onSave={onSave}
      onTest={onTest}
      onReveal={onReveal}
      onQuota={onQuota}
    />,
  )
  return { onSave, onTest, onReveal, onQuota }
}

test("keeps the stored key out of the page until it is revealed", () => {
  renderPanel()
  const input = screen.getByLabelText("API Key") as HTMLInputElement
  expect(input.type).toBe("password")
  expect(input.value).not.toContain("sk_")
  expect(input.value.length).toBeGreaterThan(0)
  expect(input.placeholder).toBe("")
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
  const hidden = screen.getByLabelText("API Key") as HTMLInputElement
  expect(hidden.type).toBe("password")
  expect(hidden.value).not.toContain("sk_live")
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

test("keeps the selected account when an earlier row is removed", async () => {
  const pool: AccountsResponse = {
    accounts: [
      { id: "acc_a", name: "A", key: "", keyPreview: "aa…aa", hasKey: true, enabled: true },
      { id: "acc_b", name: "B", key: "", keyPreview: "bb…bb", hasKey: true, enabled: true },
      { id: "acc_c", name: "C", key: "", keyPreview: "cc…cc", hasKey: true, enabled: true },
    ],
    mode: "single",
    active: 1,
    stats: {},
  }
  const onSave = vi.fn(async (_value: AccountsResponse) => {})
  render(
    <AccountsPanel
      data={pool}
      onSave={onSave}
      onTest={vi.fn(async () => ({ ok: true, ms: 1 }))}
      onReveal={vi.fn(async () => pool)}
      onQuota={async () => quota}
    />,
  )

  fireEvent.click(screen.getAllByLabelText("删除账号")[0])
  fireEvent.click(screen.getByRole("button", { name: "保存" }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  const payload = onSave.mock.calls[0][0]
  expect(payload.accounts.map((account) => account.id)).toEqual(["acc_b", "acc_c"])
  // B moved to index 0 and must still be the selected account.
  expect(payload.active).toBe(0)
})

test("tests a saved account by identity instead of a cached revealed key", async () => {
  const revealed: AccountsResponse = {
    ...data,
    accounts: [{ ...data.accounts[0], key: "sk_live_stale" }],
  }
  const { onTest } = renderPanel({ reveal: revealed })
  fireEvent.click(screen.getByRole("button", { name: "显示密钥" }))
  await waitFor(() =>
    expect((screen.getByLabelText("API Key") as HTMLInputElement).value).toBe("sk_live_stale"),
  )
  fireEvent.click(screen.getByRole("button", { name: "测试" }))
  await waitFor(() => expect(onTest).toHaveBeenCalledTimes(1))
  expect(onTest).toHaveBeenCalledWith("", "acc_1")
})

test("drops revealed keys when a new snapshot arrives", async () => {
  const first: AccountsResponse = {
    accounts: [
      { id: "acc_1", name: "main", key: "", keyPreview: "old…key", hasKey: true, enabled: true },
    ],
    mode: "single",
    active: 0,
    stats: {},
  }
  const second: AccountsResponse = {
    ...first,
    accounts: [{ ...first.accounts[0], keyPreview: "new…key" }],
  }
  const onTest = vi.fn(
    async (_key: string, _id?: string): Promise<AccountTestResponse> => ({ ok: true, ms: 1 }),
  )
  const revealed: AccountsResponse = {
    ...first,
    accounts: [{ ...first.accounts[0], key: "sk_live_stale" }],
  }

  function Harness() {
    const [snapshot, setSnapshot] = useState(first)
    return (
      <>
        <button type="button" onClick={() => setSnapshot(second)}>
          切换快照
        </button>
        <AccountsPanel
          data={snapshot}
          onSave={async () => {}}
          onTest={onTest}
          onReveal={async () => revealed}
          onQuota={async () => quota}
        />
      </>
    )
  }

  render(<Harness />)
  fireEvent.click(screen.getByRole("button", { name: "显示密钥" }))
  await waitFor(() =>
    expect((screen.getByLabelText("API Key") as HTMLInputElement).value).toBe("sk_live_stale"),
  )

  fireEvent.click(screen.getByRole("button", { name: "切换快照" }))
  await waitFor(() => {
    const input = screen.getByLabelText("API Key") as HTMLInputElement
    expect(input.type).toBe("password")
    expect(input.value).not.toContain("sk_live")
    expect(input.placeholder).not.toContain("new")
  })

  fireEvent.click(screen.getByRole("button", { name: "测试" }))
  await waitFor(() => expect(onTest).toHaveBeenCalledTimes(1))
  expect(onTest).toHaveBeenCalledWith("", "acc_1")
})

test("keeps the selected account when an empty row above it is dropped on save", async () => {
  const base: AccountsResponse = {
    accounts: [
      { id: "acc_a", name: "A", key: "", keyPreview: "aa…aa", hasKey: true, enabled: true },
    ],
    mode: "single",
    active: 0,
    stats: {},
  }
  const onSave = vi.fn(async (_value: AccountsResponse) => {})
  render(
    <AccountsPanel
      data={base}
      onSave={onSave}
      onTest={vi.fn(async () => ({ ok: true, ms: 1 }))}
      onReveal={vi.fn(async () => base)}
      onQuota={async () => quota}
    />,
  )

  const add = screen.getByRole("button", { name: "添加账号" })
  fireEvent.click(add)
  fireEvent.click(add)
  fireEvent.click(add)
  const keyInputs = screen.getAllByLabelText("API Key") as HTMLInputElement[]
  fireEvent.change(keyInputs[2], { target: { value: "key-b" } })
  fireEvent.change(keyInputs[3], { target: { value: "key-c" } })
  fireEvent.click(screen.getAllByLabelText("设为当前账号")[2])

  fireEvent.click(screen.getByRole("button", { name: "保存" }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  const payload = onSave.mock.calls[0][0]
  // The empty draft row is dropped, so B moved from index 2 to index 1 and
  // must still be the selected account.
  expect(payload.accounts.map((account) => account.name)).toEqual(["A", "账号3", "账号4"])
  expect(payload.accounts.map((account) => account.id)).toEqual(["acc_a", "", ""])
  expect(payload.active).toBe(1)
})

test("shows the quota readout with progress bars and refreshes on demand", async () => {
  const { onQuota } = renderPanel()
  // Opening the accounts tab probes once without forcing a refresh.
  await waitFor(() => expect(onQuota).toHaveBeenCalledWith(false))
  await waitFor(() => expect(screen.getAllByRole("progressbar")).toHaveLength(3))

  const bars = screen.getAllByRole("progressbar")
  expect(bars).toHaveLength(3)
  expect(bars.map((bar) => bar.getAttribute("aria-valuenow"))).toEqual(["1", "35", "17"])
  expect(screen.getByText("1%")).toBeTruthy()
  expect(screen.getByText("35%")).toBeTruthy()
  expect(screen.getByText("17%")).toBeTruthy()
  expect(screen.getByText(formatPlanExpiry("2026-10-14T16:23:10Z"))).toBeTruthy()

  fireEvent.click(screen.getByRole("button", { name: "查询配额" }))
  await waitFor(() => expect(onQuota).toHaveBeenCalledWith(true))
})

test("reports a failed quota probe without hiding the account", async () => {
  const failed: QuotaResponse = {
    accounts: [
      { account: "main", accountId: "acc_1", ok: false, error: "密钥无效或未授权", fetchedAt: 1 },
    ],
  }
  renderPanel({ quota: failed })
  await waitFor(() => expect(screen.getByText("配额不可用")).toBeTruthy())
  // The rest of the row still works: the account can be renamed and saved.
  expect((screen.getByLabelText("账号名称") as HTMLInputElement).value).toBe("main")
})
