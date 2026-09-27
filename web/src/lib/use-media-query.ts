import { useEffect, useState } from "react"

/**
 * Tracks a CSS media query. The console swaps a wide table for a stacked card
 * list on phones, and rendering only one of them keeps the DOM (and the tests)
 * free of duplicate rows.
 */
export function useMediaQuery(query: string) {
  const [matches, setMatches] = useState(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return false
    return window.matchMedia(query).matches
  })

  useEffect(() => {
    if (typeof window.matchMedia !== "function") return
    const list = window.matchMedia(query)
    const update = () => setMatches(list.matches)
    update()
    list.addEventListener("change", update)
    return () => list.removeEventListener("change", update)
  }, [query])

  return matches
}

/** Phones and other narrow viewports where the tables stop fitting. */
export function useNarrowViewport() {
  return useMediaQuery("(max-width: 639px)")
}
