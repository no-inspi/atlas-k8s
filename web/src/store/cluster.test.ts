import { beforeEach, describe, expect, it } from 'vitest'
import { useCluster } from './cluster'
import { node, pod, workload } from './fixtures'

const s = () => useCluster.getState()

beforeEach(() => s().reset())

describe('applyMessages', () => {
  it('remplace tout l’état avec un snapshot, sans alimenter le bandeau', () => {
    s().applyMessages([{ type: 'upsert', kind: 'pod', rev: 1, obj: pod({ uid: 'old' }) }])
    s().applyMessages([{ type: 'snapshot', rev: 10, nodes: [node()], pods: [pod()], workloads: [workload()] }])
    expect(s().rev).toBe(10)
    expect([...s().pods.keys()]).toEqual(['u1'])
    expect(s().nodes.size).toBe(1)
    expect(s().workloads.get('Deployment/production/api')?.replicas).toBe(3)
    expect(s().feed.filter((f) => f.text.includes('a créé'))).toHaveLength(1) // seulement l'upsert initial
    expect(s().connection).toBe('live')
  })

  it('applique upserts et deletes et suit le rev', () => {
    const v0 = s().version
    s().applyMessages([
      { type: 'upsert', kind: 'pod', rev: 1, obj: pod({ uid: 'a' }) },
      { type: 'upsert', kind: 'pod', rev: 2, obj: pod({ uid: 'b' }) },
      { type: 'delete', kind: 'pod', rev: 3, obj: pod({ uid: 'a' }) },
      { type: 'upsert', kind: 'workload', rev: 4, obj: workload() },
    ])
    expect([...s().pods.keys()]).toEqual(['b'])
    expect(s().rev).toBe(4)
    expect(s().version).toBeGreaterThan(v0)
  })

  it('suit les namespaces et leur couleur', () => {
    s().applyMessages([{ type: 'snapshot', rev: 1, namespaces: [{ name: 'prod', color: '#123456' }] }])
    expect(s().namespaces.get('prod')?.color).toBe('#123456')
    s().applyMessages([{ type: 'upsert', kind: 'namespace', rev: 2, obj: { name: 'dev' } }])
    s().applyMessages([{ type: 'delete', kind: 'namespace', rev: 3, obj: { name: 'prod' } }])
    expect([...s().namespaces.keys()]).toEqual(['dev'])
  })

  it('stocke les métriques sans changer le rev', () => {
    s().applyMessages([{ type: 'snapshot', rev: 5 }])
    s().applyMessages([{ type: 'metrics', metrics: { pods: { u1: { cpu: 120, memory: 10 } }, nodes: {} } }])
    expect(s().metrics.pods.u1.cpu).toBe(120)
    expect(s().rev).toBe(5)
  })

  it('garde au plus 5 entrées dans le bandeau, la plus récente en tête', () => {
    for (let i = 0; i < 7; i++) s().applyMessages([{ type: 'upsert', kind: 'pod', rev: i + 1, obj: pod({ uid: `p${i}`, name: `p${i}` }) }])
    expect(s().feed).toHaveLength(5)
    expect(s().feed[0].text).toContain('p6')
  })
})

describe('sélection et filtre', () => {
  it('garde la sélection d’un pod supprimé pour afficher son nom', () => {
    s().applyMessages([{ type: 'snapshot', rev: 1, pods: [pod()] }])
    s().select({ type: 'pod', key: 'u1' })
    s().applyMessages([{ type: 'delete', kind: 'pod', rev: 2, obj: pod() }])
    expect(s().selection).toEqual({ type: 'pod', key: 'u1', name: 'api-abc-x1' })
    expect(s().pods.has('u1')).toBe(false)
  })

  it('conserve l’onglet actif quand on change de pod', () => {
    s().applyMessages([{ type: 'snapshot', rev: 1, pods: [pod(), pod({ uid: 'u2', name: 'other' })] }])
    s().select({ type: 'pod', key: 'u1' })
    s().openYaml({ kind: 'ReplicaSet', name: 'api-abc' })
    expect(s().inspectorTab).toBe('yaml')
    s().select({ type: 'pod', key: 'u2' })
    expect(s().inspectorTab).toBe('yaml')
    expect(s().yamlTarget).toBeNull()
  })

  it('notifie un résultat d’action dans un toast et dans le bandeau', () => {
    s().notify('Pod api-1 supprimé')
    s().notify('refusé', 'e')
    expect(s().toasts.map((t) => t.text)).toEqual(['Pod api-1 supprimé', 'refusé'])
    expect(s().feed[0]).toEqual(expect.objectContaining({ text: 'refusé', level: 'e' }))
    s().dismissToast(s().toasts[0].id)
    expect(s().toasts).toHaveLength(1)
  })

  it('bascule le filtre de namespace', () => {
    s().toggleNsFilter('production')
    expect(s().nsFilter).toBe('production')
    s().toggleNsFilter('production')
    expect(s().nsFilter).toBeNull()
  })
})
