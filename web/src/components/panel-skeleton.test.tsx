import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, expect, test } from "vitest"

import { PanelSkeleton } from "./panel-skeleton"

afterEach(cleanup)

const placeholders = (container: HTMLElement) => container.querySelectorAll('[data-slot="skeleton"]')

// A cold console paints this before any response lands, so the heading has to
// be the real one and the frame has to announce itself as busy.
test("reserves the card frame with the real heading", () => {
  const { container } = render(
    <PanelSkeleton
      title="订阅模型"
      description="管理已订阅的模型及其上游渠道偏好。"
      rows={4}
    />,
  )
  expect(screen.getByText("订阅模型")).toBeTruthy()
  expect(screen.getByText("管理已订阅的模型及其上游渠道偏好。")).toBeTruthy()
  const card = container.querySelector("[data-panel-skeleton]")
  expect(card?.getAttribute("aria-busy")).toBe("true")
  // three action buttons, the filter row and four table rows
  expect(placeholders(container).length).toBe(9)
})

test("drops the action row and draws fields for form panels", () => {
  const { container } = render(
    <PanelSkeleton title="测试台" body="form" rows={4} actions={0} />,
  )
  expect(screen.queryByText("测试台")).toBeTruthy()
  // four labelled fields, each a label bar plus a control
  expect(placeholders(container).length).toBe(8)
})
