import type { Message } from './types'
import { useCluster } from '../store/cluster'
import { apiFetch, loginURL } from './http'

// Codes de fermeture envoyés par le backend (internal/stream/ws.go).
const RESYNC = 4000
const SESSION_EXPIRED = 4401

export function streamURL(loc: Pick<Location, 'protocol' | 'host'>, rev: number): string {
  const proto = loc.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${loc.host}/api/stream${rev ? `?rev=${rev}` : ''}`
}

interface StreamOptions {
  WebSocket?: typeof WebSocket
  /** Planifie l'application d'un lot ; une frame d'animation par défaut. */
  schedule?: (f: () => void) => void
  location?: Pick<Location, 'protocol' | 'host'>
  /** Renvoie vers la page de connexion (session expirée). */
  login?: () => void
  /** Vérifie la session avant une reconnexion (redirige si elle a expiré). */
  checkSession?: () => Promise<unknown>
}

const MIN_DELAY = 500
const MAX_DELAY = 8000

/**
 * Ouvre /api/stream, applique les messages au store par lots (un par frame)
 * et se reconnecte en reprenant au dernier rev. Renvoie une fonction d'arrêt.
 */
export function connectStream(opts: StreamOptions = {}): () => void {
  const WS = opts.WebSocket ?? WebSocket
  const schedule = opts.schedule ?? ((f) => requestAnimationFrame(() => f()))
  const loc = opts.location ?? window.location
  const login = opts.login ?? (() => window.location.assign(loginURL()))
  const checkSession = opts.checkSession ?? (() => apiFetch('/api/me'))
  const store = useCluster

  let ws: WebSocket | null = null
  let queue: Message[] = []
  let scheduled = false
  let delay = MIN_DELAY
  let stopped = false
  let timer: ReturnType<typeof setTimeout> | undefined

  const flush = () => {
    scheduled = false
    const batch = queue
    queue = []
    if (batch.length) store.getState().applyMessages(batch)
  }

  const open = () => {
    ws = new WS(streamURL(loc, store.getState().rev))
    ws.onopen = () => { delay = MIN_DELAY }
    ws.onmessage = (e: MessageEvent) => {
      queue.push(JSON.parse(e.data as string) as Message)
      if (!scheduled) {
        scheduled = true
        schedule(flush)
      }
    }
    ws.onclose = (e: CloseEvent) => {
      ws = null
      if (stopped) return
      flush()
      if (e.code === SESSION_EXPIRED) {
        stopped = true
        login()
        return
      }
      if (e.code === RESYNC) {
        // Droits modifiés : on repart d'un snapshot complet, refiltré.
        store.getState().resetRev()
        timer = setTimeout(open, 0)
        return
      }
      store.getState().setConnection('reconnecting')
      checkSession().catch(() => {})
      timer = setTimeout(open, delay)
      delay = Math.min(delay * 2, MAX_DELAY)
    }
  }

  open()
  return () => {
    stopped = true
    clearTimeout(timer)
    ws?.close()
  }
}
