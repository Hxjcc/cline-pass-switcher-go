export interface ProbeBatchResult {
  ok: number
  failed: number
  aborted: boolean
}

/**
 * Runs one probe per id with a fixed number of workers. Probing is a
 * diagnostic pass, so a single failure is counted and skipped instead of
 * stopping the batch. Aborting waits for the in-flight requests to settle
 * before returning, which lets the caller refresh state without racing them.
 */
export async function runProbeBatch(
  ids: string[],
  probe: (id: string, signal: AbortSignal) => Promise<void>,
  options: {
    signal: AbortSignal
    concurrency?: number
    onProgress?: (done: number, total: number) => void
  },
): Promise<ProbeBatchResult> {
  const { signal, onProgress } = options
  const concurrency = Math.max(1, options.concurrency ?? 3)
  const queue = [...ids]
  const total = queue.length
  let done = 0
  let ok = 0
  let failed = 0
  onProgress?.(0, total)

  const worker = async () => {
    for (;;) {
      if (signal.aborted) return
      const id = queue.shift()
      if (id === undefined) return
      try {
        await probe(id, signal)
        ok += 1
      } catch {
        if (signal.aborted) return
        failed += 1
      }
      done += 1
      onProgress?.(done, total)
    }
  }

  await Promise.all(Array.from({ length: Math.min(concurrency, total) }, worker))
  return { ok, failed, aborted: signal.aborted }
}
