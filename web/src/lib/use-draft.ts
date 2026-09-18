import { useState, type Dispatch, type SetStateAction } from "react"

// A new server snapshot replaces the draft immediately, without an effect
// rendering stale settings for one frame. Local edits never mutate the source.
export function useDraft<T>(source: T): [T, Dispatch<SetStateAction<T>>] {
  const [state, setState] = useState({ source, value: source })
  const value = state.source === source ? state.value : source
  const setValue: Dispatch<SetStateAction<T>> = (update) => {
    setState((current) => {
      const previous = current.source === source ? current.value : source
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
