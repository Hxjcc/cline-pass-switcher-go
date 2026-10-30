import { afterEach, expect, test, vi } from "vitest"
import { cachedPanelLoader, preloadPanelsWhenIdle } from "./panel-preload"

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

test("hover, background and navigation share one module load", async () => {
  let resolve!: (value: string) => void
  const load = vi.fn(() => new Promise<string>(done => { resolve = done }))
  const preload = cachedPanelLoader(load)
  const first = preload()
  expect(preload()).toBe(first)
  await Promise.resolve()
  expect(load).toHaveBeenCalledTimes(1)
  resolve("ready")
  await expect(first).resolves.toBe("ready")
  await expect(preload()).resolves.toBe("ready")
  expect(load).toHaveBeenCalledTimes(1)
})

test("a failed speculative load can be retried on navigation", async () => {
  const load = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce("ready")
  const preload = cachedPanelLoader(load)
  await expect(preload()).rejects.toThrow("offline")
  await expect(preload()).resolves.toBe("ready")
  expect(load).toHaveBeenCalledTimes(2)
})

test("idle warm-up continues after a failed module and can be cancelled", async () => {
  vi.useFakeTimers()
  vi.stubGlobal("requestIdleCallback", undefined)
  const fail = vi.fn().mockRejectedValue(new Error("offline"))
  const ready = vi.fn().mockResolvedValue("ready")
  const later = vi.fn().mockResolvedValue("later")
  const cancelled = vi.fn().mockResolvedValue("cancelled")
  const stop = preloadPanelsWhenIdle([fail, ready, later, ready, cancelled])
  expect(fail).not.toHaveBeenCalled()
  await vi.advanceTimersByTimeAsync(150)
  expect(fail).toHaveBeenCalledTimes(1)
  expect(ready).toHaveBeenCalledTimes(1)
  await vi.advanceTimersByTimeAsync(150)
  expect(later).toHaveBeenCalledTimes(1)
  stop()
  await vi.runAllTimersAsync()
  expect(cancelled).not.toHaveBeenCalled()
})
