import { beforeEach, describe, expect, it } from 'vitest'
import { useCluster } from '../store/cluster'
import { node, pod } from '../store/fixtures'
import { parseRoute, pathFor, syncRoute } from './route'

describe('parseRoute / pathFor', () => {
  it.each([
    ['/pods/production/api-7f9-x', { type: 'pod', namespace: 'production', name: 'api-7f9-x' }],
    ['/nodes/gke-n1', { type: 'node', name: 'gke-n1' }],
    ['/', null],
    ['/pods/onlyns', null],
  ])('%s', (path, route) => {
    expect(parseRoute(path)).toEqual(route)
    if (route) expect(pathFor(route as never)).toBe(path)
  })
})

describe('syncRoute', () => {
  beforeEach(() => useCluster.getState().reset())

  const fakeWindow = (path: string) => {
    const w = { location: { pathname: path }, history: { replaceState: (_: unknown, __: string, p: string) => { w.location.pathname = p } } }
    return w as unknown as Window & { location: { pathname: string } }
  }

  it('rétablit un pod partagé par lien dès qu’il arrive dans le flux', () => {
    const w = fakeWindow('/pods/production/api-abc-x1')
    const stop = syncRoute(w)
    expect(useCluster.getState().selection).toBeNull()
    useCluster.getState().applyMessages([{ type: 'snapshot', rev: 1, nodes: [node()], pods: [pod()] }])
    expect(useCluster.getState().selection).toEqual({ type: 'pod', key: 'u1', name: 'api-abc-x1' })
    stop()
  })

  it('fait suivre l’URL à la sélection', () => {
    const w = fakeWindow('/')
    const stop = syncRoute(w)
    useCluster.getState().applyMessages([{ type: 'snapshot', rev: 1, nodes: [node()], pods: [pod()] }])
    useCluster.getState().select({ type: 'node', key: 'gke-prod-default-pool-aaaa-n1' })
    expect(w.location.pathname).toBe('/nodes/gke-prod-default-pool-aaaa-n1')
    useCluster.getState().select(null)
    expect(w.location.pathname).toBe('/')
    stop()
  })
})
