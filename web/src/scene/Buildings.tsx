import { useFrame } from '@react-three/fiber'
import { useEffect, useMemo, useRef } from 'react'
import * as THREE from 'three'
import type { Node } from '../api/types'
import { useCluster } from '../store/cluster'
import { Part, opacityMaterial } from './instanced'
import { nodeStyle, type PlotGeometry } from './layout'
import { pickables } from './pick'
import { LOD_PX } from './Robots'
import type { Theme } from './theme'
import { world } from './world'

// Un node est un bâtiment sur sa parcelle : maison (standard), tente (spot) ou
// bâtiment sombre à ventilateurs (GPU). Géométries reprises du prototype ; les
// coordonnées z sont relatives au fond de la parcelle (bz).

type Style = 'std' | 'spot' | 'gpu'
type Vec = [number, number, number]

interface BPart {
  name: string
  style: Style | 'all'
  geo: THREE.BufferGeometry
  mat: string
  /** Une position par instance de la pièce sur un node. */
  at: (g: PlotGeometry) => Vec[]
  scale?: (g: PlotGeometry) => Vec
  cast?: boolean
  receive?: boolean
  opacity?: boolean
  dynamic?: boolean // position recalculée à chaque frame (fumée, pales, drapeau, alerte)
  closeUp?: boolean // seulement en zoom rapproché (fumée, pales)
}

const box = (w: number, h: number, d: number) => new THREE.BoxGeometry(w, h, d)
const roofGeo = (r: number, h: number) => new THREE.ConeGeometry(r, h, 4).rotateY(Math.PI / 4)
const front = (g: PlotGeometry) => g.depth - 0.6 // z de la barrière, depuis le fond

const PARTS: BPart[] = [
  { name: 'platform', style: 'all', geo: box(1, 0.5, 1), mat: 'platform', at: (g) => [[0, 0.25, g.depth / 2 - 0.5]], scale: (g) => [g.width, 1, g.depth], cast: true, receive: true },
  { name: 'postL', style: 'all', geo: box(0.12, 0.55, 0.12), mat: 'post', at: (g) => [[-(g.width / 2 - 0.2), 0.78, front(g)]], cast: true },
  { name: 'postR', style: 'all', geo: box(0.12, 0.55, 0.12), mat: 'post', at: (g) => [[g.width / 2 - 0.2, 0.78, front(g)]], cast: true },
  { name: 'rail', style: 'all', geo: box(1, 0.14, 0.08), mat: 'stripe', at: (g) => [[0, 0.98, front(g)]], scale: (g) => [g.width - 0.5, 1, 1], cast: true },
  { name: 'alert', style: 'all', geo: new THREE.OctahedronGeometry(0.32), mat: 'alert', at: () => [[0, 3.2, 0]], dynamic: true },
  // Maison
  { name: 'wall', style: 'std', geo: box(3.4, 1.05, 0.8), mat: 'house', at: () => [[0, 1.02, 0]], cast: true, receive: true },
  { name: 'window', style: 'std', geo: box(0.38, 0.32, 0.02), mat: 'window', at: () => [-1.05, -0.35, 0.35, 1.05].map((x) => [x, 1.1, 0.41] as Vec) },
  { name: 'roof', style: 'std', geo: roofGeo(0.6, 0.55), mat: 'roof', at: () => [[0, 1.82, 0]], scale: () => [4.3, 1, 1.05], cast: true },
  { name: 'chimney', style: 'std', geo: box(0.24, 0.55, 0.24), mat: 'roof', at: () => [[1.15, 2.0, -0.15]], cast: true },
  { name: 'puff', style: 'std', geo: new THREE.SphereGeometry(0.16, 10, 8), mat: 'puff', at: () => [0, 1, 2, 3, 4].map(() => [1.15, 2.3, -0.15] as Vec), opacity: true, dynamic: true, closeUp: true },
  // Tente (spot)
  { name: 'canvas', style: 'spot', geo: box(3.4, 0.45, 0.8), mat: 'canvas', at: () => [[0, 0.72, 0]], cast: true, receive: true },
  { name: 'tentRoof', style: 'spot', geo: roofGeo(0.6, 0.6), mat: 'tentRoof', at: () => [[0, 1.2, 0]], scale: () => [4.6, 1, 1], cast: true },
  { name: 'pole', style: 'spot', geo: box(0.04, 0.7, 0.04), mat: 'pole', at: () => [[1.4, 1.6, 0]], cast: true },
  { name: 'flag', style: 'spot', geo: new THREE.PlaneGeometry(0.5, 0.3).translate(0.25, 0, 0), mat: 'flag', at: () => [[1.42, 1.8, 0]], dynamic: true },
  // GPU
  { name: 'gpuWall', style: 'gpu', geo: box(3.5, 1.25, 0.85), mat: 'gpuWall', at: () => [[0, 1.12, 0]], cast: true, receive: true },
  { name: 'strip', style: 'gpu', geo: box(3.2, 0.07, 0.02), mat: 'strip', at: () => [[0, 1.45, 0.435]] },
  { name: 'housing', style: 'gpu', geo: box(0.75, 0.18, 0.6), mat: 'gpuWall', at: () => [[-0.9, 1.84, 0], [0.9, 1.84, 0]], cast: true },
  { name: 'blade', style: 'gpu', geo: box(0.62, 0.03, 0.1), mat: 'blade', at: () => [[-0.9, 1.95, 0], [-0.9, 1.95, 0], [0.9, 1.95, 0], [0.9, 1.95, 0]], dynamic: true, closeUp: true },
]

