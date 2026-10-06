import { describe, expect, it } from 'vitest'
import { node, pod, workload } from '../store/fixtures'
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
