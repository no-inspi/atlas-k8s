import { describe, expect, it, vi } from 'vitest'
import { logsURL, openLogs, type LogMessage } from './inspect'

class FakeWS {
  static last: FakeWS
  onmessage: ((e: { data: string }) => void) | null = null
  onclose: ((e: { code: number }) => void) | null = null
  constructor(public url: string) { FakeWS.last = this }
  close() { this.onclose?.({ code: 1000 }) }
}

describe('logsURL', () => {
  it('encode le pod et les options', () => {
    expect(logsURL({ protocol: 'https:', host: 'a.b' }, 'prod', 'api-1', { container: 'api', previous: true }))
      .toBe('wss://a.b/api/namespaces/prod/pods/api-1/logs?container=api&previous=true&tailLines=500')
  })
})

describe('openLogs', () => {
  it('relaie les messages et signale une coupure inattendue', () => {
    const got: LogMessage[] = []
    openLogs('ws://x', (m) => got.push(m), { WebSocket: FakeWS as unknown as typeof WebSocket })
    FakeWS.last.onmessage?.({ data: JSON.stringify({ type: 'lines', lines: [{ text: 'a' }] }) })
    FakeWS.last.onclose?.({ code: 1006 })
    expect(got.map((m) => m.type)).toEqual(['lines', 'error'])
  })

  it('reste silencieux après « end » ou une fermeture volontaire', () => {
    const got: LogMessage[] = []
    const close = openLogs('ws://x', (m) => got.push(m), { WebSocket: FakeWS as unknown as typeof WebSocket })
    FakeWS.last.onmessage?.({ data: JSON.stringify({ type: 'end' }) })
    FakeWS.last.onclose?.({ code: 1000 })
    close()
    expect(got.map((m) => m.type)).toEqual(['end'])
  })

  it('renvoie vers la connexion si la session expire', () => {
    const login = vi.fn()
    openLogs('ws://x', () => {}, { WebSocket: FakeWS as unknown as typeof WebSocket, login })
    FakeWS.last.onclose?.({ code: 4401 })
    expect(login).toHaveBeenCalled()
  })
})
