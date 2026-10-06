import { useVirtualizer } from '@tanstack/react-virtual'
import { useEffect, useMemo, useRef, useState } from 'react'
import { logsURL, openLogs } from '../api/inspect'
import type { Pod } from '../api/types'
import { LogBuffer, levelOf, toText, type Level } from './logbuffer'

const ROW = 19 // hauteur d'une ligne (12 px × 1,6)
const LEVEL_CLASS: Record<Level, string> = { error: 'lv-e', warn: 'lv-w', info: 'lv-i', debug: 'lv-d', '': '' }

type Status = 'connecting' | 'streaming' | 'ended' | 'error'

export function LogsTab({ p }: { p: Pod }) {
  const containers = p.containers
  const defaultContainer = containers.find((c) => !c.init)?.name ?? containers[0]?.name ?? ''
  const [container, setContainer] = useState(defaultContainer)
  const [previous, setPrevious] = useState(false)
  const [follow, setFollow] = useState(true)
  const [query, setQuery] = useState('')
  const [status, setStatus] = useState<Status>('connecting')
  const [error, setError] = useState('')
  const [dropped, setDropped] = useState(0)
  const [version, setVersion] = useState(0)
  const buffer = useRef(new LogBuffer(5000))
  const scroller = useRef<HTMLDivElement>(null)

  useEffect(() => {
    buffer.current.clear()
    setVersion((v) => v + 1)
    setStatus('connecting')
    setError('')
    setDropped(0)
    let pending = false
    const close = openLogs(logsURL(window.location, p.namespace, p.name, { container, previous }), (m) => {
      switch (m.type) {
        case 'lines':
          buffer.current.push(m.lines)
          setStatus('streaming')
          // Un rendu par frame au plus, même si les lignes arrivent en rafale.
          if (!pending) {
            pending = true
            requestAnimationFrame(() => { pending = false; setVersion((v) => v + 1) })
          }
          break
        case 'dropped':
          setDropped((d) => d + m.count)
          break
        case 'end':
          setStatus('ended')
          break
        case 'error':
          setStatus('error')
          setError(m.message)
      }
    })
    return close
  }, [p.namespace, p.name, container, previous])

  const lines = useMemo(() => buffer.current.filter(query), [query, version])
  const rows = useVirtualizer({ count: lines.length, getScrollElement: () => scroller.current, estimateSize: () => ROW, overscan: 30 })

  useEffect(() => {
    if (follow && lines.length) rows.scrollToIndex(lines.length - 1, { align: 'end' })
  }, [follow, lines.length, rows])

  // Remonter dans les logs suspend le suivi ; revenir en bas le reprend.
  const onScroll = () => {
    const el = scroller.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < ROW * 2
    if (atBottom !== follow) setFollow(atBottom)
  }

  const download = () => {
    const url = URL.createObjectURL(new Blob([toText(buffer.current.lines)], { type: 'text/plain' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `${p.name}${previous ? '-previous' : ''}.log`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <>
      <div className="toolbar">
        <label>
          Container
          <select aria-label="Container" value={container} onChange={(e) => setContainer(e.target.value)}>
            {containers.map((c) => <option key={c.name} value={c.name}>{c.name}{c.init ? ' (init)' : ''}</option>)}
          </select>
        </label>
        <label><input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> Suivre</label>
        {p.restarts > 0 && (
          <label><input type="checkbox" checked={previous} onChange={(e) => setPrevious(e.target.checked)} /> Instance précédente</label>
        )}
        <input className="filter" type="search" placeholder="Filtrer" aria-label="Filtrer les logs" value={query} onChange={(e) => setQuery(e.target.value)} />
        <span className="sp" />
        <button className="btn small" onClick={download} disabled={!buffer.current.lines.length}>Télécharger</button>
      </div>
      {(dropped > 0 || status === 'ended' || error) && (
        <div className="log-banner" role="status">
          {error && <span className="s-err">{error}</span>}
          {!error && status === 'ended' && <span>{previous ? 'Fin des logs de l\'instance précédente.' : 'Flux terminé : le container s\'est arrêté.'}</span>}
          {dropped > 0 && <span className="s-warn"> {dropped} ligne{dropped > 1 ? 's' : ''} ignorée{dropped > 1 ? 's' : ''} (le navigateur ne suivait pas).</span>}
        </div>
      )}
      <div className="code logbox" ref={scroller} onScroll={onScroll} data-testid="logs">
        {!lines.length && (
          <span className="ts">{status === 'connecting' ? 'Connexion…' : query ? 'Aucune ligne ne correspond au filtre.' : 'Aucun log.'}</span>
        )}
        <div style={{ height: rows.getTotalSize(), position: 'relative' }}>
          {rows.getVirtualItems().map((v) => {
            const l = lines[v.index]
            return (
              <div key={v.key} className="log-line" style={{ transform: `translateY(${v.start}px)` }}>
                {l.ts && <span className="ts">{l.ts.slice(11, 23)} </span>}
                <span className={LEVEL_CLASS[levelOf(l.text)]}>{l.text}</span>
              </div>
            )
          })}
        </div>
      </div>
    </>
  )
}
