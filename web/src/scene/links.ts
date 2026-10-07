import { routeKey, serviceKey, volumeKey, type Pod, type Route, type Service, type Volume } from '../api/types'
import { ALLEY, type CityLayout } from './layout'
import { TANK_PITCH, type NetLayout } from './netLayout'

// Liens au sol, tracés en angles droits par les avenues, la rue ouest (portes),
// la rue est (entrepôts) et l'allée à l'est de chaque parcelle : jamais à
// travers un bâtiment.

export type Family = 'main' | 'broken' | 'data' | 'fibre'
export type Pt = [number, number]

export interface Link {
  family: Family
  points: Pt[]
  /** Objets reliés, « type:clé » : gate:, route:, service:, pod:, volume:. */
  keys: string[]
  /** Paquets ou gouttes qui circulent (endpoint ready, pod Running). */
  live: boolean
  /** Route cassée : position du panneau « ? ». */
  sign?: Pt
}

export interface LinkInput {
  city: CityLayout
  net: NetLayout
  services: Service[]
  routes: Route[]
  volumes: Volume[]
  pods: ReadonlyMap<string, Pod>
  targets: ReadonlyMap<string, { x: number; z: number; onNode: boolean }>
}

const dedupe = (pts: Pt[]): Pt[] =>
  pts.filter((p, i) => i === 0 || Math.abs(p[0] - pts[i - 1][0]) + Math.abs(p[1] - pts[i - 1][1]) > 1e-6)

function nearestAvenue(city: CityLayout, z: number): number {
  let best = 0
  city.avenues.forEach((a, i) => { if (Math.abs(a.z - z) < Math.abs(city.avenues[best].z - z)) best = i })
  return best
}

/** De la voie laneZ à l'allée à l'est de la parcelle, puis au pod. */
function toPod(city: CityLayout, laneZ: number, pod: { x: number; z: number }, plot: { x: number }, offset: number): Pt[] {
  const alley = plot.x + city.geometry.width / 2 + ALLEY / 2 + offset
  return [[alley, laneZ], [alley, pod.z], [pod.x, pod.z]]
}

