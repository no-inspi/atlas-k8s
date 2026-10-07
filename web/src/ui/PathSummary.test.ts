import { beforeEach, describe, expect, it } from 'vitest'
import { useCluster } from '../store/cluster'
import { node, pod, route, service } from '../store/fixtures'
import { summaryText } from './PathSummary'

describe('summaryText', () => {
  beforeEach(() => useCluster.getState().reset())

  it('compte les objets du chemin d’un Service', () => {
    useCluster.getState().applyMessages([{ type: 'snapshot', rev: 1, nodes: [node()], pods: [pod()], services: [service({ endpoints: [{ podUID: 'u1', ready: true }] })], routes: [route()] }])
    const st = useCluster.getState()
    expect(summaryText({ type: 'service', key: 'production/api', name: 'api' }, st)).toMatch(/^Chemin : .*1 Service/)
  })

  it('vide sans sélection réseau, ou quand l’objet a disparu du flux', () => {
    useCluster.getState().applyMessages([{ type: 'snapshot', rev: 1, nodes: [node()], pods: [pod()] }])
    const st = useCluster.getState()
    expect(summaryText(null, st)).toBe('')
    expect(summaryText({ type: 'pod', key: 'u1', name: 'x' }, st)).toBe('')
    expect(summaryText({ type: 'service', key: 'production/api', name: 'api' }, st)).toBe('')
    expect(summaryText({ type: 'gate', key: 'nginx', name: 'nginx' }, st)).toBe('')
  })
})
