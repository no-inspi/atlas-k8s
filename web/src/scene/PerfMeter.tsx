import { useFrame, useThree } from '@react-three/fiber'
import { useRef } from 'react'

// Compteur de performance (?perf=1) : images/s, temps de frame, draw calls,
// triangles. En rendu à la demande, il ne bouge pas au repos (aucune frame) :
// data-frames donne le nombre total de frames dessinées. Écrit directement dans un élément DOM, sans re-render React.

export const perfEnabled = () => new URLSearchParams(window.location.search).has('perf')

export const perfStats = { fps: 0, frameMs: 0, calls: 0, triangles: 0, robotsMs: 0, lod: false, frames: 0 }

export function PerfMeter() {
  const { gl } = useThree()
  const frames = useRef<number[]>([])
  useFrame(() => {
    const now = performance.now()
    perfStats.frames++
    const f = frames.current
    f.push(now)
    while (f.length && now - f[0] > 1000) f.shift()
    const el = document.getElementById('perf-meter')
    if (f.length > 1 && el) {
      perfStats.fps = f.length - 1
      perfStats.frameMs = (now - f[0]) / (f.length - 1)
      perfStats.calls = gl.info.render.calls
      perfStats.triangles = gl.info.render.triangles
      el.textContent = `${perfStats.fps} img/s · ${perfStats.frameMs.toFixed(1)} ms · robots ${perfStats.robotsMs.toFixed(1)} ms${perfStats.lod ? ' (cubes)' : ''} · ${perfStats.calls} draw calls · ${(perfStats.triangles / 1000).toFixed(0)}k tri`
      el.dataset.fps = String(perfStats.fps)
      el.dataset.frames = String(perfStats.frames)
      el.dataset.robotsMs = perfStats.robotsMs.toFixed(2)
    }
  })
  return null
}
