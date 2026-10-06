// Liens profonds : /pods/{namespace}/{nom} et /nodes/{nom}. L'URL suit la
// sélection (sans recharger la page) et une sélection partagée par lien est
// rétablie dès que l'objet arrive dans le flux.

import { useCluster } from '../store/cluster'

export type Route = { type: 'pod'; namespace: string; name: string } | { type: 'node'; name: string } | null

export function parseRoute(pathname: string): Route {
  const parts = pathname.split('/').filter(Boolean).map(decodeURIComponent)
  if (parts[0] === 'pods' && parts.length === 3) return { type: 'pod', namespace: parts[1], name: parts[2] }
  if (parts[0] === 'nodes' && parts.length === 2) return { type: 'node', name: parts[1] }
  return null
}

export function pathFor(r: Route): string {
  if (!r) return '/'
  const e = encodeURIComponent
  return r.type === 'pod' ? `/pods/${e(r.namespace)}/${e(r.name)}` : `/nodes/${e(r.name)}`
}

/** Synchronise l'URL et la sélection ; renvoie la fonction de désabonnement. */
export function syncRoute(win: Pick<Window, 'location' | 'history'> = window): () => void {
  let pending = parseRoute(win.location.pathname)

  const tryRestore = () => {
    if (!pending) return
    const st = useCluster.getState()
    if (pending.type === 'pod') {
      const { namespace, name } = pending
      const p = [...st.pods.values()].find((x) => x.namespace === namespace && x.name === name)
      if (p) { pending = null; st.select({ type: 'pod', key: p.uid }) }
    } else if (st.nodes.has(pending.name) || [...st.pods.values()].some((x) => x.nodeName === pending!.name)) {
      const name = pending.name
      pending = null
      st.select({ type: 'node', key: name })
    }
  }

  const unsubVersion = useCluster.subscribe((s) => s.version, tryRestore)
  const unsubSel = useCluster.subscribe((s) => s.selection, (sel) => {
    if (pending) return // lien profond pas encore rétabli : on ne touche pas à l'URL
    const st = useCluster.getState()
    let r: Route = null
    if (sel?.type === 'pod') {
      const p = st.pods.get(sel.key)
      r = p ? { type: 'pod', namespace: p.namespace, name: p.name } : null
    } else if (sel?.type === 'node') r = { type: 'node', name: sel.key }
    const path = pathFor(r)
    if (path !== win.location.pathname) win.history.replaceState(null, '', path)
  })
  tryRestore()
  return () => { unsubVersion(); unsubSel() }
}
