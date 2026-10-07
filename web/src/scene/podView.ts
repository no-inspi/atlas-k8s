// Réglages d'affichage des pods dans la ville : ordre des blocs sur chaque
// parcelle, hauteur des blocs (request CPU ou RAM), pods masqués (namespaces
// système, types de workload).

import type { Pod } from '../api/types'

export type PodSort = 'memory' | 'cpu' | 'kind' | 'namespace' | 'name'

export interface PodView {
  sort: PodSort
  /** Masque kube-system, kube-public et kube-node-lease (sauf namespace filtré). */
  hideSystem: boolean
  /** Types de workload racine masqués (« DaemonSet », « Pod » pour un pod sans propriétaire…). */
  hiddenKinds: string[]
}

export const SYSTEM_NAMESPACES: ReadonlySet<string> = new Set(['kube-system', 'kube-public', 'kube-node-lease'])
export const DEFAULT_VIEW: PodView = { sort: 'memory', hideSystem: true, hiddenKinds: [] }
/** Tout afficher, par nom : la vue des tests et des appels sans réglages. */
export const NEUTRAL_VIEW: PodView = { sort: 'name', hideSystem: false, hiddenKinds: [] }

export const SORT_LABEL: Record<PodSort, string> = {
  memory: 'Request RAM', cpu: 'Request CPU', kind: 'Type de workload', namespace: 'Namespace', name: 'Nom',
}

/** Type du workload racine ; « Pod » pour un pod sans propriétaire. */
export const kindOf = (p: Pick<Pod, 'owner'>) => p.owner.kind || 'Pod'

const KIND_ORDER = ['DaemonSet', 'StatefulSet', 'Deployment', 'ReplicaSet', 'CronJob', 'Job', 'Pod']
const kindRank = (k: string) => {
  const i = KIND_ORDER.indexOf(k)
  return i < 0 ? KIND_ORDER.length : i
}

/** Types connus d'abord dans l'ordre de KIND_ORDER, les autres par nom. */
export function sortKinds(kinds: Iterable<string>): string[] {
  return [...new Set(kinds)].sort((a, b) => kindRank(a) - kindRank(b) || a.localeCompare(b))
}

/** Namespace affiché : les namespaces système sont masqués, sauf s'ils sont choisis dans la légende. */
export const isNamespaceVisible = (ns: string, view: PodView, nsFilter: string | null = null) =>
  !(view.hideSystem && SYSTEM_NAMESPACES.has(ns) && ns !== nsFilter)

export function isVisible(p: Pod, view: PodView, nsFilter: string | null = null): boolean {
  if (view.hiddenKinds.includes(kindOf(p))) return false
  // Choisir un namespace système dans la légende l'affiche malgré le masquage.
  if (!isNamespaceVisible(p.namespace, view, nsFilter)) return false
  return true
}

/** Ressource qui donne leur hauteur aux blocs. */
export const sizeMetric = (sort: PodSort): 'cpu' | 'memory' => (sort === 'cpu' ? 'cpu' : 'memory')

const byName = (a: Pod, b: Pod) => a.name.localeCompare(b.name)
const byOwner = (a: Pod, b: Pod) => kindOf(a).localeCompare(kindOf(b)) || a.owner.name.localeCompare(b.owner.name)

/**
 * Ordre des occupants d'une parcelle. Pour CPU et RAM, du plus gros au plus
 * petit : les blocs hauts se rangent au fond et à gauche, où ils ne cachent
 * pas les petits (la caméra regarde depuis l'avant droit).
 */
export function compareOccupants(sort: PodSort) {
  return (a: { rep: Pod; size: number }, b: { rep: Pod; size: number }): number => {
    const p = a.rep, q = b.rep
    switch (sort) {
      case 'cpu':
      case 'memory':
        return b.size - a.size || byName(p, q)
      case 'kind':
        return kindRank(kindOf(p)) - kindRank(kindOf(q)) || byOwner(p, q) || byName(p, q)
      case 'namespace':
        return p.namespace.localeCompare(q.namespace) || byOwner(p, q) || byName(p, q)
      case 'name':
        return byName(p, q)
    }
  }
}

export const BLOCK_MIN = 0.16
export const BLOCK_MAX = 1.3
/** Part de la capacité du node à partir de laquelle un bloc a la hauteur maximale. */
const FULL_SHARE = 0.2

/**
 * Hauteur d'un bloc : racine de la part de la capacité allouable du node
 * demandée par le pod, pour que les petits pods restent distincts. Un pod
 * sans request est une dalle plate.
 */
export function blockHeight(request: number, capacity: number): number {
  if (request <= 0 || capacity <= 0) return BLOCK_MIN
  return BLOCK_MIN + (BLOCK_MAX - BLOCK_MIN) * Math.min(1, Math.sqrt(request / capacity / FULL_SHARE))
}

const STORAGE_KEY = 'atlas.podView'

export function loadView(): PodView {
  try {
    const raw = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null')
    if (raw && typeof raw === 'object' && raw.sort in SORT_LABEL) {
      return { sort: raw.sort, hideSystem: raw.hideSystem !== false, hiddenKinds: Array.isArray(raw.hiddenKinds) ? raw.hiddenKinds.filter((k: unknown) => typeof k === 'string') : [] }
    }
  } catch { /* stockage indisponible : réglages par défaut */ }
  return DEFAULT_VIEW
}

export function saveView(view: PodView): void {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(view)) } catch { /* stockage indisponible */ }
}
