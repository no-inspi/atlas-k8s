import { describe, expect, it } from 'vitest'
import { layoutCity, plotGeometry, slotCapacity, queuePosition } from './layout'
import { node } from '../store/fixtures'

const n = (name: string, pool: string, extra = {}) => node({ name, pool, ...extra })

describe('layoutCity', () => {
  const nodes = [
    n('gke-b-2', 'spot-pool', { spot: true }), n('gke-a-1', 'default-pool'), n('gke-g-1', 'gpu-pool', { gpu: 1 }),
    n('gke-a-3', 'default-pool'), n('gke-a-2', 'default-pool'), n('gke-b-1', 'spot-pool', { spot: true }),
  ]

  it('ordonne les quartiers par nom de pool et devine leur type', () => {
    const c = layoutCity(nodes, plotGeometry(12))
    expect(c.districts.map((d) => d.pool)).toEqual(['default-pool', 'gpu-pool', 'spot-pool'])
    expect(c.districts.map((d) => d.style)).toEqual(['std', 'gpu', 'spot'])
  })

  it('reconnaît un pool GPU à son taint nvidia.com/gpu', () => {
    const c = layoutCity([n('gke-t-1', 'tainted', { taints: [{ key: 'nvidia.com/gpu', value: 'present', effect: 'NoSchedule' }] })], plotGeometry(12))
    expect(c.districts[0].style).toBe('gpu')
  })

  it('ne dépend pas de l’ordre d’arrivée des nodes', () => {
    const a = layoutCity(nodes, plotGeometry(12))
    const b = layoutCity([...nodes].reverse(), plotGeometry(12))
    expect([...a.plots.entries()]).toEqual([...b.plots.entries()])
  })

  it('range les nodes d’un pool par nom, de gauche à droite', () => {
    const c = layoutCity(nodes, plotGeometry(12))
    const xs = ['gke-a-1', 'gke-a-2', 'gke-a-3'].map((k) => c.plots.get(k)!.x)
    expect(xs[0]).toBeLessThan(xs[1])
    expect(xs[1]).toBeLessThan(xs[2])
  })

  it('ne superpose pas deux parcelles', () => {
    const many = Array.from({ length: 40 }, (_, i) => n(`node-${String(i).padStart(2, '0')}`, `pool-${i % 4}`))
    const geo = plotGeometry(12)
    const c = layoutCity(many, geo)
    const ps = [...c.plots.values()]
    for (let i = 0; i < ps.length; i++)
      for (let j = i + 1; j < ps.length; j++) {
        const overlap = Math.abs(ps[i].x - ps[j].x) < geo.width && Math.abs(ps[i].z - ps[j].z) < geo.depth
        expect(overlap).toBe(false)
      }
  })

  it('place la file d’attente devant la ville', () => {
    const c = layoutCity(nodes, plotGeometry(12))
    const maxZ = Math.max(...[...c.plots.values()].map((p) => p.z))
    expect(c.queue.z).toBeGreaterThan(maxZ)
  })
})

describe('slotCapacity et plotGeometry', () => {
  it('prévoit de la marge, au moins 12 et au plus 48 places', () => {
    expect(slotCapacity(3)).toBe(12)
    expect(slotCapacity(10)).toBe(16)
    expect(slotCapacity(30)).toBe(48)
    expect(slotCapacity(200)).toBe(48)
  })

  it('reproduit la parcelle du prototype pour 12 places', () => {
    const g = plotGeometry(12)
    expect(g.cols).toBe(4)
    expect(g.rows).toBe(3)
    expect(g.width).toBeCloseTo(4.1)
  })

  it('agrandit la parcelle au-delà', () => {
    expect(plotGeometry(48).width).toBeGreaterThan(plotGeometry(12).width)
  })
})

describe('queuePosition', () => {
  it('aligne les pods en attente puis passe à la ligne', () => {
    const q = { x: 0, z: 10, width: 10.5, depth: 2.6 }
    const a = queuePosition(q, 0), b = queuePosition(q, 1), far = queuePosition(q, 30)
    expect(b.x - a.x).toBeCloseTo(1.05)
    expect(a.z).toBe(b.z)
    expect(far.z).toBeGreaterThan(a.z)
  })
})
