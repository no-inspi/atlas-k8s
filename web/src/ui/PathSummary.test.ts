import { beforeEach, describe, expect, it } from 'vitest'
import { useCluster } from '../store/cluster'
import { gateway, node, pod, pv, route, service } from '../store/fixtures'
import { splitText, summaryText } from './PathSummary'

describe('summaryText', () => {
  beforeEach(() => useCluster.getState().reset())

  it('compte les objets du chemin d’un Service', () => {
    useCluster.getState().applyMessages([{ type: 'snapshot', rev: 1, nodes: [node()], pods: [pod()], services: [service({ endpoints: [{ podUID: 'u1', ready: true }] })], routes: [route()] }])
    const st = useCluster.getState()
    expect(summaryText({ type: 'service', key: 'production/api', name: 'api' }, st)).toMatch(/^Chemin : .*1 Service/)
  })

  it('vide sans sélection réseau, ou quand l’objet a disparu du flux', () => {
    useCluster.getState().applyMessages([{ type: 'snapshot', rev: 1, nodes: [node()], pods: [pod()] }])
    const st = useCluster.getState()
    expect(summaryText(null, st)).toBe('')
    expect(summaryText({ type: 'pod', key: 'u1', name: 'x' }, st)).toBe('')
    expect(summaryText({ type: 'service', key: 'production/api', name: 'api' }, st)).toBe('')
    expect(summaryText({ type: 'gate', key: 'nginx', name: 'nginx' }, st)).toBe('')
  })
})

describe('splitText', () => {
  it('dit la répartition pondérée et les miroirs d’une route, une fois par Service', () => {
    const r = route({
      rules: [
        { host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'frontend', kind: 'Service', state: 'ok', weight: 900 } },
        { host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'frontend-canary', kind: 'Service', state: 'ok', weight: 100 } },
        { host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'audit', kind: 'Service', state: 'ok', mirror: true, percent: 10 } },
        { host: 'www.example.com', path: '/', backend: { namespace: 'production', service: 'frontend', kind: 'Service', state: 'ok', weight: 900 } },
      ],
    })
    expect(splitText(r)).toBe('Répartition : frontend 90 %, frontend-canary 10 %, miroir audit 10 %')
    expect(splitText(route())).toBe('')
  })

  it('un miroir sans pourcentage vaut 0 %', () => {
    const b = { namespace: 'production', kind: 'Service' as const, state: 'ok' as const }
    expect(splitText(route({ rules: [{ path: '/', backend: { ...b, service: 'audit', mirror: true } }] }))).toBe('Répartition : miroir audit 0 %')
  })

  it('garde un poids nul, et ne dit rien d’un miroir seul sans parts', () => {
    const b = { namespace: 'production', kind: 'Service' as const, state: 'ok' as const }
    expect(splitText(route({ rules: [
      { path: '/', backend: { ...b, service: 'a', weight: 1000 } },
      { path: '/', backend: { ...b, service: 'b', weight: 0 } },
    ] }))).toBe('Répartition : a 100 %, b 0 %')
  })
})

describe('summaryText du jalon 9', () => {
  beforeEach(() => useCluster.getState().reset())

  it('ajoute la répartition au chemin d’une route pondérée', () => {
    const r = route({
      source: 'HTTPRoute', group: 'gateway.networking.k8s.io', gate: 'infra/public', gates: ['infra/public'],
      rules: [
        { host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'ok', weight: 900 } },
        { host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'api-canary', kind: 'Service', state: 'missing', weight: 100 } },
      ],
    })
    useCluster.getState().applyMessages([{
      type: 'snapshot', rev: 1, nodes: [node()], pods: [pod()], services: [service()], routes: [r],
      gateways: [gateway({ namespace: 'infra', name: 'public' })],
    }])
    const st = useCluster.getState()
    expect(summaryText({ type: 'route', key: 'HTTPRoute/production/storefront', name: 'storefront' }, st))
      .toMatch(/^Chemin : .* · Répartition : api 90 %, api-canary 10 %$/)
    expect(summaryText({ type: 'gateway', key: 'infra/public', name: 'public' }, st)).toMatch(/^Chemin : 1 porte/)
  })

  it('vide pour un PV (aucun lien) ou un Gateway disparu', () => {
    useCluster.getState().applyMessages([{ type: 'snapshot', rev: 1, persistentVolumes: [pv({ name: 'pv-old-uploads' })] }])
    const st = useCluster.getState()
    expect(summaryText({ type: 'pv', key: 'pv-old-uploads', name: 'pv-old-uploads' }, st)).toBe('')
    expect(summaryText({ type: 'gateway', key: 'infra/gone', name: 'gone' }, st)).toBe('')
  })
})
