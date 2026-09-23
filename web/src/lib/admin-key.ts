// The admin key is the only credential for the console and the management API;
// the client keys issued from the panel never open this page.
//
// It is kept in sessionStorage by default (gone when the tab closes) and only
// written to localStorage when the operator ticks "remember this device" at
// login.
const ADMIN_KEY_STORAGE = "cline-pass-switcher-admin-key"

export function readPersistentAdminKey(): string {
  try {
    return localStorage.getItem(ADMIN_KEY_STORAGE) ?? ""
  } catch {
    return ""
  }
}

export function readAdminKey(): string {
  try {
    return sessionStorage.getItem(ADMIN_KEY_STORAGE) ?? readPersistentAdminKey()
  } catch {
    return readPersistentAdminKey()
  }
}

export function storeAdminKey(key: string, remember: boolean) {
  try {
    if (key) sessionStorage.setItem(ADMIN_KEY_STORAGE, key)
    else sessionStorage.removeItem(ADMIN_KEY_STORAGE)
  } catch {
    // Storage can be unavailable in restricted browser contexts.
  }
  try {
    if (key && remember) localStorage.setItem(ADMIN_KEY_STORAGE, key)
    else localStorage.removeItem(ADMIN_KEY_STORAGE)
  } catch {
    // Storage can be unavailable in restricted browser contexts.
  }
}
