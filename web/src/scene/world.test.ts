import { describe, expect, it } from 'vitest'
import { World } from './world'
import { BLOCK_MIN, DEFAULT_VIEW, type PodView } from './podView'
import { gateway, node, pod, pv, route, service, volume } from '../store/fixtures'

const state = (version: number, nodes = [node()], pods = [pod()]) => ({
  version,
  nodes: new Map(nodes.map((n) => [n.name, n])),
  pods: new Map(pods.map((p) => [p.uid, p])),
  namespaces: new Map(),
})

describe('World', () => {
  it('ne recalcule que si la version change', () => {
    const w = new World()
    expect(w.update(state(1))).toBe(true)
    expect(w.update(state(1))).toBe(false)
  })

  it('place les pods sur leur parcelle et les pods sans node dans la file', () => {
    const w = new World()
    w.update(state(1, [node()], [pod({ uid: 'a' }), pod({ uid: 'b', nodeName: '', displayStatus: 'Pending' })]))
    expect(w.targets.get('a')?.onNode).toBe(true)
    expect(w.targets.get('b')?.onNode).toBe(false)
    expect(w.targets.get('b')!.z).toBeGreaterThan(w.targets.get('a')!.z)
  })

  it('crée des bâtiments anonymes pour les nodes que l’utilisateur ne peut pas lister', () => {
    const w = new World()
    w.update(state(1, [], [pod({ uid: 'a', nodeName: 'secret-node' }), pod({ uid: 'b', nodeName: '', displayStatus: 'Pending' })]))
    expect(w.nodes.map((n) => [n.name, n.ghost])).toEqual([['secret-node', true]])
    expect(w.targets.get('a')?.onNode).toBe(true)
    expect(w.targets.get('b')?.onNode).toBe(false)
  })
})

describe('réglages d’affichage', () => {
  const pods = [
    pod({ uid: 'small', name: 'small', requests: { cpu: 100, memory: 64 << 20 } }),
    pod({ uid: 'big', name: 'big', requests: { cpu: 50, memory: 2 * 2 ** 30 } }),
    pod({ uid: 'dns', name: 'coredns', namespace: 'kube-system', requests: { cpu: 900, memory: 70 << 20 } }),
    pod({ uid: 'ds', name: 'exporter', owner: { kind: 'DaemonSet', name: 'node-exporter' }, requests: { cpu: 0, memory: 0 } }),
  ]
  const withView = (podView: PodView, nsFilter: string | null = null) => ({ ...state(1, [node()], pods), podView, nsFilter })
  // Ordre de remplissage des places : z (rangée) puis x (colonne).
  const order = (w: World) => [...w.targets].sort(([, a], [, b]) => a.z - b.z || a.x - b.x).map(([uid]) => uid)

  it('range les plus gros pods en premier et règle la hauteur sur la ressource triée', () => {
    const w = new World()
    w.update(withView({ sort: 'memory', hideSystem: false, hiddenKinds: [] }))
    expect(order(w)).toEqual(['big', 'dns', 'small', 'ds'])
    expect(w.targets.get('big')!.h).toBeGreaterThan(w.targets.get('small')!.h)
    expect(w.targets.get('ds')!.h).toBe(BLOCK_MIN)
    w.update(withView({ sort: 'cpu', hideSystem: false, hiddenKinds: [] }))
    expect(order(w)).toEqual(['dns', 'small', 'big', 'ds'])
    expect(w.targets.get('dns')!.h).toBeGreaterThan(w.targets.get('big')!.h)
  })

  it('masque les namespaces système, sauf celui choisi dans la légende', () => {
    const w = new World()
    w.update(withView({ sort: 'name', hideSystem: true, hiddenKinds: [] }))
    expect(w.targets.has('dns')).toBe(false)
    expect(w.filtered).toBe(1)
    w.update(withView({ sort: 'name', hideSystem: true, hiddenKinds: [] }, 'kube-system'))
    expect(w.targets.has('dns')).toBe(true)
  })

  it('ne recalcule pas quand le namespace choisi ne change aucune visibilité', () => {
    const w = new World()
    const shown: PodView = { sort: 'name', hideSystem: false, hiddenKinds: [] }
    w.update(withView(shown))
    expect(w.update(withView(shown, 'production'))).toBe(false)
    w.update(withView({ ...shown, hideSystem: true }))
    expect(w.update(withView({ ...shown, hideSystem: true }, 'kube-system'))).toBe(true)
  })

  it('masque les types de workload choisis', () => {
    const w = new World()
    w.update(withView({ sort: 'name', hideSystem: false, hiddenKinds: ['DaemonSet'] }))
    expect(w.targets.has('ds')).toBe(false)
    expect(w.pods.map((p) => p.uid).sort()).toEqual(['big', 'dns', 'small'])
  })

  it('garde le node d’un pod masqué', () => {
    const w = new World()
    w.update({ ...state(1, [], [pods[2]]), podView: { sort: 'name', hideSystem: true, hiddenKinds: [] } })
    expect(w.nodes).toHaveLength(1)
    expect(w.targets.size).toBe(0)
  })
})

