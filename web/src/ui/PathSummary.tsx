import type { Route } from '../api/types'
import { world } from '../scene/world'
import { useCluster, type ClusterState, type Selection } from '../store/cluster'
import { gatesOf } from '../store/net'
import { fmtShare } from './format'

const LABELS: [string, string, string][] = [['gate', 'porte', 'portes'], ['service', 'Service', 'Services'], ['pod', 'pod', 'pods'], ['volume', 'PVC', 'PVC']]

/** L'objet sélectionné existe-t-il encore dans le flux ? (un PV n'a pas de chemin) */
function exists(sel: NonNullable<Selection>, st: ClusterState): boolean {
  switch (sel.type) {
    case 'service': return st.services.has(sel.key)
    case 'volume': return st.volumes.has(sel.key)
    case 'route': return st.routes.has(sel.key)
    case 'gateway': return st.gateways.has(sel.key)
    case 'gate': return gatesOf(st.routes.values(), st.gateways).some((g) => g.name === sel.key)
    default: return false
  }
}

/** Répartition du trafic d'une route (parts et miroirs), un Service une seule fois ; vide sans poids. */
export function splitText(r: Pick<Route, 'rules'>): string {
  const parts = new Set<string>()
  for (const { backend: b } of r.rules) {
    if (b.mirror) parts.add(`miroir ${b.service} ${b.percent ?? 100} %`)
    else if (b.weight !== undefined) parts.add(`${b.service} ${fmtShare(b.weight)}`)
  }
  return parts.size ? `Répartition : ${[...parts].join(', ')}` : ''
}

/** Texte du résumé, vide sans sélection d'une porte, d'un Gateway, d'une route, d'un Service ou d'un PVC. */
export function summaryText(sel: Selection, st: ClusterState): string {
  if (!sel || sel.type === 'pod' || sel.type === 'node' || sel.type === 'pv' || !exists(sel, st)) return ''
  world.update(st)
  // Un Gateway est dessiné en porte : son chemin part de « gate:ns/nom ».
  const path = world.pathFor(sel.type === 'gateway' ? `gate:${sel.key}` : `${sel.type}:${sel.key}`)
  const parts = LABELS.map(([type, one, many]) => {
    let n = 0
    for (const k of path) if (k.startsWith(`${type}:`)) n++
    return n ? `${n} ${n === 1 ? one : many}` : ''
  }).filter(Boolean)
  const route = sel.type === 'route' ? st.routes.get(sel.key) : undefined
  const split = route ? splitText(route) : ''
  return `Chemin : ${parts.join(' · ') || 'aucun lien'}${split ? ` · ${split}` : ''}`
}

/**
 * Résumé du chemin allumé par la sélection d'une porte, d'un Gateway, d'une
 * route, d'un Service ou d'un PVC : ce que la ville montre, dit aussi en texte
 * (lecteurs d'écran, tests), avec la répartition du trafic d'une route pondérée.
 * La région live reste montée, seul son texte change.
 */
export function PathSummary() {
  const selection = useCluster((s) => s.selection)
  useCluster((s) => s.version)
  const text = summaryText(selection, useCluster.getState())
  return <div className="path-summary" role="status" data-testid="path-summary" data-empty={text ? undefined : ''}>{text}</div>
}
