import { useEffect, useState } from 'react'
import { clusterColors } from '../scene/colors'
import { postureFor } from '../scene/posture'
import { useCluster } from '../store/cluster'

const hms = (d: Date) => d.toTimeString().slice(0, 8)

export function TopBar() {
  const me = useCluster((s) => s.me)
  const connection = useCluster((s) => s.connection)
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(id)
  }, [])
  const live = connection === 'live'
  return (
    <header className="top">
      <div className="brand">
        <svg className="brand-mark" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 2 21 7v10l-9 5-9-5V7z" fill="none" stroke="currentColor" strokeWidth="1.8" />
          <path d="M12 12 21 7M12 12v10M12 12 3 7" stroke="currentColor" strokeWidth="1.8" fill="none" opacity=".5" />
        </svg>
        <b>Cluster Atlas</b>
        {me && <span className="ctx">{me.cluster}</span>}
      </div>
      <div className={`live ${live ? '' : 'reconnecting'}`} role="status">
        <span className="dot" />
        <span className="lbl">{live ? 'Live' : connection === 'connecting' ? 'Connexion…' : 'Reconnexion…'}</span>{' '}
        <span>{hms(now)}</span>
        {me?.demo && <span className="sim">Données simulées</span>}
      </div>
    </header>
  )
}

const PENDING = new Set(['Pending', 'ContainerCreating', 'PodInitializing'])

export function Stats() {
  useCluster((s) => s.version)
  const nsFilter = useCluster((s) => s.nsFilter)
  const { nodes, pods } = useCluster.getState()
  const all = [...pods.values()]
  const nodeList = [...nodes.values()]
  const ready = nodeList.filter((n) => !n.unschedulable && n.conditions.some((c) => c.type === 'Ready' && c.status === 'True')).length
  const running = all.filter((p) => p.displayStatus === 'Running').length
  const pending = all.filter((p) => PENDING.has(p.displayStatus)).length
  const errors = all.filter((p) => postureFor(p).antenna === 'err').length
  const filtered = nsFilter ? all.filter((p) => p.namespace === nsFilter) : []
  const filteredNodes = new Set(filtered.filter((p) => p.nodeName).map((p) => p.nodeName)).size

  return (
    <div className="stats" aria-label="Résumé du cluster">
      <div className="stat"><span>Nodes prêts</span><strong>{ready}/{nodeList.length}</strong></div>
      <div className="stat"><span>Pods Running</span><strong data-testid="pods-running">{running}/{all.length}</strong></div>
      <div className={`stat ${pending ? 'warn' : ''}`}><span>En attente</span><strong>{pending}</strong></div>
      <div className={`stat ${errors ? 'err' : ''}`}><span>En erreur</span><strong>{errors}</strong></div>
      {nsFilter && (
        <div className="stat" data-testid="ns-stat">
          <span>{nsFilter}</span>
          <strong>{filtered.length} pods sur {filteredNodes} nodes</strong>
        </div>
      )}
    </div>
  )
}

export function Legend() {
  useCluster((s) => s.version)
  const nsFilter = useCluster((s) => s.nsFilter)
  const toggle = useCluster((s) => s.toggleNsFilter)
  const colors = clusterColors(useCluster.getState())
  const list = [...colors.keys()]
  return (
    <div className={`legend ${nsFilter ? 'filtering' : ''}`} role="group" aria-label="Filtrer par namespace">
      {list.map((ns) => (
        <button key={ns} className="ns" aria-pressed={nsFilter === ns} onClick={() => toggle(ns)}>
          <i style={{ background: colors.get(ns) }} />
          {ns}
        </button>
      ))}
    </div>
  )
}

export function Feed() {
  const feed = useCluster((s) => s.feed)
  return (
    <div className="feed" aria-live="polite">
      {feed.map((f) => (
        <div key={f.id} className={f.level}>
          <time>{hms(new Date(f.at))}</time>
          {f.text}
        </div>
      ))}
    </div>
  )
}

export function Hint() {
  const selected = useCluster((s) => s.selection !== null)
  const [expired, setExpired] = useState(false)
  useEffect(() => {
    const id = setTimeout(() => setExpired(true), 7000)
    return () => clearTimeout(id)
  }, [])
  const [seen, setSeen] = useState(false)
  useEffect(() => { if (selected) setSeen(true) }, [selected])
  return <div className={`hint ${expired || seen ? 'hide' : ''}`}>Touchez un pod ou un node pour l'inspecter</div>
}
