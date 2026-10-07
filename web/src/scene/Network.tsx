import { useFrame } from '@react-three/fiber'
import { useEffect, useMemo, useRef, useState } from 'react'
import * as THREE from 'three'
import { serviceKey, volumeKey } from '../api/types'
import { useCluster } from '../store/cluster'
import { gateSignal, healthSignal, volumeSignal, worst, type Signal } from './health'
import { Part, opacityBasicMaterial, opacityMaterial, roundCapacity } from './instanced'
import { pickables } from './pick'
import { LOD_PX } from './Pods'
import { pillTexture } from './Stacks'
import type { Theme } from './theme'
import { tick } from './tick'
import { world } from './world'

// Infrastructure de la ville : relais (Services) sur les avenues, portes
// (contrôleurs d'entrée) à l'ouest, citernes (PVC) dans les entrepôts. Une
// InstancedMesh par pièce : quelques draw calls quel que soit le nombre d'objets.
// Vu de loin, un relais par tronçon (pire voyant du groupe) avec un compteur.

const up = (g: THREE.BufferGeometry, h: number) => g.translate(0, h / 2, 0)
const GEOMETRY = {
  relayRing: up(new THREE.CylinderGeometry(0.5, 0.5, 0.1, 24), 0.1),
  relayBase: up(new THREE.CylinderGeometry(0.42, 0.42, 0.2, 24), 0.2),
  beacon: up(new THREE.BoxGeometry(0.2, 0.28, 0.2), 0.28),
  signPost: up(new THREE.BoxGeometry(0.08, 0.9, 0.08), 0.9),
  signPanel: up(new THREE.BoxGeometry(0.7, 0.32, 0.06), 0.32),
  gatePost: up(new THREE.BoxGeometry(0.35, 2.1, 0.35), 2.1),
  gateLintel: up(new THREE.BoxGeometry(0.42, 0.32, 1), 0.32),
  tankBody: up(new THREE.CylinderGeometry(1, 1, 1, 28), 1),
  tankCap: up(new THREE.CylinderGeometry(1.02, 1.02, 0.08, 28), 0.08),
}
type PartName = keyof typeof GEOMETRY
const NAMES = Object.keys(GEOMETRY) as PartName[]
/** Pièces lumineuses (non éclairées) : voyants. */
const GLOW: ReadonlySet<PartName> = new Set(['beacon', 'tankCap'])
const PICKABLE: PartName[] = ['relayRing', 'relayBase', 'signPanel', 'gatePost', 'gateLintel', 'tankBody']
const TANK_H = 1.4
const GATE_SPAN = 1.9 // écart entre les piliers d'une porte, le long de z
const WHITE = new THREE.Color('#ffffff')

interface RelayItem { key: string; ns: string; x: number; z: number; sig: Signal; external: boolean }

