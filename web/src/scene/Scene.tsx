import { OrbitControls } from '@react-three/drei'
import { Canvas, useFrame, useThree } from '@react-three/fiber'
import { useEffect, useRef } from 'react'
import * as THREE from 'three'
import type { OrbitControls as OrbitControlsImpl } from 'three-stdlib'
import { shallow } from 'zustand/shallow'
import { useCluster } from '../store/cluster'
import { Buildings } from './Buildings'
import { PerfMeter, perfEnabled } from './PerfMeter'
import { City } from './City'
import { Links } from './GroundLinks'
import { Network } from './Network'
import { pickables } from './pick'
import { Pods } from './Pods'
import { Selection } from './Selection'
import { Stacks } from './Stacks'
import { useReducedMotion, useTheme, type Theme } from './theme'
import { world } from './world'

const CAMERA_DIR = new THREE.Vector3(24, 26, 26).normalize()
const ZOOM_MIN = 0.6
const ZOOM_MAX = 3.2

/**
 * Cadre la ville : caméra orthographique en vue isométrique, zoom calculé pour
 * que toute la ville tienne à l'écran, bornes de zoom relatives à ce cadrage.
 */
function CameraRig() {
  const controls = useRef<OrbitControlsImpl>(null)
  const { camera, size, invalidate } = useThree()
  const framed = useRef('')

  useEffect(() => {
    // Flèches du clavier : panoramique.
    controls.current?.listenToKeyEvents(window as unknown as HTMLElement)
  }, [])

  useFrame(() => {
    const layout = world.layout
    const c = controls.current
    if (!layout || !c) return
    const key = `${layout.bounds.width}x${layout.bounds.depth}@${size.width}x${size.height}`
    if (key === framed.current) return
    const first = framed.current === ''
    framed.current = key

    const { bounds } = layout
    const span = Math.max(bounds.width, bounds.depth * 1.3)
    const aspect = size.width / size.height
    // En portrait, on accepte de rogner le décor (arbres) pour garder des pods lisibles.
    const viewHeight = Math.max(span * 0.8, (span * (aspect < 1 ? 0.9 : 1.15)) / aspect)
    const fit = size.height / viewHeight
    c.minZoom = fit * ZOOM_MIN
    c.maxZoom = fit * ZOOM_MAX
    if (first) {
      c.target.set(bounds.x, 0, bounds.z)
      camera.position.copy(CAMERA_DIR).multiplyScalar(80).add(c.target)
      camera.zoom = fit
    } else {
      camera.zoom = THREE.MathUtils.clamp(camera.zoom, c.minZoom, c.maxZoom)
    }
    camera.updateProjectionMatrix()
    c.update()
  })

  // Recherche : glisse la caméra vers l'objet trouvé et zoome assez pour le lire.
  const focus = useCluster((s) => s.focus)
  const flight = useRef<{ from: THREE.Vector3; to: THREE.Vector3; zoom0: number; zoom1: number; t: number } | null>(null)
  useEffect(() => {
    const c = controls.current
    if (!focus || !c) return
    const fit = c.minZoom / ZOOM_MIN
    flight.current = { from: c.target.clone(), to: new THREE.Vector3(focus.x, 0, focus.z), zoom0: camera.zoom, zoom1: Math.max(camera.zoom, fit * 2), t: 0 }
    invalidate()
  }, [focus, camera, invalidate])
  useFrame((_, delta) => {
    const f = flight.current, c = controls.current
    if (!f || !c) return
    f.t = Math.min(1, f.t + delta / 0.5)
    const e = 1 - Math.pow(1 - f.t, 3)
    const next = f.from.clone().lerp(f.to, e)
    camera.position.add(next.clone().sub(c.target))
    c.target.copy(next)
    camera.zoom = f.zoom0 + (f.zoom1 - f.zoom0) * e
    camera.updateProjectionMatrix()
    c.update()
    if (f.t < 1) invalidate()
    else flight.current = null
  })

  return (
    <OrbitControls
      ref={controls}
      makeDefault
      enableDamping
      dampingFactor={0.08}
      minPolarAngle={0.45}
      maxPolarAngle={1.15}
      screenSpacePanning={false}
    />
  )
}

/** Demande une frame à chaque changement du flux, de la sélection ou du filtre. */
function StoreInvalidator() {
  const invalidate = useThree((s) => s.invalidate)
  useEffect(() => {
    const stop = useCluster.subscribe((s) => [s.version, s.selection, s.nsFilter, s.podView, s.hover?.uid] as const, () => invalidate(), { equalityFn: shallow })
    const onVisible = () => invalidate()
    document.addEventListener('visibilitychange', onVisible)
    return () => { stop(); document.removeEventListener('visibilitychange', onVisible) }
  }, [invalidate])
  return null
}

