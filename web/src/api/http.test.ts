import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiFetch, loginURL } from './http'

afterEach(() => vi.unstubAllGlobals())

describe('apiFetch', () => {
  it('ajoute l’en-tête CSRF aux requêtes mutantes seulement', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await apiFetch('/api/me')
    await apiFetch('/api/access-review', { method: 'POST', body: '{}' })
    expect(new Headers(fetchMock.mock.calls[0][1].headers).get('X-Atlas-Request')).toBeNull()
    expect(new Headers(fetchMock.mock.calls[1][1].headers).get('X-Atlas-Request')).toBe('1')
  })

  it('renvoie vers la connexion sur 401', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 401 })))
    const go = vi.fn()
    await expect(apiFetch('/api/me', {}, go)).rejects.toThrow()
    expect(go).toHaveBeenCalledWith(loginURL())
  })
})

describe('loginURL', () => {
  it('garde la page courante comme retour', () => {
    expect(loginURL({ pathname: '/pods/x', search: '?a=1' } as Location)).toBe('/auth/login?return=%2Fpods%2Fx%3Fa%3D1')
  })
})
