import { describe, expect, it } from 'vitest'
import { pod } from '../store/fixtures'
import { BLOCK_MAX, BLOCK_MIN, blockHeight, compareOccupants, DEFAULT_VIEW, isVisible, kindOf, sortKinds } from './podView'

describe('podView', () => {
  it('masque kube-system par défaut', () => {
    expect(isVisible(pod({ namespace: 'kube-system' }), DEFAULT_VIEW)).toBe(false)
    expect(isVisible(pod({ namespace: 'kube-system' }), DEFAULT_VIEW, 'kube-system')).toBe(true)
    expect(isVisible(pod({ namespace: 'production' }), DEFAULT_VIEW)).toBe(true)
  })

  it('appelle « Pod » un pod sans propriétaire', () => {
    expect(kindOf(pod({ owner: { kind: '', name: '' } }))).toBe('Pod')
    expect(isVisible(pod({ owner: { kind: '', name: '' } }), { ...DEFAULT_VIEW, hiddenKinds: ['Pod'] })).toBe(false)
  })

  it('ordonne les types connus avant les autres', () => {
    expect(sortKinds(['Pod', 'Rollout', 'Deployment', 'DaemonSet', 'Deployment'])).toEqual(['DaemonSet', 'Deployment', 'Pod', 'Rollout'])
  })

  it('trie par type puis par workload puis par nom', () => {
    const occ = (name: string, kind: string, owner: string) => ({ rep: pod({ name, owner: { kind, name: owner } }), size: 0 })
    const xs = [occ('b', 'Deployment', 'web'), occ('a', 'Deployment', 'api'), occ('c', 'DaemonSet', 'exp')]
    expect(xs.sort(compareOccupants('kind')).map((o) => o.rep.name)).toEqual(['c', 'a', 'b'])
  })

  it('borne la hauteur des blocs', () => {
    expect(blockHeight(0, 4000)).toBe(BLOCK_MIN)
    expect(blockHeight(100, 0)).toBe(BLOCK_MIN)
    expect(blockHeight(4000, 4000)).toBe(BLOCK_MAX)
    expect(blockHeight(1000, 4000)).toBe(BLOCK_MAX)
    const small = blockHeight(100, 4000), mid = blockHeight(400, 4000)
    expect(small).toBeGreaterThan(BLOCK_MIN)
    expect(mid).toBeGreaterThan(small)
  })
})
