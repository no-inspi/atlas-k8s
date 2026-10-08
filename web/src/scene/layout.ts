// Disposition de la ville : quartiers (node pools), parcelles (nodes), places
// des blocs de pods sur chaque parcelle et file d'attente des pods Pending.
// Unité : la même que la scène du prototype (une parcelle de 12 places = 4,1).

import type { Node } from '../api/types'
import { fmtMem } from '../ui/format'

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
  /** Lignes de la légende : nom et type d'instance, puis capacité nominale et total allouable (absents si inconnus). */
  caption: string[]
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

export interface Avenue {
  x: number // centre
  z: number
  width: number
  depth: number
}

export interface CityLayout {
  districts: District[]
  plots: Map<string, { x: number; z: number }>
  queue: Rect
  /** Avenues entre les rangées de quartiers (ou devant la seule rangée). */
  avenues: Avenue[]
  bounds: Rect
  geometry: PlotGeometry
}

const CELL = 0.95 // pas entre deux blocs de pods
const BUILDING_DEPTH = 1.6 // fond de parcelle occupé par le bâtiment
export const ALLEY = 1.9 // allée entre deux parcelles
const DISTRICT_PAD = 1.35 // marge intérieure d'un quartier
const LABEL_STRIP = 1.7 // bande à l'avant du quartier pour sa légende sur une ou deux lignes (jamais masquée par un bâtiment)
const LABEL_STRIP_3 = 2.4 // idem sur trois lignes
const NARROW = 9 // en dessous de cette largeur de quartier, la ligne de capacité est coupée en deux
/** Profondeur de la bande de légende à l'avant d'un quartier selon le nombre de lignes. */
export const captionStrip = (lines: number) => (lines > 2 ? LABEL_STRIP_3 : LABEL_STRIP)
/** Hauteur du socle d'un quartier : bâtiments, pods et liens des quartiers sont posés dessus. */
export const SOCLE_H = 0.35
const DISTRICT_GAP = 2.4 // rue entre deux quartiers
const MIN_PLOT = 4.1
/** Largeur d'une avenue : une rangée de relais, trois voies de liens et les noms des tronçons. */
export const AVENUE = 3.6
/** Entrée ouest de la première avenue, où se tiennent les portes. */
export const GATE_ZONE = 4.5
/** Débord des avenues à l'est de la ville : la rue est (conduites des entrepôts) y passe. */
const AVENUE_EAST = 2

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

const GI = 1 << 30

/**
 * Légende d'un quartier, en trois parties. 1 : pool et type d'instance (« types
 * mixtes » s'il y en a plusieurs). 2 : nodes groupés par taille nominale arrondie
 * (vCPU et Gi entiers). 3 : total allouable. Les deux dernières sont vides si
 * aucun node n'a de capacité.
 */
