import { GATE_ZONE, type Avenue, type CityLayout, type Rect } from './layout'

// Disposition du réseau et du stockage : relais (Services) rangés par
// namespace sur les avenues, portes à l'entrée ouest de la première avenue,
// citernes (PVC) dans un quartier Entrepôts à l'est. Fonction pure : ne dépend
// que de la ville et des clés reçues.

export const RELAY_PITCH = 1.3
const RELAY_ROW = 0.85 // centre des relais depuis le bord nord de l'avenue
const GATE_PITCH = 2.2
const TANK_PITCH = 1.9
const TANK_COLS = 3
const ISLAND_LABEL = 1.0
const ISLAND_GAP = 0.8
const WAREHOUSE_GAP = 3.2 // entre la ville et les entrepôts, rue est comprise
const GiB = 2 ** 30
const UNCLASSED = '(aucune)'

export interface RelayInput { key: string; namespace: string; name: string }
export interface TankInput { key: string; namespace: string; name: string; storageClass: string; requested: number }

export interface RelaySlot {
  key: string
  ns: string
  x: number
  z: number
  avenue: number
  /** Bloc qui représente ce relais quand son namespace est regroupé. */
  group?: string
}
export interface RelayGroup { key: string; ns: string; x: number; z: number; avenue: number; members: string[] }
export interface Segment { ns: string; avenue: number; x0: number; x1: number }
export interface GateSlot { name: string; x: number; z: number }
export interface Island { storageClass: string; x: number; z: number; width: number; depth: number }
export interface TankSlot { key: string; x: number; z: number; r: number }
/** Repères d'une avenue (z) : bord nord, voies des lignes principales, des fibres et des conduites, noms des tronçons. */
export interface Lanes { north: number; main: number; fibre: number; data: number; label: number }

export interface NetLayout {
  segments: Segment[]
  relays: Map<string, RelaySlot>
  groups: RelayGroup[]
  gates: Map<string, GateSlot>
  islands: Island[]
  tanks: Map<string, TankSlot>
  warehouse: Rect | null
  lanes: Lanes[]
  /** Rue ouest (portes ↔ avenues) et rue est (entrepôts ↔ avenues). */
  westX: number
  eastX: number
  bounds: Rect
}

export function lanesOf(a: Avenue): Lanes {
  return { north: a.z - a.depth / 2, main: a.z - 0.05, fibre: a.z + 0.45, data: a.z + 0.95, label: a.z + 1.45 }
}

/** Rayon d'une citerne : logarithme de la capacité demandée, entre 0,35 et 0,8. */
export const tankRadius = (bytes: number) => Math.min(0.8, Math.max(0.35, 0.35 + 0.1 * Math.log2(Math.max(1, bytes / GiB))))

function union(a: Rect, b: Rect | null): Rect {
  if (!b) return a
  const x0 = Math.min(a.x - a.width / 2, b.x - b.width / 2), x1 = Math.max(a.x + a.width / 2, b.x + b.width / 2)
  const z0 = Math.min(a.z - a.depth / 2, b.z - b.depth / 2), z1 = Math.max(a.z + a.depth / 2, b.z + b.depth / 2)
  return { x: (x0 + x1) / 2, z: (z0 + z1) / 2, width: x1 - x0, depth: z1 - z0 }
}

