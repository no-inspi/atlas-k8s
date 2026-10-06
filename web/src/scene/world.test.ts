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
