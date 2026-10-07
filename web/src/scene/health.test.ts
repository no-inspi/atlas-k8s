import { describe, expect, it } from 'vitest'
import { gateway } from '../store/fixtures'
import { gateSignal, healthSignal, pvSignal, volumeSignal, worst } from './health'

describe('signaux', () => {
  it('traduit santé, phase et routes cassées en couleur de voyant', () => {
    expect(['ok', 'degraded', 'down', 'external'].map((h) => healthSignal(h as never))).toEqual(['ok', 'warn', 'err', 'mute'])
    expect(['Bound', 'Pending', 'Lost'].map((p) => volumeSignal({ phase: p as never }))).toEqual(['ok', 'warn', 'err'])
    expect(gateSignal({ name: 'nginx', broken: 0 })).toBe('ok')
    expect(gateSignal({ name: 'nginx', broken: 2 })).toBe('warn')
    expect(gateSignal({ name: '(sans gateway)', broken: 0, refused: 1 })).toBe('warn')
  })

  it('colore une porte Gateway selon son état', () => {
    const g = (over: Parameters<typeof gateway>[0] = {}, broken = 0, refused = 0) =>
      gateSignal({ name: 'infra/public', broken, refused, gateway: gateway(over) })
    expect(g()).toBe('ok')
    expect(g({ programmed: 'false' }, 1)).toBe('err')
    expect(g({}, 0, 1)).toBe('warn')
    expect(g({}, 1)).toBe('warn')
    expect(g({ listeners: [{ name: 'h', protocol: 'HTTP', port: 80, attachedRoutes: 0, ready: 'false' }] })).toBe('warn')
    expect(g({ programmed: 'unknown' })).toBe('mute')
    expect(g({ programmed: 'unknown' }, 0, 1)).toBe('warn')
    expect(g({ programmed: 'false', listeners: [{ name: 'h', protocol: 'HTTP', port: 80, attachedRoutes: 0, ready: 'false' }] })).toBe('err')
    expect(gateSignal({ name: 'infra/public', broken: 0, refused: 0 })).toBe('mute') // Gateway invisible
    expect(gateSignal({ name: 'infra/public', broken: 1, refused: 0 })).toBe('warn')
  })

  it('ne signale que les PV en échec', () => {
    expect(['Available', 'Released', 'Bound', 'Failed'].map((p) => pvSignal({ phase: p as never }))).toEqual(['mute', 'mute', 'mute', 'err'])
  })

  it('garde le pire signal d’un groupe', () => {
    expect(worst(['ok', 'warn', 'ok'])).toBe('warn')
    expect(worst(['mute', 'err', 'warn'])).toBe('err')
    expect(worst([])).toBe('mute')
  })
})
