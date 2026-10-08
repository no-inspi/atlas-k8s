import { useFrame } from '@react-three/fiber'
import { useEffect, useMemo, useRef } from 'react'
import * as THREE from 'three'
import { useCluster } from '../store/cluster'
import { SOCLE_H } from './layout'
import { inDistrict, isLit, splitAtDistricts, type Family, type Link, type Pt } from './links'
import { LOD_PX, questionTexture } from './Pods'
import type { Theme } from './theme'
import { tick } from './tick'
import { world, type Focus } from './world'

// Liens au sol : rubans plats (deux triangles par segment) sur lesquels un
// shader fait défiler des paquets (réseau) ou des gouttes (data). Une
// géométrie par famille : positions reconstruites quand les liens changent,
// opacité seule réécrite quand la sélection, le survol ou le filtre changent.

const WIDTH: Record<Family, number> = { main: 0.12, broken: 0.1, refused: 0.1, mirror: 0.07, fibre: 0.08, data: 0.16 }
const Y: Record<Family, number> = { main: 0.03, broken: 0.031, refused: 0.031, mirror: 0.0305, fibre: 0.032, data: 0.033 }
/** Motif : période (unités monde), part allumée, intensité entre deux paquets, vitesse. */
const DASH: Record<Family, { period: number; duty: number; base: number; speed: number }> = {
  main: { period: 1.2, duty: 0.3, base: 0.45, speed: 1.6 },
  fibre: { period: 0.9, duty: 0.35, base: 0.5, speed: 1.4 },
  data: { period: 0.7, duty: 0.25, base: 0.2, speed: 0.8 },
  broken: { period: 0.5, duty: 0.55, base: 0, speed: 0 },
  refused: { period: 0.5, duty: 0.55, base: 0, speed: 0 },
  // Miroir : pointillé fin et immobile, jamais de paquets.
  mirror: { period: 0.35, duty: 0.5, base: 0, speed: 0 },
}
const FAMILIES: Family[] = ['main', 'mirror', 'broken', 'refused', 'fibre', 'data']
/** Familles toujours dessinées en motif plein (pointillés), sans dépendre de live. */
const DASHED: ReadonlySet<Family> = new Set(['broken', 'refused', 'mirror'])

export interface Ribbon {
  position: Float32Array
  dist: Float32Array
  alpha: Float32Array
  live: Float32Array
  index: Uint32Array
}

export interface RibbonItem { points: readonly Pt[]; alpha: number; live: boolean }

/**
 * Rubans de largeur w le long des polylignes ; dist : abscisse curviligne (motif
 * continu aux coudes). lift : surélévation d'un segment selon son milieu (socles).
 */
export function ribbon(items: readonly RibbonItem[], w: number, y: number, lift?: (x: number, z: number) => number): Ribbon {
  let segs = 0
  for (const it of items) segs += Math.max(0, it.points.length - 1)
  const position = new Float32Array(segs * 12), dist = new Float32Array(segs * 4)
  const alpha = new Float32Array(segs * 4), live = new Float32Array(segs * 4), index = new Uint32Array(segs * 6)
  const h = w / 2
  let v = 0, k = 0
  for (const it of items) {
    const a = it.alpha, on = it.live ? 1 : 0
    let d = 0
    const heightOf = (i: number) => y + (lift ? lift((it.points[i][0] + it.points[i + 1][0]) / 2, (it.points[i][1] + it.points[i + 1][1]) / 2) : 0)
    for (let i = 0; i + 1 < it.points.length; i++) {
      const p0 = it.points[i], p1 = it.points[i + 1]
      const x0 = p0[0], z0 = p0[1], x1 = p1[0], z1 = p1[1]
      const len = Math.hypot(x1 - x0, z1 - z0) || 1e-6
      const ux = (x1 - x0) / len, uz = (z1 - z0) / len
      const nx = -uz * h, nz = ux * h
      // Chaque segment déborde d'une demi-largeur : les coudes restent fermés, sauf
      // là où la hauteur change (bord de socle) : le ruban ne dépasse pas le bord.
      const sy = heightOf(i)
      const f0 = i > 0 && heightOf(i - 1) !== sy ? 0 : h
      const f1 = i + 2 < it.points.length && heightOf(i + 1) !== sy ? 0 : h
      const sx0 = ux * f0, sz0 = uz * f0, sx1 = ux * f1, sz1 = uz * f1
      let o = v * 3
      position[o++] = x0 - sx0 + nx; position[o++] = sy; position[o++] = z0 - sz0 + nz
      position[o++] = x0 - sx0 - nx; position[o++] = sy; position[o++] = z0 - sz0 - nz
      position[o++] = x1 + sx1 + nx; position[o++] = sy; position[o++] = z1 + sz1 + nz
      position[o++] = x1 + sx1 - nx; position[o++] = sy; position[o] = z1 + sz1 - nz
      dist[v] = dist[v + 1] = d
      dist[v + 2] = dist[v + 3] = d + len
      alpha.fill(a, v, v + 4)
      live.fill(on, v, v + 4)
      index[k] = v; index[k + 1] = v + 1; index[k + 2] = v + 2
      index[k + 3] = v + 1; index[k + 4] = v + 3; index[k + 5] = v + 2
      v += 4
      k += 6
      d += len
    }
  }
  return { position, dist, alpha, live, index }
}

