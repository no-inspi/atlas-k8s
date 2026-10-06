import { describe, expect, it } from 'vitest'
import { legendItems } from './legend'

const counts = new Map(Object.entries({ prod: 40, staging: 10, 'team-001': 25, 'team-002': 25, monitoring: 5, argocd: 2 }))

describe('legendItems', () => {
  it('montre les namespaces les plus peuplés, triés par nom', () => {
    const { visible, hidden } = legendItems(counts, null, 3)
    expect(visible).toEqual(['prod', 'team-001', 'team-002'])
    expect(hidden).toEqual(['argocd', 'monitoring', 'staging'])
  })

  it('garde visible le namespace filtré', () => {
    expect(legendItems(counts, 'argocd', 3).visible).toEqual(['argocd', 'prod', 'team-001', 'team-002'])
  })

  it('montre tout quand il y a peu de namespaces', () => {
    expect(legendItems(counts, null).hidden).toEqual([])
  })
})
