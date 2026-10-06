import { useFrame } from '@react-three/fiber'
import { useEffect, useMemo, useRef } from 'react'
import * as THREE from 'three'
import { useCluster } from '../store/cluster'
import { Part, opacityBasicMaterial, opacityMaterial, roundCapacity } from './instanced'
import { pickables } from './pick'
import { poseAt, postureFor, type Antenna } from './posture'
import type { Theme } from './theme'
import { phaseOf, robotPositions, world } from './world'

// Un pod est un petit robot (géométries du prototype) : corps à la couleur du
// namespace, antenne à la couleur du statut, accessoire selon le workload.

type Group = 'legs' | 'body' | 'head' | 'billboard'
interface PartDef {
  name: string
  geo: THREE.BufferGeometry
  mat: string
  at: [number, number, number]
  group: Group
  only?: (ownerKind: string) => boolean
}

const box = (w: number, h: number, d: number) => new THREE.BoxGeometry(w, h, d)
const NECK = 0.58
const SIT_DROP = 0.16
const PLATFORM_TOP = 0.5

function questionTexture(): THREE.Texture {
  const c = document.createElement('canvas')
  c.width = c.height = 64
  const g = c.getContext('2d')!
  g.fillStyle = '#C23E28'
  g.beginPath()
  g.arc(32, 32, 30, 0, Math.PI * 2)
  g.fill()
  g.fillStyle = '#fff'
  g.font = 'bold 44px system-ui, sans-serif'
  g.textAlign = 'center'
  g.textBaseline = 'middle'
  g.fillText('?', 32, 35)
  const t = new THREE.CanvasTexture(c)
  t.colorSpace = THREE.SRGBColorSpace
  return t
}

const PARTS: PartDef[] = [
  { name: 'legL', geo: box(0.13, 0.16, 0.14), mat: 'dark', at: [-0.12, 0.08, 0], group: 'legs' },
  { name: 'legR', geo: box(0.13, 0.16, 0.14), mat: 'dark', at: [0.12, 0.08, 0], group: 'legs' },
  { name: 'body', geo: box(0.5, 0.42, 0.4), mat: 'body', at: [0, 0.37, 0], group: 'body' },
  { name: 'backpack', geo: box(0.34, 0.32, 0.14), mat: 'pack', at: [0, 0.4, -0.27], group: 'body', only: (k) => k === 'StatefulSet' },
  { name: 'head', geo: box(0.46, 0.34, 0.4), mat: 'head', at: [0, 0.75, 0], group: 'head' },
  { name: 'eyeL', geo: box(0.07, 0.1, 0.02), mat: 'dark', at: [-0.1, 0.77, 0.205], group: 'head' },
  { name: 'eyeR', geo: box(0.07, 0.1, 0.02), mat: 'dark', at: [0.1, 0.77, 0.205], group: 'head' },
  { name: 'antenna', geo: new THREE.CylinderGeometry(0.018, 0.018, 0.2, 6), mat: 'dark', at: [0, 1.0, 0], group: 'head' },
  { name: 'bulb', geo: new THREE.SphereGeometry(0.07, 10, 8), mat: 'bulb', at: [0, 1.13, 0], group: 'head' },
  { name: 'helmet', geo: new THREE.SphereGeometry(0.29, 16, 8, 0, Math.PI * 2, 0, Math.PI / 2), mat: 'helmet', at: [0, 0.9, 0], group: 'head', only: (k) => k === 'DaemonSet' },
  { name: 'cap', geo: box(0.48, 0.08, 0.42), mat: 'cap', at: [0, 0.96, 0], group: 'head', only: (k) => k === 'Job' },
  { name: 'visor', geo: box(0.36, 0.03, 0.2), mat: 'cap', at: [0, 0.935, 0.28], group: 'head', only: (k) => k === 'Job' },
  { name: 'question', geo: new THREE.PlaneGeometry(0.34, 0.34), mat: 'question', at: [0, 1.55, 0], group: 'billboard' },
]

