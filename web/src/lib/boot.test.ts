import { afterEach, expect, test, vi } from "vitest"
import { prepareBoot } from "./boot"

afterEach(() => {
  document.body.innerHTML = ""
  document.documentElement.removeAttribute("data-app-ready")
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

function mount(attributes: string, content = "") {
  document.body.innerHTML = `<div id="root" ${attributes}>${content}</div><div id="app-entry-cover">Cline Pass</div>`
  return document.getElementById("root")!
}

test("keeps a cached frame separate and inert until the replacement is ready", () => {
  const root = mount('data-boot-snapshot="true" inert', '<main>cached models</main>')
  const finish = prepareBoot(root)
  const snapshot = document.getElementById("app-boot-snapshot")!
  expect(snapshot.textContent).toBe("cached models")
  expect(snapshot.hasAttribute("inert")).toBe(true)
  expect(snapshot.getAttribute("aria-hidden")).toBe("true")
  root.innerHTML = "<main>live models</main>"
  expect(snapshot.textContent).toBe("cached models")
  expect(root.hasAttribute("data-boot-pending")).toBe(true)
  finish()
  expect(document.getElementById("app-boot-snapshot")).toBeNull()
  expect(root.textContent).toBe("live models")
  expect(root.hasAttribute("inert")).toBe(false)
  expect(root.hasAttribute("data-boot-pending")).toBe(false)
  expect(document.getElementById("app-entry-cover")).toBeNull()
})

test("first-load time passing alone cannot reveal a partially loaded page", async () => {
  vi.useFakeTimers()
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => window.setTimeout(() => callback(0), 16))
  vi.stubGlobal("matchMedia", () => ({ matches: false }))
  const root = mount('data-first-load="true"')
  const finish = prepareBoot(root)
  await vi.advanceTimersByTimeAsync(2000)
  expect(document.getElementById("app-entry-cover")).not.toBeNull()
  expect(root.hasAttribute("data-boot-pending")).toBe(true)
  expect(document.documentElement.hasAttribute("data-app-ready")).toBe(false)
  finish()
  finish() // StrictMode and later commits must not restart the entrance.
  await vi.advanceTimersByTimeAsync(32)
  expect(document.documentElement.hasAttribute("data-app-ready")).toBe(true)
  await vi.advanceTimersByTimeAsync(520)
  expect(document.getElementById("app-entry-cover")).toBeNull()
  expect(root.hasAttribute("data-first-load")).toBe(false)
  expect(root.hasAttribute("inert")).toBe(false)
  expect(document.documentElement.hasAttribute("data-app-ready")).toBe(false)
})

test.each(["reduced-motion", "error"])("reveals a ready or recovery page immediately for %s", mode => {
  vi.stubGlobal("matchMedia", () => ({ matches: mode === "reduced-motion" }))
  const root = mount('data-first-load="true"')
  const finish = prepareBoot(root)
  finish({ immediate: mode === "error" })
  expect(root.hasAttribute("data-first-load")).toBe(false)
  expect(root.hasAttribute("data-boot-pending")).toBe(false)
  expect(root.hasAttribute("inert")).toBe(false)
  expect(document.getElementById("app-entry-cover")).toBeNull()
})
