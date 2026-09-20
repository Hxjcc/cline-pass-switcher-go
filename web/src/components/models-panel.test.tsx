import { cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"

import { TooltipProvider } from "@/components/ui/tooltip"
import { ModelsPanel } from "./models-panel"
import type {
  ModelsResponse,
  OfficialResponse,
  ProbeResponse,
  TestResponse,
  ValidationResponse,
} from "@/types"

afterEach(cleanup)

const data: ModelsResponse = {
  subscription: [
    {
      id: "cline-pass/deepseek-v4.1-flash",
      config: {
        upstreams: ["fireworks"],
        exclude: [],
        pinMode: "strict",
        sort: null,
      },
      meta: {
        pipeline: "planner",
        pinnable: false,
        pinReason: "gateway_ignores_provider_preferences",
        upstreams: ["deepseek", "fireworks"],
        lastProvider: "deepseek",
        lastMs: 120,
      },
    },
  ],
  catalog: ["cline-pass/deepseek-v4.1-flash"],
  catalogCount: 1,
  proxyBase: "http://127.0.0.1:3123",
  officialFetch: null,
}

function renderPanel() {
  const onValidate = vi.fn(
    async (): Promise<ValidationResponse> => ({
      ok: true,
      supported: false,
      reason: "gateway_ignores_provider_preferences",
      summary: { ok: 0, limited: 0, bad: 0, auth: 0, unknown: 0 },
      results: {},
      upstreams: ["deepseek", "fireworks"],
    }),
  )
  render(
    <TooltipProvider>
      <ModelsPanel
        data={data}
        onRefresh={async () => {}}
        onProbe={async (): Promise<ProbeResponse> => ({ ok: true, ms: 1 })}
        onProbeAll={async () => ({ ok: 0, failed: 0, aborted: false })}
        probeAllProgress={null}
        onCancelProbeAll={() => {}}
        onValidate={onValidate}
        onTest={async (): Promise<TestResponse> => ({ ok: true })}
        onUpdateConfig={async () => {}}
        onFetchOfficial={async (): Promise<OfficialResponse> => ({
          ok: true,
          sources: [],
          found: 0,
          added: [],
          knownModels: [],
          ts: 0,
          total: 0,
        })}
        onRemove={async () => {}}
      />
    </TooltipProvider>,
  )
  return { onValidate }
}

test("flags an unpinnable planner and disables channel validation", () => {
  const { onValidate } = renderPanel()
  fireEvent.click(screen.getByRole("button", { name: "展开模型" }))

  expect(screen.getByText("网关已忽略上游偏好")).toBeTruthy()
  expect(screen.getByText("未命中")).toBeTruthy()

  const validate = screen.getByRole("button", {
    name: "不可校验：网关已忽略上游偏好",
  }) as HTMLButtonElement
  expect(validate.disabled).toBe(true)
  fireEvent.click(validate)
  expect(onValidate).not.toHaveBeenCalled()
})