function makeMaterials() {
  return {
    body: opacityMaterial({ color: '#ffffff', roughness: 0.5 }),
    head: opacityMaterial({ color: '#F3F6F5', roughness: 0.45 }),
    dark: opacityMaterial({ color: '#1C2528', roughness: 0.6 }),
    bulb: opacityBasicMaterial({ color: '#ffffff' }),
    helmet: opacityMaterial({ color: '#E0A63A', roughness: 0.5 }),
    pack: opacityMaterial({ color: '#6B5A49', roughness: 0.8 }),
    cap: opacityMaterial({ color: '#C4574B', roughness: 0.7 }),
    question: opacityBasicMaterial({ map: questionTexture(), depthWrite: false }),
  } as Record<string, THREE.Material>
}

interface Anim {
  x: number
  y: number
  z: number
  scale: number
  yaw: number
  fall: number
  tilt: number
  phase: number
}

const CRASH_RED = new THREE.Color('#E5402A')
const MUTE = new THREE.Color('#8A9499')
const DONE = new THREE.Color('#4F8FD8')
const DARK = new THREE.Color('#1C2528')

export function Robots({ theme, reducedMotion }: { theme: Theme; reducedMotion: boolean }) {
  const group = useRef<THREE.Group>(null)
  const materials = useMemo(makeMaterials, [])
  const state = useRef<{ parts: Map<string, Part>; capacity: number }>({ parts: new Map(), capacity: 0 })
  const anims = useRef(new Map<string, Anim>())
  const seeded = useRef(false)

  const colors = useMemo(() => {
    const antenna: Record<Antenna, THREE.Color> = {
      ok: new THREE.Color(theme.ok), warn: new THREE.Color(theme.warn), err: CRASH_RED, mute: MUTE, done: DONE,
    }
    return { antenna, accent: new THREE.Color(theme.accent) }
  }, [theme])

  useEffect(() => () => {
    state.current.parts.forEach((p) => p.dispose())
    Object.values(materials).forEach((m) => m.dispose())
  }, [materials])

  // Objets réutilisés à chaque frame : aucune allocation dans la boucle de rendu.
  const tmp = useMemo(() => ({
    base: new THREE.Matrix4(), head: new THREE.Matrix4(), m: new THREE.Matrix4(), t: new THREE.Matrix4(),
    rx: new THREE.Matrix4(), pos: new THREE.Vector3(), quat: new THREE.Quaternion(), scale: new THREE.Vector3(),
    euler: new THREE.Euler(0, 0, 0, 'YXZ'), color: new THREE.Color(), nsColor: new Map<string, THREE.Color>(),
  }), [])

  const ensureCapacity = (n: number) => {
    const s = state.current
    if (n <= s.capacity) return
    s.parts.forEach((p) => { group.current?.remove(p.mesh); p.dispose() })
    s.capacity = roundCapacity(n)
    s.parts = new Map(PARTS.map((d) => [d.name, new Part(d.geo, materials[d.mat], s.capacity, { opacity: true, name: d.name })]))
    s.parts.forEach((p) => group.current?.add(p.mesh))
    pickables.robots.meshes = [s.parts.get('body')!.mesh, s.parts.get('head')!.mesh]
  }

  useFrame(({ clock, camera }, delta) => {
    const st = useCluster.getState()
    world.update(st)
    const pods = world.pods
    ensureCapacity(pods.length)
    const parts = state.current.parts
    const t = clock.elapsedTime
    const dt = Math.min(delta, 0.05)
    const k = 1 - Math.exp(-dt * 6)
    const sel = st.selection?.type === 'pod' ? st.selection.key : null
    const firstFill = !seeded.current && pods.length > 0
    const { base, head, m, t: tr, rx, pos, quat, scale, euler, color } = tmp

    if (tmp.nsColor.size !== world.colors.size) tmp.nsColor.clear()
    const nsColor = (ns: string) => {
      let c = tmp.nsColor.get(ns)
      if (!c) tmp.nsColor.set(ns, (c = new THREE.Color(world.colors.get(ns) ?? '#7D8A94')))
      return c
    }

    const uids = pickables.robots.uids
    uids.length = 0
    let i = 0
    for (const p of pods) {
      const target = world.targets.get(p.uid)
      if (!target) continue
      const posture = postureFor(p)
      const pose = poseAt(posture.kind, t, phaseOf(p.uid), reducedMotion)
      const ty = target.onNode ? PLATFORM_TOP : 0.02

      let a = anims.current.get(p.uid)
      if (!a) {
        a = { x: target.x, y: ty, z: target.z, scale: firstFill ? pose.scale : 0.01, yaw: pose.yaw, fall: pose.fall, tilt: 0, phase: phaseOf(p.uid) }
        anims.current.set(p.uid, a)
      }
      a.x += (target.x - a.x) * k
      a.y += (ty - a.y) * k
      a.z += (target.z - a.z) * k
      a.scale += (pose.scale - a.scale) * k
      a.yaw = posture.kind === 'spin' ? pose.yaw : a.yaw + (pose.yaw - a.yaw) * k
      a.fall += (pose.fall - a.fall) * k * 0.8
      a.tilt += (pose.headTilt - a.tilt) * k
      robotPositions.set(p.uid, { x: a.x, y: a.y, z: a.z })

      // Corps : position, rotation (lacet puis bascule) et échelle.
      euler.set(a.fall, a.yaw, 0)
      quat.setFromEuler(euler)
      pos.set(a.x, a.y + pose.hop - pose.sit * SIT_DROP * a.scale, a.z)
      const s = Math.max(0.02, a.scale)
      scale.set(s, s, s)
      base.compose(pos, quat, scale)
      // Tête : pivote autour du cou quand le pod boude.
      head.copy(base).multiply(tr.makeTranslation(0, NECK, 0)).multiply(rx.makeRotationX(a.tilt)).multiply(tr.makeTranslation(0, -NECK, 0))

      const dim = st.nsFilter !== null && p.namespace !== st.nsFilter
      const opacity = dim ? 0.1 : posture.kind === 'stomp' ? 0.75 : posture.kind === 'fade' ? 0.6 : 1

      for (const d of PARTS) {
        const part = parts.get(d.name)!
        part.setOpacity(i, opacity)
        const hidden =
          (d.only && !d.only(p.owner.kind)) ||
          (d.group === 'legs' && pose.sit > 0) ||
          (d.name === 'question' && !posture.question)
        if (hidden) {
          part.hide(i)
          continue
        }
        if (d.group === 'billboard') {
          pos.set(a.x, a.y + d.at[1] * s + Math.sin(t * 3 + a.phase) * 0.04, a.z)
          scale.set(s, s, s)
          m.compose(pos, camera.quaternion, scale)
        } else {
          m.copy(d.group === 'head' ? head : base).multiply(tr.makeTranslation(d.at[0], d.at[1], d.at[2]))
          if (d.name === 'eyeL' || d.name === 'eyeR') m.multiply(rx.makeScale(1, pose.eyes, 1))
        }
        part.setMatrix(i, m)
      }

      // Couleurs : namespace (rouge pulsé si crash, teinte d'accent si sélectionné), antenne.
      color.copy(nsColor(p.namespace))
      if (posture.kind === 'fallen') color.lerp(CRASH_RED, reducedMotion ? 0.45 : 0.35 + 0.25 * Math.sin(t * 5))
      else if (p.uid === sel) color.lerp(colors.accent, 0.35)
      parts.get('body')!.setColor(i, color)
      color.copy(colors.antenna[posture.antenna])
      if (posture.blink && Math.sin(t * 8) < 0) color.lerp(DARK, 0.7)
      parts.get('bulb')!.setColor(i, color)

      uids.push(p.uid)
      i++
    }
    parts.forEach((part) => part.commit(i))
    if (firstFill) seeded.current = true

    if (anims.current.size > pods.length + 64) {
      for (const uid of anims.current.keys()) if (!st.pods.has(uid)) { anims.current.delete(uid); robotPositions.delete(uid) }
    }
  })

  return <group ref={group} />
}