export function poolCaption(pool: string, members: Node[]): [string, string, string] {
  const types = new Set(members.map((n) => n.instanceType).filter(Boolean))
  const head = types.size === 1 ? `${pool} · ${[...types][0]}` : types.size > 1 ? `${pool} · types mixtes` : pool
  const sized = members.filter((n) => !n.ghost && n.capacity.cpu > 0)
  if (!sized.length) return [head, '', '']
  const groups = new Map<string, { cpu: number; mem: number; count: number }>()
  for (const n of sized) {
    const cpu = Math.max(1, Math.round(n.capacity.cpu / 1000)), mem = Math.max(1, Math.round(n.capacity.memory / GI))
    const g = groups.get(`${cpu}/${mem}`) ?? { cpu, mem, count: 0 }
    g.count++
    groups.set(`${cpu}/${mem}`, g)
  }
  const sorted = [...groups.values()].sort((a, b) => b.count - a.count || a.cpu - b.cpu || a.mem - b.mem)
  const fmt = (g: { cpu: number; mem: number; count: number }) => `${g.count} × ${g.cpu} vCPU / ${g.mem}Gi`
  const sizes = sorted.length > 3 ? [...sorted.slice(0, 2).map(fmt), `${sorted.length - 2} autres`] : sorted.map(fmt)
  const cpu = sized.reduce((s, n) => s + n.allocatable.cpu, 0)
  const mem = sized.reduce((s, n) => s + n.allocatable.memory, 0)
  return [head, sizes.join(' + '), `${Number((cpu / 1000).toFixed(2))} vCPU / ${fmtMem(mem)} allouables`]
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
    const width = cols * pitchX - ALLEY + 2 * DISTRICT_PAD
    const [head, sizes, alloc] = poolCaption(pool, members)
    const caption = !sizes ? [head] : width < NARROW ? [head, sizes, alloc] : [head, `${sizes} · ${alloc}`]
    return {
      pool, members, cols, rows,
      style: styleOf(members),
      caption,
      width,
      depth: rows * pitchZ - ALLEY + 2 * DISTRICT_PAD + captionStrip(caption.length),
    }
  })

  const totalArea = boxes.reduce((s, b) => s + b.width * b.depth, 0)
  const maxRow = Math.max(24, Math.sqrt(totalArea) * 1.4, ...boxes.map((b) => b.width))

  // Étagères : on remplit une rangée jusqu'à maxRow, puis on passe à la suivante,
  // de l'autre côté d'une avenue.
  type Placed = (typeof boxes)[number] & { x0: number; z0: number }
  const placed: Placed[] = []
  const avenueTops: number[] = [] // bord nord de chaque avenue, avant centrage
  let x = 0, z = 0, rowDepth = 0, width = 0
  for (const b of boxes) {
    if (x > 0 && x + b.width > maxRow) {
      avenueTops.push(z + rowDepth)
      z += rowDepth + AVENUE
      x = 0
      rowDepth = 0
    }
    placed.push({ ...b, x0: x, z0: z })
    x += b.width + DISTRICT_GAP
    width = Math.max(width, x - DISTRICT_GAP)
    rowDepth = Math.max(rowDepth, b.depth)
  }
  let depth = z + rowDepth
  // Une seule rangée : l'avenue passe devant, entre la ville et la file d'attente.
  if (!avenueTops.length) {
    avenueTops.push(depth)
    depth += AVENUE
  }
  const ox = -width / 2, oz = -depth / 2

  const districts: District[] = []
  const plots = new Map<string, { x: number; z: number }>()
  for (const p of placed) {
    const dx = ox + p.x0, dz = oz + p.z0
    districts.push({ pool: p.pool, style: p.style, caption: p.caption, x: dx + p.width / 2, z: dz + p.depth / 2, width: p.width, depth: p.depth })
    p.members.forEach((n, i) => {
      plots.set(n.name, {
        x: dx + DISTRICT_PAD + (i % p.cols) * pitchX + geo.width / 2,
        z: dz + DISTRICT_PAD + Math.floor(i / p.cols) * pitchZ + geo.depth / 2,
      })
    })
  }

  const avenues: Avenue[] = avenueTops.map((top) => ({
    x: (AVENUE_EAST - GATE_ZONE) / 2, z: oz + top + AVENUE / 2, width: width + GATE_ZONE + AVENUE_EAST, depth: AVENUE,
  }))
  const queue: Rect = { x: 0, z: depth / 2 + 3.4, width: Math.max(14, width * 0.8), depth: 2.6 }
  return {
    districts, plots, queue, avenues, geometry: geo,
    // Comme les avenues : l'entrée des portes à l'ouest, la rue est à l'est.
    bounds: { x: (AVENUE_EAST - GATE_ZONE) / 2, z: 1.7, width: width + GATE_ZONE + AVENUE_EAST, depth: depth + 3.4 * 2 },
  }
}

/** Point (x, z) dans un quartier, bords compris. */
export function inDistrict(city: CityLayout, x: number, z: number): boolean {
  return city.districts.some((d) => Math.abs(x - d.x) <= d.width / 2 + 1e-6 && Math.abs(z - d.z) <= d.depth / 2 + 1e-6)
}

/** Hauteur de dessin d'un pod en (x, z) : jamais sous le dessus du socle dans un quartier (pas de traversée du flanc). */
export function drawnY(city: CityLayout, x: number, z: number, y: number): number {
  return inDistrict(city, x, z) ? Math.max(y, SOCLE_H) : y
}

/** Position du i-ème pod en attente dans la file. */
export function queuePosition(q: Rect, i: number): { x: number; z: number } {
  const perRow = Math.max(1, Math.floor((q.width - 0.6) / 1.05))
  return {
    x: q.x - q.width / 2 + 0.6 + (i % perRow) * 1.05,
    z: q.z - 0.5 + Math.floor(i / perRow) * 1.05,
  }
}
