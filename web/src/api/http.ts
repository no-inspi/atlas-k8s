// Appels REST du front : session par cookie, en-tête CSRF sur les requêtes
// mutantes, retour à la connexion quand la session a expiré.

export const CSRF_HEADER = 'X-Atlas-Request'

export function loginURL(loc: Pick<Location, 'pathname' | 'search'> = window.location): string {
  return `/auth/login?return=${encodeURIComponent(loc.pathname + loc.search)}`
}

const navigate = (url: string) => window.location.assign(url)

export async function apiFetch(path: string, init: RequestInit = {}, go: (url: string) => void = navigate): Promise<Response> {
  const method = (init.method ?? 'GET').toUpperCase()
  const headers = new Headers(init.headers)
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) headers.set(CSRF_HEADER, '1')
  const res = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (res.status === 401) {
    go(loginURL())
    throw new Error('session expirée')
  }
  return res
}
