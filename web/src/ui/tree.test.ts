import { describe, expect, it } from 'vitest'
import { gateway, node, pod, pv, route, service, volume, workload } from '../store/fixtures'
import { buildTree, flatten, keyAction } from './tree'

const st = {
  nodes: new Map([['n1', node({ name: 'n1' })]]),
  workloads: new Map([['Deployment/production/api', workload({ name: 'api', replicas: 2, readyReplicas: 1 })]]),
  pods: new Map([
    ['u1', pod({ uid: 'u1', name: 'api-1', nodeName: 'n1' })],
    ['u2', pod({ uid: 'u2', name: 'api-2', nodeName: 'n1' })],
    ['u3', pod({ uid: 'u3', name: 'lonely', namespace: 'staging', owner: { kind: '', name: '' }, nodeName: '' })],
  ]),
}

describe('buildTree', () => {
  it('range namespaces, workloads et pods, puis nodes et leurs pods', () => {
    const [nss, nodes] = buildTree(st)
    expect(nss.children!.map((n) => n.label)).toEqual(['production', 'staging'])
    const api = nss.children![0].children![0]
    expect(api.label).toBe('api')
    expect(api.detail).toBe('Deployment · 1/2 prêts')
    expect(api.children!.map((p) => p.label)).toEqual(['api-1', 'api-2'])
    expect(nss.children![1].children!.map((p) => p.label)).toEqual(['lonely']) // pod sans propriétaire
    expect(nodes.children![0].children).toHaveLength(2)
  })
})

describe('navigation au clavier', () => {
  const roots = buildTree(st)
  const open = new Set(['group:namespaces', 'ns:production'])
  const items = flatten(roots, open)

  it('n’affiche que les branches dépliées', () => {
    expect(items.map((i) => `${i.level}:${i.node.label}`)).toEqual(['1:Namespaces', '2:production', '3:api', '2:staging', '1:Nodes'])
  })

  it('suit le motif tree de WAI-ARIA', () => {
    expect(keyAction('ArrowDown', items, 'ns:production', open)).toEqual({ type: 'focus', id: 'wl:Deployment/production/api' })
    expect(keyAction('ArrowRight', items, 'wl:Deployment/production/api', open)).toEqual({ type: 'expand', id: 'wl:Deployment/production/api' })
    expect(keyAction('ArrowRight', items, 'ns:production', open)).toEqual({ type: 'focus', id: 'wl:Deployment/production/api' })
    expect(keyAction('ArrowLeft', items, 'ns:production', open)).toEqual({ type: 'collapse', id: 'ns:production' })
    expect(keyAction('ArrowLeft', items, 'wl:Deployment/production/api', open)).toEqual({ type: 'focus', id: 'ns:production' })
    expect(keyAction('End', items, 'ns:production', open)).toEqual({ type: 'focus', id: 'group:nodes' })
  })

  it('Entrée ouvre un pod ou un node dans l’inspecteur', () => {
    const all = flatten(roots, new Set([...open, 'wl:Deployment/production/api']))
    expect(keyAction('Enter', all, 'pod:u1', open)).toEqual({ type: 'activate', id: 'pod:u1' })
    expect(keyAction('Enter', items, 'group:nodes', open)).toEqual({ type: 'expand', id: 'group:nodes' })
  })
})

describe('réseau et stockage', () => {
  const withNet = {
    ...st,
    services: new Map([
      ['production/api', service()],
      ['production/ghost', service({ name: 'ghost', endpoints: [], health: 'down' })],
    ]),
    routes: new Map([['Ingress/production/storefront', route()]]),
    volumes: new Map([['production/data-0', volume()]]),
  }

  it('ajoute Entrées, Services et Stockage, seulement s’ils ne sont pas vides', () => {
    expect(buildTree(st).map((g) => g.label)).toEqual(['Namespaces', 'Nodes'])
    const roots = buildTree(withNet)
    expect(roots.map((g) => g.label)).toEqual(['Namespaces', 'Nodes', 'Entrées', 'Services', 'Stockage'])
    const [, , gates, services, storage] = roots
    expect(gates.children![0]).toEqual(expect.objectContaining({ label: 'nginx', select: { type: 'gate', key: 'nginx' } }))
    expect(gates.children![0].children![0].select).toEqual({ type: 'route', key: 'Ingress/production/storefront' })
    expect(services.children![0].label).toBe('production')
    expect(services.children![0].children!.map((s) => [s.label, s.status])).toEqual([['api', undefined], ['ghost', 'down']])
    expect(storage.children![0].label).toBe('standard-rwo')
    expect(storage.children![0].children![0]).toEqual(expect.objectContaining({ label: 'data-0', select: { type: 'volume', key: 'production/data-0' } }))
  })
})

describe('Gateway API et PV', () => {
  const withGw = {
    ...st,
    routes: new Map([['HTTPRoute/production/storefront', route({
      source: 'HTTPRoute', group: 'gateway.networking.k8s.io', gate: 'infra/public', gates: ['infra/public', 'infra/internal'],
    })]]),
    gateways: new Map([
      ['infra/public', gateway({ namespace: 'infra', name: 'public', programmed: 'true' })],
      ['infra/internal', gateway({ namespace: 'infra', name: 'internal', programmed: 'false' })],
      ['infra/idle', gateway({ namespace: 'infra', name: 'idle', programmed: 'true' })],
    ]),
    volumes: new Map([['production/data-0', volume()]]),
    persistentVolumes: new Map([['pv-old-uploads', pv({ name: 'pv-old-uploads', storageClass: 'standard-rwo', phase: 'Released' })]]),
  }

  it('range les Gateways parmi les entrées, même sans route, et une route sous chacune de ses portes', () => {
    const gates = buildTree(withGw).find((g) => g.label === 'Entrées')!
    expect(gates.children!.map((g) => [g.label, g.select, g.status])).toEqual([
      ['infra/idle', { type: 'gateway', key: 'infra/idle' }, undefined],
      ['infra/internal', { type: 'gateway', key: 'infra/internal' }, 'non programmé'],
      ['infra/public', { type: 'gateway', key: 'infra/public' }, undefined],
    ])
    const ids = gates.children!.flatMap((g) => (g.children ?? []).map((r) => r.id))
    expect(ids).toHaveLength(2)
    expect(new Set(ids).size).toBe(2)
    expect(gates.children![2].children![0].select).toEqual({ type: 'route', key: 'HTTPRoute/production/storefront' })
  })

  it('range les PV orphelins avec les PVC de leur classe', () => {
    const storage = buildTree(withGw).find((g) => g.label === 'Stockage')!
    expect(storage.detail).toBe('2')
    const cls = storage.children![0]
    expect(cls.detail).toBe('1 PVC · 1 PV')
    expect(cls.children!.map((n) => [n.label, n.select, n.status])).toEqual([
      ['data-0', { type: 'volume', key: 'production/data-0' }, undefined],
      ['pv-old-uploads', { type: 'pv', key: 'pv-old-uploads' }, 'Released'],
    ])
  })
})
