import { useFrame } from '@react-three/fiber'
import { useEffect, useMemo, useRef } from 'react'
import * as THREE from 'three'
import { useCluster } from '../store/cluster'
import { isLit, type Family } from './links'
import { LOD_PX, questionTexture } from './Pods'
import type { Theme } from './theme'
import { tick } from './tick'
import { world } from './world'

// Liens au sol : rubans plats (deux triangles par segment) sur lesquels un
// shader fait défiler des paquets (réseau) ou des gouttes (data). Une
// géométrie par famille, reconstruite seulement quand les liens, la sélection,
// le survol ou le niveau de détail changent.

const WIDTH: Record<Family, number> = { main: 0.12, broken: 0.1, fibre: 0.08, data: 0.16 }
const Y: Record<Family, number> = { main: 0.03, broken: 0.031, fibre: 0.032, data: 0.033 }
/** Motif : période (unités monde), part allumée, intensité entre deux paquets, vitesse. */
const DASH: Record<Family, { period: number; duty: number; base: number; speed: number }> = {
  main: { period: 1.2, duty: 0.3, base: 0.45, speed: 1.6 },
  fibre: { period: 0.9, duty: 0.35, base: 0.5, speed: 1.4 },
  data: { period: 0.7, duty: 0.25, base: 0.2, speed: 0.8 },
  broken: { period: 0.5, duty: 0.55, base: 0, speed: 0 },
}
const FAMILIES: Family[] = ['main', 'broken', 'fibre', 'data']

export interface Ribbon {
  position: Float32Array
  dist: Float32Array
  alpha: Float32Array
  live: Float32Array
  index: Uint32Array
}

/** Rubans de largeur w le long des polylignes ; dist : abscisse curviligne (motif continu aux coudes). */
export function ribbon(items: { points: [number, number][]; alpha: number; live: boolean }[], w: number, y: number): Ribbon {
  let segs = 0
  for (const it of items) segs += Math.max(0, it.points.length - 1)
  const position = new Float32Array(segs * 12), dist = new Float32Array(segs * 4)
  const alpha = new Float32Array(segs * 4), live = new Float32Array(segs * 4), index = new Uint32Array(segs * 6)
  let v = 0, k = 0
  for (const it of items) {
    let d = 0
    for (let i = 0; i + 1 < it.points.length; i++) {
      const [x0, z0] = it.points[i], [x1, z1] = it.points[i + 1]
      const len = Math.hypot(x1 - x0, z1 - z0) || 1e-6
      const ux = (x1 - x0) / len, uz = (z1 - z0) / len
      const nx = -uz * w / 2, nz = ux * w / 2
      // Chaque segment déborde d'une demi-largeur : les coudes restent fermés.
      const ex = ux * w / 2, ez = uz * w / 2
      const corners: [number, number, number][] = [
        [x0 - ex + nx, z0 - ez + nz, d], [x0 - ex - nx, z0 - ez - nz, d],
        [x1 + ex + nx, z1 + ez + nz, d + len], [x1 + ex - nx, z1 + ez - nz, d + len],
      ]
      for (const [x, z, dd] of corners) {
        position.set([x, y, z], v * 3)
        dist[v] = dd
        alpha[v] = it.alpha
        live[v] = it.live ? 1 : 0
        v++
      }
      const b = v - 4
      index.set([b, b + 1, b + 2, b + 1, b + 3, b + 2], k)
      k += 6
      d += len
    }
  }
  return { position, dist, alpha, live, index }
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
    const color: Record<Family, string> = { main: theme.fibre, fibre: theme.fibre, data: theme.data, broken: theme.err }
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
  // Dernière construction : clé de focus, liens et meshes (un changement de thème recrée les meshes).
  const built = useRef<{ key: string; links: unknown; meshes: unknown }>({ key: '', links: null, meshes: null })
  const question = useMemo(questionTexture, [])

  useFrame(({ clock, camera, invalidate }) => {
    const st = useCluster.getState()
    world.update(st)
    const focus = world.focusFor(st.selection, st.hoverNet, st.hover?.uid ?? null)
    const far = camera.zoom < LOD_PX
    const key = `${st.selection?.type}:${st.selection?.key}|${st.hoverNet}|${st.hover?.uid}|${far}`
    const b = built.current
    if (key !== b.key || world.links !== b.links || meshes !== b.meshes) {
      built.current = { key, links: world.links, meshes }
      const byFamily = new Map<Family, { points: [number, number][]; alpha: number; live: boolean }[]>(FAMILIES.map((f) => [f, []]))
      for (const l of world.links) {
        const lit = (!!focus.path && isLit(l, focus.path)) || (!!focus.hover && isLit(l, focus.hover))
        // Fibres : seulement sur le chemin sélectionné ou survolé, et de près.
        if (l.family === 'fibre' && (!lit || far)) continue
        const alpha = focus.dim && focus.path && !isLit(l, focus.path) ? 0.2 : 1
        byFamily.get(l.family)!.push({ points: l.points, alpha, live: l.family === 'broken' || l.live })
      }
      for (const [f, mesh] of meshes) {
        mesh.geometry.dispose()
        mesh.geometry = geometryOf(ribbon(byFamily.get(f)!, WIDTH[f], Y[f]))
      }
    }
    if (!reducedMotion && !document.hidden && !far && world.links.length) {
      const t = clock.elapsedTime
      for (const [f, mesh] of meshes) (mesh.material as THREE.ShaderMaterial).uniforms.uTime.value = t * DASH[f].speed
      tick(invalidate)
    }
  })

  useCluster((s) => s.version)
  const signs = world.links.filter((l) => l.sign)
  return (
    <>
      {[...meshes.values()].map((m, i) => <primitive key={i} object={m} />)}
      {signs.map((l, i) => (
        <sprite key={`sign-${i}`} position={[l.sign![0], 0.6, l.sign![1]]} scale={[0.45, 0.45, 1]} raycast={() => null} renderOrder={10}>
          <spriteMaterial map={question} depthTest={false} transparent />
        </sprite>
      ))}
    </>
  )
}
