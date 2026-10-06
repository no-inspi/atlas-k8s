import { apiFetch } from './http'
import { ApiError } from './inspect'

// Actions d'exploitation : le message d'erreur de l'API server est renvoyé tel quel.

const seg = encodeURIComponent

async function send<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await apiFetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const json = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, (json as { error?: string }).error ?? res.statusText)
  return json as T
}

/** Ressource au pluriel attendue par l'API pour un kind de workload. */
export const resourceOf = (kind: string) => `${kind.toLowerCase()}s`

export const deletePod = (ns: string, pod: string) => send('DELETE', `/api/namespaces/${seg(ns)}/pods/${seg(pod)}`)
export const scale = (ns: string, kind: string, name: string, replicas: number) =>
  send('PATCH', `/api/namespaces/${seg(ns)}/${resourceOf(kind)}/${seg(name)}/scale`, { replicas })
export const restart = (ns: string, kind: string, name: string) =>
  send('POST', `/api/namespaces/${seg(ns)}/${resourceOf(kind)}/${seg(name)}/restart`)
export const cordon = (node: string, on: boolean) => send('POST', `/api/nodes/${seg(node)}/${on ? 'cordon' : 'uncordon'}`)

export interface PodRef { namespace: string; name: string; owner?: string; emptyDir?: boolean }
export interface DrainPlan {
  node: string
  evict: PodRef[]
  ignored: (PodRef & { reason: string })[]
  blocking: { namespace: string; name: string; disruptionsAllowed: number; pods: string[] }[]
  pdbUnknown?: string
}
export interface DrainResult {
  node: string
  evictions: (PodRef & { result: 'evicted' | 'blocked' | 'error'; message?: string })[]
}

export const drainPlan = (node: string) => send<DrainPlan>('POST', `/api/nodes/${seg(node)}/drain?dryRun=true`)
export const drain = (node: string) => send<DrainResult>('POST', `/api/nodes/${seg(node)}/drain`)
