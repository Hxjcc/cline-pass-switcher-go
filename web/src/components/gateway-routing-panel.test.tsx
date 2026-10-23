import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"
import { TooltipProvider } from "@/components/ui/tooltip"
import { Table, TableBody } from "@/components/ui/table"
import { ModelRow } from "./model-row"
import type { GatewayCatalog, RoutingVerifyResponse, SubscriptionModel } from "@/types"

afterEach(cleanup)

const catalog: GatewayCatalog = { model: "deepseek/deepseek-v4.1-flash-1017", source: "https://example.test/endpoints", fetchedAt: Date.now(), providers: [
  { slug: "deepseek", name: "deepseek", has: ["vision", "implicit-caching", "tool-use"] },
  { slug: "other", name: "Other", has: ["tool-use"] },
] }

const privateModel: SubscriptionModel = { id: "cline-pass/deepseek-v4.1-flash-1017", config: { upstreams: [], exclude: [] }, meta: { pipeline: "planner", pinnable: false, pinReason: "single_provider", canonicalSlug: "private/deepseek-contributor", upstreams: ["openai-compatible-private"], gatewayCatalog: catalog } }

function setup(ok = true) {
  const onUpdateConfig = vi.fn(async () => {})
  const onVerifyGateway = vi.fn(async (): Promise<RoutingVerifyResponse> => ({ ok, applied: ok, checks: [{ profile: "check", accountId: "one", account: "账号一", has: ["vision", "implicit-caching"], targets: ["deepseek"], actual: [ok ? "deepseek" : "openai-compatible-private"], matched: ok, rejected: ok, ok, current: ok, checkedAt: Date.now(), ms: 1, error: ok ? undefined : "上游忽略了 only" }] }))
  render(<TooltipProvider><Table><TableBody><ModelRow model={privateModel} onProbe={async () => ({ ok: true, ms: 1 })} onValidate={async () => ({ ok: true, summary: { ok: 0, limited: 0, bad: 0, auth: 0, unknown: 0 }, results: {}, upstreams: [] })} onTest={async () => ({ ok: true })} onUpdateConfig={onUpdateConfig} onRemove={async () => {}} onRefresh={async () => {}} onReadGateway={async () => ({ ok: true, catalog, recommendedHas: ["implicit-caching", "vision"] })} onVerifyGateway={onVerifyGateway} /></TableBody></Table></TooltipProvider>)
  fireEvent.click(screen.getByRole("button", { name: "展开模型" }))
  return { onUpdateConfig, onVerifyGateway }
}

test("a private-only model exposes filtered suppliers and keeps changes as a draft until verification", async () => {
  const { onUpdateConfig, onVerifyGateway } = setup()
  expect(screen.getByText("当前探测仅发现一个渠道")).toBeTruthy()
  fireEvent.click(screen.getByRole("switch", { name: "启用能力过滤" }))
  const supplier = screen.getByRole("group", { name: "供应商 deepseek" })
  expect(screen.queryByRole("group", { name: "供应商 other" })).toBeNull()
  const pin = within(supplier).getByRole("button", { name: "设为优先" }) as HTMLButtonElement
  expect(pin.disabled).toBe(false)
  fireEvent.click(pin)
  expect(onUpdateConfig).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole("button", { name: "验证并应用" }))
  await waitFor(() => expect(onVerifyGateway).toHaveBeenCalledOnce())
  expect(onVerifyGateway.mock.calls[0]).toEqual([privateModel.id, { upstream: undefined, upstreams: ["deepseek"], exclude: [], gatewayHas: ["implicit-caching", "vision"] }, "", false])
  await waitFor(() => expect(screen.getByText("对照验证通过")).toBeTruthy())
})

test("a failed routing control retains the original configuration and displays the reason", async () => {
  const { onUpdateConfig } = setup(false)
  fireEvent.click(screen.getByRole("switch", { name: "启用能力过滤" }))
  fireEvent.click(within(screen.getByRole("group", { name: "供应商 deepseek" })).getByRole("button", { name: "设为优先" }))
  fireEvent.click(screen.getByRole("button", { name: "验证并应用" }))
  await waitFor(() => expect(screen.getByText("上游忽略了 only")).toBeTruthy())
  expect(onUpdateConfig).not.toHaveBeenCalled()
  expect(screen.getByText("待应用")).toBeTruthy()
})
