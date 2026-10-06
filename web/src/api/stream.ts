import type { Message } from './types'
import { useCluster } from '../store/cluster'

export function streamURL(loc: Pick<Location, 'protocol' | 'host'>, rev: number): string {
  const proto = loc.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${loc.host}/api/stream${rev ? `?rev=${rev}` : ''}`
}

interface StreamOptions {
  WebSocket?: typeof WebSocket
  /** Planifie l'application d'un lot ; une frame d'animation par défaut. */
  schedule?: (f: () => void) => void
  location?: Pick<Location, 'protocol' | 'host'>
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
    ws.onclose = () => {
      ws = null
      if (stopped) return
      flush()
      store.getState().setConnection('reconnecting')
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