export function Network({ theme, reducedMotion }: { theme: Theme; reducedMotion: boolean }) {
  const group = useRef<THREE.Group>(null)
  const materials = useMemo(() => ({
    solid: opacityMaterial({ color: '#ffffff', roughness: 0.55, metalness: 0.1 }),
    glow: opacityBasicMaterial({ color: '#ffffff' }),
  }), [])
  const parts = useRef(new Map<PartName, Part>())
  const capacity = useRef(0)
  const [far, setFar] = useState(false)
  useCluster((s) => s.version)
  world.update(useCluster.getState())

  const colors = useMemo(() => ({
    platform: new THREE.Color(theme.platform),
    muted: new THREE.Color(theme.muted),
    accent: new THREE.Color(theme.accent),
    tank: new THREE.Color(theme.dark ? '#5E6E75' : '#B9C6CC'),
    signal: {
      ok: new THREE.Color(theme.ok), warn: new THREE.Color(theme.warn), err: new THREE.Color(theme.err), mute: new THREE.Color(theme.muted),
    } as Record<Signal, THREE.Color>,
  }), [theme])
  const tmp = useMemo(() => ({ m: new THREE.Matrix4(), q: new THREE.Quaternion(), p: new THREE.Vector3(), s: new THREE.Vector3(), c: new THREE.Color() }), [])

  useEffect(() => () => {
    parts.current.forEach((p) => p.dispose())
    materials.solid.dispose()
    materials.glow.dispose()
  }, [materials])

  const ensure = (n: number) => {
    if (n <= capacity.current) return
    parts.current.forEach((p) => { group.current?.remove(p.mesh); p.dispose() })
    capacity.current = roundCapacity(n, 64)
    parts.current = new Map(NAMES.map((name) => [name, new Part(GEOMETRY[name], GLOW.has(name) ? materials.glow : materials.solid,
      capacity.current, { opacity: true, name, castShadow: !GLOW.has(name) })]))
    parts.current.forEach((p) => group.current?.add(p.mesh))
  }

  useFrame(({ camera, clock, invalidate }) => {
    const st = useCluster.getState()
    world.update(st)
    const net = world.net
    const isFar = camera.zoom < LOD_PX
    if (isFar !== far) setFar(isFar)
    ensure(Math.max(1, (net?.relays.size ?? 0) + 2 * (net?.gates.size ?? 0) + (net?.tanks.size ?? 0)))
    const P = parts.current
    const counts = new Map<PartName, number>()
    const keys = new Map<PartName, string[]>(PICKABLE.map((n) => [n, []]))
    const { m, q, p, s, c } = tmp
    const put = (name: PartName, x: number, y: number, z: number, sx: number, sy: number, sz: number, color: THREE.Color, opacity: number, key = '') => {
      const part = P.get(name)!
      const i = counts.get(name) ?? 0
      p.set(x, y, z)
      s.set(sx, sy, sz)
      part.setMatrix(i, m.compose(p, q, s))
      part.setColor(i, color)
      part.setOpacity(i, opacity)
      counts.set(name, i + 1)
      keys.get(name)?.push(key)
    }
    const focus = world.focusFor(st.selection, st.hoverNet, st.hover?.uid ?? null)
    const selKey = st.selection ? `${st.selection.type}:${st.selection.key}` : ''
    const alpha = (key: string, ns?: string) =>
      st.nsFilter && ns && ns !== st.nsFilter ? 0.1 : focus.dim && focus.path && key && !focus.path.has(key) ? 0.25 : 1
    const still = reducedMotion || document.hidden
    const t = clock.elapsedTime
    let blinking = false

    if (net) {
      // Relais.
      const byKey = new Map(world.services.map((sv) => [serviceKey(sv), sv]))
      const items: RelayItem[] = []
      if (isFar) {
        const agg = new Map<string, { ns: string; x0: number; x1: number; z: number; sigs: Signal[] }>()
        for (const [k, r] of net.relays) {
          const sv = byKey.get(k)
          if (!sv) continue
          const id = `${r.ns}|${r.avenue}`
          const a = agg.get(id) ?? { ns: r.ns, x0: r.x, x1: r.x, z: r.z, sigs: [] }
          a.x0 = Math.min(a.x0, r.x)
          a.x1 = Math.max(a.x1, r.x)
          a.sigs.push(healthSignal(sv.health))
          agg.set(id, a)
        }
        for (const a of agg.values()) items.push({ key: '', ns: a.ns, x: (a.x0 + a.x1) / 2, z: a.z, sig: worst(a.sigs), external: false })
      } else {
        const groupSigs = new Map<string, Signal[]>()
        for (const [k, r] of net.relays) {
          const sv = byKey.get(k)
          if (!sv) continue
          if (r.group) { groupSigs.set(r.group, [...(groupSigs.get(r.group) ?? []), healthSignal(sv.health)]); continue }
          items.push({ key: `service:${k}`, ns: r.ns, x: r.x, z: r.z, sig: healthSignal(sv.health), external: sv.type === 'ExternalName' })
        }
        for (const g of net.groups) items.push({ key: '', ns: g.ns, x: g.x, z: g.z, sig: worst(groupSigs.get(g.key) ?? []), external: false })
      }
      for (const it of items) {
        const a = alpha(it.key, it.ns)
        c.set(world.colors.get(it.ns) ?? '#7D8A94')
        if (it.sig === 'err' && !still) blinking = true
        const beacon = it.sig === 'err' && !still && Math.sin(t * 6) < 0 ? colors.muted : colors.signal[it.sig]
        if (it.external) {
          put('signPost', it.x, 0, it.z, 1, 1, 1, colors.muted, a)
          put('signPanel', it.x, 0.9, it.z, 1, 1, 1, c, a, it.key)
          continue
        }
        put('relayRing', it.x, 0, it.z, 1, 1, 1, c, a, it.key)
        put('relayBase', it.x, 0.1, it.z, 1, 1, 1, it.key && it.key === selKey ? colors.accent : colors.platform, a, it.key)
        put('beacon', it.x, 0.3, it.z, 1, 1, 1, beacon, a)
      }

      // Portes : deux piliers et un linteau ; orange si une de leurs routes est cassée.
      for (const g of world.gates) {
        const slot = net.gates.get(g.name)
        if (!slot) continue
        const key = `gate:${g.name}`
        const a = alpha(key)
        c.copy(gateSignal(g) === 'warn' ? colors.signal.warn : colors.accent)
        if (key === selKey) c.lerp(WHITE, 0.35)
        put('gatePost', slot.x, 0, slot.z - GATE_SPAN / 2, 1, 1, 1, c, a, key)
        put('gatePost', slot.x, 0, slot.z + GATE_SPAN / 2, 1, 1, 1, c, a, key)
        put('gateLintel', slot.x, 2.1, slot.z, 1, 1, GATE_SPAN + 0.35, c, a, key)
      }

      // Citernes : grises si Bound, translucides orange si Pending, rouges si Lost.
      for (const v of world.volumes) {
        const slot = net.tanks.get(volumeKey(v))
        if (!slot) continue
        const key = `volume:${volumeKey(v)}`
        const sig = volumeSignal(v)
        const a = alpha(key, v.namespace)
        const body = key === selKey ? colors.accent : sig === 'ok' ? colors.tank : colors.signal[sig]
        put('tankBody', slot.x, 0, slot.z, slot.r, TANK_H, slot.r, body, sig === 'warn' ? a * 0.45 : a, key)
        put('tankCap', slot.x, TANK_H, slot.z, slot.r, 1, slot.r, colors.signal[sig], a)
      }
    }

    P.forEach((part, name) => part.commit(counts.get(name) ?? 0))
    pickables.net = PICKABLE.map((name) => ({ mesh: P.get(name)!.mesh, keys: keys.get(name)! }))
    if (blinking) tick(invalidate)
  })

  // Libellés des portes, compteurs des blocs regroupés (de près) ou des tronçons (de loin).
  const net = world.net
  const counters: { id: string; x: number; z: number; n: number }[] = []
  if (net && !far) for (const g of net.groups) counters.push({ id: g.key, x: g.x, z: g.z, n: g.members.length })
  if (net && far) {
    const agg = new Map<string, { x0: number; x1: number; z: number; n: number }>()
    for (const r of net.relays.values()) {
      const id = `${r.ns}|${r.avenue}`
      const a = agg.get(id) ?? { x0: r.x, x1: r.x, z: r.z, n: 0 }
      a.x0 = Math.min(a.x0, r.x)
      a.x1 = Math.max(a.x1, r.x)
      a.n++
      agg.set(id, a)
    }
    for (const [id, a] of agg) if (a.n > 1) counters.push({ id, x: (a.x0 + a.x1) / 2, z: a.z, n: a.n })
  }

  return (
    <>
      <group ref={group} />
      {net && [...net.gates.values()].map((g) => {
        const pill = pillTexture(g.name, theme)
        return (
          <sprite key={`gate-${g.name}`} position={[g.x, 3.0, g.z]} scale={[0.35 * pill.aspect, 0.35, 1]} raycast={() => null} renderOrder={10}>
            <spriteMaterial map={pill.tex} depthTest={false} transparent />
          </sprite>
        )
      })}
      {counters.map((k) => (
        <sprite key={`count-${k.id}`} position={[k.x, 1.2, k.z]} scale={[0.7, 0.35, 1]} raycast={() => null} renderOrder={10}>
          <spriteMaterial map={pillTexture(`×${k.n}`, theme).tex} depthTest={false} transparent />
        </sprite>
      ))}
    </>
  )
}
