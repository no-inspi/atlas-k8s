import { describe, expect, it } from 'vitest'
import { route, service, volume } from './fixtures'
import { gatesOf, readyCount, routeBroken, routesTo, servicesOfPod, volumesOfPod } from './net'

const missing = route({ name: 'admin', source: 'IngressRoute', group: 'traefik.io', gate: 'traefik',
  rules: [{ match: 'Host(`admin`)', backend: { namespace: 'production', service: 'ghost', kind: 'Service', state: 'missing' } }] })

describe('portes', () => {
  it('regroupe les routes par porte, triées, et compte les routes cassées', () => {
    const gates = gatesOf([missing, route(), route({ name: 'b', gate: 'nginx' })])
    expect(gates.map((g) => [g.name, g.routes.length, g.broken])).toEqual([['nginx', 2, 0], ['traefik', 1, 1]])
    expect(gates[0].routes.map((r) => r.name)).toEqual(['b', 'storefront'])
    expect(routeBroken(missing)).toBe(true)
  })
})

describe('relations', () => {
  it('relie Services, routes, pods et volumes', () => {
    expect(routesTo([route(), missing], service())).toHaveLength(1)
    expect(servicesOfPod([service(), service({ name: 'x', endpoints: [] })], 'u1').map((s) => s.name)).toEqual(['api'])
    expect(volumesOfPod([volume(), volume({ name: 'd1', pods: [] })], 'u1').map((v) => v.name)).toEqual(['data-0'])
    expect(readyCount(service({ endpoints: [{ podUID: 'a', ready: true }, { podUID: 'b', ready: false }] }))).toBe(1)
  })
})
