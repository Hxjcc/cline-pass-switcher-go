import { act, cleanup, renderHook } from "@testing-library/react"
import { afterEach, expect, test } from "vitest"
import { useDraft } from "./use-draft"

afterEach(cleanup)

test("keeps edits until a new server snapshot arrives", () => {
  const original = { proxyKey: "original", enabled: true }
  const { result, rerender } = renderHook(({ data }) => useDraft(data), { initialProps: { data: original } })
  act(() => result.current[1]((value) => ({ ...value, proxyKey: "edited" })))
  rerender({ data: original })
  expect(result.current[0].proxyKey).toBe("edited")
  expect(original.proxyKey).toBe("original")
  rerender({ data: { proxyKey: "updated-on-server", enabled: false } })
  expect(result.current[0]).toEqual({ proxyKey: "updated-on-server", enabled: false })
  act(() => result.current[1]((value) => ({ ...value, proxyKey: "next-edit" })))
  expect(result.current[0]).toEqual({ proxyKey: "next-edit", enabled: false })
})
