import { describe, expect, it } from 'vitest'
import { feedForNode, feedForPod } from './feed'
import { node, pod } from './fixtures'

describe('feedForPod', () => {
  it('annonce une création', () => {
    const f = feedForPod(undefined, pod({ nodeName: '', displayStatus: 'Pending' }))
    expect(f?.text).toBe('Deployment api a créé api-abc-x1')
    expect(f?.level).toBe('')
  })

  it('annonce un placement avec le nom court du node', () => {
    const f = feedForPod(pod({ nodeName: '', displayStatus: 'Pending' }), pod({ displayStatus: 'ContainerCreating' }))
    expect(f?.text).toBe('Scheduler : api-abc-x1 → n1')
  })

  it('signale un redémarrage en erreur', () => {
    const f = feedForPod(pod(), pod({ displayStatus: 'Error', restarts: 3 }))
    expect(f).toEqual(expect.objectContaining({ text: 'api-abc-x1 a redémarré (3 redémarrages)', level: 'e' }))
  })

  it('signale un passage en erreur sans redémarrage', () => {
    const f = feedForPod(pod({ displayStatus: 'ContainerCreating' }), pod({ displayStatus: 'ImagePullBackOff' }))
    expect(f).toEqual(expect.objectContaining({ text: 'api-abc-x1 : ImagePullBackOff', level: 'e' }))
  })

  it('signale un pod impossible à placer', () => {
    const prev = pod({ nodeName: '', displayStatus: 'Pending' })
    const f = feedForPod(prev, { ...prev, statusMessage: '0/6 nodes are available: 6 Insufficient cpu.' })
    expect(f).toEqual(expect.objectContaining({ text: 'api-abc-x1 ne peut pas être placé', level: 'w' }))
  })

  it("annonce l'arrêt puis reste silencieux à la suppression", () => {
    const terminating = pod({ displayStatus: 'Terminating' })
    expect(feedForPod(pod(), terminating)?.text).toBe('Arrêt de api-abc-x1')
    expect(feedForPod(terminating, undefined)).toBeNull()
    expect(feedForPod(pod(), undefined)?.text).toBe('Pod supprimé : api-abc-x1')
  })

  it('ignore les changements sans intérêt', () => {
    expect(feedForPod(pod(), pod({ podIP: '10.0.0.9' }))).toBeNull()
    expect(feedForPod(pod({ displayStatus: 'CrashLoopBackOff' }), pod({ displayStatus: 'Running' }))).toBeNull()
  })
})

describe('feedForNode', () => {
  it('annonce cordon et uncordon', () => {
    expect(feedForNode(node(), node({ unschedulable: true }))).toEqual(expect.objectContaining({ text: 'Node n1 cordonné', level: 'w' }))
    expect(feedForNode(node({ unschedulable: true }), node())?.text).toBe('Node n1 réactivé')
    expect(feedForNode(node(), node({ requested: { cpu: 1, memory: 1 } }))).toBeNull()
  })
})
