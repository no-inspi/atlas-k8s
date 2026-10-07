import { describe, expect, it } from 'vitest'
import { node, pod, route, service, volume, workload } from '../store/fixtures'
import { search } from './searchRank'

const st = {
  nodes: new Map([['gke-prod-api-node', node({ name: 'gke-prod-api-node' })]]),
  workloads: new Map([['Deployment/production/api', workload({ name: 'api' })]]),
  pods: new Map([
    ['u1', pod({ uid: 'u1', name: 'api-7f9-x1' })],
    ['u2', pod({ uid: 'u2', name: 'web-1', owner: { kind: 'Deployment', name: 'web' } })],
    ['u3', pod({ uid: 'u3', name: 'rapid-1', owner: { kind: 'Deployment', name: 'rapid' } })],
  ]),
}

describe('search', () => {
  it('classe les préfixes avant les sous-chaînes, workloads avant pods', () => {
    const r = search('api', st)
    expect(r.map((x) => `${x.type}:${x.label}`)).toEqual(['workload:api', 'pod:api-7f9-x1', 'node:gke-prod-api-node', 'pod:rapid-1'])
  })

  it('un workload désigne un de ses pods', () => {
    expect(search('api', st)[0].podUid).toBe('u1')
  })

  it('ignore la casse et les requêtes vides', () => {
    expect(search('WEB', st).map((x) => x.label)).toEqual(['web-1'])
    expect(search('  ', st)).toEqual([])
  })
})

describe('réseau et stockage', () => {
  const st = {
    pods: new Map(), nodes: new Map(), workloads: new Map(),
    services: new Map([['production/api', service()]]),
    routes: new Map([['Ingress/production/storefront', route()]]),
    volumes: new Map([['production/data-0', volume()]]),
  }

  it('trouve Services, routes (par nom ou par hôte), PVC et portes', () => {
    expect(search('api', st).map((r) => [r.type, r.key])).toEqual([['service', 'production/api']])
    expect(search('shop.example', st).map((r) => r.key)).toEqual(['Ingress/production/storefront'])
    expect(search('data', st)[0]).toEqual(expect.objectContaining({ type: 'volume', key: 'production/data-0' }))
    expect(search('ngi', st)[0]).toEqual(expect.objectContaining({ type: 'gate', key: 'nginx' }))
  })
})
