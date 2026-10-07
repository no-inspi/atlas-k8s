import { useFrame } from '@react-three/fiber'
import { useEffect, useMemo, useRef } from 'react'
import * as THREE from 'three'
import { useCluster } from '../store/cluster'
import { Part, opacityBasicMaterial, opacityMaterial, roundCapacity } from './instanced'
import { perfStats } from './PerfMeter'
import { pickables } from './pick'
import { kindOf } from './podView'
import { poseAt, postureFor, type Antenna } from './posture'
import type { Theme } from './theme'
import { BLOCK_SIDE, phaseOf, podPositions, world } from './world'

// Un pod est un bloc graphite sobre posé sur sa case, haut selon sa request.
// Deux signaux lumineux seulement :
//  - un liseré qui fait le tour du dessus, à la couleur du namespace ;
//  - une icône au centre du dessus pour l'état (blanche si tout va bien,
//    colorée seulement en cas de problème) dont la forme dit le type de workload.

// Repère des niveaux de détail des bâtiments : hauteur à l'écran (px) d'un bloc d'une unité.
export const LOD_PX = 14
const PLATFORM_TOP = 0.5

type IconShape = 'iconSquare' | 'iconDome' | 'iconDisk' | 'iconPyramid'
const ICONS: IconShape[] = ['iconSquare', 'iconDome', 'iconDisk', 'iconPyramid']

/** Forme de l'icône d'état par type de workload (reprise dans les filtres). */
export function iconShape(kind: string): IconShape {
  switch (kind) {
    case 'DaemonSet': return 'iconDome'
    case 'StatefulSet': return 'iconDisk'
    case 'Job':
    case 'CronJob': return 'iconPyramid'
    default: return 'iconSquare'
  }
}

/** Cadre carré creux de côté 1 (épaisseur `band`), posé à plat, base à y = 0. */
function squareRing(band: number, height: number): THREE.BufferGeometry {
  const o = 0.5, i = 0.5 - band
  const shape = new THREE.Shape([new THREE.Vector2(-o, -o), new THREE.Vector2(o, -o), new THREE.Vector2(o, o), new THREE.Vector2(-o, o)])
  shape.holes.push(new THREE.Path([new THREE.Vector2(-i, -i), new THREE.Vector2(-i, i), new THREE.Vector2(i, i), new THREE.Vector2(i, -i)]))
  return new THREE.ExtrudeGeometry(shape, { depth: height, bevelEnabled: false }).rotateX(-Math.PI / 2)
}

// Géométries dont la base est à y = 0.
const up = (g: THREE.BufferGeometry, h: number) => g.translate(0, h / 2, 0)
const RIM_H = 0.05
const GEOMETRY = {
  body: up(new THREE.BoxGeometry(1, 1, 1), 1),
  // Le liseré déborde à peine du bloc et du dessus : une arête lumineuse.
  rim: squareRing(0.07, RIM_H),
  iconSquare: up(new THREE.BoxGeometry(0.2, 0.035, 0.2), 0.035),
  iconDome: new THREE.SphereGeometry(0.12, 20, 10, 0, Math.PI * 2, 0, Math.PI / 2),
  iconDisk: up(new THREE.CylinderGeometry(0.12, 0.12, 0.05, 24), 0.05),
  iconPyramid: up(new THREE.ConeGeometry(0.15, 0.17, 4).rotateY(Math.PI / 4), 0.17),
  question: new THREE.PlaneGeometry(0.4, 0.4),
}

