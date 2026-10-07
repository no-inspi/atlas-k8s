import { describe, expect, it } from 'vitest'
import { ribbon } from './GroundLinks'

describe('ribbon', () => {
  it('quatre sommets et deux triangles par segment, abscisse curviligne continue', () => {
    const r = ribbon([{ points: [[0, 0], [2, 0], [2, 3]], alpha: 0.5, live: true }], 0.2, 0.03)
    expect(r.position.length).toBe(2 * 4 * 3)
    expect(r.index.length).toBe(2 * 6)
    expect([...r.dist]).toEqual([0, 0, 2, 2, 2, 2, 5, 5])
    expect(r.alpha[0]).toBe(0.5)
    expect(r.live[7]).toBe(1)
    expect(r.position[1]).toBeCloseTo(0.03)
  })

  it('accepte des liens sans segment', () => {
    expect(ribbon([{ points: [[1, 1]], alpha: 1, live: false }], 0.1, 0).index.length).toBe(0)
  })
})
