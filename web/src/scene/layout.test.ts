import { describe, expect, it } from 'vitest'
import { layoutCity, plotGeometry, slotCapacity, SlotAllocator, queuePosition } from './layout'
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

describe('SlotAllocator', () => {
  it('donne la première place libre et la garde', () => {
    const a = new SlotAllocator(12)
    expect(a.assign('n1', 'p1')).toBe(0)
    expect(a.assign('n1', 'p2')).toBe(1)
    expect(a.assign('n1', 'p1')).toBe(0)
  })

  it('ne déplace pas un pod quand son voisin disparaît, et réutilise la place libérée', () => {
    const a = new SlotAllocator(12)
    a.assign('n1', 'p1'); a.assign('n1', 'p2'); a.assign('n1', 'p3')
    a.sync([{ uid: 'p2', nodeName: 'n1' }, { uid: 'p3', nodeName: 'n1' }])
    expect(a.slotOf('p3')).toBe(2)
    expect(a.assign('n1', 'p4')).toBe(0)
  })

  it('déménage un pod qui change de node et renvoie -1 quand le node est plein', () => {
    const a = new SlotAllocator(1)
    a.assign('n1', 'p1')
    expect(a.assign('n1', 'p2')).toBe(-1)
    expect(a.assign('n2', 'p1')).toBe(0)
    expect(a.assign('n1', 'p2')).toBe(0)
  })

  it('libère les pods sans node', () => {
    const a = new SlotAllocator(4)
    a.assign('n1', 'p1')
    a.sync([{ uid: 'p1', nodeName: '' }])
    expect(a.slotOf('p1')).toBeUndefined()
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
