import type { Node, Pod } from '../api/types'
import type { ClusterState } from '../store/cluster'
import { clusterColors } from './colors'
import { postureFor } from './posture'
import { layoutCity, plotGeometry, queuePosition, slotCapacity, type CityLayout } from './layout'
import { blockHeight, compareOccupants, isVisible, NEUTRAL_VIEW, sizeMetric, type PodView } from './podView'

/** Côté d'un bloc (le pas entre deux cases est de 0,95). */
export const BLOCK_SIDE = 0.78

/** Bloc du pod en coordonnées monde. */
export interface PodTarget {
  x: number
  z: number
  /** Hauteur du bloc (unités monde, à l'échelle 1). */
  h: number
  onNode: boolean
  /** Pod représenté par une pile : il n'est pas dessiné. */
  hidden?: boolean
}

/** Pile d'un workload sur un node trop chargé : un bloc et un compteur. */
export interface Stack {
  uid: string // pod représentant (le plus en difficulté)
  owner: string // workload (« Deployment/api ») ou namespace si regroupé par namespace
  count: number
  x: number
  z: number
  /** Hauteur du dessus de la pile au-dessus de la plateforme. */
  top: number
}

/** Au-delà, les pods d'un node sont regroupés par workload (spec, « Performance »). */
export const GROUP_THRESHOLD = 48

const severity = (p: Pod) => ({ err: 3, warn: 2, mute: 1, done: 1, ok: 0 })[postureFor(p).antenna]
const ownerKey = (p: Pod) => (p.owner.kind ? `${p.owner.kind}/${p.owner.name}` : `Pod/${p.name}`)

type WorldInput = Pick<ClusterState, 'version' | 'nodes' | 'pods' | 'namespaces'> & { podView?: PodView; nsFilter?: string | null }

/** Occupant d'une place : un pod, ou une pile (représentant et membres). */
interface Occupant {
  rep: Pod
  pods: Pod[]
  size: number // somme des requests de la ressource qui fixe la hauteur
  label?: string // libellé de pile
}

/**
 * Vue dérivée du store, partagée par tous les objets de la scène : disposition
 * de la ville, place de chaque pod, couleurs de namespace. Recalculée seulement
 * quand `version` ou les réglages d'affichage changent, jamais à chaque frame.
 */
export class World {
  version = -1
  layout: CityLayout | null = null
  nodes: Node[] = []
  /** Pods dessinés (après filtres d'affichage). */
  pods: Pod[] = []
  /** Pods masqués par les réglages d'affichage. */
  filtered = 0
  targets = new Map<string, PodTarget>()
  stacks: Stack[] = []
  colors = new Map<string, string>()
  private viewKey = ''
  private layoutKey = ''
  private capacity = 0

  update(st: WorldInput): boolean {
    const view = st.podView ?? NEUTRAL_VIEW
    const nsFilter = st.nsFilter ?? null
    const viewKey = `${view.sort}|${view.hideSystem}|${view.hiddenKinds.join(',')}|${view.hideSystem ? nsFilter : ''}`
    if (st.version === this.version && viewKey === this.viewKey) return false
    this.version = st.version
    this.viewKey = viewKey
    const all = [...st.pods.values()]
    this.pods = all.filter((p) => isVisible(p, view, nsFilter))
    this.filtered = all.length - this.pods.length
    // Les nodes ne dépendent pas des filtres : la ville ne bouge pas quand on masque des pods.
    this.nodes = [...st.nodes.values(), ...ghostNodes(st.nodes, all)].sort((a, b) => a.name.localeCompare(b.name))

    // Taille des parcelles fixée au premier état reçu : pas de redimensionnement en direct.
    if (!this.capacity && this.nodes.length) {
      const perNode = new Map<string, number>()
      for (const p of all) if (p.nodeName) perNode.set(p.nodeName, (perNode.get(p.nodeName) ?? 0) + 1)
      this.capacity = slotCapacity(Math.max(0, ...perNode.values()))
    }
    const key = this.nodes.map((n) => `${n.pool}/${n.name}`).join(',')
    if (this.capacity && key !== this.layoutKey) {
      this.layoutKey = key
      this.layout = layoutCity(this.nodes, plotGeometry(this.capacity))
    }
    this.colors = clusterColors(st)
    this.placePods(view, st.nodes)
    return true
  }

