// Arbre de la vue Liste : namespaces → workloads → pods, nodes → pods, puis
// entrées → routes, Services par namespace et PVC par classe de stockage.
// Fonctions pures : construction, aplatissement selon les nœuds dépliés,
// navigation au clavier (motif « tree » de WAI-ARIA).

import {
  routeKey, serviceKey, volumeKey, workloadKey, type Node, type Pod, type Route, type Service, type Volume, type Workload,
} from '../api/types'
import type { SelectionType } from '../store/cluster'
import { gatesOf, readyCount, routeBroken } from '../store/net'
import { fmtMem } from './format'

export interface TreeNode {
  id: string
  label: string
  detail?: string
  status?: string
  /** Sélection ouverte dans l'inspecteur. */
  select?: { type: SelectionType; key: string }
  children?: TreeNode[]
}

export interface VisibleItem {
  node: TreeNode
  level: number // 1 = racine
  parent?: string
}

const byLabel = (a: TreeNode, b: TreeNode) => a.label.localeCompare(b.label)

/** Ajoute x à la liste de k (sans recopier la liste). */
function pushTo<T>(m: Map<string, T[]>, k: string, x: T) {
  const list = m.get(k)
  if (list) list.push(x)
  else m.set(k, [x])
}

const podItem = (p: Pod): TreeNode => ({ id: `pod:${p.uid}`, label: p.name, status: p.displayStatus, select: { type: 'pod', key: p.uid } })

export function buildTree(st: {
  pods: ReadonlyMap<string, Pod>; nodes: ReadonlyMap<string, Node>; workloads: ReadonlyMap<string, Workload>
  services?: ReadonlyMap<string, Service>; routes?: ReadonlyMap<string, Route>; volumes?: ReadonlyMap<string, Volume>
}): TreeNode[] {
  const pods = [...st.pods.values()]
  const byNs = new Map<string, Map<string, Pod[]>>()
  for (const p of pods) {
    const owner = p.owner.kind ? workloadKey({ kind: p.owner.kind, namespace: p.namespace, name: p.owner.name }) : ''
    const ws = byNs.get(p.namespace) ?? new Map<string, Pod[]>()
    const list = ws.get(owner)
    if (list) list.push(p)
    else ws.set(owner, [p])
    byNs.set(p.namespace, ws)
  }
  const namespaces: TreeNode[] = [...byNs].map(([ns, ws]) => {
    const children: TreeNode[] = []
    for (const [key, members] of ws) {
      const items = members.map(podItem).sort(byLabel)
      if (!key) { children.push(...items); continue }
      const w = st.workloads.get(key)
      const [kind, , name] = key.split('/')
      children.push({
        id: `wl:${key}`, label: name, detail: w ? `${kind} · ${w.readyReplicas}/${w.replicas} prêts` : kind, children: items,
      })
    }
    return { id: `ns:${ns}`, label: ns, detail: `${[...ws.values()].reduce((s, m) => s + m.length, 0)} pods`, children: children.sort(byLabel) }
  }).sort(byLabel)

  const podsOnNode = new Map<string, Pod[]>()
  for (const p of pods) if (p.nodeName) pushTo(podsOnNode, p.nodeName, p)
  const nodes: TreeNode[] = [...st.nodes.values()].map((n) => ({
    id: `node:${n.name}`, label: n.name, detail: `${n.pool} · ${podsOnNode.get(n.name)?.length ?? 0} pods`,
    status: n.unschedulable ? 'SchedulingDisabled' : undefined,
    select: { type: 'node' as const, key: n.name },
    children: (podsOnNode.get(n.name) ?? []).map(podItem).sort(byLabel),
  })).sort(byLabel)

  const roots: TreeNode[] = [
    { id: 'group:namespaces', label: 'Namespaces', detail: `${namespaces.length}`, children: namespaces },
    { id: 'group:nodes', label: 'Nodes', detail: `${nodes.length}`, children: nodes },
  ]

  const gates = gatesOf(st.routes?.values() ?? [])
  if (gates.length) roots.push({
    id: 'group:gates', label: 'Entrées', detail: `${gates.length}`,
    children: gates.map((g) => ({
      id: `gate:${g.name}`, label: g.name, detail: `${g.routes.length} routes`, status: g.broken ? 'route cassée' : undefined,
      select: { type: 'gate', key: g.name },
      children: g.routes.map((r) => ({
        id: `route:${routeKey(r)}`, label: r.name, detail: `${r.source} · ${r.namespace}`,
        status: routeBroken(r) ? 'Service introuvable' : undefined, select: { type: 'route', key: routeKey(r) },
      })),
    })),
  })

  const services = [...(st.services?.values() ?? [])]
  if (services.length) {
    const byNs = new Map<string, Service[]>()
    for (const s of services) pushTo(byNs, s.namespace, s)
    roots.push({
      id: 'group:services', label: 'Services', detail: `${services.length}`,
      children: [...byNs].map(([ns, ss]) => ({
        id: `svcns:${ns}`, label: ns, detail: `${ss.length} Services`,
        children: ss.map((s) => ({
          id: `service:${serviceKey(s)}`, label: s.name,
          detail: s.type === 'ExternalName' ? `ExternalName · ${s.externalName}` : `${s.type} · ${readyCount(s)}/${s.endpoints.length} ready`,
          status: s.health === 'down' || s.health === 'degraded' ? s.health : undefined,
          select: { type: 'service' as const, key: serviceKey(s) },
        })).sort(byLabel),
      })).sort(byLabel),
    })
  }

  const volumes = [...(st.volumes?.values() ?? [])]
  if (volumes.length) {
    const byClass = new Map<string, Volume[]>()
    for (const v of volumes) pushTo(byClass, v.storageClass || '(aucune)', v)
    roots.push({
      id: 'group:storage', label: 'Stockage', detail: `${volumes.length}`,
      children: [...byClass].map(([c, vs]) => ({
        id: `class:${c}`, label: c, detail: `${vs.length} PVC`,
        children: vs.map((v) => ({
          id: `volume:${volumeKey(v)}`, label: v.name, detail: `${v.namespace} · ${fmtMem(v.requested)}`,
          status: v.phase !== 'Bound' ? v.phase : undefined, select: { type: 'volume' as const, key: volumeKey(v) },
        })).sort(byLabel),
      })).sort(byLabel),
    })
  }
  return roots
}

