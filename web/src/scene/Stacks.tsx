import { useMemo } from 'react'
import * as THREE from 'three'
import { useCluster } from '../store/cluster'
import type { Theme } from './theme'
import { world } from './world'

// Compteurs des piles (« ×12 ») au-dessus du bloc qui représente les pods d'un
// workload sur un node trop chargé.

const cache = new Map<string, { tex: THREE.CanvasTexture; aspect: number }>()

/** Pastille de texte (compteur « ×12 », nom de porte) ; aspect = largeur / hauteur. */
export function pillTexture(text: string, theme: Theme): { tex: THREE.CanvasTexture; aspect: number } {
  const key = `${text}|${theme.ink}|${theme.bg}`
  let hit = cache.get(key)
  if (hit) {
    // Récemment utilisée : en fin de file, loin de l'éviction.
    cache.delete(key)
    cache.set(key, hit)
    return hit
  }
  const c = document.createElement('canvas')
  c.height = 64
  c.width = Math.max(128, 40 + text.length * 19)
  const g = c.getContext('2d')!
  g.fillStyle = theme.ink
  g.beginPath()
  g.roundRect(4, 8, c.width - 8, 48, 24)
  g.fill()
  g.fillStyle = theme.bg
  g.font = `700 32px ${theme.font}`
  g.textAlign = 'center'
  g.textBaseline = 'middle'
  g.fillText(text, c.width / 2, 33)
  const tex = new THREE.CanvasTexture(c)
  tex.colorSpace = THREE.SRGBColorSpace
  hit = { tex, aspect: c.width / c.height }
  // Éviction de la plus ancienne : sa texture GPU est libérée (three la renverrait
  // au GPU si un sprite l'utilisait encore).
  if (cache.size >= 200) {
    const [oldKey, old] = cache.entries().next().value!
    cache.delete(oldKey)
    old.tex.dispose()
  }
  cache.set(key, hit)
  return hit
}

export function Stacks({ theme }: { theme: Theme }) {
  useCluster((s) => s.version)
  world.update(useCluster.getState())
  const stacks = world.stacks
  const items = useMemo(() => stacks.map((st) => ({ ...st, tex: pillTexture(`×${st.count}`, theme).tex })), [stacks, theme])
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