  private placePods(view: PodView, known: ReadonlyMap<string, Node>) {
    this.targets.clear()
    this.stacks = []
    const layout = this.layout
    if (!layout) return
    const geo = layout.geometry
    const metric = sizeMetric(view.sort)
    const cmp = compareOccupants(view.sort)
    const sizeOf = (pods: Pod[]) => pods.reduce((s, p) => s + p.requests[metric], 0)
    // Au-delà du seuil, ou s'il n'y a plus de place sur la parcelle, on empile.
    const limit = Math.min(GROUP_THRESHOLD, geo.slots)

    const byNode = groupBy(this.pods.filter((p) => layout.plots.has(p.nodeName)), (p) => p.nodeName)
    for (const [nodeName, pods] of byNode) {
      let occupants: Occupant[]
      if (pods.length <= limit) {
        occupants = pods.map((p) => ({ rep: p, pods: [p], size: p.requests[metric] }))
      } else {
        // Par workload ; s'il y a encore plus de piles que de places, par namespace.
        const byWorkload = groupBy(pods, (p) => `${p.namespace}/${ownerKey(p)}`)
        const byNs = byWorkload.size > geo.slots
        const grouped = byNs ? groupBy(pods, (p) => p.namespace) : byWorkload
        occupants = [...grouped].map(([key, members]) => ({
          rep: [...members].sort((a, b) => severity(b) - severity(a) || a.name.localeCompare(b.name))[0],
          pods: members,
          size: sizeOf(members),
          // une pile d'un pod n'en est pas une
          label: members.length > 1 ? (byNs ? key : ownerKey(members[0])) : undefined,
        }))
      }
      occupants.sort(cmp)

      const plot = layout.plots.get(nodeName)!
      // Capacité du node ; pour un node anonyme, le total demandé par ses pods.
      const capacity = known.get(nodeName)?.allocatable[metric] || sizeOf(pods)
      occupants.forEach((o, i) => {
        const local = geo.slot(Math.min(i, geo.slots - 1))
        const at = { x: plot.x + local.x, z: plot.z + local.z, h: blockHeight(o.size, capacity) }
        if (o.label) this.stacks.push({ uid: o.rep.uid, owner: o.label, count: o.pods.length, x: at.x, z: at.z, top: at.h })
        for (const p of o.pods) this.targets.set(p.uid, { ...at, onNode: true, hidden: p !== o.rep || undefined })
      })
    }

    // File d'attente : hauteur relative au plus gros node connu.
    const biggest = Math.max(0, ...[...known.values()].map((n) => n.allocatable[metric]))
    const pending = this.pods
      .filter((p) => !layout.plots.has(p.nodeName))
      .sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.name.localeCompare(b.name))
    pending.forEach((p, i) => this.targets.set(p.uid, {
      ...queuePosition(layout.queue, i), onNode: false,
      h: blockHeight(p.requests[metric], biggest || p.requests[metric] * 4),
    }))
  }
}

/**
 * Sans droit de lister les nodes, l'utilisateur connaît quand même leur nom par
 * ses pods : on dessine un bâtiment anonyme, sans capacité ni détails.
 */
export function ghostNodes(known: ReadonlyMap<string, Node>, pods: Pod[]): Node[] {
  const names = new Set(pods.map((p) => p.nodeName).filter((n) => n && !known.has(n)))
  return [...names].map((name) => ({
    name, pool: 'nodes non visibles', instanceType: '', zone: '', spot: false, gpu: 0,
    allocatable: { cpu: 0, memory: 0 }, requested: { cpu: 0, memory: 0 }, conditions: [], taints: [],
    unschedulable: false, kubeletVersion: '', createdAt: '', ghost: true,
  }))
}

function groupBy<T>(xs: T[], key: (x: T) => string): Map<string, T[]> {
  const m = new Map<string, T[]>()
  for (const x of xs) m.set(key(x), [...(m.get(key(x)) ?? []), x])
  return m
}

export const world = new World()

/** Phase d'animation stable par pod, pour que les blocs ne bougent pas en rythme. */
export function phaseOf(uid: string): number {
  let h = 0
  for (let i = 0; i < uid.length; i++) h = (h * 31 + uid.charCodeAt(i)) | 0
  return ((h >>> 0) % 6283) / 1000
}

/** Positions animées courantes des blocs (base et hauteur), lues par la sélection, les arcs et la recherche. */
export const podPositions = new Map<string, { x: number; y: number; z: number; h: number }>()
