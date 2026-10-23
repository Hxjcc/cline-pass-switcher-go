import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"
import { TooltipProvider } from "@/components/ui/tooltip"
import { Table, TableBody } from "@/components/ui/table"
import { ModelRow } from "./model-row"
import { recommendedHas } from "@/lib/gateway-routing"
import type { GatewayCatalog, RoutingVerifyResponse, SubscriptionModel } from "@/types"

afterEach(cleanup)

const catalog: GatewayCatalog = { model: "deepseek/deepseek-v4.1-flash-1017", source: "https://example.test/endpoints", fetchedAt: Date.now(), providers: [
  { slug: "deepseek", name: "deepseek", has: ["vision", "implicit-caching", "reasoning", "tool-use", "structured-output"] },
  { slug: "other", name: "Other", has: ["tool-use"] },
  { slug: "vision-only", name: "Vision", has: ["vision"] },
] }

const privateModel: SubscriptionModel = { id: "cline-pass/deepseek-v4.1-flash-1017", config: { upstreams: [], exclude: [] }, meta: { pipeline: "planner", pinnable: false, pinReason: "single_provider", canonicalSlug: "private/deepseek-contributor", upstreams: ["openai-compatible-private"], gatewayCatalog: catalog } }

function setup(ok = true, model = privateModel) {
  const onUpdateConfig = vi.fn(async () => {})
  const onReadGateway = vi.fn(async (_modelID: string, _refresh: boolean, upstreams: string[] = []) => ({ ok: true, catalog, recommendedHas: recommendedHas(catalog, upstreams) }))
  const onVerifyGateway = vi.fn(async (): Promise<RoutingVerifyResponse> => ({ ok, applied: ok, checks: [{ profile: "check", accountId: "one", account: "账号一", has: ["vision", "implicit-caching"], targets: ["deepseek"], actual: [ok ? "deepseek" : "openai-compatible-private"], matched: ok, rejected: ok, ok, current: ok, checkedAt: Date.now(), ms: 1, error: ok ? undefined : "上游忽略了 only" }] }))
  render(<TooltipProvider><Table><TableBody><ModelRow model={model} onProbe={async () => ({ ok: true, ms: 1 })} onValidate={async () => ({ ok: true, summary: { ok: 0, limited: 0, bad: 0, auth: 0, unknown: 0 }, results: {}, upstreams: [] })} onTest={async () => ({ ok: true })} onUpdateConfig={onUpdateConfig} onRemove={async () => {}} onRefresh={async () => {}} onReadGateway={onReadGateway} onVerifyGateway={onVerifyGateway} /></TableBody></Table></TooltipProvider>)
  fireEvent.click(screen.getByRole("button", { name: "展开模型" }))
  return { onUpdateConfig, onVerifyGateway, onReadGateway }
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
  expect(onVerifyGateway.mock.calls[0]).toEqual([privateModel.id, { upstream: undefined, upstreams: ["deepseek"], exclude: [], gatewayHas: ["implicit-caching", "reasoning", "structured-output", "tool-use", "vision"] }, "", false])
  await waitFor(() => expect(screen.getByText("对照验证通过")).toBeTruthy())
})

test("recommendations use selected suppliers' common capabilities without saving the draft", async () => {
  const { onReadGateway, onUpdateConfig, onVerifyGateway } = setup(true, { ...privateModel, config: { upstreams: ["deepseek", "other"], exclude: [], gatewayHas: ["vision"] } })
  fireEvent.click(screen.getByRole("button", { name: "使用推荐" }))
  await waitFor(() => expect(screen.getByRole("checkbox", { name: "要求工具调用能力" }).getAttribute("aria-checked")).toBe("true"))
  expect(onReadGateway).toHaveBeenCalledWith(privateModel.id, true, ["deepseek", "other"])
  expect(screen.getByRole("checkbox", { name: "要求图片输入能力" }).getAttribute("aria-checked")).toBe("false")
  expect(screen.getByText(/推荐依据：deepseek、Other的共同能力/)).toBeTruthy()
  expect(onUpdateConfig).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole("button", { name: "验证并应用" }))
  await waitFor(() => expect(onVerifyGateway).toHaveBeenCalledWith(privateModel.id, expect.objectContaining({ upstreams: ["deepseek", "other"], gatewayHas: ["tool-use"] }), "", false))
})

test("an empty common recommendation preserves the current draft and selected suppliers", async () => {
  const { onReadGateway, onUpdateConfig } = setup(true, { ...privateModel, config: { upstreams: ["other", "vision-only"], exclude: [], gatewayHas: ["vision"] } })
  fireEvent.click(screen.getByRole("button", { name: "使用推荐" }))
  await waitFor(() => expect(onReadGateway).toHaveBeenCalledWith(privateModel.id, true, ["other", "vision-only"]))
  await waitFor(() => expect((screen.getByRole("button", { name: "使用推荐" }) as HTMLButtonElement).disabled).toBe(false))
  expect(screen.getByRole("checkbox", { name: "要求图片输入能力" }).getAttribute("aria-checked")).toBe("true")
  expect(screen.getByText(/推荐依据：Other、Vision的共同能力/)).toBeTruthy()
  expect(screen.queryByText("待应用")).toBeNull()
  expect(onUpdateConfig).not.toHaveBeenCalled()
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