export function questionTexture(): THREE.Texture {
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

function makeMaterials() {
  return {
    // Graphite satiné ; la teinte exacte vient du thème, par instance.
    body: opacityMaterial({ color: '#ffffff', roughness: 0.32, metalness: 0.35 }),
    // Signaux non éclairés : ils gardent leur couleur, comme des LED.
    glow: opacityBasicMaterial({ color: '#ffffff' }),
    question: opacityBasicMaterial({ map: questionTexture(), depthWrite: false }),
  }
}

interface Anim {
  x: number
  y: number
  z: number
  h: number
  scale: number
  yaw: number
}

const CRASH_RED = new THREE.Color('#F0452E')
const DARK = new THREE.Color('#0E1316')
const WHITE = new THREE.Color('#ffffff')
const Y = new THREE.Vector3(0, 1, 0)

export function Pods({ theme, reducedMotion }: { theme: Theme; reducedMotion: boolean }) {
  const group = useRef<THREE.Group>(null)
  const materials = useMemo(makeMaterials, [])
  const state = useRef<{ parts: Map<string, Part>; capacity: number }>({ parts: new Map(), capacity: 0 })
  const anims = useRef(new Map<string, Anim>())
  const seeded = useRef(false)
  const lastPods = useRef<unknown>(null)

  const colors = useMemo(() => {
    // État : blanc froid quand tout va bien, la couleur est réservée aux problèmes.
    const status: Record<Antenna, THREE.Color> = {
      ok: new THREE.Color(theme.dark ? '#E8F1F6' : '#F4F8FA'),
      warn: new THREE.Color(theme.warn),
      err: CRASH_RED,
      mute: new THREE.Color('#6E7A82'),
      done: new THREE.Color('#7FA7D6'),
    }
    return {
      status,
      body: new THREE.Color(theme.dark ? '#3A434A' : '#2C3439'),
      accent: new THREE.Color(theme.accent),
    }
  }, [theme])

  useEffect(() => () => {
    state.current.parts.forEach((p) => p.dispose())
    Object.values(materials).forEach((m) => m.dispose())
  }, [materials])

  // Objets réutilisés à chaque frame : aucune allocation dans la boucle de rendu.
  const tmp = useMemo(() => ({
    m: new THREE.Matrix4(), pos: new THREE.Vector3(), quat: new THREE.Quaternion(), scale: new THREE.Vector3(),
    color: new THREE.Color(), nsColor: new Map<string, THREE.Color>(),
  }), [])

  const ensureCapacity = (n: number) => {
    const s = state.current
    if (n <= s.capacity) return
    s.parts.forEach((p) => { group.current?.remove(p.mesh); p.dispose() })
    s.capacity = roundCapacity(n)
    const make = (name: keyof typeof GEOMETRY, mat: THREE.Material, shadow = false) =>
      [name, new Part(GEOMETRY[name], mat, s.capacity, { opacity: true, name, castShadow: shadow, receiveShadow: shadow })] as const
    s.parts = new Map([
      make('body', materials.body, true),
      make('rim', materials.glow),
      ...ICONS.map((c) => make(c, materials.glow)),
      make('question', materials.question),
    ])
    s.parts.forEach((p) => group.current?.add(p.mesh))
    pickables.pods.meshes = ['body', 'rim', ...ICONS].map((n) => s.parts.get(n)!.mesh)
  }

  useFrame(({ clock, camera, invalidate }, delta) => {
    const started = performance.now()
    const st = useCluster.getState()
    world.update(st)
    const pods = world.pods
    ensureCapacity(pods.length)
    const parts = state.current.parts
    const body = parts.get('body')!, rim = parts.get('rim')!, question = parts.get('question')!
    const t = clock.elapsedTime
    const dt = Math.min(delta, 0.05)
    const k = 1 - Math.exp(-dt * 6)
    const sel = st.selection?.type === 'pod' ? st.selection.key : null
    const hover = st.hover?.uid ?? null
    const firstFill = !seeded.current && pods.length > 0
    const { m, pos, quat, scale, color } = tmp
    const still = reducedMotion || document.hidden
    let moving = false
    let animated = false // au moins un bloc s'anime (attente, démarrage, crash)

    if (tmp.nsColor.size !== world.colors.size) tmp.nsColor.clear()
    const nsColor = (ns: string) => {
      let c = tmp.nsColor.get(ns)
      if (!c) tmp.nsColor.set(ns, (c = new THREE.Color(world.colors.get(ns) ?? '#7D8A94')))
      return c
    }
    const place = (part: Part, i: number, x: number, y: number, z: number, sx: number, sy: number, sz: number) => {
      pos.set(x, y, z)
      scale.set(sx, sy, sz)
      part.setMatrix(i, m.compose(pos, quat, scale))
    }

    const uids = pickables.pods.uids
    uids.length = 0
    let i = 0
    for (const p of pods) {
      const target = world.targets.get(p.uid)
      if (!target || target.hidden) continue // pod représenté par une pile
      const posture = postureFor(p)
      const pose = poseAt(posture.kind, t, phaseOf(p.uid), still)
      const ty = target.onNode ? PLATFORM_TOP : 0.02
      // Seuls l'attente (saut) et le démarrage (rotation) bougent ; un bloc sain est immobile.
      const yaw = posture.kind === 'spin' ? pose.yaw : 0
      if (!still && (posture.kind === 'stomp' || posture.kind === 'spin' || posture.kind === 'fallen' || posture.blink || posture.question)) animated = true

      let a = anims.current.get(p.uid)
      if (!a) {
        a = { x: target.x, y: ty, z: target.z, h: target.h, scale: firstFill ? pose.scale : 0.01, yaw }
        anims.current.set(p.uid, a)
      }
      if (Math.abs(target.x - a.x) + Math.abs(target.z - a.z) + Math.abs(ty - a.y) + Math.abs(target.h - a.h) + Math.abs(pose.scale - a.scale) > 0.002) moving = true
      a.x += (target.x - a.x) * k
      a.y += (ty - a.y) * k
      a.z += (target.z - a.z) * k
      a.h += (target.h - a.h) * k
      a.scale += (pose.scale - a.scale) * k
      a.yaw = posture.kind === 'spin' ? yaw : a.yaw + (yaw - a.yaw) * k
      const s = Math.max(0.02, a.scale)
      const y = a.y + pose.hop
      const side = BLOCK_SIDE * s, h = a.h * s
      podPositions.set(p.uid, { x: a.x, y: a.y, z: a.z, h })

      const dim = st.nsFilter !== null && p.namespace !== st.nsFilter
      const opacity = dim ? 0.1 : posture.kind === 'stomp' ? 0.75 : posture.kind === 'fade' ? 0.55 : 1
      quat.setFromAxisAngle(Y, a.yaw)

      // Corps graphite : rougeoie si crash, teinte d'accent si sélectionné, s'éclaircit au survol.
      place(body, i, a.x, y, a.z, side, h, side)
      body.setOpacity(i, opacity)
      color.copy(colors.body)
      if (posture.kind === 'fallen') color.lerp(CRASH_RED, still ? 0.3 : 0.2 + 0.15 * Math.sin(t * 5))
      else if (p.uid === sel) color.lerp(colors.accent, 0.4)
      if (p.uid === hover) color.lerp(WHITE, 0.18)
      body.setColor(i, color)

      // Liseré du namespace, affleurant au bord du dessus.
      place(rim, i, a.x, y + h - RIM_H * s + 0.006, a.z, side + 0.02, s, side + 0.02)
      rim.setOpacity(i, opacity)
      rim.setColor(i, nsColor(p.namespace))

      // Icône d'état : forme selon le type de workload.
      const shape = iconShape(kindOf(p))
      color.copy(colors.status[posture.antenna])
      if (posture.blink && !still && Math.sin(t * 8) < 0) color.lerp(DARK, 0.7)
      for (const c of ICONS) {
        const icon = parts.get(c)!
        if (c !== shape) { icon.hide(i); continue }
        place(icon, i, a.x, y + h, a.z, s, s, s)
        icon.setOpacity(i, opacity)
        icon.setColor(i, color)
      }

      // Image introuvable : point d'interrogation face à la caméra.
      if (posture.question) {
        pos.set(a.x, y + h + 0.55 + (still ? 0 : Math.sin(t * 3 + phaseOf(p.uid)) * 0.04), a.z)
        scale.set(s, s, s)
        question.setMatrix(i, m.compose(pos, camera.quaternion, scale))
        question.setOpacity(i, opacity)
      } else question.hide(i)

      uids.push(p.uid)
      i++
    }
    parts.forEach((part) => part.commit(i))
    perfStats.robotsMs = performance.now() - started
    perfStats.lod = false
    // Rendu à la demande : une nouvelle frame seulement s'il reste quelque chose à animer.
    if (animated || moving) invalidate()
    if (firstFill) seeded.current = true

    // Pods disparus ou masqués par un filtre : on oublie leur position (sélection, recherche).
    if (lastPods.current !== pods) {
      lastPods.current = pods
      const shown = new Set(uids)
      for (const uid of anims.current.keys()) if (!shown.has(uid)) { anims.current.delete(uid); podPositions.delete(uid) }
    }
  })

  return <group ref={group} />
}