/** Pièces dont un clic sélectionne le node (une instance par node). */
const PICK = ['platform', 'wall', 'canvas', 'gpuWall']

const isReady = (n: Node) => n.conditions.find((c) => c.type === 'Ready')?.status === 'True'

function makeMaterials(theme: Theme): Record<string, THREE.Material> {
  const std = (color: string, extra: THREE.MeshStandardMaterialParameters = {}) => new THREE.MeshStandardMaterial({ color, roughness: 0.8, ...extra })
  return {
    platform: std('#ffffff', { roughness: 0.7 }),
    post: std('#3A3A3A'),
    stripe: std('#E0A63A'),
    alert: new THREE.MeshBasicMaterial({ color: theme.err }),
    house: std('#ffffff'),
    window: new THREE.MeshBasicMaterial({ color: '#ffffff' }),
    roof: std(theme.roof),
    puff: opacityMaterial({ color: '#ffffff', depthWrite: false, roughness: 1 }),
    canvas: std('#EFE3CF', { roughness: 0.9 }),
    tentRoof: std('#C4574B', { roughness: 0.85 }),
    pole: std('#6B5A49'),
    flag: std('#E0A63A', { side: THREE.DoubleSide }),
    gpuWall: std('#3E4A54', { roughness: 0.6 }),
    strip: new THREE.MeshBasicMaterial({ color: '#5BE39A' }),
    blade: std('#C9D3D8'),
  }
}

const WINDOW_OFF = new THREE.Color('#2B3A41')
const WINDOW_ON = new THREE.Color('#F6C76B')
const GREY = new THREE.Color('#8A9499')

