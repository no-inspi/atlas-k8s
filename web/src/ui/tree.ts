// Arbre de la vue Liste : namespaces → workloads → pods, et nodes → pods.
// Fonctions pures : construction, aplatissement selon les nœuds dépliés,
// navigation au clavier (motif « tree » de WAI-ARIA).

import { workloadKey, type Node, type Pod, type Workload } from '../api/types'
import type { SelectionType } from '../store/cluster'

export interface TreeNode {
  id: string
  label: string
  detail?: string
  status?: string
  /** Sélection ouverte dans l'inspecteur (pods et nodes). */
  select?: { type: SelectionType; key: string }
  children?: TreeNode[]
}

export interface VisibleItem {
  node: TreeNode
  level: number // 1 = racine
  parent?: string
}

const byLabel = (a: TreeNode, b: TreeNode) => a.label.localeCompare(b.label)

const podItem = (p: Pod): TreeNode => ({ id: `pod:${p.uid}`, label: p.name, status: p.displayStatus, select: { type: 'pod', key: p.uid } })

export function buildTree(st: { pods: ReadonlyMap<string, Pod>; nodes: ReadonlyMap<string, Node>; workloads: ReadonlyMap<string, Workload> }): TreeNode[] {
  const pods = [...st.pods.values()]
  const byNs = new Map<string, Map<string, Pod[]>>()
  for (const p of pods) {
    const owner = p.owner.kind ? workloadKey({ kind: p.owner.kind, namespace: p.namespace, name: p.owner.name }) : ''
    const ws = byNs.get(p.namespace) ?? new Map<string, Pod[]>()
    ws.set(owner, [...(ws.get(owner) ?? []), p])
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
  for (const p of pods) if (p.nodeName) podsOnNode.set(p.nodeName, [...(podsOnNode.get(p.nodeName) ?? []), p])
  const nodes: TreeNode[] = [...st.nodes.values()].map((n) => ({
    id: `node:${n.name}`, label: n.name, detail: `${n.pool} · ${podsOnNode.get(n.name)?.length ?? 0} pods`,
    status: n.unschedulable ? 'SchedulingDisabled' : undefined,
    select: { type: 'node' as const, key: n.name },
    children: (podsOnNode.get(n.name) ?? []).map(podItem).sort(byLabel),
  })).sort(byLabel)

  return [
    { id: 'group:namespaces', label: 'Namespaces', detail: `${namespaces.length}`, children: namespaces },
    { id: 'group:nodes', label: 'Nodes', detail: `${nodes.length}`, children: nodes },
  ]
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
