import type { Route, Service, Volume } from '../api/types'

// Relations dérivées du flux : portes (déduites des routes), routes d'un
// Service, Services et volumes d'un pod.

export interface Gate {
  name: string
  routes: Route[]
  /** Routes dont un backend est introuvable. */
  broken: number
}

export const routeBroken = (r: Route) => r.rules.some((x) => x.backend.state === 'missing')

/** Ordre des noms indépendant de la locale du navigateur. */
const byName = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0)
const byNsName = (a: { namespace: string; name: string }, b: { namespace: string; name: string }) =>
  byName(a.namespace, b.namespace) || byName(a.name, b.name)

/** Portes triées par nom, routes de chaque porte triées par namespace puis nom. */
export function gatesOf(routes: Iterable<Route>): Gate[] {
  const by = new Map<string, Route[]>()
  for (const r of routes) {
    const list = by.get(r.gate)
    if (list) list.push(r)
    else by.set(r.gate, [r])
  }
  return [...by]
    .sort(([a], [b]) => byName(a, b))
    .map(([name, rs]) => {
      const sorted = [...rs].sort(byNsName)
      return { name, routes: sorted, broken: sorted.filter(routeBroken).length }
    })
}

export const routesTo = (routes: Iterable<Route>, svc: Pick<Service, 'namespace' | 'name'>) =>
  [...routes].filter((r) => r.rules.some((x) => x.backend.kind === 'Service' && x.backend.namespace === svc.namespace && x.backend.service === svc.name))

export const servicesOfPod = (services: Iterable<Service>, uid: string) =>
  [...services].filter((s) => s.endpoints.some((e) => e.podUID === uid))

export const volumesOfPod = (volumes: Iterable<Volume>, uid: string) => [...volumes].filter((v) => v.pods.includes(uid))

export const readyCount = (s: Service) => s.endpoints.filter((e) => e.ready).length
