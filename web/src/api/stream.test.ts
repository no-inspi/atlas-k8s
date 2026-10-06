import { beforeEach, describe, expect, it, vi } from 'vitest'
import { connectStream, streamURL } from './stream'
import { useCluster } from '../store/cluster'

class FakeWS {
  static all: FakeWS[] = []
  onopen: (() => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onclose: ((e: { code: number }) => void) | null = null
  closed = false
  constructor(public url: string) { FakeWS.all.push(this) }
  close() { this.closed = true; this.onclose?.({ code: 1000 }) }
  emit(m: unknown) { this.onmessage?.({ data: JSON.stringify(m) }) }
}

beforeEach(() => {
  FakeWS.all = []
  useCluster.getState().reset()
  vi.useFakeTimers()
})

const opts = (extra = {}) => ({
  WebSocket: FakeWS as unknown as typeof WebSocket,
  schedule: (f: () => void) => setTimeout(f, 0),
  location: { protocol: 'https:', host: 'atlas.example.com' } as Location,
  checkSession: () => Promise.resolve(),
  ...extra,
})

describe('streamURL', () => {
  it('passe en wss et ajoute le rev', () => {
    expect(streamURL({ protocol: 'https:', host: 'a.b' } as Location, 0)).toBe('wss://a.b/api/stream')
    expect(streamURL({ protocol: 'http:', host: 'localhost:5173' } as Location, 42)).toBe('ws://localhost:5173/api/stream?rev=42')
  })
})

describe('connectStream', () => {
  it('regroupe les messages reçus en un seul lot appliqué au store', () => {
    const spy = vi.spyOn(useCluster.getState(), 'applyMessages')
    connectStream(opts())
    const ws = FakeWS.all[0]
    ws.onopen?.()
    ws.emit({ type: 'snapshot', rev: 3 })
    ws.emit({ type: 'metrics', metrics: { pods: {}, nodes: {} } })
    expect(spy).not.toHaveBeenCalled()
    vi.runOnlyPendingTimers()
    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy.mock.calls[0][0]).toHaveLength(2)
    expect(useCluster.getState().rev).toBe(3)
  })

  it('se reconnecte avec son dernier rev et un délai croissant', () => {
    connectStream(opts())
    FakeWS.all[0].onopen?.()
    FakeWS.all[0].emit({ type: 'snapshot', rev: 7 })
    vi.runOnlyPendingTimers()

    FakeWS.all[0].onclose?.({ code: 1006 })
    expect(useCluster.getState().connection).toBe('reconnecting')
    vi.advanceTimersByTime(499)
    expect(FakeWS.all).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(FakeWS.all).toHaveLength(2)
    expect(FakeWS.all[1].url).toBe('wss://atlas.example.com/api/stream?rev=7')

    FakeWS.all[1].onclose?.({ code: 1006 })
    vi.advanceTimersByTime(999)
    expect(FakeWS.all).toHaveLength(2)
    vi.advanceTimersByTime(1)
    expect(FakeWS.all).toHaveLength(3)
  })

  it('repart d’un snapshot complet quand les droits changent (4000)', () => {
    connectStream(opts())
    FakeWS.all[0].emit({ type: 'snapshot', rev: 7 })
    vi.runOnlyPendingTimers()
    FakeWS.all[0].onclose?.({ code: 4000 })
    vi.advanceTimersByTime(0)
    expect(FakeWS.all[1].url).toBe('wss://atlas.example.com/api/stream')
  })

  it('renvoie vers la connexion quand la session expire (4401)', () => {
    const login = vi.fn()
    connectStream(opts({ login }))
    FakeWS.all[0].onclose?.({ code: 4401 })
    expect(login).toHaveBeenCalled()
    vi.advanceTimersByTime(10_000)
    expect(FakeWS.all).toHaveLength(1)
  })

  it('s’arrête proprement', () => {
    const stop = connectStream(opts())
    stop()
    expect(FakeWS.all[0].closed).toBe(true)
    vi.advanceTimersByTime(10_000)
    expect(FakeWS.all).toHaveLength(1)
  })
})
