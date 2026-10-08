import { useState, type Dispatch, type SetStateAction } from "react"

// A new server snapshot replaces the draft immediately, without an effect
// rendering stale settings for one frame. Local edits never mutate the source.
// A reconciler can preserve edits when the snapshot only updates metadata.
export function useDraft<T>(source: T, reconcile?: (draft: T, next: T, previous: T) => T): [T, Dispatch<SetStateAction<T>>] {
  const [state, setState] = useState({ source, value: source })
  const value = state.source === source
    ? state.value
    : reconcile ? reconcile(state.value, source, state.source) : source
  if (state.source !== source) setState({ source, value })
  const setValue: Dispatch<SetStateAction<T>> = (update) => {
    setState((current) => {
      const previous = current.source === source
        ? current.value
        : reconcile ? reconcile(current.value, source, current.source) : source
      return {
        source,
        value: typeof update === "function"
          ? (update as (previous: T) => T)(previous)
          : update,
      }
    })
  }
  return [value, setValue]
}
