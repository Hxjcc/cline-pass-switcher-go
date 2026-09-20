import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, expect, test } from "vitest"

import { TestBench } from "./test-bench"
import type { SubscriptionModel, TestResponse } from "@/types"

afterEach(cleanup)

const models: SubscriptionModel[] = [
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
      pinReason: "gateway_ignores_provider_preferences",
      upstreams: ["deepseek", "fireworks"],
    },
  },
]

test("disables target-channel selection for an unpinnable model", () => {
  render(
    <TestBench
      models={models}
      onTest={async (): Promise<TestResponse> => ({ ok: true })}
    />,
  )

  expect(screen.getByText("当前模型不可指定目标渠道")).toBeTruthy()
  const target = screen.getAllByRole("combobox")[1]
  expect(target.hasAttribute("disabled") || target.getAttribute("aria-disabled") === "true").toBe(
    true,
  )
})
