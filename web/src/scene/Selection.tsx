import { useFrame } from '@react-three/fiber'
import { useMemo } from 'react'
import * as THREE from 'three'
import { useCluster } from '../store/cluster'
import type { Theme } from './theme'
import { podPositions, world } from './world'

// Marqueur flottant au-dessus de l'objet sélectionné et arcs vers les autres
// pods du même workload.

const MAX_ARCS = 14
const SEG = 20

export function Selection({ theme }: { theme: Theme }) {
  const { marker, arcs, geo } = useMemo(() => {
    const marker = new THREE.Mesh(new THREE.ConeGeometry(0.28, 0.6, 16), new THREE.MeshStandardMaterial({ color: theme.accent }))
    marker.rotation.x = Math.PI
    marker.visible = false
    marker.raycast = () => {}
    const geo = new THREE.BufferGeometry()
    geo.setAttribute('position', new THREE.BufferAttribute(new Float32Array(MAX_ARCS * SEG * 2 * 3), 3))
    const arcs = new THREE.LineSegments(geo, new THREE.LineBasicMaterial({ color: theme.accent, transparent: true, opacity: 0.9 }))
    arcs.frustumCulled = false
    arcs.raycast = () => {}
    return { marker, arcs, geo }
  }, [theme])

  useFrame(({ clock, invalidate }) => {
    const st = useCluster.getState()
    const sel = st.selection
    const t = clock.elapsedTime
    const pos = geo.attributes.position.array as Float32Array
    let v = 0
    marker.visible = false

    if (sel?.type === 'pod') {
      const a = podPositions.get(sel.key)
      const pod = st.pods.get(sel.key)
      if (a && pod) {
        marker.visible = true
        marker.position.set(a.x, a.y + a.h + 0.75 + Math.sin(t * 3) * 0.12, a.z)
        const siblings = world.pods.filter((p) => p.uid !== pod.uid && p.namespace === pod.namespace &&
          p.owner.kind === pod.owner.kind && p.owner.name === pod.owner.name && !world.targets.get(p.uid)?.hidden).slice(0, MAX_ARCS)
        for (const s of siblings) {
          const b = podPositions.get(s.uid)
          if (!b) continue
          const h = 1.4 + Math.hypot(b.x - a.x, b.z - a.z) * 0.18
          for (let i = 0; i < SEG; i++)
            for (const f of [i / SEG, (i + 1) / SEG]) {
              pos[v++] = a.x + (b.x - a.x) * f
              pos[v++] = a.y + a.h + (b.y + b.h - a.y - a.h) * f + 4 * h * f * (1 - f)
              pos[v++] = a.z + (b.z - a.z) * f
            }
        }
      }
    } else if (sel?.type === 'node') {
      const p = world.layout?.plots.get(sel.key)
      if (p && world.layout) {
        marker.visible = true
        marker.position.set(p.x, 3.4 + Math.sin(t * 3) * 0.12, p.z - world.layout.geometry.depth / 2 + 0.5)
      }
    }
    geo.setDrawRange(0, v / 3)
    geo.attributes.position.needsUpdate = true
    if (marker.visible && !document.hidden) invalidate() // le marqueur flotte
  })

  return (
    <>
      <primitive object={marker} />
      <primitive object={arcs} />
    </>
  )
}
