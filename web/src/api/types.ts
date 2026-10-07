// Miroir du modèle réduit Go (internal/model) et des messages de /api/stream.
// CPU en millicores, mémoire en octets, dates en ISO 8601.

export interface Resources {
  cpu: number
  memory: number
  pods?: number
}

export interface Condition {
  type: string
  status: string
  reason?: string
  message?: string
}

export interface Taint {
  key: string
  value?: string
  effect: string
}

export interface Node {
  name: string
  pool: string
  instanceType: string
  zone: string
  spot: boolean
  gpu: number
  allocatable: Resources
  requested: Resources
  conditions: Condition[]
  taints: Taint[]
  unschedulable: boolean
  kubeletVersion: string
  createdAt: string
  /** Node que l'utilisateur ne peut pas lister, connu par le nodeName de ses pods. */
  ghost?: boolean
}

export interface ContainerStatus {
  name: string
  image: string
  ready: boolean
  state: 'waiting' | 'running' | 'terminated' | ''
  reason?: string
  restarts: number
  init?: boolean
}

export interface OwnerRef {
  kind: string
  name: string
}

export interface Pod {
  uid: string
  name: string
  namespace: string
  nodeName: string
  phase: string
  displayStatus: string
  statusMessage?: string
  ready: boolean
  restarts: number
  containers: ContainerStatus[]
  owner: OwnerRef
  requests: Resources
  limits: Resources
  podIP: string
  createdAt: string
  qosClass: string
}

export interface ArgoInfo {
  application: string
  syncStatus: string
}

export interface Workload {
  kind: string
  name: string
  namespace: string
  replicas: number
  readyReplicas: number
  argocd?: ArgoInfo
}

export interface Namespace {
  name: string
  color?: string // annotation atlas.io/color
}

export interface Usage {
  cpu: number
  memory: number
}

export interface Metrics {
  pods: Record<string, Usage>
  nodes: Record<string, Usage>
}

export type Health = 'ok' | 'degraded' | 'down' | 'external'

export interface ServicePort {
  name?: string
  port: number
  targetPort?: string
  protocol: string
  nodePort?: number
}

export interface Endpoint {
  podUID: string
  ready: boolean
}

export interface Service {
  namespace: string
  name: string
  type: string // ClusterIP | NodePort | LoadBalancer | ExternalName
  headless?: boolean
  clusterIP?: string
  ports: ServicePort[]
  loadBalancer?: string[]
  externalName?: string
  endpoints: Endpoint[]
  health: Health
}

export type Tri = 'true' | 'false' | 'unknown'
export type RouteSource = 'Ingress' | 'IngressRoute' | 'IngressRouteTCP' | 'IngressRouteUDP' | 'HTTPRoute' | 'GRPCRoute'

export interface Backend {
  namespace: string
  service: string
  port?: string
  kind: string // Service | TraefikService | autre kind Gateway API
  state: 'ok' | 'missing' | 'refused' | 'indirect'
  /** Part du trafic de la règle, en pour mille (seulement si la règle a plusieurs backends). */
  weight?: number
  /** Backend miroir (Traefik mirroring) : copie du trafic, sans réponse. */
  mirror?: boolean
  /** Miroir : pourcentage du trafic copié. */
  percent?: number
  /** TraefikService racine traversé, « ns/name ». */
  via?: string
}

export interface Rule {
  host?: string
  path?: string
  match?: string
  backend: Backend
}

/** État d'une route Gateway API vis-à-vis d'un de ses Gateways. */
export interface RouteParent {
  gateway: string // « ns/name »
  accepted: Tri
  resolvedRefs: Tri
  reason?: string
}

export interface Route {
  source: RouteSource
  group: string
  namespace: string
  name: string
  /** Première porte (compatibilité) : gates[0]. */
  gate: string
  /** Portes qui servent la route ; une porte Gateway s'appelle « ns/name ». */
  gates: string[]
  rules: Rule[]
  addresses?: string[]
  parents?: RouteParent[]
}

export interface Listener {
  name: string
  protocol: string
  port: number
  hostname?: string
  attachedRoutes: number
  ready: Tri
}

export interface Gateway {
  namespace: string
  name: string
  class: string
  accepted: Tri
  programmed: Tri
  reason?: string
  message?: string
  addresses?: string[]
  listeners: Listener[]
}

/** PersistentVolume sans PVC existant (citerne vide). */
export interface PersistentVolume {
  name: string
  storageClass: string
  capacity: number
  accessModes: string[]
  reclaimPolicy: string
  phase: 'Available' | 'Released' | 'Failed' | 'Bound'
  /** Ancien PVC, « ns/name ». */
  claimRef?: string
}

export interface Volume {
  namespace: string
  name: string
  storageClass: string
  requested: number
  capacity: number
  accessModes: string[]
  phase: 'Pending' | 'Bound' | 'Lost'
  volumeName?: string
  pods: string[]
}

export type Kind = 'node' | 'pod' | 'workload' | 'namespace' | 'service' | 'route' | 'volume' | 'gateway' | 'persistentVolume'

export type Message =
  | {
      type: 'snapshot'; rev: number; nodes?: Node[]; pods?: Pod[]; workloads?: Workload[]; namespaces?: Namespace[]
      services?: Service[]; routes?: Route[]; volumes?: Volume[]; gateways?: Gateway[]; persistentVolumes?: PersistentVolume[]
    }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'node'; obj: Node }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'pod'; obj: Pod }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'workload'; obj: Workload }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'namespace'; obj: Namespace }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'service'; obj: Service }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'route'; obj: Route }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'volume'; obj: Volume }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'gateway'; obj: Gateway }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'persistentVolume'; obj: PersistentVolume }
  | { type: 'metrics'; metrics: Metrics }

export interface Me {
  user: string
  groups: string[]
  cluster: string
  demo: boolean
  authenticated: boolean
  features?: { actions: boolean; exec: boolean; execDeniedNamespaces: string[] }
}

export const workloadKey = (w: Pick<Workload, 'kind' | 'namespace' | 'name'>) => `${w.kind}/${w.namespace}/${w.name}`
export const serviceKey = (s: Pick<Service, 'namespace' | 'name'>) => `${s.namespace}/${s.name}`
export const routeKey = (r: Pick<Route, 'source' | 'namespace' | 'name'>) => `${r.source}/${r.namespace}/${r.name}`
export const volumeKey = (v: Pick<Volume, 'namespace' | 'name'>) => `${v.namespace}/${v.name}`
export const gatewayKey = (g: Pick<Gateway, 'namespace' | 'name'>) => `${g.namespace}/${g.name}`
export const pvKey = (p: Pick<PersistentVolume, 'name'>) => p.name
