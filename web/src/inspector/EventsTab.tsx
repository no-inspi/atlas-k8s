import { useEffect, useState } from 'react'
import { getEvents, type KubeEvent } from '../api/inspect'
import type { Pod } from '../api/types'
import { age } from '../ui/format'

const REFRESH_MS = 3000

/** Événements du pod, du plus récent au plus ancien, rafraîchis tant que l'onglet est ouvert. */
export function EventsTab({ p }: { p: Pod }) {
  const [events, setEvents] = useState<KubeEvent[] | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    setEvents(null)
    setError('')
    const load = () =>
      getEvents(p.namespace, p.name)
        .then((e) => { if (live) { setEvents(e); setError('') } })
        .catch((e: Error) => live && setError(e.message))
    load()
    const id = setInterval(load, REFRESH_MS)
    return () => { live = false; clearInterval(id) }
  }, [p.namespace, p.name])

  if (error) return <div className="p-body"><p className="note s-err">{error}</p></div>
  if (!events) return <div className="p-body"><p className="note">Chargement…</p></div>
  if (!events.length) return <div className="p-body"><p className="note">Aucun événement récent pour ce pod.</p></div>
  return (
    <div className="p-body">
      <table className="evt" data-testid="events">
        <tbody>
          {events.map((e, i) => (
            <tr key={i} className={e.type === 'Warning' ? 'warning' : ''}>
              <td>{age(e.lastSeen)}</td>
              <td>
                <span className={`r ${e.type === 'Warning' ? 's-err' : ''}`}>{e.reason}</span>
                {e.count > 1 && <span className="later"> ×{e.count}</span>}
                {e.source && <span className="later"> · {e.source}</span>}
                <br />
                {e.message}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
