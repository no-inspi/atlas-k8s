import { describe, expect, it } from 'vitest'
import { gateSignal, healthSignal, volumeSignal, worst } from './health'

describe('signaux', () => {
  it('traduit santé, phase et routes cassées en couleur de voyant', () => {
    expect(['ok', 'degraded', 'down', 'external'].map((h) => healthSignal(h as never))).toEqual(['ok', 'warn', 'err', 'mute'])
    expect(['Bound', 'Pending', 'Lost'].map((p) => volumeSignal({ phase: p as never }))).toEqual(['ok', 'warn', 'err'])
    expect(gateSignal({ broken: 0 })).toBe('ok')
    expect(gateSignal({ broken: 2 })).toBe('warn')
  })

  it('garde le pire signal d’un groupe', () => {
    expect(worst(['ok', 'warn', 'ok'])).toBe('warn')
    expect(worst(['mute', 'err', 'warn'])).toBe('err')
    expect(worst([])).toBe('mute')
  })
})
