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

test("rebases metadata while advancing the source used for later updates", () => {
  const original = { name: "server", requests: 4 }
  const reconcile = (draft: typeof original, next: typeof original, previous: typeof original) =>
    previous.name === next.name ? { ...draft, requests: next.requests } : next
  const { result, rerender } = renderHook(({ data }) => useDraft(data, reconcile), { initialProps: { data: original } })
  act(() => result.current[1]((row) => ({ ...row, name: "edited" })))
  rerender({ data: { name: "server", requests: 0 } })
  expect(result.current[0]).toEqual({ name: "edited", requests: 0 })
  rerender({ data: { name: "server", requests: 2 } })
  expect(result.current[0]).toEqual({ name: "edited", requests: 2 })
  rerender({ data: { name: "updated-on-server", requests: 3 } })
  expect(result.current[0]).toEqual({ name: "updated-on-server", requests: 3 })
  act(() => result.current[1]((row) => ({ ...row, name: "next edit" })))
  rerender({ data: { name: "updated-on-server", requests: 0 } })
  expect(result.current[0]).toEqual({ name: "next edit", requests: 0 })
})