export function flatten(roots: TreeNode[], expanded: ReadonlySet<string>): VisibleItem[] {
  const out: VisibleItem[] = []
  const walk = (ns: TreeNode[], level: number, parent?: string) => {
    for (const n of ns) {
      out.push({ node: n, level, parent })
      if (n.children?.length && expanded.has(n.id)) walk(n.children, level + 1, n.id)
    }
  }
  walk(roots, 1)
  return out
}

export type TreeAction =
  | { type: 'focus'; id: string }
  | { type: 'expand'; id: string }
  | { type: 'collapse'; id: string }
  | { type: 'activate'; id: string }
  | null

/** Effet d'une touche sur l'élément focalisé (WAI-ARIA tree pattern). */
export function keyAction(key: string, items: VisibleItem[], focused: string, expanded: ReadonlySet<string>): TreeAction {
  const i = items.findIndex((it) => it.node.id === focused)
  if (i < 0) return items[0] ? { type: 'focus', id: items[0].node.id } : null
  const it = items[i]
  const hasChildren = !!it.node.children?.length
  switch (key) {
    case 'ArrowDown': return items[i + 1] ? { type: 'focus', id: items[i + 1].node.id } : null
    case 'ArrowUp': return items[i - 1] ? { type: 'focus', id: items[i - 1].node.id } : null
    case 'Home': return { type: 'focus', id: items[0].node.id }
    case 'End': return { type: 'focus', id: items[items.length - 1].node.id }
    case 'ArrowRight':
      if (hasChildren && !expanded.has(it.node.id)) return { type: 'expand', id: it.node.id }
      if (hasChildren) return { type: 'focus', id: items[i + 1].node.id }
      return null
    case 'ArrowLeft':
      if (hasChildren && expanded.has(it.node.id)) return { type: 'collapse', id: it.node.id }
      return it.parent ? { type: 'focus', id: it.parent } : null
    case 'Enter':
    case ' ':
      if (it.node.select) return { type: 'activate', id: it.node.id }
      return hasChildren ? (expanded.has(it.node.id) ? { type: 'collapse', id: it.node.id } : { type: 'expand', id: it.node.id }) : null
  }
  return null
}