/**
 * Clic court (moins de 6 px de déplacement) : sélectionne un pod ou un node.
 * Survol à la souris : pod sous le pointeur (infobulle), une fois par frame au plus.
 */
function Picker() {
  const { gl, camera } = useThree()
  useEffect(() => {
    const ray = new THREE.Raycaster()
    const ptr = new THREE.Vector2()
    let down: { x: number; y: number } | null = null
    let frame = 0
    const el = gl.domElement

    const hitAt = (clientX: number, clientY: number) => {
      const r = el.getBoundingClientRect()
      ptr.set(((clientX - r.left) / r.width) * 2 - 1, -((clientY - r.top) / r.height) * 2 + 1)
      ray.setFromCamera(ptr, camera)
      const pods = pickables.pods.meshes, nodes = pickables.nodes.meshes
      const hit = ray.intersectObjects([...pods, ...nodes], false)[0]
      if (!hit || hit.instanceId === undefined) return null
      if (pods.includes(hit.object as THREE.InstancedMesh)) {
        const uid = pickables.pods.uids[hit.instanceId]
        return uid ? { type: 'pod' as const, key: uid } : null
      }
      const name = pickables.nodes.names[hit.instanceId]
      return name ? { type: 'node' as const, key: name } : null
    }

    const onDown = (e: PointerEvent) => { down = { x: e.clientX, y: e.clientY } }
    const onUp = (e: PointerEvent) => {
      if (!down || Math.hypot(e.clientX - down.x, e.clientY - down.y) > 6) return
      down = null
      useCluster.getState().select(hitAt(e.clientX, e.clientY))
    }
    const onMove = (e: PointerEvent) => {
      if (e.pointerType !== 'mouse' || e.buttons) return
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(() => {
        const hit = hitAt(e.clientX, e.clientY)
        const pod = hit?.type === 'pod' ? hit.key : null
        el.style.cursor = hit ? 'pointer' : ''
        useCluster.getState().setHover(pod ? { uid: pod, x: e.clientX, y: e.clientY } : null)
      })
    }
    const onLeave = () => {
      cancelAnimationFrame(frame)
      el.style.cursor = ''
      useCluster.getState().setHover(null)
    }
    el.addEventListener('pointerdown', onDown)
    el.addEventListener('pointerup', onUp)
    el.addEventListener('pointermove', onMove)
    el.addEventListener('pointerleave', onLeave)
    return () => {
      cancelAnimationFrame(frame)
      el.removeEventListener('pointerdown', onDown)
      el.removeEventListener('pointerup', onUp)
      el.removeEventListener('pointermove', onMove)
      el.removeEventListener('pointerleave', onLeave)
    }
  }, [gl, camera])
  return null
}

function Lights({ theme }: { theme: Theme }) {
  const sun = useRef<THREE.DirectionalLight>(null)
  useFrame(() => {
    const l = sun.current, layout = world.layout
    if (!l || !layout) return
    const r = Math.max(layout.bounds.width, layout.bounds.depth) / 1.4 + 4
    const cam = l.shadow.camera
    if (cam.right !== r) {
      Object.assign(cam, { left: -r, right: r, top: r, bottom: -r })
      cam.updateProjectionMatrix()
    }
  })
  return (
    <>
      <hemisphereLight args={['#ffffff', theme.ground, 2.4]} />
      <directionalLight
        ref={sun}
        position={[14, 26, 10]}
        intensity={2.1}
        castShadow
        shadow-mapSize={[2048, 2048]}
        shadow-camera-near={1}
        shadow-camera-far={90}
        shadow-radius={4}
      />
    </>
  )
}

export function Scene() {
  const theme = useTheme()
  const reducedMotion = useReducedMotion()
  return (
    <Canvas
      orthographic
      flat
      // Rendu à la demande : une frame par changement d'état, interaction ou animation en cours.
      frameloop="demand"
      shadows={{ type: THREE.PCFSoftShadowMap }}
      dpr={[1, 2]}
      camera={{ position: [24, 26, 26], near: 0.1, far: 400, zoom: 30 }}
      aria-label="Vue 3D du cluster"
    >
      <color attach="background" args={[theme.bg]} />
      <Lights theme={theme} />
      <City theme={theme} />
      <Buildings theme={theme} />
      <Links theme={theme} reducedMotion={reducedMotion} />
      <Network theme={theme} reducedMotion={reducedMotion} />
      <Pods theme={theme} reducedMotion={reducedMotion} />
      <Stacks theme={theme} />
      <Selection theme={theme} />
      <CameraRig />
      <Picker />
      <StoreInvalidator />
      {perfEnabled() && <PerfMeter />}
    </Canvas>
  )
}
