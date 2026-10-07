import { useEffect, useState } from 'react'

// Tokens CSS (styles.css) relus pour colorer la scène three.js.
const TOKENS = {
  bg: '--bg', ground: '--ground', road: '--road', zoneStd: '--zone-std', zoneSpot: '--zone-spot', zoneGpu: '--zone-gpu',
  queue: '--queue', platform: '--platform', ink: '--ink', muted: '--muted', accent: '--accent', ok: '--ok', warn: '--warn',
  err: '--err', tree: '--tree', house: '--house', roof: '--roof', font: '--font',
  avenue: '--avenue', warehouse: '--warehouse', fibre: '--fibre', data: '--data',
} as const

export type Theme = { [K in keyof typeof TOKENS]: string } & { dark: boolean }

export function readTheme(): Theme {
  const cs = getComputedStyle(document.documentElement)
  const t = Object.fromEntries(Object.entries(TOKENS).map(([k, v]) => [k, cs.getPropertyValue(v).trim() || '#888'])) as unknown as Theme
  const forced = document.documentElement.dataset.theme
  t.dark = forced ? forced === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches
  return t
}

/** Thème courant, mis à jour quand le système ou l'attribut data-theme change. */
export function useTheme(): Theme {
  const [theme, setTheme] = useState(readTheme)
  useEffect(() => {
    const update = () => setTheme(readTheme())
    const mq = matchMedia('(prefers-color-scheme: dark)')
    mq.addEventListener('change', update)
    const mo = new MutationObserver(update)
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    return () => {
      mq.removeEventListener('change', update)
      mo.disconnect()
    }
  }, [])
  return theme
}

export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(() => matchMedia('(prefers-reduced-motion: reduce)').matches)
  useEffect(() => {
    const mq = matchMedia('(prefers-reduced-motion: reduce)')
    const update = () => setReduced(mq.matches)
    mq.addEventListener('change', update)
    return () => mq.removeEventListener('change', update)
  }, [])
  return reduced
}
