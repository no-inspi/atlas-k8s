import { describe, expect, it } from 'vitest'
import { routeKey, serviceKey, volumeKey, type Pod } from '../api/types'
import { node, pod, route, service, volume } from '../store/fixtures'
import { layoutCity, plotGeometry } from './layout'
import { buildLinks, inDistrict, isLit, PathMemo, pathOf, podUidsOf, sameTopology, splitAtDistricts, type Link } from './links'
import { layoutNetwork, TANK_PITCH } from './netLayout'

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
    const t = net.tanks.get('production/data-db-0')!
    // D'abord entre deux rangées de citernes, puis vers la rue est : jamais à travers une citerne.
    expect(d.points[1]).toEqual([t.x, t.z - TANK_PITCH / 2])
    expect(d.points[2]).toEqual([net.eastX, t.z - TANK_PITCH / 2])
  })

  it('la rue est passe sur l’avenue', () => {
    for (const a of city.avenues) expect(a.x + a.width / 2).toBeGreaterThan(net.eastX)
  })

  it('une seule route cassée par porte, route et namespace manquant', () => {
    const twice = route({ name: 'admin2', source: 'IngressRoute', group: 'traefik.io', gate: 'traefik', rules: [
      { match: 'Host(`a`)', backend: { namespace: 'production', service: 'ghost', kind: 'Service', state: 'missing' } },
      { match: 'Host(`b`)', backend: { namespace: 'production', service: 'phantom', kind: 'Service', state: 'missing' } },
    ] })
    const ls = buildLinks({ city, net, services: [], routes: [twice], volumes: [], pods: new Map(), targets })
    expect(ls.filter((l) => l.family === 'broken')).toHaveLength(1)
  })

  it('porte le namespace du Service, du backend de la route ou du PVC', () => {
    const svc = service({ namespace: 'shop', name: 'web', endpoints: [{ podUID: 'a', ready: true }] })
    const vol = volume({ namespace: 'db', name: 'pg', pods: ['db'] })
    const n2 = layoutNetwork(city, [{ key: serviceKey(svc), namespace: 'shop', name: 'web' }], ['nginx', 'traefik'],
      [{ key: volumeKey(vol), namespace: 'db', name: 'pg', storageClass: vol.storageClass, requested: vol.requested }])
    // Route déclarée dans « edge », vers un Service de « shop » et un Service manquant de « ghosts ».
    const r = route({ namespace: 'edge', name: 'front', rules: [
      { host: 'a', path: '/', backend: { namespace: 'shop', service: 'web', port: '80', kind: 'Service', state: 'ok' } },
      { host: 'b', path: '/', backend: { namespace: 'ghosts', service: 'nope', port: '80', kind: 'Service', state: 'missing' } },
    ] })
    const ls = buildLinks({ city, net: n2, services: [svc], routes: [r], volumes: [vol], pods: new Map(pods.map((p) => [p.uid, p])), targets })
    const nsOf = (f: Link['family']) => ls.filter((l) => l.family === f).map((l) => l.ns)
    expect(nsOf('main')).toEqual(['shop'])
    expect(nsOf('broken')).toEqual(['ghosts'])
    expect(nsOf('fibre')).toEqual(['shop'])
    expect(nsOf('data')).toEqual(['db'])
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

  it('d’une route : ses Services, pas les autres routes de la même ligne', () => {
    const p = pathOf(`route:${routeKey(shop)}`, links)
    expect([...p]).toEqual(expect.arrayContaining(['gate:nginx', 'service:production/api', 'pod:a']))
    expect(p.has(`route:${routeKey(shop2)}`)).toBe(false)
  })

  it('un lien est allumé quand tous ses objets (hors routes) sont sur le chemin', () => {
    const p = pathOf('pod:a', links)
    expect(isLit(fam('main')[0], p)).toBe(true)
    expect(isLit(fam('data')[0], p)).toBe(false)
  })
})

