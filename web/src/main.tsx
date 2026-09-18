import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { ThemeProvider } from 'next-themes'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from '@/components/ui/sonner'
import './index.css'
import App from './App.tsx'

const ROOT_SNAPSHOT_STORAGE = 'cline-pass-switcher-root-snapshot-v1'
// Must match the pre-paint script in index.html so the boot frame and React
// agree on the theme.
const THEME_STORAGE = 'cline-pass-switcher-theme'
const rootElement = document.getElementById('root')!

function saveRootSnapshot() {
  if (!rootElement.firstElementChild) return

  try {
    const snapshot = rootElement.cloneNode(true) as HTMLElement
    snapshot.querySelectorAll<HTMLInputElement>('input[type="password"]').forEach((field) => {
      field.setAttribute('value', 'x'.repeat(field.value.length))
    })
    sessionStorage.setItem(ROOT_SNAPSHOT_STORAGE, snapshot.innerHTML)
  } catch {
    // Storage can be unavailable in restricted browser contexts.
  }
}

window.addEventListener('pagehide', saveRootSnapshot)
window.addEventListener('beforeunload', saveRootSnapshot)

rootElement.removeAttribute('inert')
rootElement.removeAttribute('data-boot-snapshot')

createRoot(rootElement).render(
  <StrictMode>
    <ThemeProvider
      attribute="class"
      defaultTheme="system"
      enableSystem
        storageKey={THEME_STORAGE}
      >
      <TooltipProvider>
        <App />
        <Toaster position="top-right" />
      </TooltipProvider>
    </ThemeProvider>
  </StrictMode>,
)

if (rootElement.hasAttribute('data-first-load')) {
  window.requestAnimationFrame(() => {
    window.requestAnimationFrame(() => {
      window.setTimeout(() => {
        document.documentElement.setAttribute('data-app-ready', '')
        window.setTimeout(() => {
          rootElement.removeAttribute('data-first-load')
          document.documentElement.removeAttribute('data-app-ready')
          document.getElementById('app-entry-cover')?.remove()
        }, 520)
      }, 60)
    })
  })
}