describe('regroupement au-delà de 48 pods', () => {
  const many = (n: number, owners: string[]) =>
    Array.from({ length: n }, (_, i) => pod({
      uid: `p${i}`, name: `p${i}`, owner: { kind: 'Deployment', name: owners[i % owners.length] },
      displayStatus: i === 7 ? 'CrashLoopBackOff' : 'Running',
    }))

  it('empile les pods par workload, un représentant par pile', () => {
    const w = new World()
    w.update(state(1, [node()], many(60, ['api', 'web', 'worker', 'cache', 'db'])))
    expect(w.stacks).toHaveLength(5)
    expect(w.stacks.reduce((s, x) => s + x.count, 0)).toBe(60)
    const visible = [...w.targets.values()].filter((t) => !t.hidden)
    expect(visible).toHaveLength(5)
    // Le représentant d'une pile est son pod le plus en difficulté.
    // p7 (7 mod 5 = 2 → worker) est en CrashLoopBackOff.
    const worker = w.stacks.find((s) => s.owner === 'Deployment/worker')!
    expect(worker.uid).toBe('p7')
  })

  it('ne regroupe pas en dessous du seuil', () => {
    const w = new World()
    w.update(state(1, [node()], many(30, ['api', 'web'])))
    expect(w.stacks).toHaveLength(0)
    expect([...w.targets.values()].every((t) => !t.hidden)).toBe(true)
  })
})

describe('regroupement : cas limites', () => {
  it('une pile d’un seul pod n’en est pas une', () => {
    const w = new World()
    const pods = Array.from({ length: 50 }, (_, i) => pod({ uid: `p${i}`, name: `p${i}`, owner: { kind: 'Deployment', name: i < 40 ? 'big' : `solo-${i}` } }))
    w.update(state(1, [node()], pods))
    expect(w.stacks.map((s) => s.count)).toEqual([40])
    expect([...w.targets.values()].filter((t) => !t.hidden)).toHaveLength(11)
  })

  it('regroupe par namespace quand les workloads sont trop nombreux pour la parcelle', () => {
    const w = new World()
    const pods = Array.from({ length: 120 }, (_, i) => pod({
      uid: `p${i}`, name: `p${i}`, namespace: `ns-${i % 6}`, owner: { kind: 'Deployment', name: `w-${i % 60}` },
    }))
    w.update(state(1, [node()], pods))
    expect(w.stacks).toHaveLength(6)
    expect(w.stacks.every((s) => s.owner.startsWith('ns-'))).toBe(true)
  })
})