describe('mémorisation du chemin', () => {
  // Mêmes objets reliés, tracé et état différents : une reconstruction sans changement de topologie.
  const moved = links.map((l) => ({ ...l, points: l.points.map(([x, z]) => [x + 1, z] as [number, number]), live: !l.live }))
  const fewer = links.filter((l) => !l.keys.includes('pod:b'))

  it('compare la topologie : familles et objets reliés, dans l’ordre', () => {
    expect(sameTopology(links, moved)).toBe(true)
    expect(sameTopology(links, fewer)).toBe(false)
    expect(sameTopology(links, links.map((l, i) => (i === 0 ? { ...l, family: l.family === 'data' ? 'fibre' as const : 'data' as const } : l)))).toBe(false)
    expect(sameTopology(links, links.map((l, i) => (i === 0 ? { ...l, keys: [...l.keys, 'pod:x'] } : l)))).toBe(false)
  })

  it('même sélection, mêmes liens : même objet, sans recalcul', () => {
    const memo = new PathMemo()
    const p = memo.get('gate:nginx', links)
    expect([...p].sort()).toEqual([...pathOf('gate:nginx', links)].sort())
    expect(memo.get('gate:nginx', links)).toBe(p)
    expect(memo.get('service:production/db', links)).not.toBe(p)
    expect(memo.get('gate:nginx', links)).toBe(p) // deux sélections en cache (sélection et survol)
  })

  it('liens reconstruits à l’identique : pas de recalcul ; topologie changée : recalcul', () => {
    const memo = new PathMemo()
    const p = memo.get('service:production/api', links)
    expect(memo.get('service:production/api', moved)).toBe(p)
    const q = memo.get('service:production/api', fewer)
    expect(q).not.toBe(p)
    expect(q.has('pod:b')).toBe(false)
    expect(p.has('pod:b')).toBe(true)
  })

  it('au-delà du seuil, évince les plus anciennes et garde la sélection redemandée', () => {
    const memo = new PathMemo()
    const sel = memo.get('gate:nginx', links)
    const first = memo.get('pod:h0', links)
    for (let i = 1; i < 200; i++) {
      memo.get(`pod:h${i}`, links) // survol qui balaie la ville
      expect(memo.get('gate:nginx', links)).toBe(sel) // sélection demandée à chaque frame
    }
    expect(memo.size).toBeLessThanOrEqual(64)
    expect(memo.get('pod:h0', links)).not.toBe(first) // survol ancien : évincé, recalculé
    expect(memo.get('gate:nginx', links)).toBe(sel)
  })

  it('extrait les uids des pods d’un chemin', () => {
    expect([...podUidsOf(pathOf('service:production/api', links))].sort()).toEqual(['a', 'b']) // pod en attente : pas de fibre
    expect(podUidsOf(new Set(['gate:nginx'])).size).toBe(0)
  })
})