/**
 * Réécrit l'opacité de chaque polyligne d'un ruban déjà construit (mêmes
 * polylignes, même ordre), sans toucher aux positions. Renvoie true si une
 * valeur a changé.
 */
export function writeAlpha(alpha: Float32Array, items: readonly { points: readonly Pt[] }[], value: (i: number) => number): boolean {
  let v = 0, changed = false
  for (let i = 0; i < items.length; i++) {
    const n = Math.max(0, items[i].points.length - 1) * 4
    if (!n) continue
    const a = value(i)
    if (alpha[v] !== Math.fround(a)) {
      alpha.fill(a, v, v + n)
      changed = true
    }
    v += n
  }
  return changed
}

/**
 * Opacité d'un lien : 10 % hors du namespace filtré, 20 % hors du chemin d'une
 * sélection réseau ; un miroir est discret (60 %), une ligne de poids 0 pâle (35 %).
 */
export function linkAlpha(l: Link, nsFilter: string | null, focus: Focus): number {
  const base = nsFilter && l.ns !== nsFilter ? 0.1 : focus.dim && focus.path && !isLit(l, focus.path) ? 0.2 : 1
  return base * (l.family === 'mirror' ? 0.6 : l.weight === 0 ? 0.35 : 1)
}

/** Panneau « ⊘ » d'une route refusée par son Gateway. */
function refusedTexture(): THREE.Texture {
  const c = document.createElement('canvas')
  c.width = c.height = 64
  const g = c.getContext('2d')!
  g.fillStyle = '#C23E28'
  g.beginPath()
  g.arc(32, 32, 30, 0, Math.PI * 2)
  g.fill()
  g.strokeStyle = '#fff'
  g.lineWidth = 6
  g.beginPath()
  g.arc(32, 32, 16, 0, Math.PI * 2)
  g.moveTo(20.7, 43.3)
  g.lineTo(43.3, 20.7)
  g.stroke()
  const t = new THREE.CanvasTexture(c)
  t.colorSpace = THREE.SRGBColorSpace
  return t
}

const vertexShader = /* glsl */ `
attribute float aDist;
attribute float aAlpha;
attribute float aLive;
varying float vDist;
varying float vAlpha;
varying float vLive;
void main() {
  vDist = aDist;
  vAlpha = aAlpha;
  vLive = aLive;
  gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
}`

const fragmentShader = /* glsl */ `
uniform vec3 uColor;
uniform float uTime;
uniform float uPeriod;
uniform float uDuty;
uniform float uBase;
varying float vDist;
varying float vAlpha;
varying float vLive;
void main() {
  float phase = fract((vDist - uTime) / uPeriod);
  float on = phase < uDuty ? 1.0 : uBase;
  // Lien inactif (endpoint non ready, pod arrêté) : trait plein et pâle.
  gl_FragColor = vec4(uColor, vAlpha * mix(0.35, on, vLive));
}`

function geometryOf(r: Ribbon): THREE.BufferGeometry {
  const g = new THREE.BufferGeometry()
  g.setAttribute('position', new THREE.BufferAttribute(r.position, 3))
  g.setAttribute('aDist', new THREE.BufferAttribute(r.dist, 1))
  g.setAttribute('aAlpha', new THREE.BufferAttribute(r.alpha, 1))
  g.setAttribute('aLive', new THREE.BufferAttribute(r.live, 1))
  g.setIndex(new THREE.BufferAttribute(r.index, 1))
  return g
}

