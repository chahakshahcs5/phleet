import {
  createContext,
  useContext,
  useLayoutEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'

export type Theme = 'light' | 'dark' | 'system'
type Resolved = 'light' | 'dark'

export interface Palette {
  primary: string
  secondary: string
  preset: string
}

const STORAGE_KEY = 'phleet-theme'
const PALETTE_KEY = 'phleet-palette'

export const DEFAULT_PALETTE: Palette = {
  primary: '#047857',
  secondary: '#14b8a6',
  preset: 'mint-emerald',
}

const HEX = /^#(?:[0-9a-f]{3}|[0-9a-f]{6})$/i

function normalizeHex(value: string): string {
  let v = value.trim().toLowerCase()
  if (!v.startsWith('#')) v = '#' + v
  if (v.length === 4) {
    v = '#' + v[1] + v[1] + v[2] + v[2] + v[3] + v[3]
  }
  return v
}

export function isHexColor(value: string): boolean {
  return HEX.test(value.trim())
}

function loadPalette(): Palette {
  try {
    const raw = localStorage.getItem(PALETTE_KEY)
    if (!raw) return DEFAULT_PALETTE
    const parsed = JSON.parse(raw) as Partial<Palette>
    if (
      typeof parsed.primary === 'string' &&
      isHexColor(parsed.primary) &&
      typeof parsed.secondary === 'string' &&
      isHexColor(parsed.secondary)
    ) {
      return {
        primary: normalizeHex(parsed.primary),
        secondary: normalizeHex(parsed.secondary),
        preset: typeof parsed.preset === 'string' ? parsed.preset : 'custom',
      }
    }
  } catch {
    /* storage disabled or malformed */
  }
  return DEFAULT_PALETTE
}

function channel(v: number): number {
  const c = v / 255
  return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}

function luminance(hex: string): number {
  const n = normalizeHex(hex).slice(1)
  const r = parseInt(n.slice(0, 2), 16)
  const g = parseInt(n.slice(2, 4), 16)
  const b = parseInt(n.slice(4, 6), 16)
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
}

function contrast(a: string, b: string): number {
  const l1 = luminance(a)
  const l2 = luminance(b)
  const light = Math.max(l1, l2)
  const dark = Math.min(l1, l2)
  return (light + 0.05) / (dark + 0.05)
}

function foregroundFor(hex: string): string {
  return contrast(hex, '#ffffff') >= contrast(hex, '#09090b') ? '#ffffff' : '#09090b'
}

function mix(color: string, percent: number, other: 'black' | 'white'): string {
  return `color-mix(in oklab, ${color} ${percent}%, ${other})`
}

function applyPalette(palette: Palette, resolved: Resolved) {
  const root = document.documentElement
  const mint = palette.preset === 'mint-emerald'

  if (mint) {
    // Preserve phleet's original emerald look for the default palette:
    // light = dark emerald with white text, dark = bright emerald with dark text.
    if (resolved === 'light') {
      root.style.setProperty('--accent', '#047857')
      root.style.setProperty('--accent-foreground', '#ffffff')
      root.style.setProperty('--color-emerald-200', '#064e3b')
      root.style.setProperty('--color-emerald-300', '#065f46')
      root.style.setProperty('--color-emerald-400', '#047857')
      root.style.setProperty('--color-emerald-500', '#10b981')
      root.style.setProperty('--color-emerald-900', '#064e3b')
    } else {
      root.style.setProperty('--accent', '#34d399')
      root.style.setProperty('--accent-foreground', '#09090b')
      root.style.setProperty('--color-emerald-200', '#a7f3d0')
      root.style.setProperty('--color-emerald-300', '#6ee7b7')
      root.style.setProperty('--color-emerald-400', '#34d399')
      root.style.setProperty('--color-emerald-500', '#10b981')
      root.style.setProperty('--color-emerald-900', '#064e3b')
    }
  } else {
    root.style.setProperty('--accent', palette.primary)
    root.style.setProperty('--accent-foreground', foregroundFor(palette.primary))
    if (resolved === 'light') {
      root.style.setProperty('--color-emerald-200', mix(palette.primary, 48, 'black'))
      root.style.setProperty('--color-emerald-300', mix(palette.primary, 58, 'black'))
      root.style.setProperty('--color-emerald-400', palette.primary)
      root.style.setProperty('--color-emerald-500', mix(palette.primary, 82, 'black'))
      root.style.setProperty('--color-emerald-900', mix(palette.primary, 26, 'black'))
    } else {
      root.style.setProperty('--color-emerald-200', mix(palette.primary, 65, 'white'))
      root.style.setProperty('--color-emerald-300', mix(palette.primary, 82, 'white'))
      root.style.setProperty('--color-emerald-400', palette.primary)
      root.style.setProperty('--color-emerald-500', mix(palette.primary, 82, 'black'))
      root.style.setProperty('--color-emerald-900', mix(palette.primary, 25, 'black'))
    }
  }

  root.style.setProperty('--accent-2', palette.secondary)
  root.style.setProperty('--accent-2-foreground', foregroundFor(palette.secondary))
}

interface ThemeContextValue {
  theme: Theme
  resolved: Resolved
  setTheme: (theme: Theme) => void
  palette: Palette
  setPrimary: (hex: string) => void
  setSecondary: (hex: string) => void
  applyPalette: (palette: Palette) => void
  resetPalette: () => void
}

const ThemeContext = createContext<ThemeContextValue>({
  theme: 'light',
  resolved: 'light',
  setTheme: () => {},
  palette: DEFAULT_PALETTE,
  setPrimary: () => {},
  setSecondary: () => {},
  applyPalette: () => {},
  resetPalette: () => {},
})

function systemPrefersDark(): boolean {
  return typeof window !== 'undefined' && window.matchMedia('(prefers-color-scheme: dark)').matches
}

function initialTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored
  } catch {
    /* storage disabled */
  }
  return 'light'
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<Theme>(initialTheme)
  const [systemDark, setSystemDark] = useState<boolean>(() => systemPrefersDark())
  const [palette, setPalette] = useState<Palette>(loadPalette)

  const resolved: Resolved = theme === 'system' ? (systemDark ? 'dark' : 'light') : theme

  useLayoutEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => setSystemDark(media.matches)
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [])

  useLayoutEffect(() => {
    document.documentElement.dataset.theme = resolved
    document.documentElement.style.colorScheme = resolved
    const meta = document.querySelector('meta[name="theme-color"]')
    if (meta) meta.setAttribute('content', resolved === 'light' ? '#f6f7f8' : '#08080a')
    try {
      localStorage.setItem(STORAGE_KEY, theme)
    } catch {
      /* storage disabled */
    }
  }, [theme, resolved])

  useLayoutEffect(() => {
    applyPalette(palette, resolved)
    try {
      localStorage.setItem(PALETTE_KEY, JSON.stringify(palette))
    } catch {
      /* storage disabled */
    }
  }, [palette, resolved])

  const idSetter = (key: 'primary' | 'secondary') => (hex: string) => {
    if (!isHexColor(hex)) return
    setPalette((prev) => ({ ...prev, [key]: normalizeHex(hex), preset: 'custom' }))
  }

  const value = useMemo<ThemeContextValue>(
    () => ({
      theme,
      resolved,
      setTheme,
      palette,
      setPrimary: idSetter('primary'),
      setSecondary: idSetter('secondary'),
      applyPalette: (next) =>
        setPalette({
          primary: normalizeHex(next.primary),
          secondary: normalizeHex(next.secondary),
          preset: next.preset || 'custom',
        }),
      resetPalette: () => setPalette({ ...DEFAULT_PALETTE }),
    }),
    [theme, resolved, palette],
  )

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  return useContext(ThemeContext)
}
