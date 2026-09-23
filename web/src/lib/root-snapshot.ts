// The boot frame is raw HTML kept in sessionStorage and re-inserted before
// React mounts, so whatever it captures survives reloads in the same tab.
//
// React writes controlled values onto the value *attribute*: a
// `<input type="text" value="sk-...">` serializes with that value even though
// the value was only ever set as a property. Matching on `type="password"`
// alone therefore misses every "show key" toggle, which flips the field to
// text - so masking follows the field (data-secret), and password inputs stay
// covered as a fallback.
const SECRET_FIELD_SELECTOR = 'input[type="password"], input[data-secret]'

/** Serializes the app shell for the boot frame with secret fields masked. */
export function snapshotHtml(root: HTMLElement): string {
  const snapshot = root.cloneNode(true) as HTMLElement
  snapshot.querySelectorAll<HTMLInputElement>(SECRET_FIELD_SELECTOR).forEach((field) => {
    field.setAttribute("value", "x".repeat(field.value.length))
  })
  return snapshot.innerHTML
}