export function Links({ theme, reducedMotion }: { theme: Theme; reducedMotion: boolean }) {
  const meshes = useMemo(() => {
    const color: Record<Family, string> = {
      main: theme.fibre, mirror: theme.fibre, fibre: theme.fibre, data: theme.data, broken: theme.err, refused: theme.err,
    }
    return new Map(FAMILIES.map((f) => {
      const material = new THREE.ShaderMaterial({
        vertexShader, fragmentShader, transparent: true, depthWrite: false, side: THREE.DoubleSide,
        uniforms: {
          uColor: { value: new THREE.Color(color[f]) }, uTime: { value: 0 },
          uPeriod: { value: DASH[f].period }, uDuty: { value: DASH[f].duty }, uBase: { value: DASH[f].base },
        },
      })
      const mesh = new THREE.Mesh(new THREE.BufferGeometry(), material)
      mesh.frustumCulled = false
      mesh.raycast = () => {}
      mesh.renderOrder = 1
      return [f, mesh] as const
    }))
  }, [theme])
  useEffect(() => () => meshes.forEach((m) => { m.geometry.dispose(); (m.material as THREE.Material).dispose() }), [meshes])
  /**
   * Dernière construction. Positions : seulement quand les liens (ou les meshes,
   * recréés au changement de thème) changent ; fibres : aussi quand le focus ou
   * le niveau de détail change (elles n'existent que sur le chemin) ; sinon on
   * ne réécrit que l'opacité.
   */
  const built = useRef<{ links: Link[] | null; meshes: unknown; fibreKey: string; alphaKey: string; drawn: Map<Family, Link[]>; points: Map<Family, Pt[][]>; fibres: Link[] }>({
    links: null, meshes: null, fibreKey: '', alphaKey: '', drawn: new Map(), points: new Map(), fibres: [],
  })
  const question = useMemo(questionTexture, [])
  const refused = useMemo(refusedTexture, [])
  useEffect(() => () => { question.dispose(); refused.dispose() }, [question, refused])

  useFrame(({ clock, camera, invalidate }) => {
    const st = useCluster.getState()
    world.update(st)
    const focus = world.focusFor(st.selection, st.hoverNet, st.hover?.uid ?? null)
    const far = camera.zoom < LOD_PX
    const nsFilter = st.nsFilter
    const focusKey = `${st.selection?.type}:${st.selection?.key}|${st.hoverNet}|${st.hover?.uid}`
    const fibreKey = `${focusKey}|${far}`
    const alphaKey = `${focusKey}|${nsFilter}`
    const alphaOf = (l: Link) => linkAlpha(l, nsFilter, focus)
    const city = world.layout
    const lift = (x: number, z: number) => (city && inDistrict(city, x, z) ? SOCLE_H : 0)
    const setGeometry = (f: Family, ls: Link[]) => {
      const mesh = meshes.get(f)!
      // Coupés aux bords des quartiers : chaque segment est sur un socle ou au sol.
      const pts = ls.map((l) => (city ? splitAtDistricts(city, l.points) : [...l.points]))
      built.current.points.set(f, pts)
      mesh.geometry.dispose()
      mesh.geometry = geometryOf(ribbon(ls.map((l, i) => ({ points: pts[i], alpha: alphaOf(l), live: DASHED.has(l.family) || l.live })), WIDTH[f], Y[f], lift))
    }
    const b = built.current
    const topology = world.links !== b.links || meshes !== b.meshes
    const rebuilt = new Set<Family>()
    if (topology) {
      b.links = world.links
      b.meshes = meshes
      b.drawn = new Map(FAMILIES.map((f) => [f, []]))
      b.fibres = []
      for (const l of world.links) (l.family === 'fibre' ? b.fibres : b.drawn.get(l.family)!).push(l)
      for (const f of FAMILIES) if (f !== 'fibre') { setGeometry(f, b.drawn.get(f)!); rebuilt.add(f) }
    }
    if (topology || fibreKey !== b.fibreKey) {
      b.fibreKey = fibreKey
      // Fibres : seulement sur le chemin sélectionné ou survolé, et de près.
      const lit = far ? [] : b.fibres.filter((l) =>
        (!!focus.path && isLit(l, focus.path)) || (!!focus.hover && isLit(l, focus.hover)))
      b.drawn.set('fibre', lit)
      setGeometry('fibre', lit)
      rebuilt.add('fibre')
    }
    if (alphaKey !== b.alphaKey) {
      b.alphaKey = alphaKey
      for (const f of FAMILIES) {
        if (rebuilt.has(f)) continue
        const ls = b.drawn.get(f)!
        const attr = meshes.get(f)!.geometry.getAttribute('aAlpha') as THREE.BufferAttribute | undefined
        // Mêmes polylignes (coupées) que lors de la construction du ruban.
        const pts = b.points.get(f)
        if (attr && pts && writeAlpha(attr.array as Float32Array, pts.map((points) => ({ points })), (i) => alphaOf(ls[i]))) attr.needsUpdate = true
      }
    }
    if (!reducedMotion && !document.hidden && !far && world.links.length) {
      const t = clock.elapsedTime
      for (const [f, mesh] of meshes) (mesh.material as THREE.ShaderMaterial).uniforms.uTime.value = t * DASH[f].speed
      tick(invalidate)
    }
  })

  useCluster((s) => s.version)
  const nsFilter = useCluster((s) => s.nsFilter)
  const signs = world.links.filter((l) => l.sign)
  return (
    <>
      {[...meshes.values()].map((m, i) => <primitive key={i} object={m} />)}
      {signs.map((l, i) => (
        <sprite key={`sign-${i}`} position={[l.sign![0], 0.6, l.sign![1]]} scale={[0.45, 0.45, 1]} raycast={() => null} renderOrder={10}>
          <spriteMaterial map={l.family === 'refused' ? refused : question} depthTest={false} transparent opacity={nsFilter && l.ns !== nsFilter ? 0.1 : 1} />
        </sprite>
      ))}
    </>
  )
}
