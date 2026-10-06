import type { Node, Pod } from '../api/types'
import type { ClusterState } from '../store/cluster'
import { clusterColors } from './colors'
import { postureFor } from './posture'
import { layoutCity, plotGeometry, queuePosition, slotCapacity, SlotAllocator, type CityLayout } from './layout'

export interface PodTarget {
  x: number
  z: number
  onNode: boolean
  /** Pod représenté par une pile : il n'est pas dessiné. */
  hidden?: boolean
}

/** Pile d'un workload sur un node trop chargé : un robot et un compteur. */
export interface Stack {
  uid: string // pod représentant (le plus en difficulté)
  owner: string // workload (« Deployment/api ») ou namespace si regroupé par namespace
  count: number
  x: number
  z: number
}

/** Au-delà, les pods d'un node sont regroupés par workload (spec, « Performance »). */
export const GROUP_THRESHOLD = 48

const severity = (p: Pod) => ({ err: 3, warn: 2, mute: 1, done: 1, ok: 0 })[postureFor(p).antenna]
const ownerKey = (p: Pod) => (p.owner.kind ? `${p.owner.kind}/${p.owner.name}` : `Pod/${p.name}`)

/**
 * Vue dérivée du store, partagée par tous les objets de la scène : disposition
 * de la ville, place de chaque pod, couleurs de namespace. Recalculée seulement
 * quand `version` change, jamais à chaque frame.
 */
export class World {
  version = -1
  layout: CityLayout | null = null
  nodes: Node[] = []
  pods: Pod[] = []
  targets = new Map<string, PodTarget>()
  stacks: Stack[] = []
  colors = new Map<string, string>()
  private layoutKey = ''
  private slots: SlotAllocator | null = null

  update(st: Pick<ClusterState, 'version' | 'nodes' | 'pods' | 'namespaces'>): boolean {
    if (st.version === this.version) return false
    this.version = st.version
    this.pods = [...st.pods.values()]
    this.nodes = [...st.nodes.values(), ...ghostNodes(st.nodes, this.pods)].sort((a, b) => a.name.localeCompare(b.name))

    // Taille des parcelles fixée au premier état reçu : pas de redimensionnement en direct.
    if (!this.slots && this.nodes.length) {
      const perNode = new Map<string, number>()
      for (const p of this.pods) if (p.nodeName) perNode.set(p.nodeName, (perNode.get(p.nodeName) ?? 0) + 1)
      this.slots = new SlotAllocator(slotCapacity(Math.max(0, ...perNode.values())))
    }
    const key = this.nodes.map((n) => `${n.pool}/${n.name}`).join(',')
    if (this.slots && key !== this.layoutKey) {
      this.layoutKey = key
      this.layout = layoutCity(this.nodes, plotGeometry(this.slots.capacity))
    }
    this.colors = clusterColors(st)
    this.placePods()
    return true
  }

  private placePods() {
    this.targets.clear()
    this.stacks = []
    const layout = this.layout
    if (!layout || !this.slots) return
    const geo = layout.geometry

    // Occupants des places : un pod, ou une pile de workload sur un node trop chargé.
    const byNode = new Map<string, Pod[]>()
    for (const p of this.pods) if (layout.plots.has(p.nodeName)) byNode.set(p.nodeName, [...(byNode.get(p.nodeName) ?? []), p])
    const occupants: { uid: string; nodeName: string }[] = []
    const groups = new Map<string, { label: string; pods: Pod[] }>() // id de pile → libellé et pods
    for (const [nodeName, pods] of byNode) {
      if (pods.length <= GROUP_THRESHOLD) {
        for (const p of pods) occupants.push({ uid: p.uid, nodeName })
        continue
      }
      // Par workload ; s'il y a encore plus de piles que de places, par namespace.
      const byWorkload = groupBy(pods, (p) => `${p.namespace}/${ownerKey(p)}`)
      const byNs = byWorkload.size > geo.slots
      const grouped = byNs ? groupBy(pods, (p) => p.namespace) : byWorkload
      for (const [key, members] of grouped) {
        if (members.length === 1) { // une pile d'un pod n'en est pas une
          occupants.push({ uid: members[0].uid, nodeName })
          continue
        }
        const id = `stack:${nodeName}:${key}`
        groups.set(id, { label: byNs ? key : ownerKey(members[0]), pods: members })
        occupants.push({ uid: id, nodeName })
      }
    }
    this.slots.sync(occupants)
    const slotPosition = (id: string, nodeName: string) => {
      const plot = layout.plots.get(nodeName)!
      const local = geo.slot(this.slots!.slotOf(id) ?? geo.slots - 1)
      return { x: plot.x + local.x, z: plot.z + local.z }
    }
    for (const [id, { label, pods }] of groups) {
      const rep = [...pods].sort((a, b) => severity(b) - severity(a) || a.name.localeCompare(b.name))[0]
      const at = slotPosition(id, rep.nodeName)
      this.stacks.push({ uid: rep.uid, owner: label, count: pods.length, ...at })
      for (const p of pods) this.targets.set(p.uid, { ...at, onNode: true, hidden: p.uid !== rep.uid })
    }

    const pending = this.pods
      .filter((p) => !layout.plots.has(p.nodeName))
      .sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.name.localeCompare(b.name))
    pending.forEach((p, i) => this.targets.set(p.uid, { ...queuePosition(layout.queue, i), onNode: false }))

    for (const p of this.pods) {
      if (!layout.plots.has(p.nodeName) || this.targets.has(p.uid)) continue
      this.targets.set(p.uid, { ...slotPosition(p.uid, p.nodeName), onNode: true })
    }
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

/** Phase d'animation stable par pod, pour que les robots ne bougent pas en rythme. */
export function phaseOf(uid: string): number {
  let h = 0
  for (let i = 0; i < uid.length; i++) h = (h * 31 + uid.charCodeAt(i)) | 0
  return ((h >>> 0) % 6283) / 1000
}

/** Positions animées courantes des robots, lues par la sélection et les arcs. */
export const robotPositions = new Map<string, { x: number; y: number; z: number }>()