describe('réseau et stockage', () => {
  const st = (version: number, extra: Partial<{ hideSystem: boolean }> = {}) => ({
    ...state(version, [node()], [pod({ uid: 'u1' })]),
    services: new Map([['production/api', service()], ['kube-system/kube-dns', service({ namespace: 'kube-system', name: 'kube-dns', endpoints: [] })]]),
    routes: new Map([['Ingress/production/storefront', route()]]),
    volumes: new Map([['production/data-0', volume()]]),
    podView: { ...DEFAULT_VIEW, hideSystem: extra.hideSystem ?? true },
  })

  it('dispose relais, portes et citernes, et trace les liens', () => {
    const w = new World()
    w.update(st(1))
    expect([...w.net!.relays.keys()]).toEqual(['production/api']) // kube-system masqué
    expect([...w.net!.gates.keys()]).toEqual(['nginx'])
    expect(w.net!.tanks.has('production/data-0')).toBe(true)
    expect(w.links.map((l) => l.family).sort()).toEqual(['data', 'fibre', 'main'])
    expect(w.layout!.bounds.width).toBeGreaterThan(0)
    const w2 = new World()
    w2.update(st(1, { hideSystem: false }))
    expect(w2.net!.relays.has('kube-system/kube-dns')).toBe(true)
  })

  it('estompe hors du chemin d’une sélection réseau, pas d’une sélection de pod', () => {
    const w = new World()
    w.update(st(1))
    const f = w.focusFor({ type: 'service', key: 'production/api', name: 'api' }, null, null)
    expect(f.dim).toBe(true)
    expect(f.path!.has('pod:u1')).toBe(true)
    expect(w.focusFor({ type: 'pod', key: 'u1', name: 'x' }, null, null).dim).toBe(false)
    expect(w.focusFor(null, 'gate:nginx', null).hover!.has('service:production/api')).toBe(true)
    expect(w.focusFor(null, null, null)).toEqual({ path: null, hover: null, dim: false })
  })

  it('situe un objet réseau', () => {
    const w = new World()
    w.update(st(1))
    expect(w.positionOf('service', 'production/api')).toEqual(expect.objectContaining({ x: expect.any(Number) }))
    expect(w.positionOf('route', 'Ingress/production/storefront')).toEqual(w.positionOf('gate', 'nginx'))
    expect(w.positionOf('volume', 'absent')).toBeNull()
  })

  it('ajoute les portes Gateway et les citernes vides', () => {
    const w = new World()
    w.update({
      ...st(1),
      gateways: new Map([['infra/public', gateway()], ['kube-system/sys', gateway({ namespace: 'kube-system', name: 'sys' })]]),
      persistentVolumes: new Map([['pv-1', pv()]]),
    })
    expect([...w.net!.gates.keys()]).toEqual(['infra/public', 'nginx']) // kube-system masqué
    expect(w.gates.find((g) => g.name === 'infra/public')!.gateway?.class).toBe('eg')
    expect(w.net!.orphans.map((o) => o.key)).toEqual(['pv-1'])
    expect(w.positionOf('gateway', 'infra/public')).toEqual(w.positionOf('gate', 'infra/public'))
    expect(w.positionOf('pv', 'pv-1')).toEqual(expect.objectContaining({ x: expect.any(Number) }))
    expect(w.positionOf('pv', 'absent')).toBeNull()
    const f = w.focusFor({ type: 'gateway', key: 'infra/public', name: 'public' }, null, null)
    expect(f.dim).toBe(true)
    expect(f.path!.has('gate:infra/public')).toBe(true)
    expect(w.focusFor({ type: 'pv', key: 'pv-1', name: 'pv-1' }, null, null).dim).toBe(true)
  })

  it('redispose quand un PV orphelin apparaît', () => {
    const w = new World()
    w.update(st(1))
    const before = w.net
    w.update({ ...st(2), persistentVolumes: new Map([['pv-1', pv()]]) })
    expect(w.net).not.toBe(before)
    expect(w.net!.orphans).toHaveLength(1)
  })
})
