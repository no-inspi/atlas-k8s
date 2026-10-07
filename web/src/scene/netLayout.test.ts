import { describe, expect, it } from 'vitest'
import { node } from '../store/fixtures'
import { GATE_ZONE, layoutCity, plotGeometry } from './layout'
import { layoutNetwork, RELAY_PITCH, RELAY_PITCH_MIN, tankRadius, type RelayInput, type TankInput } from './netLayout'

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

describe('à l’échelle (100 nodes, 114 namespaces × 4 Services, 228 PVC)', () => {
  const big = layoutCity(Array.from({ length: 100 }, (_, i) => node({ name: `n-${String(i).padStart(3, '0')}`, pool: ['default', 'highmem', 'spot'][i % 3] })), plotGeometry(48))
  const nss = Array.from({ length: 114 }, (_, i) => `ns-${String(i).padStart(3, '0')}`)
  const relays = nss.flatMap((ns) => ['api', 'web', 'db', 'cache'].map((n) => relay(ns, n)))
  const tanks = Array.from({ length: 228 }, (_, i) => tank(nss[i % 114], `data-${i}`, 'standard-rwo'))
  const net = layoutNetwork(big, relays, ['nginx'], tanks)

  it('une ville de deux rangées, une avenue', () => {
    expect(big.avenues).toHaveLength(1)
  })

  it('ne pose jamais deux relais (ou blocs) au même endroit', () => {
    expect(net.relays.size).toBe(relays.length)
    const spots = new Map<string, string>()
    for (const r of net.relays.values()) {
      const id = r.group ?? r.key
      const at = `${r.avenue}|${r.x.toFixed(6)}`
      expect(spots.get(at) ?? id).toBe(id)
      spots.set(at, id)
    }
    for (const g of net.groups) expect(spots.get(`${g.avenue}|${g.x.toFixed(6)}`)).toBe(g.key)
  })

  it('des tronçons qui ne se chevauchent pas, dans l’avenue', () => {
    const a = big.avenues[0]
    const segs = [...net.segments].sort((p, q) => p.avenue - q.avenue || p.x0 - q.x0)
    for (let i = 1; i < segs.length; i++)
      if (segs[i].avenue === segs[i - 1].avenue) expect(segs[i].x0).toBeGreaterThanOrEqual(segs[i - 1].x1 - 1e-6)
    for (const s of segs) {
      expect(s.x0).toBeGreaterThanOrEqual(a.x - a.width / 2 + GATE_ZONE - 1e-6)
      expect(s.x1).toBeLessThanOrEqual(net.eastX)
    }
    expect(new Set(net.segments.map((s) => s.ns)).size).toBe(114)
  })

  it('un entrepôt à peu près aussi profond que la ville, sans toucher la file d’attente', () => {
    const top = Math.min(...big.districts.map((d) => d.z - d.depth / 2))
    const bottom = Math.max(...big.districts.map((d) => d.z + d.depth / 2), ...big.avenues.map((a) => a.z + a.depth / 2))
    const w = net.warehouse!
    expect(w.depth).toBeLessThanOrEqual(bottom - top + 2)
    expect(net.tanks.size).toBe(228)
    const q = big.queue
    expect(w.x - w.width / 2).toBeGreaterThan(q.x + q.width / 2)
  })
})

describe('trop de relais', () => {
  it('resserre le pas avant de regrouper', () => {
    const fits = layoutNetwork(city, [relay('a', '1'), relay('b', '1')], [], [])
    const x = (n: typeof fits, k: string) => n.relays.get(k)!.x
    expect(x(fits, 'b/1') - x(fits, 'a/1')).toBeCloseTo(2 * RELAY_PITCH)
    const avail = city.avenues[0].width - GATE_ZONE - 0.6 - 1.6
    const n = Math.floor(avail / RELAY_PITCH) + 2 // un peu trop pour le pas normal, sans regroupement
    const tight = layoutNetwork(city, Array.from({ length: n }, (_, i) => relay('a', `s-${String(i).padStart(3, '0')}`)), [], [])
    expect(tight.groups).toHaveLength(0)
    const pitch = x(tight, 'a/s-001') - x(tight, 'a/s-000')
    expect(pitch).toBeLessThan(RELAY_PITCH)
    expect(pitch).toBeGreaterThanOrEqual(RELAY_PITCH_MIN)
  })

  it('plus de namespaces que de places au pas minimal : un bloc par namespace, chacun à sa place', () => {
    const many = Array.from({ length: 200 }, (_, i) => relay(`ns-${String(i).padStart(3, '0')}`, 'svc'))
    const net = layoutNetwork(city, many, [], [])
    expect(net.relays.size).toBe(200)
    const xs = new Set([...net.relays.values()].map((r) => `${r.avenue}|${r.x.toFixed(6)}`))
    expect(xs.size).toBe(200)
  })
})

describe('ville vide', () => {
  it('rend un réseau vide, sans valeurs infinies', () => {
    const empty = layoutCity([], plotGeometry(12))
    const net = layoutNetwork(empty, [relay('a', 'b')], ['nginx'], [tank('a', 'b', 'x')])
    expect(net.relays.size + net.gates.size + net.tanks.size + net.segments.length + net.islands.length).toBe(0)
    expect(net.warehouse).toBeNull()
    for (const v of [net.westX, net.eastX, net.bounds.x, net.bounds.z, net.bounds.width, net.bounds.depth]) expect(Number.isFinite(v)).toBe(true)
  })
})

describe('portes', () => {
  it('comptent dans les limites de la ville', () => {
    const net = layoutNetwork(city, [], Array.from({ length: 12 }, (_, i) => `gate-${String(i).padStart(2, '0')}`), [])
    const b = net.bounds
    for (const g of net.gates.values()) {
      expect(g.z).toBeGreaterThanOrEqual(b.z - b.depth / 2)
      expect(g.z).toBeLessThanOrEqual(b.z + b.depth / 2)
    }
  })

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
