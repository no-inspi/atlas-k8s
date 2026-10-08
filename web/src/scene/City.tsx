import { useMemo } from 'react'
import * as THREE from 'three'
import { useCluster } from '../store/cluster'
import { shortNode } from '../store/feed'
import { SOCLE_H, type CityLayout, type District, type DistrictStyle } from './layout'
import type { Theme } from './theme'
import { world } from './world'

// Décor statique : sol, rues, quartiers teintés, libellés peints au sol, arbres.
// Redessiné seulement quand les nodes ou le thème changent.

const textures = new Map<string, THREE.CanvasTexture>()

function labelTexture(lines: readonly string[], w: number, h: number, align: CanvasTextAlign, size: number, theme: Theme) {
  const key = [lines.join('\n'), w, h, align, size, theme.muted, theme.font].join('|')
  let tex = textures.get(key)
  if (tex) return tex
  const c = document.createElement('canvas')
  c.width = Math.round(w * 64)
  c.height = Math.round(h * 64)
  const g = c.getContext('2d')!
  const rows = lines.filter(Boolean)
  g.fillStyle = theme.muted
  g.textBaseline = 'middle'
  g.textAlign = align
  rows.forEach((text, i) => {
    // Première ligne en gras, les suivantes plus petites ; police réduite si le texte déborde.
    const weight = i === 0 ? 600 : 500
    let px = i === 0 ? size : Math.round(size * 0.72)
    g.font = `${weight} ${px}px ${theme.font}`
    while (px > 12 && g.measureText(text).width > c.width - 16) g.font = `${weight} ${(px -= 2)}px ${theme.font}`
    g.fillText(text, align === 'left' ? 8 : c.width / 2, (c.height * (i + 0.5)) / rows.length)
  })
  tex = new THREE.CanvasTexture(c)
  tex.colorSpace = THREE.SRGBColorSpace
  tex.anisotropy = 4
  if (textures.size > 400) textures.clear()
  textures.set(key, tex)
  return tex
}

function GroundLabel(props: { text: string | readonly string[]; w: number; h: number; x: number; z: number; y?: number; align?: CanvasTextAlign; size?: number; theme: Theme }) {
  const { text, w, h, x, z, y = 0.03, align = 'left', size = 56, theme } = props
  const map = labelTexture(typeof text === 'string' ? [text] : text, w, h, align, size, theme)
  return (
    <mesh rotation-x={-Math.PI / 2} position={[x, y, z]} raycast={() => null}>
      <planeGeometry args={[w, h]} />
      <meshBasicMaterial map={map} transparent depthWrite={false} />
    </mesh>
  )
}

function Plane({ w, d, x, z, y, color }: { w: number; d: number; x: number; z: number; y: number; color: string }) {
  return (
    <mesh rotation-x={-Math.PI / 2} position={[x, y, z]} receiveShadow raycast={() => null}>
      <planeGeometry args={[w, d]} />
      <meshStandardMaterial color={color} roughness={0.95} />
    </mesh>
  )
}

/** Socle d'un quartier : dessus de la couleur de zone, tranches assombries. */
function Socle({ d, color }: { d: District; color: string }) {
  const side = useMemo(() => '#' + new THREE.Color(color).multiplyScalar(0.75).getHexString(), [color])
  return (
    <mesh position={[d.x, SOCLE_H / 2, d.z]} castShadow receiveShadow raycast={() => null}>
      <boxGeometry args={[d.width, SOCLE_H, d.depth]} />
      {/* Faces de la boîte : +x, -x, +y (dessus), -y, +z, -z. */}
      {[0, 1, 2, 3, 4, 5].map((i) => (
        <meshStandardMaterial key={i} attach={`material-${i}`} color={i === 2 ? color : side} roughness={0.95} />
      ))}
    </mesh>
  )
}

/** Arbres disposés de façon déterministe autour de la ville. */
function treePositions(layout: CityLayout): [number, number, number][] {
  const { bounds } = layout
  const hw = bounds.width / 2 + 3, hd = bounds.depth / 2 + 3
  const out: [number, number, number][] = []
  const per = Math.max(3, Math.round((bounds.width + bounds.depth) / 5))
  for (let i = 0; i < per; i++) {
    const f = (i + 0.5) / per
    const j = Math.sin(i * 12.9898) * 0.5
    out.push([-hw + f * 2 * hw, 1, bounds.z - hd - 1 + j]) // derrière
    out.push([(i % 2 ? -1 : 1) * (hw + 1 + j), 1, bounds.z - hd + f * 2 * hd]) // côtés
  }
  return out.map(([x, , z], i) => [x, 0.8 + ((i * 37) % 5) / 10, z])
}

