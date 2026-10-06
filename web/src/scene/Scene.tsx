import { OrbitControls } from '@react-three/drei'
import { Canvas, useFrame, useThree } from '@react-three/fiber'
import { useEffect, useRef } from 'react'
import * as THREE from 'three'
import type { OrbitControls as OrbitControlsImpl } from 'three-stdlib'
import { useCluster } from '../store/cluster'
import { Buildings } from './Buildings'
import { PerfMeter, perfEnabled } from './PerfMeter'
import { City } from './City'
import { pickables } from './pick'
import { Robots } from './Robots'
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
    // En portrait, on accepte de rogner le décor (arbres) pour garder des robots lisibles.
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
    const stop = useCluster.subscribe((s) => [s.version, s.selection, s.nsFilter] as const, () => invalidate())
    const onVisible = () => invalidate()
    document.addEventListener('visibilitychange', onVisible)
    return () => { stop(); document.removeEventListener('visibilitychange', onVisible) }
  }, [invalidate])
  return null
}

/** Clic court (moins de 6 px de déplacement) : sélectionne un pod ou un node. */
function Picker() {
  const { gl, camera } = useThree()
  useEffect(() => {
    const ray = new THREE.Raycaster()
    const ptr = new THREE.Vector2()
    let down: { x: number; y: number } | null = null
    const onDown = (e: PointerEvent) => { down = { x: e.clientX, y: e.clientY } }
    const onUp = (e: PointerEvent) => {
      if (!down || Math.hypot(e.clientX - down.x, e.clientY - down.y) > 6) return
      down = null
      const r = gl.domElement.getBoundingClientRect()
      ptr.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1)
      ray.setFromCamera(ptr, camera)
      const robots = pickables.robots.meshes, nodes = pickables.nodes.meshes
      const hit = ray.intersectObjects([...robots, ...nodes], false)[0]
      const { select } = useCluster.getState()
      if (!hit || hit.instanceId === undefined) return select(null)
      if (robots.includes(hit.object as THREE.InstancedMesh)) {
        const uid = pickables.robots.uids[hit.instanceId]
        if (uid) select({ type: 'pod', key: uid })
      } else {
        const name = pickables.nodes.names[hit.instanceId]
        if (name) select({ type: 'node', key: name })
      }
    }
    const el = gl.domElement
    el.addEventListener('pointerdown', onDown)
    el.addEventListener('pointerup', onUp)
    return () => {
      el.removeEventListener('pointerdown', onDown)
      el.removeEventListener('pointerup', onUp)
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
      <Robots theme={theme} reducedMotion={reducedMotion} />
      <Stacks theme={theme} />
      <Selection theme={theme} />
      <CameraRig />
      <Picker />
      <StoreInvalidator />
      {perfEnabled() && <PerfMeter />}
    </Canvas>
  )
}
