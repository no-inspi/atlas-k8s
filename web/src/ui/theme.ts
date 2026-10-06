// Choix du thème : système, clair ou sombre. Mémorisé dans le navigateur (simple
// confort : si le stockage est indisponible, on suit le système).

export type ThemeChoice = 'system' | 'light' | 'dark'

const KEY = 'atlas.theme'

export function loadTheme(): ThemeChoice {
  try {
    const v = localStorage.getItem(KEY)
    return v === 'light' || v === 'dark' ? v : 'system'
  } catch {
    return 'system'
  }
}

export function applyTheme(choice: ThemeChoice) {
  const root = document.documentElement
  if (choice === 'system') delete root.dataset.theme
  else root.dataset.theme = choice
  try {
    if (choice === 'system') localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, choice)
  } catch {
    // stockage indisponible : le choix vaut pour cette page seulement
  }
}

export const nextTheme = (c: ThemeChoice): ThemeChoice => (c === 'system' ? 'light' : c === 'light' ? 'dark' : 'system')
