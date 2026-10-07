import type { Gateway, Route, Service, Volume } from '../api/types'

// Relations dérivées du flux : portes (déduites des routes, enrichies des
// Gateways reçus), routes d'un Service, Services et volumes d'un pod.

export interface Gate {
  name: string
  routes: Route[]
  /** Routes dont un backend est introuvable. */
  broken: number
  /** Routes refusées par leur Gateway. */
  refused: number
  /** Gateway représenté par la porte (Gateway API), s'il est visible. */
  gateway?: Gateway
}

export const routeBroken = (r: Route) => r.rules.some((x) => x.backend.state === 'missing')
export const routeRefused = (r: Route) => r.rules.some((x) => x.backend.state === 'refused')

/** Une porte Gateway s'appelle « ns/name » ; une IngressClass ne contient jamais de « / ». */
export const isGatewayGate = (name: string) => name.includes('/')

/** Portes d'une route ; repli sur gate pour un objet antérieur au jalon 9. */
export const gatesOfRoute = (r: Route): string[] => (r.gates?.length ? r.gates : [r.gate])

/** Ordre des noms indépendant de la locale du navigateur. */
const byName = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0)
const byNsName = (a: { namespace: string; name: string }, b: { namespace: string; name: string }) =>
  byName(a.namespace, b.namespace) || byName(a.name, b.name)

/**
 * Portes triées par nom : une par porte citée par une route (une route à
 * plusieurs portes apparaît sous chacune), plus une par Gateway visible, même
 * sans route. Routes de chaque porte triées par namespace puis nom.
 */
export function gatesOf(routes: Iterable<Route>, gateways?: ReadonlyMap<string, Gateway>): Gate[] {
  const by = new Map<string, Route[]>()
  for (const k of gateways?.keys() ?? []) by.set(k, [])
  for (const r of routes)
    for (const g of new Set(gatesOfRoute(r))) {
      const list = by.get(g)
      if (list) list.push(r)
      else by.set(g, [r])
    }
  return [...by]
    .sort(([a], [b]) => byName(a, b))
    .map(([name, rs]) => {
      const sorted = [...rs].sort(byNsName)
      const gw = gateways?.get(name)
      return {
        name, routes: sorted, broken: sorted.filter(routeBroken).length, refused: sorted.filter(routeRefused).length,
        ...(gw ? { gateway: gw } : {}),
      }
    })
}

export const routesTo = (routes: Iterable<Route>, svc: Pick<Service, 'namespace' | 'name'>) =>
  [...routes].filter((r) => r.rules.some((x) => x.backend.kind === 'Service' && x.backend.namespace === svc.namespace && x.backend.service === svc.name))

export const servicesOfPod = (services: Iterable<Service>, uid: string) =>
  [...services].filter((s) => s.endpoints.some((e) => e.podUID === uid))

export const volumesOfPod = (volumes: Iterable<Volume>, uid: string) => [...volumes].filter((v) => v.pods.includes(uid))

export const readyCount = (s: Service) => s.endpoints.filter((e) => e.ready).length
