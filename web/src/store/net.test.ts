import { describe, expect, it } from 'vitest'
import { gatewayKey } from '../api/types'
import { gateway, route, service, volume } from './fixtures'
import { gatesOf, gatesOfRoute, isGatewayGate, readyCount, routeBroken, routeRefused, routesTo, servicesOfPod, volumesOfPod } from './net'

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

describe('portes Gateway API', () => {
  const gw = gateway()
  const canary = route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', name: 'canary', gate: 'infra/public', gates: ['infra/public', 'infra/internal'] })
  const legacy = route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', name: 'legacy', gate: 'infra/public', gates: ['infra/public'],
    rules: [{ backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'refused' } }] })

  it('range une route sous chacune de ses portes et joint le Gateway visible', () => {
    const gates = gatesOf([canary, legacy, route()], new Map([[gatewayKey(gw), gw]]))
    expect(gates.map((g) => [g.name, g.routes.map((r) => r.name), g.refused, !!g.gateway])).toEqual([
      ['infra/internal', ['canary'], 0, false],
      ['infra/public', ['canary', 'legacy'], 1, true],
      ['nginx', ['storefront'], 0, false],
    ])
  })

  it('garde une porte pour un Gateway sans route', () => {
    expect(gatesOf([], new Map([[gatewayKey(gw), gw]])).map((g) => [g.name, g.routes.length])).toEqual([['infra/public', 0]])
  })

  it('se replie sur gate quand gates est vide', () => {
    const old = { ...route(), gates: [] as string[] }
    expect(gatesOfRoute(old)).toEqual(['nginx'])
    expect(gatesOf([old]).map((g) => g.name)).toEqual(['nginx'])
  })

  it('distingue refus et backend introuvable, et les portes Gateway', () => {
    expect(routeRefused(legacy)).toBe(true)
    expect(routeBroken(legacy)).toBe(false)
    expect(isGatewayGate('infra/public')).toBe(true)
    expect(isGatewayGate('nginx')).toBe(false)
    expect(isGatewayGate('(sans gateway)')).toBe(false)
  })
})