export function buildLinks(i: LinkInput): Link[] {
  const { city, net } = i
  const out: Link[] = []
  const anchor = (uid: string) => {
    const t = i.targets.get(uid), p = i.pods.get(uid)
    if (!t || !t.onNode || !p) return null
    const plot = city.plots.get(p.nodeName)
    return plot ? { t, p, plot, avenue: nearestAvenue(city, plot.z) } : null
  }

  // Lignes principales : une par couple (porte, Service), toutes routes confondues.
  const mains = new Map<string, Link>()
  const brokenSeen = new Set<string>() // une route cassée par (porte, route, namespace manquant)
  for (const r of i.routes) {
    const gate = net.gates.get(r.gate)
    if (!gate) continue
    const rk = `route:${routeKey(r)}`
    for (const rule of r.rules) {
      const b = rule.backend
      if (b.kind !== 'Service') continue
      const sk = `${b.namespace}/${b.service}`
      if (b.state === 'missing') {
        const id = `${r.gate}|${rk}|${b.namespace}`
        if (brokenSeen.has(id)) continue
        brokenSeen.add(id)
        const seg = net.segments.find((s) => s.ns === b.namespace)
        const lane = net.lanes[seg?.avenue ?? 0]
        const x = seg ? seg.x0 : net.westX + 1.2
        out.push({ family: 'broken', keys: [`gate:${r.gate}`, rk], live: false, sign: [x, lane.main],
          points: dedupe([[gate.x + 0.3, gate.z], [net.westX, gate.z], [net.westX, lane.main], [x, lane.main]]) })
        continue
      }
      const relay = net.relays.get(sk)
      if (!relay) continue
      const id = `${r.gate}|${sk}`
      const prev = mains.get(id)
      if (prev) {
        if (!prev.keys.includes(rk)) prev.keys.push(rk)
        continue
      }
      const lane = net.lanes[relay.avenue]
      mains.set(id, { family: 'main', live: true, keys: [`gate:${r.gate}`, `service:${sk}`, rk],
        points: dedupe([[gate.x + 0.3, gate.z], [net.westX, gate.z], [net.westX, lane.main], [relay.x, lane.main], [relay.x, relay.z]]) })
    }
  }
  out.push(...mains.values())

  // Fibres : du relais à chaque pod endpoint placé sur un node.
  for (const s of i.services) {
    const relay = net.relays.get(serviceKey(s))
    if (!relay) continue
    for (const e of s.endpoints) {
      const a = anchor(e.podUID)
      if (!a) continue
      const lr = net.lanes[relay.avenue], la = net.lanes[a.avenue]
      const head: Pt[] = relay.avenue === a.avenue
        ? [[relay.x, relay.z], [relay.x, lr.fibre]]
        : [[relay.x, relay.z], [relay.x, lr.fibre], [net.westX, lr.fibre], [net.westX, la.fibre]]
      out.push({ family: 'fibre', live: e.ready, keys: [`service:${serviceKey(s)}`, `pod:${e.podUID}`],
        points: dedupe([...head, ...toPod(city, la.fibre, a.t, a.plot, -0.2)]) })
    }
  }

  // Conduites : de la citerne aux pods qui montent le PVC, par la rue est.
  for (const v of i.volumes) {
    const tank = net.tanks.get(volumeKey(v))
    if (!tank) continue
    const row = tank.z - TANK_PITCH / 2 // entre deux rangées de citernes : jamais à travers une autre
    for (const uid of v.pods) {
      const a = anchor(uid)
      if (!a) continue
      const la = net.lanes[a.avenue]
      out.push({ family: 'data', live: a.p.displayStatus === 'Running', keys: [`volume:${volumeKey(v)}`, `pod:${uid}`],
        points: dedupe([[tank.x, tank.z], [tank.x, row], [net.eastX, row], [net.eastX, la.data], ...toPod(city, la.data, a.t, a.plot, 0.2)]) })
    }
  }
  return out
}

const typeOf = (k: string) => k.slice(0, k.indexOf(':'))

/**
 * Chemin d'un objet (« type:clé ») : ce qui s'allume quand on le sélectionne.
 * Porte ou route → Services → pods → volumes (une route n'entraîne pas les
 * autres routes de ses lignes) ; Service → portes, pods →
 * volumes ; pod → Services → portes, et volumes ; volume → pods → Services.
 */
export function pathOf(sel: string, links: Link[]): Set<string> {
  const out = new Set([sel])
  const via = (from: Set<string>, fams: Family[], keep = (_k: string) => true) => {
    const added = new Set<string>()
    for (const l of links)
      if (fams.includes(l.family) && l.keys.some((k) => from.has(k)))
        for (const k of l.keys) if (keep(k) && !out.has(k)) { out.add(k); added.add(k) }
    return added
  }
  const only = (s: Set<string>, type: string) => new Set([...s].filter((k) => typeOf(k) === type))
  const self = new Set([sel])
  switch (typeOf(sel)) {
    case 'gate':
    case 'route': {
      // Une ligne principale est partagée par les routes vers un même Service :
      // d'une route, on ne prend que ses objets, pas les routes sœurs.
      const first = via(self, ['main', 'broken'], (k) => typeOf(sel) !== 'route' || typeOf(k) !== 'route')
      const svcs = only(first, 'service')
      via(only(via(svcs, ['fibre']), 'pod'), ['data'])
      break
    }
    case 'service':
      via(self, ['main'])
      via(only(via(self, ['fibre']), 'pod'), ['data'])
      break
    case 'pod':
      via(only(via(self, ['fibre']), 'service'), ['main'])
      via(self, ['data'])
      break
    case 'volume':
      via(only(via(self, ['data']), 'pod'), ['fibre'])
      break
  }
  return out
}

/** Lien allumé : tous ses objets, routes exceptées, sont sur le chemin. */
export const isLit = (l: Link, path: ReadonlySet<string>) => l.keys.every((k) => k.startsWith('route:') || path.has(k))
