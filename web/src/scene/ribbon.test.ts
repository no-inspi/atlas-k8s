import { describe, expect, it } from 'vitest'
import { linkAlpha, ribbon, writeAlpha } from './GroundLinks'
import type { Link } from './links'

describe('ribbon', () => {
  it('relève chaque segment selon la hauteur à son milieu', () => {
    const lift = (x: number) => (x > 2 ? 0.35 : 0)
    const r = ribbon([{ points: [[0, 0], [2, 0], [4, 0]], alpha: 1, live: true }], 0.2, 0.03, lift)
    const ys = [...r.position].filter((_, i) => i % 3 === 1).map((y) => Math.round(y * 100) / 100)
    expect(ys).toEqual([0.03, 0.03, 0.03, 0.03, 0.38, 0.38, 0.38, 0.38])
  })

  it('ne déborde pas à un changement de hauteur, mais ferme les coudes de même hauteur', () => {
    const lift = (x: number) => (x > 2 ? 0.35 : 0)
    const r = ribbon([{ points: [[0, 0], [2, 0], [4, 0]], alpha: 1, live: true }], 0.2, 0, lift)
    // Sommets 2 et 3 : fin du premier segment ; sommets 4 et 5 : début du second.
    expect(r.position[6]).toBe(2)
    expect(r.position[9]).toBe(2)
    expect(r.position[12]).toBe(2)
    expect(r.position[15]).toBe(2)
    // Extrémités de polyligne : débord conservé.
    expect(r.position[0]).toBeCloseTo(-0.1)
    expect(r.position[18]).toBeCloseTo(4.1)
    const flat = ribbon([{ points: [[0, 0], [2, 0], [4, 0]], alpha: 1, live: true }], 0.2, 0)
    expect(flat.position[6]).toBeCloseTo(2.1)
    expect(flat.position[12]).toBeCloseTo(1.9)
  })

  it('quatre sommets et deux triangles par segment, abscisse curviligne continue', () => {
    const r = ribbon([{ points: [[0, 0], [2, 0], [2, 3]], alpha: 0.5, live: true }], 0.2, 0.03)
    expect(r.position.length).toBe(2 * 4 * 3)
    expect(r.index.length).toBe(2 * 6)
    expect([...r.dist]).toEqual([0, 0, 2, 2, 2, 2, 5, 5])
    expect(r.alpha[0]).toBe(0.5)
    expect(r.live[7]).toBe(1)
    expect(r.position[1]).toBeCloseTo(0.03)
  })

  it('accepte des liens sans segment', () => {
    expect(ribbon([{ points: [[1, 1]], alpha: 1, live: false }], 0.1, 0).index.length).toBe(0)
  })

  it('écrit les coins dans l’ordre attendu', () => {
    const r = ribbon([{ points: [[0, 0], [2, 0]], alpha: 1, live: true }], 0.2, 0)
    // Segment le long de x, débord d'une demi-largeur (0,1) de chaque côté.
    expect([...r.position].map((x) => Math.round(x * 100) / 100)).toEqual([-0.1, 0, 0.1, -0.1, 0, -0.1, 2.1, 0, 0.1, 2.1, 0, -0.1])
    expect([...r.index]).toEqual([0, 1, 2, 1, 3, 2])
  })
})

describe('writeAlpha', () => {
  const items = [
    { points: [[0, 0], [1, 0], [1, 1]] as [number, number][], alpha: 1, live: true },
    { points: [[5, 5]] as [number, number][], alpha: 1, live: true }, // sans segment : aucun sommet
    { points: [[0, 0], [0, 3]] as [number, number][], alpha: 1, live: true },
  ]

  it('réécrit l’opacité de chaque polyligne sans toucher aux positions', () => {
    const r = ribbon(items, 0.1, 0)
    const before = [...r.position]
    expect(writeAlpha(r.alpha, items, (i) => (i === 2 ? 0.1 : 1))).toBe(true)
    expect([...r.alpha].map((a) => Math.round(a * 10) / 10)).toEqual([1, 1, 1, 1, 1, 1, 1, 1, 0.1, 0.1, 0.1, 0.1])
    expect([...r.position]).toEqual(before)
  })

  it('signale l’absence de changement', () => {
    const r = ribbon(items, 0.1, 0)
    expect(writeAlpha(r.alpha, items, () => 1)).toBe(false)
  })
})

describe('linkAlpha', () => {
  const l = (over: Partial<Link> = {}): Link => ({ family: 'main', points: [[0, 0], [1, 0]], keys: ['gate:nginx', 'service:production/api'], live: true, ns: 'production', ...over })
  const none = { path: null, hover: null, dim: false, podUids: null }

  it('estompe hors namespace filtré puis hors chemin', () => {
    expect(linkAlpha(l(), null, none)).toBe(1)
    expect(linkAlpha(l(), 'staging', none)).toBe(0.1)
    expect(linkAlpha(l(), null, { path: new Set(['gate:nginx']), hover: null, dim: true, podUids: null })).toBe(0.2)
    expect(linkAlpha(l(), null, { path: new Set(['gate:nginx', 'service:production/api']), hover: null, dim: true, podUids: null })).toBe(1)
  })

  it('atténue une ligne de poids 0 et les miroirs', () => {
    expect(linkAlpha(l({ weight: 0 }), null, none)).toBeCloseTo(0.35)
    expect(linkAlpha(l({ weight: 100 }), null, none)).toBe(1)
    expect(linkAlpha(l({ family: 'mirror', live: false }), null, none)).toBeCloseTo(0.6)
    expect(linkAlpha(l({ weight: 0 }), 'staging', none)).toBeCloseTo(0.035)
  })
})
