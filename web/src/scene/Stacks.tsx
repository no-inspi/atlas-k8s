import { useMemo } from 'react'
import * as THREE from 'three'
import { useCluster } from '../store/cluster'
import type { Theme } from './theme'
import { world } from './world'

// Compteurs des piles (« ×12 ») au-dessus du bloc qui représente les pods d'un
// workload sur un node trop chargé.

const cache = new Map<string, THREE.CanvasTexture>()

function counterTexture(text: string, theme: Theme): THREE.CanvasTexture {
  const key = `${text}|${theme.ink}|${theme.bg}`
  let tex = cache.get(key)
  if (tex) return tex
  const c = document.createElement('canvas')
  c.width = 128
  c.height = 64
  const g = c.getContext('2d')!
  g.fillStyle = theme.ink
  g.beginPath()
  g.roundRect(4, 8, 120, 48, 24)
  g.fill()
  g.fillStyle = theme.bg
  g.font = `700 32px ${theme.font}`
  g.textAlign = 'center'
  g.textBaseline = 'middle'
  g.fillText(text, 64, 33)
  tex = new THREE.CanvasTexture(c)
  tex.colorSpace = THREE.SRGBColorSpace
  if (cache.size > 200) cache.clear()
  cache.set(key, tex)
  return tex
}

export function Stacks({ theme }: { theme: Theme }) {
  useCluster((s) => s.version)
  world.update(useCluster.getState())
  const stacks = world.stacks
  const items = useMemo(() => stacks.map((st) => ({ ...st, tex: counterTexture(`×${st.count}`, theme) })), [stacks, theme])
  return (
    <group>
      {items.map((st) => (
        <sprite key={st.uid} position={[st.x, 0.5 + st.top + 0.55, st.z]} scale={[0.7, 0.35, 1]} raycast={() => null} renderOrder={10}>
          <spriteMaterial map={st.tex} depthTest={false} transparent />
        </sprite>
      ))}
    </group>
  )
}
