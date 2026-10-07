// Recherche (/) parmi les pods, nodes, workloads, Services, routes, portes,
// Gateways, PVC et PV visibles par l'utilisateur.

import {
  routeKey, serviceKey, volumeKey, workloadKey, type Gateway, type Node, type PersistentVolume, type Pod, type Route, type Service,
  type Volume, type Workload,
} from '../api/types'
import { gatesOf, gatesOfRoute } from '../store/net'

export interface SearchResult {
  type: 'pod' | 'node' | 'workload' | 'service' | 'route' | 'volume' | 'gate' | 'gateway' | 'pv'
  key: string // uid du pod, nom du node, de la porte ou du PV, clé du workload, du Service, de la route, du volume ou du Gateway
  label: string
  detail: string
  /** Pod à sélectionner pour un workload (le premier de ses pods). */
  podUid?: string
}

const MAX_RESULTS = 20

/**
 * Correspondance insensible à la casse ; classement : nom qui commence par la
 * requête, puis contient la requête ; nodes et workloads avant les pods à
 * score égal (moins nombreux, plus souvent cherchés).
 */
export function search(query: string, st: {
  pods: ReadonlyMap<string, Pod>; nodes: ReadonlyMap<string, Node>; workloads: ReadonlyMap<string, Workload>
  services?: ReadonlyMap<string, Service>; routes?: ReadonlyMap<string, Route>; volumes?: ReadonlyMap<string, Volume>
  gateways?: ReadonlyMap<string, Gateway>; persistentVolumes?: ReadonlyMap<string, PersistentVolume>
}): SearchResult[] {
  const q = query.trim().toLowerCase()
  if (!q) return []
  const scored: [number, SearchResult][] = []
  const score = (name: string, typeRank: number) => {
    const n = name.toLowerCase()
    if (n.startsWith(q)) return typeRank
    if (n.includes(q)) return 10 + typeRank
    return -1
  }
  const podsByOwner = new Map<string, Pod>()
  for (const p of st.pods.values()) {
    const k = workloadKey({ kind: p.owner.kind, namespace: p.namespace, name: p.owner.name })
    if (!podsByOwner.has(k)) podsByOwner.set(k, p)
  }
  for (const n of st.nodes.values()) {
    const s = score(n.name, 0)
    if (s >= 0) scored.push([s, { type: 'node', key: n.name, label: n.name, detail: `Node · ${n.pool}` }])
  }
  for (const w of st.workloads.values()) {
    const s = score(w.name, 1)
    const first = podsByOwner.get(workloadKey(w))
    if (s >= 0 && first) scored.push([s, { type: 'workload', key: workloadKey(w), label: w.name, detail: `${w.kind} · ${w.namespace}`, podUid: first.uid }])
  }
  for (const p of st.pods.values()) {
    const s = score(p.name, 2)
    if (s >= 0) scored.push([s, { type: 'pod', key: p.uid, label: p.name, detail: `Pod · ${p.namespace} · ${p.displayStatus}` }])
  }
  const routes = [...(st.routes?.values() ?? [])]
  for (const g of gatesOf(routes, st.gateways)) {
    const ss = [score(g.name, 0), g.gateway ? score(g.gateway.name, 0) : -1].filter((x) => x >= 0)
    if (!ss.length) continue
    scored.push([Math.min(...ss), g.gateway
      ? { type: 'gateway', key: g.name, label: g.name, detail: `Gateway · ${g.gateway.class} · ${g.routes.length} routes` }
      : { type: 'gate', key: g.name, label: g.name, detail: `Porte · ${g.routes.length} routes` }])
  }
  for (const sv of st.services?.values() ?? []) {
    const s = score(sv.name, 1)
    if (s >= 0) scored.push([s, { type: 'service', key: serviceKey(sv), label: sv.name, detail: `Service · ${sv.namespace} · ${sv.type}` }])
  }
  for (const r of routes) {
    const ss = [r.name, ...r.rules.map((x) => x.host ?? '')].map((n) => (n ? score(n, 1) : -1)).filter((x) => x >= 0)
    if (ss.length) scored.push([Math.min(...ss), { type: 'route', key: routeKey(r), label: r.name, detail: `${r.source} · ${r.namespace} · porte ${gatesOfRoute(r).join(', ')}` }])
  }
  for (const v of st.volumes?.values() ?? []) {
    const s = score(v.name, 1)
    if (s >= 0) scored.push([s, { type: 'volume', key: volumeKey(v), label: v.name, detail: `PVC · ${v.namespace} · ${v.phase}` }])
  }
  for (const p of st.persistentVolumes?.values() ?? []) {
    const s = score(p.name, 1)
    if (s >= 0) scored.push([s, { type: 'pv', key: p.name, label: p.name, detail: `PV · ${p.phase} · ${p.storageClass || '(aucune)'}` }])
  }
  return scored
    .sort((a, b) => a[0] - b[0] || a[1].label.localeCompare(b[1].label))
    .slice(0, MAX_RESULTS)
    .map(([, r]) => r)
}
