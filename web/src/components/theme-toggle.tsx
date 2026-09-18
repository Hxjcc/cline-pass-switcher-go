import type { MouseEvent } from "react"
import { flushSync } from "react-dom"
import { Moon, Sun } from "lucide-react"
import { useTheme } from "next-themes"

import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

const REVEAL_MS = 520
const FALLBACK_MS = 400

export function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme()
  // resolvedTheme is undefined for the very first client render; treating that
  // as light matches the boot frame, so the icon only flips when it must.
  const dark = resolvedTheme === "dark"
  const label = dark ? "切换到亮色" : "切换到暗色"

  const toggle = (event: MouseEvent<HTMLButtonElement>) => {
    const next = dark ? "light" : "dark"
    const root = document.documentElement
    const apply = () => {
      // next-themes applies the class in an effect; setting it here as well
      // guarantees the new-state snapshot (or the CSS fallback) sees the
      // new theme immediately.
      root.classList.toggle("dark", next === "dark")
      flushSync(() => setTheme(next))
    }

    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      apply()
      return
    }

    if (typeof document.startViewTransition !== "function") {
      root.classList.add("theme-transition")
      apply()
      window.setTimeout(() => root.classList.remove("theme-transition"), FALLBACK_MS)
      return
    }

    // Keyboard activation reports (0, 0); fall back to the button centre.
    const rect = event.currentTarget.getBoundingClientRect()
    const x = event.clientX || rect.left + rect.width / 2
    const y = event.clientY || rect.top + rect.height / 2
    const radius = Math.hypot(
      Math.max(x, window.innerWidth - x),
      Math.max(y, window.innerHeight - y),
    )
    const transition = document.startViewTransition(apply)
    transition.ready
      .then(() => {
        root.animate(
          {
            clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${radius}px at ${x}px ${y}px)`],
          },
          {
            duration: REVEAL_MS,
            easing: "cubic-bezier(0.4, 0, 0.2, 1)",
            pseudoElement: "::view-transition-new(root)",
          },
        )
      })
      .catch(() => {
        // The transition was skipped (e.g. another one started); the theme
        // itself has already been applied.
      })
  }

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant="outline"
            size="icon-sm"
            className="relative overflow-hidden"
            aria-label={label}
            onClick={toggle}
          />
        }
      >
        {/* The icon shows the action (moon = go dark), as before. */}
        <Moon className="rotate-0 scale-100 transition-transform duration-300 dark:-rotate-90 dark:scale-0" />
        <Sun className="absolute rotate-90 scale-0 transition-transform duration-300 dark:rotate-0 dark:scale-100" />
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}
