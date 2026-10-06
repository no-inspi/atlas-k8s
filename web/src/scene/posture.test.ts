import { describe, expect, it } from 'vitest'
import { poseAt, postureFor, type PostureKind } from './posture'

const p = (displayStatus: string, ready = true) => ({ displayStatus, ready })

// Table « Statuts et postures » de la spec.
describe('postureFor', () => {
  it.each([
    ['Running', true, 'ok', false, 'idle'],
    ['Running', false, 'warn', false, 'sulk'],
    ['Pending', false, 'warn', false, 'stomp'],
    ['ContainerCreating', false, 'warn', false, 'spin'],
    ['PodInitializing', false, 'warn', false, 'spin'],
    ['Init:0/1', false, 'warn', false, 'spin'],
    ['CrashLoopBackOff', false, 'err', true, 'fallen'],
    ['Error', false, 'err', true, 'fallen'],
    ['OOMKilled', false, 'err', true, 'fallen'],
    ['ImagePullBackOff', false, 'err', false, 'sitting'],
    ['ErrImagePull', false, 'err', false, 'sitting'],
    ['Terminating', false, 'mute', false, 'shrink'],
    ['Completed', false, 'done', false, 'fade'],
  ])('%s (ready=%s) → antenne %s, clignote=%s, %s', (status, ready, antenna, blink, kind) => {
    const r = postureFor(p(status as string, ready as boolean))
    expect(r.antenna).toBe(antenna)
    expect(r.blink).toBe(blink)
    expect(r.kind).toBe(kind)
  })

  it('affiche un point d’interrogation pour ImagePullBackOff', () => {
    expect(postureFor(p('ImagePullBackOff', false)).question).toBe(true)
    expect(postureFor(p('Running')).question).toBe(false)
  })

  it('traite un statut inconnu comme un avertissement', () => {
    expect(postureFor(p('Unknown', false)).antenna).toBe('warn')
  })
})

describe('poseAt', () => {
  const kinds: PostureKind[] = ['idle', 'sulk', 'stomp', 'spin', 'fallen', 'sitting', 'shrink', 'fade']

  it('reste borné pour toutes les postures', () => {
    for (const k of kinds)
      for (let t = 0; t < 20; t += 0.37) {
        const q = poseAt(k, t, 1.3, false)
        expect(Math.abs(q.hop)).toBeLessThan(0.3)
        expect(q.scale).toBeGreaterThan(0)
        expect(q.scale).toBeLessThanOrEqual(1)
        expect(q.eyes).toBeGreaterThan(0)
      }
  })

  it('fait tomber les pods en erreur et réduit ceux en création', () => {
    expect(poseAt('fallen', 3, 0, false).fall).toBeLessThan(-1)
    expect(poseAt('spin', 3, 0, false).scale).toBe(0.8)
    expect(poseAt('shrink', 3, 0, false).scale).toBeLessThan(0.1)
    expect(poseAt('sulk', 3, 0, false).headTilt).toBeGreaterThan(0.2)
  })

  it('coupe les animations idle avec prefers-reduced-motion', () => {
    const a = poseAt('idle', 1, 0.5, true)
    const b = poseAt('idle', 7, 0.5, true)
    expect(a).toEqual(b)
    expect(a.eyes).toBe(1)
  })
})