export function City({ theme }: { theme: Theme }) {
  // Les libellés des nodes changent avec le cordon : on s'abonne à cette seule information.
  const nodeKey = useCluster((s) => [...s.nodes.values()].map((n) => `${n.pool}/${n.name}/${n.unschedulable ? 1 : 0}`).sort().join(','))
  // Le réseau (avenues, tronçons, entrepôts) change avec le flux et les réglages d'affichage.
  useCluster((s) => s.version)
  useCluster((s) => s.podView)
  useCluster((s) => s.nsFilter)
  world.update(useCluster.getState())
  const layout = world.layout
  const net = world.net

  const zone: Record<DistrictStyle, string> = { std: theme.zoneStd, spot: theme.zoneSpot, gpu: theme.zoneGpu }
  const trees = useMemo(() => (layout ? treePositions(layout) : []), [layout])

  if (!layout) return null
  const { bounds, queue, geometry: g } = layout
  const cordoned = new Set(nodeKey.split(',').filter((k) => k.endsWith('/1')).map((k) => k.split('/')[1]))

  return (
    <group>
      <Plane w={600} d={600} x={0} z={0} y={0} color={theme.ground} />
      <Plane w={bounds.width + 5} d={bounds.depth + 4} x={bounds.x} z={bounds.z} y={0.005} color={theme.road} />
      {layout.districts.map((d) => {
        const w = Math.min(d.width - 0.4, 14)
        return (
          <group key={d.pool}>
            <Socle d={d} color={zone[d.style]} />
            <GroundLabel text={d.caption} w={w} h={1.5} x={d.x - d.width / 2 + 0.2 + w / 2} z={d.z + d.depth / 2 - 0.85} y={SOCLE_H + 0.02} theme={theme} />
          </group>
        )
      })}
      {[...layout.plots.entries()].map(([name, p]) => (
        <GroundLabel key={name} text={shortNode(name) + (cordoned.has(name) ? ' · cordon' : '')} w={g.width} h={0.7} x={p.x} z={p.z + g.depth / 2 + 0.45} align="center" size={34} y={SOCLE_H + 0.03} theme={theme} />
      ))}
      {layout.avenues.map((a, i) => (
        <Plane key={`avenue-${i}`} w={a.width} d={a.depth} x={a.x} z={a.z} y={0.012} color={theme.avenue} />
      ))}
      {net?.segments.map((s, i) => {
        const lanes = net.lanes[s.avenue]
        const w = s.x1 - s.x0
        return (
          <group key={`segment-${i}`}>
            <Plane w={w - 0.1} d={0.22} x={(s.x0 + s.x1) / 2} z={lanes.north + 0.13} y={0.016} color={world.colors.get(s.ns) ?? theme.muted} />
            <GroundLabel text={s.ns} w={Math.max(1.2, w - 0.1)} h={0.6} x={(s.x0 + s.x1) / 2} z={lanes.label} align="center" size={30} theme={theme} />
          </group>
        )
      })}
      {net?.warehouse && (
        <Plane w={net.warehouse.width} d={net.warehouse.depth} x={net.warehouse.x} z={net.warehouse.z} y={0.01} color={theme.warehouse} />
      )}
      {net?.islands.map((is) => (
        <group key={`island-${is.storageClass}`}>
          <Plane w={is.width} d={is.depth} x={is.x} z={is.z} y={0.014} color={theme.platform} />
          <GroundLabel text={is.storageClass} w={is.width - 0.4} h={0.8} x={is.x} z={is.z - is.depth / 2 + 0.5} align="center" size={34} theme={theme} />
        </group>
      ))}
      <Plane w={queue.width} d={queue.depth} x={queue.x} z={queue.z} y={0.01} color={theme.queue} />
      <GroundLabel text="File d'attente du scheduler (Pending)" w={14} h={1} x={queue.x - queue.width / 2 + 7} z={queue.z - queue.depth / 2 - 0.55} theme={theme} />
      {trees.map(([x, s, z], i) => (
        <group key={i} position={[x, 0, z]} scale={s}>
          <mesh position-y={1.25} castShadow raycast={() => null}>
            <coneGeometry args={[0.7, 1.8, 8]} />
            <meshStandardMaterial color={theme.tree} roughness={0.95} />
          </mesh>
          <mesh position-y={0.25} raycast={() => null}>
            <cylinderGeometry args={[0.12, 0.12, 0.5, 6]} />
            <meshStandardMaterial color="#8B7A66" />
          </mesh>
        </group>
      ))}
    </group>
  )
}
