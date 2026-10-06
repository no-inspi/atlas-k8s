// Terminal xterm.js relié à /api/namespaces/{ns}/pods/{pod}/exec. Chargé à la
// demande, comme Monaco. stdin et stdout passent en messages binaires, la
// taille du terminal en JSON.
import { FitAddon } from '@xterm/addon-fit'
import { Terminal as XTerm } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { useEffect, useRef } from 'react'
import { loginURL } from '../api/http'
import { streamURL } from '../api/stream'

export type TermStatus = 'connecting' | 'open' | 'closed'

export default function Terminal({ ns, pod, container, session, onStatus }: {
  ns: string
  pod: string
  container: string
  /** Changer cette valeur rouvre une session (bouton Reconnecter). */
  session: number
  onStatus: (s: TermStatus) => void
}) {
  const host = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const css = getComputedStyle(document.documentElement)
    const term = new XTerm({
      cursorBlink: true, fontSize: 12.5, scrollback: 5000,
      fontFamily: css.getPropertyValue('--mono').trim() || 'monospace',
      theme: { background: css.getPropertyValue('--code-bg').trim(), foreground: css.getPropertyValue('--code-ink').trim() },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host.current!)
    fit.fit()
    term.focus()

    const e = encodeURIComponent
    const url = streamURL(window.location, 0).replace('/api/stream', `/api/namespaces/${e(ns)}/pods/${e(pod)}/exec`) + `?container=${e(container)}`
    const ws = new WebSocket(url)
    ws.binaryType = 'arraybuffer'
    const enc = new TextEncoder()
    let ended = false
    onStatus('connecting')
    term.writeln(`\x1b[2m$ kubectl exec -it -n ${ns} ${pod} -c ${container} -- sh\x1b[0m`)

    const sendSize = () => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
    }
    // Les frappes tapées avant l'ouverture sont gardées, pas perdues.
    let queued: Uint8Array[] = []
    ws.onopen = () => {
      onStatus('open')
      sendSize()
      queued.forEach((b) => ws.send(b))
      queued = []
    }
    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) return term.write(new Uint8Array(ev.data))
      const m = JSON.parse(ev.data as string) as { type: string; message?: string; code?: number }
      if (m.type === 'error') { ended = true; term.writeln(`\r\n\x1b[31m${m.message}\x1b[0m`) }
      if (m.type === 'exit') { ended = true; term.writeln(`\r\n\x1b[2mSession terminée${m.code ? ` (code ${m.code})` : ''}.\x1b[0m`) }
    }
    ws.onclose = (ev) => {
      if (ev.code === 4401) return window.location.assign(loginURL())
      if (!ended) term.writeln(`\r\n\x1b[2mConnexion fermée (code ${ev.code}).\x1b[0m`)
      onStatus('closed')
    }
    const data = term.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d))
      else if (ws.readyState === WebSocket.CONNECTING) queued.push(enc.encode(d))
    })
    const resized = term.onResize(sendSize)
    const ro = new ResizeObserver(() => fit.fit())
    ro.observe(host.current!)

    return () => {
      ro.disconnect()
      data.dispose()
      resized.dispose()
      ws.close()
      term.dispose()
    }
  }, [ns, pod, container, session, onStatus])

  return <div className="term-host" ref={host} data-testid="terminal" />
}
