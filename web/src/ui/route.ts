// Liens profonds : /pods/{ns}/{nom}, /nodes/{nom}, /services/{ns}/{nom},
// /volumes/{ns}/{nom}, /routes/{ingress|ingressroute}/{ns}/{nom}, /gates/{nom}.
// L'URL suit la sélection (sans recharger la page) et une sélection partagée
// par lien est rétablie dès que l'objet arrive dans le flux.

import { useCluster, type ClusterState, type Selection, type SelectionType } from '../store/cluster'

export type Route =
  | { type: 'pod'; namespace: string; name: string }
  | { type: 'node'; name: string }
  | { type: 'service'; namespace: string; name: string }
  | { type: 'volume'; namespace: string; name: string }
  | { type: 'route'; source: 'Ingress' | 'IngressRoute'; namespace: string; name: string }
  | { type: 'gate'; name: string }
  | null

const SOURCES: Record<string, 'Ingress' | 'IngressRoute'> = { ingress: 'Ingress', ingressroute: 'IngressRoute' }

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
  }
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
      return [...st.routes.values()].some((x) => x.gate === r.name) ? { type: 'gate', key: r.name } : null
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
    case 'gate': return { type: 'gate', name: sel.key }
    case 'service':
    case 'volume': {
      const [namespace, name] = sel.key.split('/')
      return { type: sel.type, namespace, name }
    }
    case 'route': {
      const [source, namespace, name] = sel.key.split('/')
      return { type: 'route', source: source as 'Ingress' | 'IngressRoute', namespace, name }
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
