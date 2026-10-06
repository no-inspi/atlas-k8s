// Correspondance statut → antenne et posture du robot (table « Statuts et
// postures » de la spec), et animation de chaque posture.

export type Antenna = 'ok' | 'warn' | 'err' | 'mute' | 'done'
export type PostureKind = 'idle' | 'sulk' | 'stomp' | 'spin' | 'fallen' | 'sitting' | 'shrink' | 'fade'

export interface Posture {
  antenna: Antenna
  blink: boolean // antenne clignotante
  kind: PostureKind
  question: boolean // point d'interrogation au-dessus de la tête
}

const CRASHED = new Set(['CrashLoopBackOff', 'Error', 'OOMKilled', 'RunContainerError', 'CreateContainerError'])
const IMAGE = new Set(['ImagePullBackOff', 'ErrImagePull', 'InvalidImageName'])
const STARTING = new Set(['ContainerCreating', 'PodInitializing'])

export function postureFor(p: { displayStatus: string; ready: boolean }): Posture {
  const s = p.displayStatus
  const r = (antenna: Antenna, kind: PostureKind, blink = false, question = false): Posture => ({ antenna, blink, kind, question })
  if (s === 'Running') return p.ready ? r('ok', 'idle') : r('warn', 'sulk')
  if (s === 'Pending') return r('warn', 'stomp')
  if (STARTING.has(s) || s.startsWith('Init:')) return r('warn', 'spin')
  if (CRASHED.has(s) || s.startsWith('Init:Error') || s.startsWith('Init:CrashLoopBackOff')) return r('err', 'fallen', true)
  if (IMAGE.has(s)) return r('err', 'sitting', false, true)
  if (s === 'Terminating') return r('mute', 'shrink')
  if (s === 'Completed' || s === 'Succeeded') return r('done', 'fade')
  return r('warn', 'sulk')
}

export interface Pose {
  hop: number // élévation du corps
  yaw: number // rotation autour de l'axe vertical
  fall: number // bascule en arrière (radians, négatif)
  headTilt: number // tête baissée (radians)
  sit: number // 0 debout, 1 assis
  scale: number // échelle cible
  eyes: number // échelle verticale des yeux (clignement)
}

const FACE = Math.PI / 4 // les robots regardent la caméra isométrique

export function poseAt(kind: PostureKind, t: number, phase: number, reducedMotion: boolean): Pose {
  const pose: Pose = { hop: 0, yaw: FACE, fall: 0, headTilt: 0, sit: 0, scale: 1, eyes: 1 }
  const still = reducedMotion
  switch (kind) {
    case 'idle':
      if (!still) {
        pose.hop = Math.abs(Math.sin(t * 2.2 + phase)) * 0.035
        pose.yaw = FACE + Math.sin(t * 0.7 + phase) * 0.3
        pose.eyes = Math.sin(t * 0.9 + phase * 3) > 0.97 ? 0.15 : 1
      }
      break
    case 'sulk':
      pose.headTilt = 0.35
      break
    case 'stomp':
      if (!still) {
        pose.hop = Math.abs(Math.sin(t * 4 + phase)) * 0.22
        pose.yaw = FACE + Math.sin(t * 2 + phase) * 0.6
      }
      break
    case 'spin':
      pose.scale = 0.8
      if (!still) pose.yaw = t * 6 + phase
      break
    case 'fallen':
      pose.fall = -1.35
      pose.eyes = 0.35
      break
    case 'sitting':
      pose.sit = 1
      break
    case 'shrink':
      pose.scale = 0.02
      break
    case 'fade':
      pose.eyes = 0.15
      break
  }
  return pose
}
