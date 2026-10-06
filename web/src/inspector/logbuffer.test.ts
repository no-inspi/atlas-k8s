import { describe, expect, it } from 'vitest'
import { LogBuffer, levelOf, toText } from './logbuffer'

describe('levelOf', () => {
  it.each([
    ['ERROR dial tcp: connection refused', 'error'],
    ['FATAL panic: cannot start', 'error'],
    ['level=error msg="boom"', 'error'],
    ['{"level":"warn","msg":"slow"}', 'warn'],
    ['WARN  GET /x 404', 'warn'],
    ['INFO  GET /healthz 200', 'info'],
    ['DEBUG cache hit', 'debug'],
    ['10.0.0.1 - "GET / HTTP/1.1" 200', ''],
    ['errors_total 0', ''],
  ])('%s → %s', (text, level) => {
    expect(levelOf(text)).toBe(level)
  })
})

describe('LogBuffer', () => {
  const line = (i: number) => ({ ts: `2026-10-06T09:00:0${i % 10}Z`, text: `line ${i}` })

  it('garde au plus N lignes, les plus récentes', () => {
    const b = new LogBuffer(3)
    b.push([line(1), line(2)])
    b.push([line(3), line(4)])
    expect(b.lines.map((l) => l.text)).toEqual(['line 2', 'line 3', 'line 4'])
    expect(b.total).toBe(4)
  })

  it('filtre sans tenir compte de la casse', () => {
    const b = new LogBuffer(10)
    b.push([{ text: 'GET /orders 200' }, { text: 'POST /Orders 503' }, { text: 'GET /healthz 200' }])
    expect(b.filter('orders').map((l) => l.text)).toEqual(['GET /orders 200', 'POST /Orders 503'])
    expect(b.filter('')).toHaveLength(3)
  })

  it('se vide', () => {
    const b = new LogBuffer(10)
    b.push([line(1)])
    b.clear()
    expect(b.lines).toHaveLength(0)
  })
})

describe('toText', () => {
  it('produit le fichier téléchargé', () => {
    expect(toText([{ ts: '2026-10-06T09:00:00Z', text: 'a' }, { text: 'b' }])).toBe('2026-10-06T09:00:00Z a\nb\n')
  })
})
