import { describe, expect, it } from 'vitest'
import { NS_PALETTE, clusterColors, nsColors } from './colors'
import { pod, service, volume } from '../store/fixtures'

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

  it('respecte la couleur imposée par l’annotation atlas.io/color', () => {
    const colors = nsColors(['production', 'staging'], new Map([['production', '#112233']]))
    expect(colors.get('production')).toBe('#112233')
    expect(NS_PALETTE).toContain(colors.get('staging'))
  })

  it('accepte plus de namespaces que de teintes', () => {
    const colors = nsColors(Array.from({ length: 20 }, (_, i) => `ns-${i}`))
    expect(colors.size).toBe(20)
  })
})

describe('clusterColors', () => {
  it('colore aussi les namespaces qui n’ont que des Services ou des volumes', () => {
    const colors = clusterColors({
      pods: new Map([['u1', pod()]]), namespaces: new Map(),
      services: new Map([['edge/lb', service({ namespace: 'edge', name: 'lb' })]]),
      volumes: new Map([['data/d', volume({ namespace: 'data', name: 'd' })]]),
    })
    expect([...colors.keys()].sort()).toEqual(['data', 'edge', 'production'])
  })
})
