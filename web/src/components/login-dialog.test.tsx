import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"
import { LoginDialog } from "./login-dialog"

afterEach(cleanup)

test("shows login errors and clears the previous key when reopened", async () => {
  const onLogin = vi.fn().mockRejectedValue(new Error("wrong key"))
  const onOpenChange = vi.fn()
  const { rerender } = render(<LoginDialog open onLogin={onLogin} onOpenChange={onOpenChange} />)
  fireEvent.change(screen.getByLabelText("管理密钥"), { target: { value: " fake-key " } })
  fireEvent.click(screen.getByRole("button", { name: "进入控制台" }))
  await waitFor(() => expect(screen.getByText("wrong key")).toBeTruthy())
  expect(onLogin).toHaveBeenCalledWith("fake-key", false)
  expect(onOpenChange).not.toHaveBeenCalled()
  rerender(<LoginDialog open={false} onLogin={onLogin} onOpenChange={onOpenChange} />)
  rerender(<LoginDialog open onLogin={onLogin} onOpenChange={onOpenChange} />)
  expect((screen.getByLabelText("管理密钥") as HTMLInputElement).value).toBe("")
  expect(screen.queryByText("wrong key")).toBeNull()
})

// Remembering the key is opt-in: without the checkbox it must not be persisted
// beyond the browser session.
test("asks before persisting the key on this device", async () => {
  const onLogin = vi.fn().mockResolvedValue(undefined)
  render(<LoginDialog open onLogin={onLogin} onOpenChange={vi.fn()} />)
  fireEvent.change(screen.getByLabelText("管理密钥"), { target: { value: "fake-key" } })
  fireEvent.click(screen.getByRole("checkbox", { name: "在这台设备上记住密钥" }))
  fireEvent.click(screen.getByRole("button", { name: "进入控制台" }))
  await waitFor(() => expect(onLogin).toHaveBeenCalledWith("fake-key", true))
})
