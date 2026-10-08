// Liens profonds : /pods/{ns}/{nom}, /nodes/{nom}, /services/{ns}/{nom},
// /volumes/{ns}/{nom}, /routes/{source}/{ns}/{nom}, /gates/{nom},
// /gateways/{ns}/{nom}, /persistentvolumes/{nom}. L'URL suit la sélection (sans
// recharger la page) et une sélection partagée par lien est rétablie dès que
// l'objet arrive dans le flux. /gates/{ns}%2F{nom} désigne la porte d'un
// Gateway : visible, il s'ouvre et l'URL devient /gateways/{ns}/{nom}.

import type { RouteSource } from '../api/types'
import { useCluster, type ClusterState, type Selection, type SelectionType } from '../store/cluster'
import { gatesOf, isGatewayGate } from '../store/net'

export type Route =
  | { type: 'pod'; namespace: string; name: string }
  | { type: 'node'; name: string }
  | { type: 'service'; namespace: string; name: string }
  | { type: 'volume'; namespace: string; name: string }
  | { type: 'route'; source: RouteSource; namespace: string; name: string }
  | { type: 'gate'; name: string }
  | { type: 'gateway'; namespace: string; name: string }
  | { type: 'pv'; name: string }
  | null

const SOURCES: Record<string, RouteSource> = {
  ingress: 'Ingress', ingressroute: 'IngressRoute', ingressroutetcp: 'IngressRouteTCP', ingressrouteudp: 'IngressRouteUDP',
  httproute: 'HTTPRoute', grpcroute: 'GRPCRoute',
}

export function parseRoute(pathname: string): Route {
  let parts: string[]
  try {
    parts = pathname.split('/').filter(Boolean).map(decodeURIComponent)
  } catch {
    return null // encodage invalide (« %E0%A4 ») : pas de lien profond
  }
  const [head, ...rest] = parts
  if (head === 'pods' && rest.length === 2) return { type: 'pod', namespace: rest[0], name: rest[1] }
  if (head === 'nodes' && rest.length === 1) return { type: 'node', name: rest[0] }
  if (head === 'services' && rest.length === 2) return { type: 'service', namespace: rest[0], name: rest[1] }
  if (head === 'volumes' && rest.length === 2) return { type: 'volume', namespace: rest[0], name: rest[1] }
  if (head === 'routes' && rest.length === 3 && SOURCES[rest[0]]) return { type: 'route', source: SOURCES[rest[0]], namespace: rest[1], name: rest[2] }
  if (head === 'gates' && rest.length === 1) return { type: 'gate', name: rest[0] }
  if (head === 'gateways' && rest.length === 2) return { type: 'gateway', namespace: rest[0], name: rest[1] }
  if (head === 'persistentvolumes' && rest.length === 1) return { type: 'pv', name: rest[0] }
  return null
}

export function pathFor(r: Route): string {
  if (!r) return '/'
  const e = encodeURIComponent
  switch (r.type) {
    case 'pod': return `/pods/${e(r.namespace)}/${e(r.name)}`
    case 'node': return `/nodes/${e(r.name)}`
    case 'service': return `/services/${e(r.namespace)}/${e(r.name)}`
    case 'volume': return `/volumes/${e(r.namespace)}/${e(r.name)}`
    case 'route': return `/routes/${r.source.toLowerCase()}/${e(r.namespace)}/${e(r.name)}`
    case 'gate': return `/gates/${e(r.name)}`
    case 'gateway': return `/gateways/${e(r.namespace)}/${e(r.name)}`
    case 'pv': return `/persistentvolumes/${e(r.name)}`
  }
}

/** Route d'un Gateway désigné par sa clé « ns/nom ». */
function gatewayRoute(key: string): Route {
  const i = key.indexOf('/')
  return { type: 'gateway', namespace: key.slice(0, i), name: key.slice(i + 1) }
}

/** Sélection désignée par une route, si l'objet est déjà dans le flux. */
function selectionFor(r: NonNullable<Route>, st: ClusterState): { type: SelectionType; key: string } | null {
  switch (r.type) {
    case 'pod': {
      const p = [...st.pods.values()].find((x) => x.namespace === r.namespace && x.name === r.name)
      return p ? { type: 'pod', key: p.uid } : null
    }
    case 'node':
      return st.nodes.has(r.name) || [...st.pods.values()].some((x) => x.nodeName === r.name) ? { type: 'node', key: r.name } : null
    case 'service': {
      const key = `${r.namespace}/${r.name}`
      return st.services.has(key) ? { type: 'service', key } : null
    }
    case 'volume': {
      const key = `${r.namespace}/${r.name}`
      return st.volumes.has(key) ? { type: 'volume', key } : null
    }
    case 'route': {
      const key = `${r.source}/${r.namespace}/${r.name}`
      return st.routes.has(key) ? { type: 'route', key } : null
    }
    case 'gate':
      if (isGatewayGate(r.name) && st.gateways.has(r.name)) return { type: 'gateway', key: r.name }
      return gatesOf(st.routes.values(), st.gateways).some((g) => g.name === r.name) ? { type: 'gate', key: r.name } : null
    case 'gateway': {
      const key = `${r.namespace}/${r.name}`
      return st.gateways.has(key) ? { type: 'gateway', key } : null
    }
    case 'pv':
      return st.persistentVolumes.has(r.name) ? { type: 'pv', key: r.name } : null
  }
}

function routeFor(sel: Selection, st: ClusterState): Route {
  if (!sel) return null
  switch (sel.type) {
    case 'pod': {
      const p = st.pods.get(sel.key)
      return p ? { type: 'pod', namespace: p.namespace, name: p.name } : null
    }
    case 'node': return { type: 'node', name: sel.key }
    case 'gate': return st.gateways.has(sel.key) ? gatewayRoute(sel.key) : { type: 'gate', name: sel.key }
    case 'gateway': return gatewayRoute(sel.key)
    case 'pv': return { type: 'pv', name: sel.key }
    case 'service':
    case 'volume': {
      const [namespace, name] = sel.key.split('/')
      return { type: sel.type, namespace, name }
    }
    case 'route': {
      const [source, namespace, name] = sel.key.split('/')
      return { type: 'route', source: source as RouteSource, namespace, name }
    }
  }
}

/** Synchronise l'URL et la sélection ; renvoie la fonction de désabonnement. */
export function syncRoute(win: Pick<Window, 'location' | 'history'> = window): () => void {
  let pending = parseRoute(win.location.pathname)

  const tryRestore = () => {
    if (!pending) return
    const st = useCluster.getState()
    const sel = selectionFor(pending, st)
    if (sel) {
      pending = null
      st.select(sel)
    }
  }

  const unsubVersion = useCluster.subscribe((s) => s.version, tryRestore)
  const unsubSel = useCluster.subscribe((s) => s.selection, (sel) => {
    if (pending) {
      if (!sel) return // lien profond pas encore rétabli : on ne touche pas à l'URL
      pending = null // l'utilisateur a choisi autre chose : l'URL le suit, le lien est abandonné
    }
    const path = pathFor(routeFor(sel, useCluster.getState()))
    if (path !== win.location.pathname) win.history.replaceState(null, '', path)
  })
  tryRestore()
  return () => { unsubVersion(); unsubSel() }
}
