import type { Node, Pod } from '../api/types'
import type { ClusterState } from '../store/cluster'
import { clusterColors } from './colors'
import { layoutCity, plotGeometry, queuePosition, slotCapacity, SlotAllocator, type CityLayout } from './layout'

export interface PodTarget {
  x: number
  z: number
  onNode: boolean
}

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
  colors = new Map<string, string>()
  private layoutKey = ''
  private slots: SlotAllocator | null = null

  update(st: Pick<ClusterState, 'version' | 'nodes' | 'pods' | 'namespaces'>): boolean {
    if (st.version === this.version) return false
    this.version = st.version
    this.nodes = [...st.nodes.values()].sort((a, b) => a.name.localeCompare(b.name))
    this.pods = [...st.pods.values()]

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
    const layout = this.layout
    if (!layout || !this.slots) return
    const geo = layout.geometry
    this.slots.sync(this.pods.filter((p) => layout.plots.has(p.nodeName)))

    const pending = this.pods
      .filter((p) => !layout.plots.has(p.nodeName))
      .sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.name.localeCompare(b.name))
    pending.forEach((p, i) => this.targets.set(p.uid, { ...queuePosition(layout.queue, i), onNode: false }))

    for (const p of this.pods) {
      const plot = layout.plots.get(p.nodeName)
      if (!plot) continue
      // Au-delà de la capacité, les pods s'empilent sur la dernière place (regroupement : jalon 7).
      const slot = this.slots.slotOf(p.uid) ?? geo.slots - 1
      const local = geo.slot(slot)
      this.targets.set(p.uid, { x: plot.x + local.x, z: plot.z + local.z, onNode: true })
    }
  }
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
