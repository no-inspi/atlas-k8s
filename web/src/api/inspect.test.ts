import { describe, expect, it, vi } from 'vitest'
import { eventsResource, gatewayRef, logsURL, nsSeg, openLogs, pvRef, routeRef, serviceRef, volumeRef, type LogMessage } from './inspect'
import { gateway, pv, route, service, volume } from '../store/fixtures'

class FakeWS {
  static last: FakeWS
  onmessage: ((e: { data: string }) => void) | null = null
  onclose: ((e: { code: number }) => void) | null = null
  constructor(public url: string) { FakeWS.last = this }
  close() { this.onclose?.({ code: 1000 }) }
}

describe('logsURL', () => {
  it('encode le pod et les options', () => {
    expect(logsURL({ protocol: 'https:', host: 'a.b' }, 'prod', 'api-1', { container: 'api', previous: true }))
      .toBe('wss://a.b/api/namespaces/prod/pods/api-1/logs?container=api&previous=true&tailLines=500')
  })
})

describe('openLogs', () => {
  it('relaie les messages et signale une coupure inattendue', () => {
    const got: LogMessage[] = []
    openLogs('ws://x', (m) => got.push(m), { WebSocket: FakeWS as unknown as typeof WebSocket })
    FakeWS.last.onmessage?.({ data: JSON.stringify({ type: 'lines', lines: [{ text: 'a' }] }) })
    FakeWS.last.onclose?.({ code: 1006 })
    expect(got.map((m) => m.type)).toEqual(['lines', 'error'])
  })

  it('reste silencieux après « end » ou une fermeture volontaire', () => {
    const got: LogMessage[] = []
    const close = openLogs('ws://x', (m) => got.push(m), { WebSocket: FakeWS as unknown as typeof WebSocket })
    FakeWS.last.onmessage?.({ data: JSON.stringify({ type: 'end' }) })
    FakeWS.last.onclose?.({ code: 1000 })
    close()
    expect(got.map((m) => m.type)).toEqual(['end'])
  })

  it('renvoie vers la connexion si la session expire', () => {
    const login = vi.fn()
    openLogs('ws://x', () => {}, { WebSocket: FakeWS as unknown as typeof WebSocket, login })
    FakeWS.last.onclose?.({ code: 4401 })
    expect(login).toHaveBeenCalled()
  })
})

describe('références', () => {
  it('désigne Services, PVC, Ingress et IngressRoute pour le YAML et les événements', () => {
    expect(serviceRef(service())).toEqual({ group: '', version: 'v1', kind: 'Service', namespace: 'production', name: 'api' })
    expect(volumeRef(volume())).toEqual({ group: '', version: 'v1', kind: 'PersistentVolumeClaim', namespace: 'production', name: 'data-0' })
    expect(routeRef(route())).toEqual({ group: 'networking.k8s.io', version: 'v1', kind: 'Ingress', namespace: 'production', name: 'storefront' })
    expect(routeRef(route({ source: 'IngressRoute', group: 'traefik.containo.us' })))
      .toEqual(expect.objectContaining({ group: 'traefik.containo.us', version: 'v1alpha1', kind: 'IngressRoute' }))
    expect(['Pod', 'Service', 'PersistentVolumeClaim', 'Ingress', 'IngressRoute'].map(eventsResource))
      .toEqual(['pods', 'services', 'persistentvolumeclaims', 'ingresses', 'ingressroutes'])
  })
})

describe('références du jalon 9', () => {
  it('désigne les routes Gateway API et Traefik TCP/UDP, les Gateways et les PV', () => {
    expect(routeRef(route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io' })))
      .toEqual({ group: 'gateway.networking.k8s.io', version: 'v1', kind: 'HTTPRoute', namespace: 'production', name: 'storefront' })
    expect(routeRef(route({ source: 'GRPCRoute', group: 'gateway.networking.k8s.io' })).kind).toBe('GRPCRoute')
    expect(routeRef(route({ source: 'IngressRouteTCP', group: 'traefik.containo.us' })))
      .toEqual(expect.objectContaining({ group: 'traefik.containo.us', version: 'v1alpha1', kind: 'IngressRouteTCP' }))
    expect(routeRef(route({ source: 'IngressRouteUDP', group: 'traefik.io' })).kind).toBe('IngressRouteUDP')
    expect(gatewayRef(gateway({ namespace: 'infra', name: 'public' })))
      .toEqual({ group: 'gateway.networking.k8s.io', version: 'v1', kind: 'Gateway', namespace: 'infra', name: 'public' })
    expect(pvRef(pv({ name: 'pv-1' }))).toEqual({ group: '', version: 'v1', kind: 'PersistentVolume', namespace: '', name: 'pv-1' })
    expect(['Gateway', 'HTTPRoute', 'GRPCRoute', 'IngressRouteTCP', 'IngressRouteUDP', 'PersistentVolume'].map(eventsResource))
      .toEqual(['gateways', 'httproutes', 'grpcroutes', 'ingressroutetcps', 'ingressrouteudps', 'persistentvolumes'])
  })

  it('remplace un namespace vide par « _ » dans les URL (objet cluster-scoped)', () => {
    expect(nsSeg('')).toBe('_')
    expect(nsSeg('production')).toBe('production')
    expect(nsSeg('a b')).toBe('a%20b')
  })
})
