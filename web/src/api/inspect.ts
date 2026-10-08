import type { ArgoInfo, Gateway, PersistentVolume, Route, Service, Volume } from './types'
import { apiFetch, loginURL } from './http'
import { streamURL } from './stream'

// Appels de l'inspecteur : propriétaires, YAML, événements (REST) et logs (WebSocket).

export interface Ref {
  group: string
  version: string
  kind: string
  namespace: string
  name: string
}

export interface KubeEvent {
  type: string
  reason: string
  message: string
  count: number
  firstSeen: string
  lastSeen: string
  source?: string
}

export interface YamlDoc extends Ref {
  yaml: string
  argocd?: ArgoInfo
}

/** Erreur de l'API, avec le message de l'API server tel quel. */
export class ApiError extends Error {
  constructor(readonly status: number, message: string) {
    super(message)
  }
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await apiFetch(path)
  const body = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, (body as { error?: string }).error ?? res.statusText)
  return body as T
}

const seg = encodeURIComponent

export const getOwners = (ns: string, pod: string) =>
  getJSON<{ chain: Ref[] }>(`/api/namespaces/${seg(ns)}/pods/${seg(pod)}/owner`).then((r) => r.chain)

/** Segment de namespace d'une URL : « _ » pour un objet cluster-scoped (PV). */
export const nsSeg = (ns: string) => seg(ns || '_')

export const getEvents = (ns: string, name: string, resource = 'pods') =>
  getJSON<{ events: KubeEvent[] }>(`/api/namespaces/${nsSeg(ns)}/${seg(resource)}/${seg(name)}/events`).then((r) => r.events)

export const serviceRef = (s: Pick<Service, 'namespace' | 'name'>): Ref =>
  ({ group: '', version: 'v1', kind: 'Service', namespace: s.namespace, name: s.name })

export const volumeRef = (v: Pick<Volume, 'namespace' | 'name'>): Ref =>
  ({ group: '', version: 'v1', kind: 'PersistentVolumeClaim', namespace: v.namespace, name: v.name })

export const routeRef = (r: Pick<Route, 'source' | 'group' | 'namespace' | 'name'>): Ref => {
  switch (r.source) {
    case 'Ingress':
      return { group: 'networking.k8s.io', version: 'v1', kind: 'Ingress', namespace: r.namespace, name: r.name }
    case 'HTTPRoute':
    case 'GRPCRoute':
      return { group: 'gateway.networking.k8s.io', version: 'v1', kind: r.source, namespace: r.namespace, name: r.name }
    default: // IngressRoute, IngressRouteTCP, IngressRouteUDP : groupe Traefik de l'objet
      return { group: r.group, version: 'v1alpha1', kind: r.source, namespace: r.namespace, name: r.name }
  }
}

export const gatewayRef = (g: Pick<Gateway, 'namespace' | 'name'>): Ref =>
  ({ group: 'gateway.networking.k8s.io', version: 'v1', kind: 'Gateway', namespace: g.namespace, name: g.name })

export const pvRef = (p: Pick<PersistentVolume, 'name'>): Ref =>
  ({ group: '', version: 'v1', kind: 'PersistentVolume', namespace: '', name: p.name })

const RESOURCES: Record<string, string> = {
  Pod: 'pods', Service: 'services', PersistentVolumeClaim: 'persistentvolumeclaims', Ingress: 'ingresses', IngressRoute: 'ingressroutes',
}

/** Ressource de l'URL des événements d'un kind. */
export const eventsResource = (kind: string) => RESOURCES[kind] ?? `${kind.toLowerCase()}s`

export const getYaml = (r: Ref) =>
  getJSON<YamlDoc>(`/api/yaml/${seg(r.group || 'core')}/${seg(r.version)}/${seg(r.kind)}/${nsSeg(r.namespace)}/${seg(r.name)}`)

export function logsURL(loc: Pick<Location, 'protocol' | 'host'>, ns: string, pod: string, o: { container: string; previous: boolean; tailLines?: number }) {
  const q = new URLSearchParams({ container: o.container, previous: String(o.previous), tailLines: String(o.tailLines ?? 500) })
  return streamURL(loc, 0).replace('/api/stream', `/api/namespaces/${seg(ns)}/pods/${seg(pod)}/logs`) + `?${q}`
}

export type LogMessage =
  | { type: 'lines'; lines: { ts?: string; text: string }[] }
  | { type: 'dropped'; count: number }
  | { type: 'end' }
  | { type: 'error'; message: string; status?: number }

/** Ouvre le flux de logs ; renvoie la fonction de fermeture. */
export function openLogs(
  url: string,
  on: (m: LogMessage) => void,
  opts: { WebSocket?: typeof WebSocket; login?: () => void } = {},
): () => void {
  const WS = opts.WebSocket ?? WebSocket
  const login = opts.login ?? (() => window.location.assign(loginURL()))
  let closed = false
  let ended = false
  const ws = new WS(url)
  ws.onmessage = (e: MessageEvent) => {
    const m = JSON.parse(e.data as string) as LogMessage
    if (m.type === 'end' || m.type === 'error') ended = true
    on(m)
  }
  ws.onclose = (e: CloseEvent) => {
    if (closed) return
    if (e.code === 4401) return login()
    if (!ended) on({ type: 'error', message: `connexion aux logs interrompue (code ${e.code})` })
  }
  return () => {
    closed = true
    ws.close()
  }
}
