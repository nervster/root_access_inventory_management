import { useEffect, useState } from 'react'

// Light or dark: the device's setting until someone picks one with the header's toggle, then their
// pick, remembered in this browser. The `dark` class on <html> switches Tailwind's dark: styles;
// index.html sets it before the first paint, so a dark page doesn't flash white.
export type Theme = 'light' | 'dark'

const storageKey = 'plantstock.theme'
const darkQuery = '(prefers-color-scheme: dark)'

function chosen(): Theme | null {
  try {
    const value = localStorage.getItem(storageKey)
    return value === 'light' || value === 'dark' ? value : null
  } catch {
    return null // storage blocked (private mode, browser settings): follow the device
  }
}

function current(): Theme {
  return chosen() ?? (matchMedia(darkQuery).matches ? 'dark' : 'light')
}

function apply(theme: Theme) {
  document.documentElement.classList.toggle('dark', theme === 'dark')
}

/** The theme in use, and a toggle that switches to the other one and remembers it. */
export function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(current)

  // Until someone picks, follow the device when its setting changes (e.g. automatic at sunset).
  useEffect(() => {
    const media = matchMedia(darkQuery)
    const onChange = () => {
      if (chosen()) return
      const next = current()
      apply(next)
      setTheme(next)
    }
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [])

  const toggle = () => {
    const next: Theme = theme === 'dark' ? 'light' : 'dark'
    try {
      localStorage.setItem(storageKey, next)
    } catch {
      // Not remembered; it still applies until the page is reloaded.
    }
    apply(next)
    setTheme(next)
  }
  return [theme, toggle]
}
