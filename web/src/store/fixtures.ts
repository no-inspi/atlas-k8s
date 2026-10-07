import type { Gateway, Node, PersistentVolume, Pod, Route, Service, Volume, Workload } from '../api/types'

export function pod(over: Partial<Pod> = {}): Pod {
  return {
    uid: 'u1', name: 'api-abc-x1', namespace: 'production', nodeName: 'gke-prod-default-pool-aaaa-n1',
    phase: 'Running', displayStatus: 'Running', ready: true, restarts: 0,
    containers: [{ name: 'api', image: 'api:1', ready: true, state: 'running', restarts: 0 }],
    owner: { kind: 'Deployment', name: 'api' }, requests: { cpu: 250, memory: 256 << 20 },
    limits: { cpu: 0, memory: 384 << 20 }, podIP: '10.52.0.4', createdAt: '2026-10-06T08:00:00Z', qosClass: 'Burstable',
    ...over,
  }
}

export function node(over: Partial<Node> = {}): Node {
  return {
    name: 'gke-prod-default-pool-aaaa-n1', pool: 'default-pool', instanceType: 'e2-standard-4', zone: 'europe-west1-b',
    spot: false, gpu: 0, allocatable: { cpu: 3920, memory: 13000 << 20, pods: 110 }, requested: { cpu: 1000, memory: 1 << 30 },
    conditions: [{ type: 'Ready', status: 'True' }], taints: [], unschedulable: false,
    kubeletVersion: 'v1.31.4', createdAt: '2026-10-01T00:00:00Z',
    ...over,
  }
}

export function workload(over: Partial<Workload> = {}): Workload {
  return { kind: 'Deployment', name: 'api', namespace: 'production', replicas: 3, readyReplicas: 3, ...over }
}

export function service(over: Partial<Service> = {}): Service {
  return {
    namespace: 'production', name: 'api', type: 'ClusterIP', clusterIP: '10.96.0.10',
    ports: [{ name: 'http', port: 80, targetPort: 'http', protocol: 'TCP' }],
    endpoints: [{ podUID: 'u1', ready: true }], health: 'ok',
    ...over,
  }
}

export function route(over: Partial<Route> = {}): Route {
  const gate = over.gate ?? over.gates?.[0] ?? 'nginx'
  return {
    source: 'Ingress', group: 'networking.k8s.io', namespace: 'production', name: 'storefront', gate, gates: over.gates ?? [gate],
    rules: [{ host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'api', port: '80', kind: 'Service', state: 'ok' } }],
    ...over,
  }
}

export function volume(over: Partial<Volume> = {}): Volume {
  return {
    namespace: 'production', name: 'data-0', storageClass: 'standard-rwo', requested: 10 * 2 ** 30, capacity: 10 * 2 ** 30,
    accessModes: ['ReadWriteOnce'], phase: 'Bound', volumeName: 'pvc-1', pods: ['u1'],
    ...over,
  }
}

export function gateway(over: Partial<Gateway> = {}): Gateway {
  return {
    namespace: 'infra', name: 'public', class: 'eg', accepted: 'true', programmed: 'true', addresses: ['203.0.113.10'],
    listeners: [{ name: 'https', protocol: 'HTTPS', port: 443, hostname: '*.example.com', attachedRoutes: 1, ready: 'true' }],
    ...over,
  }
}

export function pv(over: Partial<PersistentVolume> = {}): PersistentVolume {
  return {
    name: 'pv-1', storageClass: 'standard-rwo', capacity: 20 * 2 ** 30, accessModes: ['ReadWriteOnce'],
    reclaimPolicy: 'Retain', phase: 'Released', claimRef: 'production/old-data',
    ...over,
  }
}
