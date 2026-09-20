import { expect, test } from "vitest"
import { runProbeBatch } from "./probe-batch"

test("probes every model a few at a time and reports progress", async () => {
  const ids = ["m1", "m2", "m3", "m4", "m5"]
  const seen: string[] = []
  const progress: number[] = []
  let running = 0
  let peak = 0

  const result = await runProbeBatch(
    ids,
    async (id) => {
      running += 1
      peak = Math.max(peak, running)
      seen.push(id)
      await new Promise((resolve) => setTimeout(resolve, 5))
      running -= 1
    },
    {
      signal: new AbortController().signal,
      concurrency: 2,
      onProgress: (done) => progress.push(done),
    },
  )

  expect(result).toEqual({ ok: 5, failed: 0, aborted: false })
  expect([...seen].sort()).toEqual(ids)
  expect(peak).toBe(2)
  expect(progress).toEqual([0, 1, 2, 3, 4, 5])
})

test("counts failures without stopping the batch", async () => {
  const result = await runProbeBatch(
    ["good", "bad", "also-good"],
    async (id) => {
      if (id === "bad") throw new Error("probe failed")
    },
    { signal: new AbortController().signal },
  )
  expect(result).toEqual({ ok: 2, failed: 1, aborted: false })
})

test("stops early when the caller aborts", async () => {
  const controller = new AbortController()
  const seen: string[] = []
  const result = await runProbeBatch(
    ["m1", "m2", "m3", "m4"],
    async (id) => {
      seen.push(id)
      controller.abort()
    },
    { signal: controller.signal, concurrency: 1 },
  )
  expect(result.aborted).toBe(true)
  expect(seen).toEqual(["m1"])
})

test("handles an empty list", async () => {
  const result = await runProbeBatch([], async () => {}, {
    signal: new AbortController().signal,
  })
  expect(result).toEqual({ ok: 0, failed: 0, aborted: false })
})
