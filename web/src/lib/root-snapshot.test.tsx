import { cleanup, render } from "@testing-library/react"
import { afterEach, expect, test } from "vitest"

import { snapshotHtml } from "./root-snapshot"

afterEach(cleanup)

// React writes controlled values to the value attribute, so a revealed key
// would land in the boot frame unless masking follows the field rather than
// the input type.
test("masks a secret rendered as plain text", () => {
  const { container } = render(<input data-secret="1" type="text" value="sk-live-secret" readOnly />)
  const html = snapshotHtml(container)
  expect(html).not.toContain("sk-live-secret")
  expect(html).toContain(`value="${"x".repeat("sk-live-secret".length)}"`)
})

test("still masks password fields that carry no marker", () => {
  const { container } = render(<input type="password" value="sk-legacy" readOnly />)
  expect(snapshotHtml(container)).not.toContain("sk-legacy")
})

test("leaves ordinary fields alone", () => {
  const { container } = render(<input type="text" value="账号1" readOnly />)
  expect(snapshotHtml(container)).toContain("账号1")
})
