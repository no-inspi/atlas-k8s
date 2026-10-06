// Recherche (/) parmi les pods, nodes et workloads visibles par l'utilisateur.

import { workloadKey, type Node, type Pod, type Workload } from '../api/types'

export interface SearchResult {
  type: 'pod' | 'node' | 'workload'
  key: string // uid du pod, nom du node, clé du workload
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
export function search(query: string, st: { pods: ReadonlyMap<string, Pod>; nodes: ReadonlyMap<string, Node>; workloads: ReadonlyMap<string, Workload> }): SearchResult[] {
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
  return scored
    .sort((a, b) => a[0] - b[0] || a[1].label.localeCompare(b[1].label))
    .slice(0, MAX_RESULTS)
    .map(([, r]) => r)
}
