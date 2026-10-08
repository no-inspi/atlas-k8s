import { describe, expect, it } from 'vitest'
import { age, fmtCpu, fmtMem, fmtShare, pct } from './format'

describe('format', () => {
  it('formate le CPU comme kubectl', () => {
    expect(fmtCpu(250)).toBe('250m')
    expect(fmtCpu(1000)).toBe('1')
    expect(fmtCpu(3920)).toBe('3.92')
    expect(fmtCpu(2500)).toBe('2.5')
  })

  it('formate la mémoire en Mi ou Gi', () => {
    expect(fmtMem(256 * 2 ** 20)).toBe('256Mi')
    expect(fmtMem(1.5 * 2 ** 30)).toBe('1.5Gi')
    expect(fmtMem(13000 * 2 ** 20)).toBe('12.7Gi')
  })

  it('donne un âge compact', () => {
    const now = Date.parse('2026-10-06T10:00:00Z')
    expect(age('2026-10-06T09:59:30Z', now)).toBe('30s')
    expect(age('2026-10-06T09:15:00Z', now)).toBe('45m')
    expect(age('2026-10-06T07:50:00Z', now)).toBe('2h10')
    expect(age('2026-10-03T08:00:00Z', now)).toBe('3j2h')
  })

  it('borne les pourcentages', () => {
    expect(pct(50, 200)).toBe(25)
    expect(pct(5, 0)).toBe(0)
    expect(pct(300, 200)).toBe(100)
  })

  it('formate une part du trafic en pour mille', () => {
    expect(fmtShare(900)).toBe('90 %')
    expect(fmtShare(1000)).toBe('100 %')
    expect(fmtShare(333)).toBe('33.3 %')
    expect(fmtShare(0)).toBe('0 %')
  })
})
