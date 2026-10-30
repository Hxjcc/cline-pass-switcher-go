export type PanelLoader = () => Promise<unknown>

/** Share code loading between background warm-up, hover and React.lazy. */
export function cachedPanelLoader<T>(load: () => Promise<T>): () => Promise<T> {
  let pending: Promise<T> | undefined
  return () => {
    pending ??= Promise.resolve().then(load).catch(error => {
      // A failed speculative fetch must not poison a later navigation.
      pending = undefined
      throw error
    })
    return pending
  }
}

/** Warm code in small idle batches; importing does not mount panel effects. */
export function preloadPanelsWhenIdle(loaders: PanelLoader[]): () => void {
  const remaining = [...loaders]
  let stopped = false
  let idle: number | undefined
  let timer: number | undefined
  const run = () => {
    idle = undefined
    timer = undefined
    if (stopped) return
    const batch = remaining.splice(0, 2)
    void Promise.allSettled(batch.map(load => load())).then(() => {
      if (!stopped && remaining.length) schedule()
    })
  }
  const schedule = () => {
    if (typeof window.requestIdleCallback === "function") idle = window.requestIdleCallback(run, { timeout: 1000 })
    else timer = window.setTimeout(run, 150)
  }
  schedule()
  return () => {
    stopped = true
    if (idle !== undefined) window.cancelIdleCallback?.(idle)
    if (timer !== undefined) window.clearTimeout(timer)
  }
}