describe('Gateway API et Traefik complet', () => {
  const canary = route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', name: 'canary',
    gate: 'infra/public', gates: ['infra/public', 'infra/internal'], rules: [
      { host: 'shop', path: '/', backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'ok', weight: 900 } },
      { host: 'shop', path: '/', backend: { namespace: 'production', service: 'db', kind: 'Service', state: 'ok', weight: 0 } },
    ] })
  const shadow = route({ source: 'IngressRoute', group: 'traefik.io', name: 'shadow', gate: 'traefik', rules: [
    { match: 'Host(`s`)', backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'ok', via: 'production/split' } },
    { match: 'Host(`s`)', backend: { namespace: 'production', service: 'db', kind: 'Service', state: 'ok', mirror: true, percent: 10, via: 'production/split' } },
  ] })
  const legacy = route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', name: 'legacy', gate: 'infra/public', rules: [
    { backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'refused' } },
    { backend: { namespace: 'production', service: 'db', kind: 'Service', state: 'refused' } },
  ] })
  const lost = route({ source: 'IngressRoute', group: 'traefik.io', name: 'lost', gate: 'traefik', rules: [
    { match: 'Host(`l`)', backend: { namespace: 'production', service: 'nowhere', kind: 'TraefikService', state: 'missing' } },
  ] })
  const gnet = layoutNetwork(city, [api, db].map((s) => ({ key: serviceKey(s), namespace: s.namespace, name: s.name })),
    ['infra/internal', 'infra/public', 'traefik'], [])
  const ls = buildLinks({ city, net: gnet, services: [api, db], routes: [canary, shadow, legacy, lost], volumes: [],
    pods: new Map(pods.map((p) => [p.uid, p])), targets })
  const of = (f: Link['family']) => ls.filter((l) => l.family === f)
  const main = (gate: string, svc: string) => of('main').find((l) => l.keys[0] === `gate:${gate}` && l.keys[1] === `service:${svc}`)

  it('trace une ligne principale depuis chaque porte de la route', () => {
    expect(of('main').filter((l) => l.keys.includes(`route:${routeKey(canary)}`)).map((l) => l.keys.slice(0, 2)).sort()).toEqual([
      ['gate:infra/internal', 'service:production/api'], ['gate:infra/internal', 'service:production/db'],
      ['gate:infra/public', 'service:production/api'], ['gate:infra/public', 'service:production/db'],
    ])
  })

  it('porte le poids et éteint les paquets d’une ligne de poids 0', () => {
    expect([main('infra/public', 'production/api')!.weight, main('infra/public', 'production/api')!.live]).toEqual([900, true])
    expect([main('infra/public', 'production/db')!.weight, main('infra/public', 'production/db')!.live]).toEqual([0, false])
    expect(main('traefik', 'production/api')!.weight).toBeUndefined()
  })

  it('dessine les miroirs à part, sans paquets', () => {
    expect(of('mirror').map((l) => [l.keys[0], l.keys[1], l.live])).toEqual([['gate:traefik', 'service:production/db', false]])
    expect(main('traefik', 'production/db')).toBeUndefined()
  })

  it('signale une route refusée une fois par porte et namespace, avec un panneau', () => {
    expect(of('refused').map((l) => l.keys)).toEqual([['gate:infra/public', `route:${routeKey(legacy)}`]])
    expect(of('refused')[0].sign).toBeDefined()
    expect(of('main').some((l) => l.keys.includes(`route:${routeKey(legacy)}`))).toBe(false)
  })

  it('une référence Traefik externe (indirect) : ni ligne, ni panneau', () => {
    const ext = route({ source: 'IngressRoute', group: 'traefik.io', name: 'dashboard', gate: 'traefik', rules: [
      { match: 'Host(`d`)', backend: { namespace: 'production', service: 'api@internal', kind: 'TraefikService', state: 'indirect' } },
    ] })
    const xs = buildLinks({ city, net: gnet, services: [api, db], routes: [ext], volumes: [],
      pods: new Map(pods.map((p) => [p.uid, p])), targets })
    expect(xs.filter((l) => l.keys.includes(`route:${routeKey(ext)}`))).toEqual([])
    expect(xs.some((l) => l.sign)).toBe(false)
  })

  it('signale aussi un TraefikService introuvable', () => {
    expect(of('broken').map((l) => l.keys)).toEqual([['gate:traefik', `route:${routeKey(lost)}`]])
  })

  it('fusionne le poids de deux routes sur la même porte et le même Service : non pondéré l’emporte', () => {
    const zero = route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', name: 'zero', gate: 'infra/public', rules: [
      { backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'ok', weight: 0 } },
    ] })
    const plain = route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', name: 'plain', gate: 'infra/public', rules: [
      { backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'ok' } },
    ] })
    const m = buildLinks({ city, net: gnet, services: [api], routes: [zero, plain], volumes: [], pods: new Map(), targets })
      .filter((l) => l.family === 'main')
    expect(m).toHaveLength(1)
    expect(m[0].weight).toBeUndefined()
    expect(m[0].live).toBe(true)
  })

  it('une route refusée à deux portes trace deux lignes refusées', () => {
    const twice = route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', name: 'twice', gate: 'infra/public',
      gates: ['infra/public', 'infra/internal'], rules: [
        { backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'refused' } },
      ] })
    const r = buildLinks({ city, net: gnet, services: [api], routes: [twice], volumes: [], pods: new Map(), targets })
      .filter((l) => l.family === 'refused')
    expect(r.map((l) => l.keys[0]).sort()).toEqual(['gate:infra/internal', 'gate:infra/public'])
  })

  it('chemin d’un Gateway : sa porte, ses routes, ses Services et leurs pods ; d’un PV : lui seul', () => {
    const p = pathOf('gateway:infra/public', ls)
    expect([...p]).toEqual(expect.arrayContaining([
      'gateway:infra/public', 'gate:infra/public', `route:${routeKey(legacy)}`, 'service:production/api', 'service:production/db', 'pod:a', 'pod:db',
    ]))
    expect(p.has('gate:traefik')).toBe(false)
    expect([...pathOf('pv:pv-1', ls)]).toEqual(['pv:pv-1'])
    expect([...pathOf('service:production/db', ls)]).toEqual(expect.arrayContaining(['gate:traefik', 'gate:infra/public', 'gate:infra/internal']))
    expect([...pathOf('pod:db', ls)]).toEqual(expect.arrayContaining(['service:production/db', 'gate:traefik']))
  })
})

describe('quartiers et liens', () => {
  const d = city.districts[0]
  const west = d.x - d.width / 2, south = d.z + d.depth / 2

  it('inDistrict : intérieur et bords compris, extérieur exclu', () => {
    expect(inDistrict(city, d.x, d.z)).toBe(true)
    expect(inDistrict(city, west, d.z)).toBe(true)
    expect(inDistrict(city, west - 0.01, d.z)).toBe(false)
  })

  it('splitAtDistricts : un point au bord quand le segment entre dans un quartier', () => {
    const pts = splitAtDistricts(city, [[d.x, south + 2], [d.x, d.z]])
    expect(pts).toHaveLength(3)
    expect(pts[1][0]).toBeCloseTo(d.x)
    expect(pts[1][1]).toBeCloseTo(south)
  })

  it('splitAtDistricts : rien à couper hors des quartiers ou dedans', () => {
    expect(splitAtDistricts(city, [[west - 5, south + 1], [west - 1, south + 1]])).toHaveLength(2)
    expect(splitAtDistricts(city, [[d.x - 0.5, d.z], [d.x + 0.5, d.z]])).toHaveLength(2)
  })
})
