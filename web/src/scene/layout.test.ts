import { describe, expect, it } from 'vitest'
import { AVENUE, GATE_ZONE, captionStrip, drawnY, layoutCity, plotGeometry, poolCaption, slotCapacity, queuePosition, SOCLE_H } from './layout'
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

  describe('avenues', () => {
    const geo = plotGeometry(12)

    it('une seule rangée : une avenue devant, avant la file d’attente', () => {
      const c = layoutCity([n('a-1', 'p'), n('a-2', 'p')], geo)
      expect(c.avenues).toHaveLength(1)
      const a = c.avenues[0]
      const maxZ = Math.max(...[...c.plots.values()].map((p) => p.z))
      expect(a.z - AVENUE / 2).toBeGreaterThan(maxZ)
      expect(c.queue.z - c.queue.depth / 2).toBeGreaterThan(a.z + AVENUE / 2)
    })

    it('une avenue entre chaque paire de rangées, sans toucher les parcelles', () => {
      const many = Array.from({ length: 40 }, (_, i) => n(`node-${String(i).padStart(2, '0')}`, `pool-${i % 4}`))
      const c = layoutCity(many, geo)
      const rows = new Set(c.districts.map((d) => (d.z - d.depth / 2).toFixed(3))).size
      expect(rows).toBeGreaterThan(1)
      expect(c.avenues).toHaveLength(rows - 1)
      const plots = [...c.plots.values()]
      for (const a of c.avenues) {
        expect(plots.some((p) => p.z < a.z)).toBe(true)
        expect(plots.some((p) => p.z > a.z)).toBe(true)
        for (const p of plots) expect(Math.abs(p.z - a.z)).toBeGreaterThan(AVENUE / 2 + geo.depth / 2 - 1e-6)
      }
    })

    it('commence à l’ouest de la ville, où se tiennent les portes', () => {
      const c = layoutCity(nodes, geo)
      const left = Math.min(...c.districts.map((d) => d.x - d.width / 2))
      const right = Math.max(...c.districts.map((d) => d.x + d.width / 2))
      for (const a of c.avenues) expect(a.x - a.width / 2).toBeCloseTo(left - GATE_ZONE)
      // À l'est, l'avenue déborde de deux unités : la rue est des conduites y passe.
      for (const a of c.avenues) expect(a.x + a.width / 2).toBeCloseTo(right + 2)
      expect(c.bounds.x - c.bounds.width / 2).toBeLessThanOrEqual(left - GATE_ZONE + 1e-6)
    })

    it('englobe les avenues dans ses limites', () => {
      const c = layoutCity(nodes, geo)
      for (const a of c.avenues) {
        expect(c.bounds.x - c.bounds.width / 2).toBeLessThanOrEqual(a.x - a.width / 2 + 1e-6)
        expect(c.bounds.x + c.bounds.width / 2).toBeGreaterThanOrEqual(a.x + a.width / 2 - 1e-6)
      }
    })
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

describe('poolCaption', () => {
  const GI = 1 << 30
  // e2-standard-2 : capacité 2 vCPU / 7,77 Gi, allouable 1,93 vCPU / 6 Gi.
  const e2s2 = (name: string) => node({
    name, pool: 'p', instanceType: 'e2-standard-2',
    capacity: { cpu: 2000, memory: 8145248 * 1024 }, allocatable: { cpu: 1930, memory: 6 * GI },
  })
  const e2s4 = (name: string) => node({
    name, pool: 'p', instanceType: 'e2-standard-4',
    capacity: { cpu: 4000, memory: 16 * GI }, allocatable: { cpu: 3920, memory: 13000 * (1 << 20) },
  })

  it('pool homogène : type, taille nominale arrondie et total allouable', () => {
    expect(poolCaption('p', [e2s2('a'), e2s2('b'), e2s2('c')])).toEqual([
      'p · e2-standard-2', '3 × 2 vCPU / 8Gi', '5.79 vCPU / 18Gi allouables',
    ])
  })

  it('pool mixte : groupes par taille, du plus nombreux au moins nombreux', () => {
    expect(poolCaption('p', [e2s4('c'), e2s2('a'), e2s2('b')])).toEqual([
      'p · types mixtes', '2 × 2 vCPU / 8Gi + 1 × 4 vCPU / 16Gi', '7.78 vCPU / 24.7Gi allouables',
    ])
  })

  it('au-delà de 3 tailles : les 2 premières puis le reste compté', () => {
    const sized = [1, 2, 3, 4].map((c) => node({ name: `n${c}`, pool: 'p', capacity: { cpu: c * 1000, memory: 4 * GI } }))
    expect(poolCaption('p', sized)[1]).toMatch(/^1 × 1 vCPU \/ 4Gi \+ 1 × 2 vCPU \/ 4Gi \+ 2 autres$/)
  })

  it('nodes fantômes ou sans capacité : une seule ligne', () => {
    const ghost = node({ name: 'g', pool: 'nodes non visibles', instanceType: '', capacity: { cpu: 2000, memory: 8 * GI }, ghost: true })
    expect(poolCaption('nodes non visibles', [ghost])).toEqual(['nodes non visibles', '', ''])
  })

  it('fantôme et vrai node dans le même pool : seul le vrai node compte', () => {
    const ghost = node({ name: 'g', pool: 'p', capacity: { cpu: 2000, memory: 8 * GI }, allocatable: { cpu: 1930, memory: 6 * GI }, ghost: true })
    const [, l2, l3] = poolCaption('p', [ghost, e2s2('a')])
    expect(l2).toBe('1 × 2 vCPU / 8Gi')
    expect(l3).toMatch(/^1\.93 vCPU/)
  })

  it('petits totaux : vCPU toujours en cœurs, mémoire nominale jamais 0Gi', () => {
    const small = node({ name: 's', pool: 'p', capacity: { cpu: 1000, memory: 2 * GI }, allocatable: { cpu: 940, memory: 1.5 * GI } })
    expect(poolCaption('p', [small]).slice(1)).toEqual(['1 × 1 vCPU / 2Gi', '0.94 vCPU / 1.5Gi allouables'])
    const tiny = node({ name: 't', pool: 'p', capacity: { cpu: 1000, memory: 256 * (1 << 20) }, allocatable: { cpu: 900, memory: 200 * (1 << 20) } })
    expect(poolCaption('p', [tiny])[1]).toBe('1 × 1 vCPU / 1Gi')
  })

  it('type d’instance inconnu : le nom du pool seul', () => {
    expect(poolCaption('p', [node({ instanceType: '' })])[0]).toBe('p')
  })

  it('layoutCity reporte la légende sur le quartier', () => {
    const c = layoutCity([e2s2('a')], plotGeometry(12))
    expect(c.districts[0].caption[0]).toBe('p · e2-standard-2')
  })

  it('quartier étroit (1 node) : trois lignes ; large (3 nodes) : deux lignes jointes par « · »', () => {
    const geo = plotGeometry(12)
    const narrow = layoutCity([e2s2('a')], geo).districts[0]
    expect(narrow.caption).toEqual(['p · e2-standard-2', '1 × 2 vCPU / 8Gi', '1.93 vCPU / 6Gi allouables'])
    const wide = layoutCity([e2s2('a'), e2s2('b'), e2s2('c')], geo).districts[0]
    expect(wide.caption).toEqual(['p · e2-standard-2', '3 × 2 vCPU / 8Gi · 5.79 vCPU / 18Gi allouables'])
  })

  it('pool sans capacité : une seule ligne', () => {
    const ghost = node({ name: 'g', pool: 'nodes non visibles', instanceType: '', capacity: { cpu: 2000, memory: 8 * GI }, ghost: true })
    expect(layoutCity([ghost], plotGeometry(12)).districts[0].caption).toEqual(['nodes non visibles'])
  })

  it('la bande de légende d’un quartier étroit est plus profonde que celle d’un quartier large', () => {
    const geo = plotGeometry(12)
    const narrow = layoutCity([e2s2('a')], geo).districts[0]
    const wide = layoutCity([e2s2('a'), e2s2('b'), e2s2('c')], geo).districts[0]
    expect(captionStrip(3)).toBeGreaterThan(captionStrip(2))
    expect(narrow.depth - wide.depth).toBeCloseTo(captionStrip(3) - captionStrip(2))
  })
})

describe('drawnY', () => {
  const c = layoutCity([node({ name: 'a', pool: 'p' })], plotGeometry(12))
  const d = c.districts[0]

  it('dans un quartier, un pod ne descend pas sous le dessus du socle', () => {
    expect(drawnY(c, d.x, d.z, 0.02)).toBe(SOCLE_H)
    expect(drawnY(c, d.x, d.z, SOCLE_H + 0.1)).toBeCloseTo(SOCLE_H + 0.1)
  })

  it('hors quartier, la hauteur est inchangée', () => {
    expect(drawnY(c, d.x + d.width, d.z, 0.02)).toBe(0.02)
  })
})
