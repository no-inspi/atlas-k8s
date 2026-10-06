import { describe, expect, it } from 'vitest'
import { NS_PALETTE, nsColors } from './colors'

describe('nsColors', () => {
  it('donne une couleur de la palette, stable d’un appel à l’autre', () => {
    const a = nsColors(['production', 'staging'])
    const b = nsColors(['staging', 'production'])
    expect(a.get('production')).toBe(b.get('production'))
    expect(NS_PALETTE).toContain(a.get('production'))
  })

  it('évite les collisions tant que la palette suffit', () => {
    const names = Array.from({ length: 12 }, (_, i) => `ns-${i}`)
    const colors = nsColors(names)
    expect(new Set(colors.values()).size).toBe(12)
  })

  it('accepte plus de namespaces que de teintes', () => {
    const colors = nsColors(Array.from({ length: 20 }, (_, i) => `ns-${i}`))
    expect(colors.size).toBe(20)
  })
})
