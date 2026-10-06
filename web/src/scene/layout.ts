// Disposition de la ville : quartiers (node pools), parcelles (nodes), places
// des blocs de pods sur chaque parcelle et file d'attente des pods Pending.
// Unité : la même que la scène du prototype (une parcelle de 12 places = 4,1).

import type { Node } from '../api/types'

export type DistrictStyle = 'std' | 'spot' | 'gpu'

export interface PlotGeometry {
  slots: number
  cols: number
  rows: number
  width: number
  depth: number
  /** Position locale (par rapport au centre de la parcelle) de chaque place. */
  slot(i: number): { x: number; z: number }
}

export interface District {
  pool: string
  style: DistrictStyle
  label: string
  x: number // centre
  z: number
  width: number
  depth: number
}

export interface Rect {
  x: number
  z: number
  width: number
  depth: number
}

export interface CityLayout {
  districts: District[]
  plots: Map<string, { x: number; z: number }>
  queue: Rect
  bounds: Rect
  geometry: PlotGeometry
}

const CELL = 0.95 // pas entre deux blocs de pods
const BUILDING_DEPTH = 1.6 // fond de parcelle occupé par le bâtiment
const ALLEY = 1.9 // allée entre deux parcelles
const DISTRICT_PAD = 1.2 // marge intérieure d'un quartier
const LABEL_STRIP = 1.0 // bande à l'avant du quartier pour son nom (jamais masquée par un bâtiment)
const DISTRICT_GAP = 2.4 // rue entre deux quartiers
const MIN_PLOT = 4.1

/** Places par parcelle : 1,5 × le max observé, multiple de 4, entre 12 et 48. */
export function slotCapacity(maxPodsPerNode: number): number {
  const want = Math.ceil((maxPodsPerNode * 1.5) / 4) * 4
  return Math.min(48, Math.max(12, want))
}

export function plotGeometry(slots: number): PlotGeometry {
  const cols = Math.max(4, Math.round(Math.sqrt((slots * 4) / 3)))
  const rows = Math.ceil(slots / cols)
  const width = Math.max(MIN_PLOT, cols * CELL + 0.3)
  const depth = Math.max(MIN_PLOT, BUILDING_DEPTH + (rows - 1) * CELL + 0.6)
  const z0 = -depth / 2 + BUILDING_DEPTH
  return {
    slots, cols, rows, width, depth,
    slot: (i) => ({ x: ((i % cols) - (cols - 1) / 2) * CELL, z: z0 + Math.floor(i / cols) * CELL }),
  }
}

/** Node GPU : ressource nvidia.com/gpu allouable, ou taint nvidia.com/gpu (pools GPU GKE). */
export const isGpuNode = (n: Node) => n.gpu > 0 || n.taints.some((t) => t.key === 'nvidia.com/gpu')

/** Style de bâtiment d'un node. */
export const nodeStyle = (n: Node): DistrictStyle => (isGpuNode(n) ? 'gpu' : n.spot ? 'spot' : 'std')

function styleOf(nodes: Node[]): DistrictStyle {
  if (nodes.some(isGpuNode)) return 'gpu'
  if (nodes.some((n) => n.spot)) return 'spot'
  return 'std'
}

/**
 * Quartiers triés par nom de pool, nodes triés par nom en grille dans leur
 * quartier, quartiers rangés en rangées (algorithme d'étagères), le tout centré
 * sur l'origine. Le résultat ne dépend que de l'ensemble des nodes.
 */
export function layoutCity(nodes: Node[], geo: PlotGeometry): CityLayout {
  const byPool = new Map<string, Node[]>()
  for (const n of nodes) byPool.set(n.pool, [...(byPool.get(n.pool) ?? []), n])
  const pools = [...byPool.keys()].sort()

  const pitchX = geo.width + ALLEY
  const pitchZ = geo.depth + ALLEY

  const boxes = pools.map((pool) => {
    const members = byPool.get(pool)!.sort((a, b) => a.name.localeCompare(b.name))
    const cols = Math.min(members.length, Math.max(3, Math.ceil(Math.sqrt(members.length))))
    const rows = Math.ceil(members.length / cols)
    const sample = members[0]
    return {
      pool, members, cols, rows,
      style: styleOf(members),
      label: sample.instanceType ? `${pool} · ${sample.instanceType}` : pool,
      width: cols * pitchX - ALLEY + 2 * DISTRICT_PAD,
      depth: rows * pitchZ - ALLEY + 2 * DISTRICT_PAD + LABEL_STRIP,
    }
  })

  const totalArea = boxes.reduce((s, b) => s + b.width * b.depth, 0)
  const maxRow = Math.max(24, Math.sqrt(totalArea) * 1.4, ...boxes.map((b) => b.width))

  // Étagères : on remplit une rangée jusqu'à maxRow, puis on passe à la suivante.
  type Placed = (typeof boxes)[number] & { x0: number; z0: number }
  const placed: Placed[] = []
  let x = 0, z = 0, rowDepth = 0, width = 0
  for (const b of boxes) {
    if (x > 0 && x + b.width > maxRow) {
      z += rowDepth + DISTRICT_GAP
      x = 0
      rowDepth = 0
    }
    placed.push({ ...b, x0: x, z0: z })
    x += b.width + DISTRICT_GAP
    width = Math.max(width, x - DISTRICT_GAP)
    rowDepth = Math.max(rowDepth, b.depth)
  }
  const depth = z + rowDepth
  const ox = -width / 2, oz = -depth / 2

  const districts: District[] = []
  const plots = new Map<string, { x: number; z: number }>()
  for (const p of placed) {
    const dx = ox + p.x0, dz = oz + p.z0
    districts.push({ pool: p.pool, style: p.style, label: p.label, x: dx + p.width / 2, z: dz + p.depth / 2, width: p.width, depth: p.depth })
    p.members.forEach((n, i) => {
      plots.set(n.name, {
        x: dx + DISTRICT_PAD + (i % p.cols) * pitchX + geo.width / 2,
        z: dz + DISTRICT_PAD + Math.floor(i / p.cols) * pitchZ + geo.depth / 2,
      })
    })
  }

  const queue: Rect = { x: 0, z: depth / 2 + 3.4, width: Math.max(14, width * 0.8), depth: 2.6 }
  return {
    districts, plots, queue, geometry: geo,
    bounds: { x: 0, z: 1.7, width: width, depth: depth + 3.4 * 2 },
  }
}

/** Position du i-ème pod en attente dans la file. */
export function queuePosition(q: Rect, i: number): { x: number; z: number } {
  const perRow = Math.max(1, Math.floor((q.width - 0.6) / 1.05))
  return {
    x: q.x - q.width / 2 + 0.6 + (i % perRow) * 1.05,
    z: q.z - 0.5 + Math.floor(i / perRow) * 1.05,
  }
}
