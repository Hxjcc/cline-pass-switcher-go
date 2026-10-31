import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { ThemeProvider } from 'next-themes'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from '@/components/ui/sonner'
import { snapshotHtml } from '@/lib/root-snapshot'
import { prepareBoot } from '@/lib/boot'
import './index.css'
import App from './App.tsx'
import { ErrorBoundary } from './components/error-boundary'

const ROOT_SNAPSHOT_STORAGE = 'cline-pass-switcher-root-snapshot-v1'
// Must match the pre-paint script in index.html so the boot frame and React
// agree on the theme.
const THEME_STORAGE = 'cline-pass-switcher-theme'
const rootElement = document.getElementById('root')!

function saveRootSnapshot() {
  // A reload during boot must keep the last complete cached frame rather
  // than replace it with React's hidden placeholder or Suspense fallback.
  if (!rootElement.firstElementChild || rootElement.hasAttribute('data-boot-pending')) return

  try {
    sessionStorage.setItem(ROOT_SNAPSHOT_STORAGE, snapshotHtml(rootElement))
  } catch {
    // Storage can be unavailable in restricted browser contexts.
  }
}

window.addEventListener('pagehide', saveRootSnapshot)
window.addEventListener('beforeunload', saveRootSnapshot)

const finishBoot = prepareBoot(rootElement)

createRoot(rootElement).render(
  <StrictMode>
    <ErrorBoundary onError={() => finishBoot({ immediate: true })}>
    <ThemeProvider
      attribute="class"
      defaultTheme="system"
      enableSystem
        storageKey={THEME_STORAGE}
      >
      <TooltipProvider>
        <App onReady={finishBoot} />
        <Toaster
          position="top-right"
          offset={{ top: 'var(--toast-top-offset)', right: 24 }}
          mobileOffset={{ top: 'var(--toast-top-offset)', left: 16, right: 16 }}
        />
      </TooltipProvider>
    </ThemeProvider>
    </ErrorBoundary>
  </StrictMode>,
)
