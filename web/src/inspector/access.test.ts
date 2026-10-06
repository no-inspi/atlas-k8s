import { describe, expect, it } from 'vitest'
import { deniedText, nodeChecks, podChecks } from './access'

describe('vérifications d’accès', () => {
  it('couvre les actions possibles selon le propriétaire du pod', () => {
    const dep = podChecks({ namespace: 'prod', name: 'api-1', owner: { kind: 'Deployment', name: 'api' } })
    expect(Object.keys(dep).sort()).toEqual(['delete', 'exec', 'restart', 'scale'])
    expect(dep.scale).toEqual({ verb: 'patch', group: 'apps', resource: 'deployments', subresource: 'scale', namespace: 'prod', name: 'api' })
    const ds = podChecks({ namespace: 'mon', name: 'ne-1', owner: { kind: 'DaemonSet', name: 'ne' } })
    expect(Object.keys(ds).sort()).toEqual(['delete', 'exec', 'restart'])
    const job = podChecks({ namespace: 'prod', name: 'b-1', owner: { kind: 'Job', name: 'b' } })
    expect(Object.keys(job).sort()).toEqual(['delete', 'exec'])
    expect(nodeChecks('n1').cordon).toEqual({ verb: 'patch', resource: 'nodes', name: 'n1' })
  })

  it('formule le refus comme la spec', () => {
    expect(deniedText({ verb: 'delete', resource: 'pods', namespace: 'kube-system' })).toBe("Vous n'avez pas le droit delete sur pods dans kube-system")
    expect(deniedText({ verb: 'patch', group: 'apps', resource: 'deployments', subresource: 'scale' })).toBe("Vous n'avez pas le droit patch sur deployments.apps/scale")
  })
})
