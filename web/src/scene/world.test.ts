import { describe, expect, it } from 'vitest'
import { World } from './world'
import { node, pod } from '../store/fixtures'

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

  it('garde la place d’un pod quand un voisin disparaît', () => {
    const w = new World()
    w.update(state(1, [node()], [pod({ uid: 'a' }), pod({ uid: 'b' }), pod({ uid: 'c' })]))
    const before = w.targets.get('c')
    w.update(state(2, [node()], [pod({ uid: 'a' }), pod({ uid: 'c' })]))
    expect(w.targets.get('c')).toEqual(before)
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