export function Buildings({ theme }: { theme: Theme }) {
  const group = useRef<THREE.Group>(null)
  const materials = useMemo(() => makeMaterials(theme), [theme])
  const parts = useRef(new Map<string, Part>())
  const built = useRef({ key: '', nodes: [] as Node[] })
  const fans = useRef(new Map<string, number>())

  const palette = useMemo(() => ({
    platform: new THREE.Color(theme.platform), house: new THREE.Color(theme.house),
    warn: new THREE.Color(theme.warn), accent: new THREE.Color(theme.accent),
  }), [theme])

  useEffect(() => () => {
    parts.current.forEach((p) => p.dispose())
    Object.values(materials).forEach((m) => m.dispose())
  }, [materials])

  const tmp = useMemo(() => ({
    m: new THREE.Matrix4(), pos: new THREE.Vector3(), quat: new THREE.Quaternion(), scale: new THREE.Vector3(),
    color: new THREE.Color(), up: new THREE.Vector3(0, 1, 0),
  }), [])

  useFrame(({ clock, camera, invalidate }, delta) => {
    const st = useCluster.getState()
    world.update(st)
    const layout = world.layout
    if (!layout) return
    const g = layout.geometry
    const nodes = world.nodes.filter((n) => layout.plots.has(n.name))
    // Fumée et pales seulement de près (robots d'au moins 3 fois le seuil de niveau de détail).
    const closeUp = camera.zoom * 1.2 >= 3 * LOD_PX
    const t = clock.elapsedTime
    const sel = st.selection?.type === 'node' ? st.selection.key : null

    // (Re)crée les pièces quand le nombre de nodes ou le matériau change.
    const key = `${nodes.length}:${materials.platform.uuid}`
    if (key !== built.current.key) {
      parts.current.forEach((p) => { group.current?.remove(p.mesh); p.dispose() })
      parts.current = new Map(PARTS.map((d) => [d.name, new Part(d.geo, materials[d.mat], Math.max(1, nodes.length * d.at(g).length), {
        castShadow: d.cast, receiveShadow: d.receive, opacity: d.opacity, name: d.name,
      })]))
      parts.current.forEach((p) => group.current?.add(p.mesh))
      pickables.nodes.meshes = PICK.map((n) => parts.current.get(n)!.mesh)
      built.current.key = key
    }
    pickables.nodes.names = nodes.map((n) => n.name)

    const { m, pos, quat, scale, color, up } = tmp
    nodes.forEach((n, ni) => {
      const plot = layout.plots.get(n.name)!
      const style = nodeStyle(n)
      // Un node anonyme n'a pas de conditions connues : pas d'alerte, juste grisé.
      const ready = isReady(n) || !!n.ghost
      const load = Math.min(1, n.requested.cpu / Math.max(1, n.allocatable.cpu))
      const bz = plot.z - g.depth / 2 + 0.5
      const fan = (fans.current.get(n.name) ?? 0) + Math.min(delta, 0.05) * (2 + load * 18)
      fans.current.set(n.name, fan)

      for (const d of PARTS) {
        const part = parts.current.get(d.name)!
        const offsets = d.at(g)
        offsets.forEach(([x, y, z], j) => {
          const idx = ni * offsets.length + j
          const shown =
            (d.style === 'all' || d.style === style) &&
            (!d.closeUp || closeUp) &&
            (!['postL', 'postR', 'rail'].includes(d.name) || n.unschedulable) &&
            (d.name !== 'alert' || !ready)
          if (!shown) return part.hide(idx)

          const [sx, sy, sz] = d.scale ? d.scale(g) : [1, 1, 1]
          pos.set(plot.x + x, y, bz + z)
          scale.set(sx, sy, sz)
          quat.identity()
          if (d.name === 'puff') {
            const ph = (t * 0.45 + j / offsets.length) % 1
            pos.set(plot.x + x + Math.sin(ph * 4 + j) * 0.12, y + ph * 1.5, bz + z + ph * 0.3)
            scale.setScalar(0.5 + ph * 1.3)
            part.setOpacity(idx, ready ? (1 - ph) * 0.55 * Math.min(1, load * 1.4) : 0)
          } else if (d.name === 'blade') {
            quat.setFromAxisAngle(up, (ready ? fan : 0) + (j % 2) * (Math.PI / 2))
          } else if (d.name === 'flag') {
            quat.setFromAxisAngle(up, Math.sin(t * 3 + plot.x) * 0.35)
          } else if (d.name === 'alert') {
            pos.y += Math.sin(t * 3) * 0.12
            quat.setFromAxisAngle(up, t)
          }
          part.setMatrix(idx, m.compose(pos, quat, scale))
        })
      }

      // Couleurs par instance : parcelle (cordon, sélection), murs et fenêtres (NotReady, charge).
      color.copy(palette.platform)
      if (n.unschedulable) color.lerp(palette.warn, 0.35 + 0.1 * Math.sin(t * 2))
      if (n.name === sel) color.lerp(palette.accent, 0.25)
      parts.current.get('platform')!.setColor(ni, color)

      color.copy(palette.house)
      if (!ready || n.ghost) color.lerp(GREY, 0.6)
      parts.current.get('wall')!.setColor(ni, color)

      color.copy(WINDOW_OFF)
      if (ready && theme.dark) color.lerp(WINDOW_ON, 0.5 + load * 0.5)
      for (let j = 0; j < 4; j++) parts.current.get('window')!.setColor(ni * 4 + j, color)
    })

    for (const d of PARTS) parts.current.get(d.name)!.commit(nodes.length * d.at(g).length)
    // Fumée, pales, drapeaux, cordons et alertes s'animent : frames continues de près seulement.
    if (closeUp && nodes.length && !document.hidden) invalidate()
  })

  return <group ref={group} />
}
