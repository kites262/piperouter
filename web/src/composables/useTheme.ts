import { readonly, ref } from 'vue'

/**
 * Two-state theme (dark / light) with persistence.
 *
 * The choice is stored in localStorage and reflected on <html data-theme>,
 * which the CSS token overrides in styles/main.css key off. On the very first
 * visit (no stored choice) we follow the OS `prefers-color-scheme`. An inline
 * script in index.html applies the same resolution before first paint to avoid
 * a flash; this composable is the runtime source of truth thereafter.
 */
export type Theme = 'dark' | 'light'

const STORAGE_KEY = 'piperouter-theme'

function systemTheme(): Theme {
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

function storedTheme(): Theme | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return v === 'dark' || v === 'light' ? v : null
  } catch {
    return null // localStorage can be blocked (private mode); fall back gracefully.
  }
}

function persist(theme: Theme): void {
  try {
    localStorage.setItem(STORAGE_KEY, theme)
  } catch {
    /* ignore write failures — the in-memory choice still applies this session */
  }
}

function apply(theme: Theme): void {
  document.documentElement.dataset.theme = theme
}

function resolveInitial(): Theme {
  const stored = storedTheme()
  if (stored) return stored
  // Honor whatever the FOUC script already resolved onto <html>, else the OS.
  const attr = document.documentElement.dataset.theme
  return attr === 'dark' || attr === 'light' ? attr : systemTheme()
}

// Module-level singleton so every component shares one reactive theme.
const theme = ref<Theme>(resolveInitial())
apply(theme.value)

function setTheme(next: Theme): void {
  theme.value = next
  persist(next)
  apply(next)
}

function toggle(): void {
  setTheme(theme.value === 'dark' ? 'light' : 'dark')
}

export function useTheme() {
  return { theme: readonly(theme), setTheme, toggle }
}
