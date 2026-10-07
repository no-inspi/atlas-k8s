import { GATE_ZONE, type Avenue, type CityLayout, type Rect } from './layout'

// Disposition du réseau et du stockage : relais (Services) rangés par
// namespace sur les avenues, portes à l'entrée ouest de la première avenue,
// citernes (PVC) dans un quartier Entrepôts à l'est. Fonction pure : ne dépend
// que de la ville et des clés reçues.

export const RELAY_PITCH = 1.3
/** Pas minimal avant de regrouper des namespaces. */
export const RELAY_PITCH_MIN = 0.65
/** Marge entre le dernier relais et le bout est de l'avenue (la rue est passe à 0,6 du bout). */
const RELAY_END = 1.6
const RELAY_ROW = 0.85 // centre des relais depuis le bord nord de l'avenue
const GATE_PITCH = 2.2
export const TANK_PITCH = 1.9
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

/** Ordre des noms indépendant de la locale du navigateur. */
export const byName = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0)

/** Bande de chaque avenue où se posent les relais, entre les portes et la rue est. */
const relayBands = (avenues: Avenue[]) =>
  avenues.map((a) => ({ x0: a.x - a.width / 2 + GATE_ZONE + 0.6, x1: a.x + a.width / 2 - RELAY_END }))

/** Nombre de places au pas p. */
const capacity = (bands: { x0: number; x1: number }[], p: number) =>
  bands.reduce((n, b) => n + Math.max(0, Math.floor((b.x1 - b.x0) / p + 1e-9)), 0)

/** Plus grand pas ≤ hi qui offre au moins need places (recherche dichotomique). */
function fitPitch(bands: { x0: number; x1: number }[], need: number, hi: number): number {
  if (capacity(bands, hi) >= need) return hi
  const longest = Math.max(...bands.map((b) => b.x1 - b.x0))
  let lo = longest / need // une seule avenue suffit à ce pas
  for (let k = 0; k < 60; k++) {
    const mid = (lo + hi) / 2
    if (capacity(bands, mid) >= need) lo = mid
    else hi = mid
  }
  return lo
}

function emptyNetwork(city: CityLayout): NetLayout {
  return {
    segments: [], relays: new Map(), groups: [], gates: new Map(), islands: [], tanks: new Map(), warehouse: null,
    lanes: [], westX: 0, eastX: 0, bounds: city.bounds,
  }
}

