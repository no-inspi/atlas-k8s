import { useEffect, useState } from 'react'
import { apiFetch } from '../api/http'

// Vérifications d'accès (SelfSubjectAccessReview au nom de l'utilisateur) pour
// griser les actions. Le refus final reste celui de l'API server.

export interface Check {
  verb: string
  group?: string
  resource: string
  subresource?: string
  namespace?: string
  name?: string
}

export interface Decision {
  allowed: boolean
  reason?: string
}

export const checkKey = (c: Check) => [c.verb, c.group ?? '', c.resource, c.subresource ?? '', c.namespace ?? '', c.name ?? ''].join('|')

/** Texte de l'infobulle d'un bouton refusé (spec). */
export function deniedText(c: Check): string {
  const res = (c.group ? `${c.resource}.${c.group}` : c.resource) + (c.subresource ? `/${c.subresource}` : '')
  return `Vous n'avez pas le droit ${c.verb} sur ${res}${c.namespace ? ` dans ${c.namespace}` : ''}`
}

/** Vérifications nécessaires aux actions d'un pod. */
export function podChecks(p: { namespace: string; name: string; owner: { kind: string; name: string } }): Record<string, Check> {
  const checks: Record<string, Check> = {
    delete: { verb: 'delete', resource: 'pods', namespace: p.namespace, name: p.name },
    exec: { verb: 'create', resource: 'pods', subresource: 'exec', namespace: p.namespace, name: p.name },
  }
  const res = `${p.owner.kind.toLowerCase()}s`
  if (p.owner.kind === 'Deployment' || p.owner.kind === 'StatefulSet')
    checks.scale = { verb: 'patch', group: 'apps', resource: res, subresource: 'scale', namespace: p.namespace, name: p.owner.name }
  if (['Deployment', 'StatefulSet', 'DaemonSet'].includes(p.owner.kind))
    checks.restart = { verb: 'patch', group: 'apps', resource: res, namespace: p.namespace, name: p.owner.name }
  return checks
}

export function nodeChecks(node: string): Record<string, Check> {
  return {
    cordon: { verb: 'patch', resource: 'nodes', name: node },
    drain: { verb: 'create', resource: 'pods', subresource: 'eviction' },
  }
}

/** Décisions par action ; null tant que la réponse n'est pas arrivée. */
export function useAccess(checks: Record<string, Check>): Record<string, Decision> | null {
  const [decisions, setDecisions] = useState<Record<string, Decision> | null>(null)
  const key = Object.values(checks).map(checkKey).join(';')
  useEffect(() => {
    let live = true
    setDecisions(null)
    const names = Object.keys(checks)
    apiFetch('/api/access-review', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ checks: names.map((n) => checks[n]) }),
    })
      .then((r) => r.json())
      .then((body: { results: Decision[] }) => {
        if (live) setDecisions(Object.fromEntries(names.map((n, i) => [n, body.results[i] ?? { allowed: false }])))
      })
      .catch(() => live && setDecisions(Object.fromEntries(names.map((n) => [n, { allowed: false, reason: 'vérification impossible' }]))))
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  return decisions
}