export function layoutNetwork(city: CityLayout, relays: RelayInput[], gates: string[], tanks: TankInput[]): NetLayout {
  const avenues = city.avenues
  const lanes = avenues.map(lanesOf)
  const left = Math.min(...city.districts.map((d) => d.x - d.width / 2))
  const right = Math.max(...city.districts.map((d) => d.x + d.width / 2))
  const top = Math.min(...city.districts.map((d) => d.z - d.depth / 2))
  const a0 = avenues[0]
  const westX = left - 1.9
  const eastX = right + 1.4

  // Places des relais, de l'ouest vers l'est, avenue après avenue.
  const slots: { avenue: number; x: number }[] = []
  avenues.forEach((a, i) => {
    const x0 = a.x - a.width / 2 + GATE_ZONE + 0.6, x1 = a.x + a.width / 2 - 0.6
    for (let x = x0 + RELAY_PITCH / 2; x <= x1 - RELAY_PITCH / 2 + 1e-9; x += RELAY_PITCH) slots.push({ avenue: i, x })
  })
  if (!slots.length) slots.push({ avenue: 0, x: a0.x })

  const byNs = new Map<string, RelayInput[]>()
  for (const r of relays) byNs.set(r.namespace, [...(byNs.get(r.namespace) ?? []), r])
  const nss = [...byNs.keys()].sort()
  for (const ns of nss) byNs.get(ns)!.sort((a, b) => a.name.localeCompare(b.name))

  // Trop de relais : on regroupe les plus gros namespaces en un seul bloc jusqu'à tenir.
  const grouped = new Set<string>()
  const need = () => nss.reduce((s, ns) => s + (grouped.has(ns) ? 1 : byNs.get(ns)!.length), 0) + Math.max(0, nss.length - 1)
  const bySize = [...nss].sort((a, b) => byNs.get(b)!.length - byNs.get(a)!.length || a.localeCompare(b))
  for (const ns of bySize) {
    if (need() <= slots.length) break
    if (byNs.get(ns)!.length > 1) grouped.add(ns)
  }

  const out = new Map<string, RelaySlot>()
  const groups: RelayGroup[] = []
  const segments: Segment[] = []
  let next = 0
  const take = () => slots[Math.min(next++, slots.length - 1)] // au-delà : empilés sur la dernière place
  for (const ns of nss) {
    const items = byNs.get(ns)!
    const used: { avenue: number; x: number }[] = []
    if (grouped.has(ns)) {
      const s = take()
      const key = `ns:${ns}`
      const z = lanes[s.avenue].north + RELAY_ROW
      groups.push({ key, ns, x: s.x, z, avenue: s.avenue, members: items.map((r) => r.key) })
      for (const r of items) out.set(r.key, { key: r.key, ns, x: s.x, z, avenue: s.avenue, group: key })
      used.push(s)
    } else {
      for (const r of items) {
        const s = take()
        out.set(r.key, { key: r.key, ns, x: s.x, z: lanes[s.avenue].north + RELAY_ROW, avenue: s.avenue })
        used.push(s)
      }
    }
    // Un tronçon par avenue traversée.
    for (const s of used) {
      const last = segments[segments.length - 1]
      if (last && last.ns === ns && last.avenue === s.avenue) last.x1 = s.x + RELAY_PITCH / 2
      else segments.push({ ns, avenue: s.avenue, x0: s.x - RELAY_PITCH / 2, x1: s.x + RELAY_PITCH / 2 })
    }
    next++ // une place libre entre deux namespaces
  }

  // Portes : en colonne à l'entrée de la première avenue.
  const names = [...new Set(gates)].sort()
  const gx = a0.x - a0.width / 2 + 1.2
  const gateSlots = new Map(names.map((name, k) => [name, { name, x: gx, z: a0.z + (k - (names.length - 1) / 2) * GATE_PITCH }]))

  // Entrepôts : un îlot par StorageClass, empilés du nord au sud.
  const byClass = new Map<string, TankInput[]>()
  for (const t of tanks) {
    const c = t.storageClass || UNCLASSED
    byClass.set(c, [...(byClass.get(c) ?? []), t])
  }
  const wx = right + WAREHOUSE_GAP
  const width = TANK_COLS * TANK_PITCH + 0.8
  const islands: Island[] = []
  const tankSlots = new Map<string, TankSlot>()
  let z = top
  for (const c of [...byClass.keys()].sort()) {
    const ts = byClass.get(c)!.sort((a, b) => a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name))
    const rows = Math.ceil(ts.length / TANK_COLS)
    const depth = ISLAND_LABEL + rows * TANK_PITCH + 0.4
    islands.push({ storageClass: c, x: wx + width / 2, z: z + depth / 2, width, depth })
    ts.forEach((t, k) => tankSlots.set(t.key, {
      key: t.key,
      x: wx + 0.4 + TANK_PITCH * ((k % TANK_COLS) + 0.5),
      z: z + ISLAND_LABEL + TANK_PITCH * (Math.floor(k / TANK_COLS) + 0.5),
      r: tankRadius(t.requested),
    }))
    z += depth + ISLAND_GAP
  }
  const zEnd = z - ISLAND_GAP
  const warehouse = islands.length ? { x: wx + width / 2, z: (top + zEnd) / 2, width: width + 1, depth: zEnd - top + 1 } : null

  return {
    segments, relays: out, groups, gates: gateSlots, islands, tanks: tankSlots, warehouse, lanes, westX, eastX,
    bounds: union(city.bounds, warehouse && { ...warehouse, width: warehouse.width + 2, depth: warehouse.depth + 2 }),
  }
}
