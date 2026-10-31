/** Keep the existing boot frame visible until React has committed a ready page. */
export function prepareBoot(root: HTMLElement) {
  let snapshot: HTMLDivElement | undefined
  let finished = false

  if (root.hasAttribute("data-boot-snapshot")) {
    snapshot = document.createElement("div")
    snapshot.id = "app-boot-snapshot"
    snapshot.setAttribute("aria-hidden", "true")
    snapshot.setAttribute("inert", "")
    snapshot.append(...Array.from(root.childNodes))
    root.after(snapshot)
  }

  root.setAttribute("data-boot-pending", "")
  root.setAttribute("inert", "")

  return ({ immediate = false }: { immediate?: boolean } = {}) => {
    if (finished) return
    finished = true
    root.removeAttribute("inert")
    root.removeAttribute("data-boot-pending")
    root.removeAttribute("data-boot-snapshot")
    snapshot?.remove()
    snapshot = undefined

    const cleanup = () => {
      root.removeAttribute("data-first-load")
      document.documentElement.removeAttribute("data-app-ready")
      document.getElementById("app-entry-cover")?.remove()
    }
    if (!root.hasAttribute("data-first-load") || immediate || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) {
      cleanup()
      return
    }

    // Start the entrance only after the complete first page is in the DOM.
    window.requestAnimationFrame(() => {
      window.requestAnimationFrame(() => {
        document.documentElement.setAttribute("data-app-ready", "")
        window.setTimeout(cleanup, 520)
      })
    })
  }
}
