import { describe, expect, it } from 'vitest'
import { routeKey, serviceKey, volumeKey, type Pod } from '../api/types'
import { node, pod, route, service, volume } from '../store/fixtures'
import { layoutCity, plotGeometry } from './layout'
import { buildLinks, isLit, pathOf, type Link } from './links'
import { layoutNetwork } from './netLayout'

const nodes = [node({ name: 'n1', pool: 'p' }), node({ name: 'n2', pool: 'p' })]
const city = layoutCity(nodes, plotGeometry(12))
const pods: Pod[] = [
  pod({ uid: 'a', nodeName: 'n1' }), pod({ uid: 'b', nodeName: 'n2' }),
  pod({ uid: 'pending', nodeName: '', displayStatus: 'Pending' }),
  pod({ uid: 'db', name: 'db-0', nodeName: 'n2', displayStatus: 'Running' }),
]
const targets = new Map(pods.map((p) => {
  const plot = city.plots.get(p.nodeName)
  return [p.uid, plot ? { x: plot.x, z: plot.z + 1, onNode: true } : { x: 0, z: 40, onNode: false }]
}))
const api = service({ endpoints: [{ podUID: 'a', ready: true }, { podUID: 'b', ready: false }, { podUID: 'pending', ready: false }] })
const db = service({ name: 'db', headless: true, endpoints: [{ podUID: 'db', ready: true }] })
const shop = route()
const shop2 = route({ name: 'storefront-v2' })
const broken = route({ name: 'admin', source: 'IngressRoute', group: 'traefik.io', gate: 'traefik',
  rules: [{ match: 'Host(`admin`)', backend: { namespace: 'production', service: 'ghost', kind: 'Service', state: 'missing' } }] })
const data = volume({ name: 'data-db-0', pods: ['db'] })

const net = layoutNetwork(city, [api, db].map((s) => ({ key: serviceKey(s), namespace: s.namespace, name: s.name })), ['nginx', 'traefik'],
  [{ key: volumeKey(data), namespace: data.namespace, name: data.name, storageClass: data.storageClass, requested: data.requested }])
const links = buildLinks({ city, net, services: [api, db], routes: [shop, shop2, broken], volumes: [data],
  pods: new Map(pods.map((p) => [p.uid, p])), targets })
const fam = (f: Link['family']) => links.filter((l) => l.family === f)

describe('buildLinks', () => {
  it('une ligne principale par couple porte-Service, quelle que soit la route', () => {
    expect(fam('main')).toHaveLength(1)
    const main = fam('main')[0]
    expect(main.keys).toEqual(['gate:nginx', 'service:production/api', `route:${routeKey(shop)}`, `route:${routeKey(shop2)}`])
    const relay = net.relays.get('production/api')!
    expect(main.points[main.points.length - 1]).toEqual([relay.x, relay.z])
  })

  it('trace une route cassée jusqu’au tronçon de son namespace, avec un panneau', () => {
    const [b] = fam('broken')
    expect(b.keys).toEqual(['gate:traefik', `route:${routeKey(broken)}`])
    expect(b.sign![0]).toBeCloseTo(net.segments.find((s) => s.ns === 'production')!.x0)
  })

  it('relie chaque endpoint placé, pas les pods en attente', () => {
    const fibres = fam('fibre').filter((l) => l.keys[0] === 'service:production/api')
    expect(fibres.map((l) => [l.keys[1], l.live])).toEqual([['pod:a', true], ['pod:b', false]])
    const last = fibres[0].points[fibres[0].points.length - 1]
    expect(last).toEqual([targets.get('a')!.x, targets.get('a')!.z])
  })

  it('relie une citerne aux pods qui la montent, par la rue est', () => {
    const [d] = fam('data')
    expect(d.keys).toEqual(['volume:production/data-db-0', 'pod:db'])
    expect(d.live).toBe(true)
    expect(d.points[1][0]).toBe(net.eastX)
  })

  it('ne trace que des segments horizontaux ou verticaux', () => {
    for (const l of links)
      for (let i = 1; i < l.points.length; i++) {
        const [x0, z0] = l.points[i - 1], [x1, z1] = l.points[i]
        expect(Math.abs(x1 - x0) < 1e-9 || Math.abs(z1 - z0) < 1e-9).toBe(true)
      }
  })
})

describe('pathOf', () => {
  it('d’un Service : ses portes, ses pods et leurs volumes', () => {
    const p = pathOf('service:production/db', links)
    expect([...p].sort()).toEqual(['pod:db', 'service:production/db', 'volume:production/data-db-0'])
    expect([...pathOf('service:production/api', links)]).toEqual(expect.arrayContaining(['gate:nginx', 'pod:a', 'pod:b']))
  })

  it('d’une porte : ses Services puis leurs pods', () => {
    expect([...pathOf('gate:nginx', links)]).toEqual(expect.arrayContaining(['service:production/api', 'pod:a', `route:${routeKey(shop)}`]))
  })

  it('d’un pod : ses Services, leurs portes et ses volumes', () => {
    expect([...pathOf('pod:db', links)].sort()).toEqual(['pod:db', 'service:production/db', 'volume:production/data-db-0'])
    expect([...pathOf('pod:a', links)]).toEqual(expect.arrayContaining(['service:production/api', 'gate:nginx']))
  })

  it('un lien est allumé quand tous ses objets (hors routes) sont sur le chemin', () => {
    const p = pathOf('pod:a', links)
    expect(isLit(fam('main')[0], p)).toBe(true)
    expect(isLit(fam('data')[0], p)).toBe(false)
  })
})
