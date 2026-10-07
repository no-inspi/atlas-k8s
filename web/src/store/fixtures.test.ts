import { describe, expect, it } from 'vitest'
import { route } from './fixtures'

describe('fixture route', () => {
  it('garde gate === gates[0] quand seul gates est passé', () => {
    const r = route({ gates: ['infra/public', 'infra/internal'] })
    expect(r.gate).toBe('infra/public')
    expect(r.gates).toEqual(['infra/public', 'infra/internal'])
  })
  it('dérive gates de gate', () => {
    expect(route({ gate: 'traefik' }).gates).toEqual(['traefik'])
    expect(route().gate).toBe('nginx')
  })
})
