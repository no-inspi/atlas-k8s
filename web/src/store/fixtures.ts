import type { Node, Pod, Workload } from '../api/types'

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
