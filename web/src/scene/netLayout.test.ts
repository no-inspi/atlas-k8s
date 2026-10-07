import { describe, expect, it } from 'vitest'
import { node } from '../store/fixtures'
import { layoutCity, plotGeometry } from './layout'
import { layoutNetwork, RELAY_PITCH, tankRadius, type RelayInput, type TankInput } from './netLayout'

const city = layoutCity([node({ name: 'a-1', pool: 'p' }), node({ name: 'a-2', pool: 'p' }), node({ name: 'a-3', pool: 'p' })], plotGeometry(12))
const relay = (ns: string, name: string): RelayInput => ({ key: `${ns}/${name}`, namespace: ns, name })
const tank = (ns: string, name: string, storageClass: string, gib = 10): TankInput => ({ key: `${ns}/${name}`, namespace: ns, name, storageClass, requested: gib * 2 ** 30 })
const GiB = 2 ** 30

describe('relais', () => {
  const relays = [relay('staging', 'api'), relay('production', 'web'), relay('production', 'api')]

  it('range les namespaces par nom, les relais par nom, avec un espace entre deux namespaces', () => {
    const net = layoutNetwork(city, relays, [], [])
    const x = (k: string) => net.relays.get(k)!.x
    expect(x('production/api')).toBeLessThan(x('production/web'))
    expect(x('production/web') - x('production/api')).toBeCloseTo(RELAY_PITCH)
    expect(x('staging/api') - x('production/web')).toBeCloseTo(2 * RELAY_PITCH)
    expect(net.segments.map((s) => s.ns)).toEqual(['production', 'staging'])
    expect(net.segments[0].x1 - net.segments[0].x0).toBeCloseTo(2 * RELAY_PITCH)
  })

  it('ne dépend pas de l’ordre d’arrivée', () => {
    const a = layoutNetwork(city, relays, ['nginx', 'traefik'], [])
    const b = layoutNetwork(city, [...relays].reverse(), ['traefik', 'nginx'], [])
    expect([...a.relays.entries()].sort()).toEqual([...b.relays.entries()].sort())
    expect([...a.gates.entries()]).toEqual([...b.gates.entries()])
  })

  it('pose les relais sur l’avenue, au-delà des portes', () => {
    const net = layoutNetwork(city, relays, ['nginx'], [])
    const a = city.avenues[0]
    for (const r of net.relays.values()) {
      expect(Math.abs(r.z - a.z)).toBeLessThan(a.depth / 2)
      expect(r.x).toBeGreaterThan(net.gates.get('nginx')!.x)
      expect(r.x).toBeGreaterThan(net.westX)
    }
  })

  it('regroupe les plus gros namespaces quand l’avenue est pleine', () => {
    const many = [
      ...Array.from({ length: 60 }, (_, i) => relay('big', `s-${String(i).padStart(2, '0')}`)),
      relay('small', 'a'), relay('small', 'b'),
    ]
    const net = layoutNetwork(city, many, [], [])
    expect(net.groups.map((g) => g.ns)).toEqual(['big'])
    expect(net.groups[0].members).toHaveLength(60)
    expect(net.relays.get('big/s-00')!.group).toBe('ns:big')
    expect(net.relays.get('small/a')!.group).toBeUndefined()
    expect(net.relays.size).toBe(62)
  })
})

describe('portes', () => {
  it('se tiennent à l’entrée ouest de la première avenue, triées par nom', () => {
    const net = layoutNetwork(city, [], ['traefik', 'nginx'], [])
    const [g1, g2] = [net.gates.get('nginx')!, net.gates.get('traefik')!]
    expect(g1.x).toBe(g2.x)
    expect(g1.z).toBeLessThan(g2.z)
    expect(g1.x).toBeLessThan(net.westX)
  })
})

describe('entrepôts', () => {
  const tanks = [tank('production', 'b', 'standard-rwo'), tank('production', 'a', 'standard-rwo', 100), tank('staging', 'x', ''), tank('monitoring', 'm', 'premium-rwo', 1)]

  it('un îlot par StorageClass, à l’est de la ville', () => {
    const net = layoutNetwork(city, [], [], tanks)
    expect(net.islands.map((i) => i.storageClass)).toEqual(['(aucune)', 'premium-rwo', 'standard-rwo'])
    const right = Math.max(...city.districts.map((d) => d.x + d.width / 2))
    for (const t of net.tanks.values()) expect(t.x).toBeGreaterThan(right)
    expect(net.eastX).toBeGreaterThan(right)
    expect(net.eastX).toBeLessThan(Math.min(...[...net.tanks.values()].map((t) => t.x)))
    expect(net.tanks.get('production/a')!.x).toBeLessThan(net.tanks.get('production/b')!.x)
  })

  it('agrandit la ville pour les contenir', () => {
    const net = layoutNetwork(city, [], [], tanks)
    const b = net.bounds, w = net.warehouse!
    expect(b.x + b.width / 2).toBeGreaterThanOrEqual(w.x + w.width / 2)
    expect(layoutNetwork(city, [], [], []).warehouse).toBeNull()
  })

  it('dimensionne les citernes selon la capacité, entre deux bornes', () => {
    expect(tankRadius(1 * GiB)).toBeCloseTo(0.35)
    expect(tankRadius(8 * GiB)).toBeGreaterThan(tankRadius(1 * GiB))
    expect(tankRadius(10_000 * GiB)).toBe(0.8)
  })
})