export function layoutNetwork(city: CityLayout, relays: RelayInput[], gates: string[], tanks: TankInput[]): NetLayout {
  const avenues = city.avenues
  if (!city.districts.length || !avenues.length) return emptyNetwork(city)
  const lanes = avenues.map(lanesOf)
  const left = Math.min(...city.districts.map((d) => d.x - d.width / 2))
  const right = Math.max(...city.districts.map((d) => d.x + d.width / 2))
  const top = Math.min(...city.districts.map((d) => d.z - d.depth / 2))
  const bottom = Math.max(...city.districts.map((d) => d.z + d.depth / 2), ...avenues.map((a) => a.z + a.depth / 2))
  const a0 = avenues[0]
  const westX = left - 1.9
  const eastX = right + 1.4

  const byNs = new Map<string, RelayInput[]>()
  for (const r of relays) {
    const list = byNs.get(r.namespace)
    if (list) list.push(r)
    else byNs.set(r.namespace, [r])
  }
  const nss = [...byNs.keys()].sort(byName)
  for (const ns of nss) byNs.get(ns)!.sort((a, b) => byName(a.name, b.name))

  // Pas des relais : RELAY_PITCH avec une place libre entre deux namespaces si
  // tout tient ; sinon pas resserré jusqu'à RELAY_PITCH_MIN ; sinon plus de
  // place libre et les plus gros namespaces se replient en un bloc ; sinon un
  // bloc par namespace, au pas qu'il faut. Jamais deux objets au même endroit.
  const bands = relayBands(avenues)
  const total = relays.length
  const grouped = new Set<string>()
  let gap = 1
  let pitch = fitPitch(bands, total + nss.length - 1, RELAY_PITCH)
  if (pitch < RELAY_PITCH_MIN) {
    pitch = RELAY_PITCH_MIN
    gap = 0
    const cap = capacity(bands, pitch)
    let need = total
    const bySize = [...nss].sort((a, b) => byNs.get(b)!.length - byNs.get(a)!.length || byName(a, b))
    for (const ns of bySize) {
      if (need <= cap) break
      const n = byNs.get(ns)!.length
      if (n > 1) { grouped.add(ns); need -= n - 1 }
    }
    if (need > cap) pitch = fitPitch(bands, nss.length, RELAY_PITCH_MIN)
  }

  // Places, de l'ouest vers l'est, avenue après avenue.
  const slots: { avenue: number; x: number }[] = []
  bands.forEach((b, i) => {
    const n = Math.max(0, Math.floor((b.x1 - b.x0) / pitch + 1e-9))
    for (let j = 0; j < n; j++) slots.push({ avenue: i, x: b.x0 + pitch * (j + 0.5) })
  })

  const out = new Map<string, RelaySlot>()
  const groups: RelayGroup[] = []
  const segments: Segment[] = []
  let next = 0
  const take = () => slots[next++]
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
      if (last && last.ns === ns && last.avenue === s.avenue) last.x1 = s.x + pitch / 2
      else segments.push({ ns, avenue: s.avenue, x0: s.x - pitch / 2, x1: s.x + pitch / 2 })
    }
    next += gap // une place libre entre deux namespaces, quand il y a la place
  }

  // Portes : en colonne à l'entrée de la première avenue.
  const names = [...new Set(gates)].sort(byName)
  const gx = a0.x - a0.width / 2 + 1.2
  const gateSlots = new Map(names.map((name, k) => [name, { name, x: gx, z: a0.z + (k - (names.length - 1) / 2) * GATE_PITCH }]))

  // Entrepôts : un îlot par StorageClass, empilés du nord au sud, assez de
  // colonnes pour rester à peu près aussi profonds que la ville.
  const byClass = new Map<string, TankInput[]>()
  for (const t of tanks) {
    const c = t.storageClass || UNCLASSED
    const list = byClass.get(c)
    if (list) list.push(t)
    else byClass.set(c, [t])
  }
  const classes = [...byClass.keys()].sort(byName)
  const islandDepth = (n: number, cols: number) => ISLAND_LABEL + Math.ceil(n / cols) * TANK_PITCH + 0.4
  const depthWith = (cols: number) =>
    classes.reduce((d, c) => d + islandDepth(byClass.get(c)!.length, cols), 0) + ISLAND_GAP * Math.max(0, classes.length - 1)
  const largest = Math.max(0, ...classes.map((c) => byClass.get(c)!.length))
  let cols = Math.max(TANK_COLS, Math.ceil(tanks.length / Math.max(1, Math.floor((bottom - top) / TANK_PITCH))))
  while (cols < largest && depthWith(cols) > bottom - top) cols++

  const q = city.queue
  const wx = Math.max(right, q.x + q.width / 2) + WAREHOUSE_GAP
  const width = cols * TANK_PITCH + 0.8
  const islands: Island[] = []
  const tankSlots = new Map<string, TankSlot>()
  let z = top
  for (const c of classes) {
    const ts = byClass.get(c)!.sort((a, b) => byName(a.namespace, b.namespace) || byName(a.name, b.name))
    const depth = islandDepth(ts.length, cols)
    islands.push({ storageClass: c, x: wx + width / 2, z: z + depth / 2, width, depth })
    ts.forEach((t, k) => tankSlots.set(t.key, {
      key: t.key,
      x: wx + 0.4 + TANK_PITCH * ((k % cols) + 0.5),
      z: z + ISLAND_LABEL + TANK_PITCH * (Math.floor(k / cols) + 0.5),
      r: tankRadius(t.requested),
    }))
    z += depth + ISLAND_GAP
  }
  const zEnd = z - ISLAND_GAP
  const warehouse = islands.length ? { x: wx + width / 2, z: (top + zEnd) / 2, width: width + 1, depth: zEnd - top + 1 } : null

  // Limites : la ville, les portes (leur colonne peut dépasser l'avenue) et les entrepôts.
  let bounds = union(city.bounds, warehouse && { ...warehouse, width: warehouse.width + 2, depth: warehouse.depth + 2 })
  if (names.length) {
    const span = (names.length - 1) * GATE_PITCH + 2 * GATE_PITCH
    bounds = union(bounds, { x: gx, z: a0.z, width: 2, depth: span })
  }
  return { segments, relays: out, groups, gates: gateSlots, islands, tanks: tankSlots, warehouse, lanes, westX, eastX, bounds }
}
