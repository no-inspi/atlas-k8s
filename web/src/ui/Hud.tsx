import { useEffect, useState } from 'react'
import { clusterColors } from '../scene/colors'
import { legendItems } from './legend'
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
      {me?.authenticated && (
        <div className="user">
          <span className="user-name" title={me.groups.join(', ')}>{me.user}</span>
          <a className="logout" href="/auth/logout">Déconnexion</a>
        </div>
      )}
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
  const hasPods = all.some((p) => p.nodeName)
  const ready = nodeList.filter((n) => !n.unschedulable && n.conditions.some((c) => c.type === 'Ready' && c.status === 'True')).length
  const running = all.filter((p) => p.displayStatus === 'Running').length
  const pending = all.filter((p) => PENDING.has(p.displayStatus)).length
  const errors = all.filter((p) => postureFor(p).antenna === 'err').length
  const filtered = nsFilter ? all.filter((p) => p.namespace === nsFilter) : []
  const filteredNodes = new Set(filtered.filter((p) => p.nodeName).map((p) => p.nodeName)).size

  return (
    <div className="stats" aria-label="Résumé du cluster">
      <div className="stat" title={nodeList.length || !hasPods ? undefined : "Vous n'avez pas le droit de lister les nodes"}>
        <span>Nodes prêts</span><strong>{nodeList.length || !hasPods ? `${ready}/${nodeList.length}` : '—'}</strong>
      </div>
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
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const st = useCluster.getState()
  const colors = clusterColors(st)
  const counts = new Map<string, number>()
  for (const p of st.pods.values()) counts.set(p.namespace, (counts.get(p.namespace) ?? 0) + 1)
  const { visible, hidden } = legendItems(counts, nsFilter)
  const chip = (ns: string) => (
    <button key={ns} className="ns" aria-pressed={nsFilter === ns} onClick={() => toggle(ns)}>
      <i style={{ background: colors.get(ns) }} />
      {ns}
    </button>
  )
  const matches = hidden.filter((ns) => ns.includes(query.trim()))
  return (
    <div className={`legend ${nsFilter ? 'filtering' : ''}`} role="group" aria-label="Filtrer par namespace">
      {visible.map(chip)}
      {hidden.length > 0 && (
        <button className="ns more" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          +{hidden.length} namespace{hidden.length > 1 ? 's' : ''}
        </button>
      )}
      {open && hidden.length > 0 && (
        <div className="legend-more" role="dialog" aria-label="Autres namespaces">
          <input type="search" placeholder="Filtrer les namespaces" aria-label="Filtrer les namespaces" value={query}
            onChange={(e) => setQuery(e.target.value)} autoFocus />
          <div className="legend-more-list">
            {matches.map((ns) => (
              <span key={ns} onClick={() => setOpen(false)} style={{ display: 'contents' }}>{chip(ns)}</span>
            ))}
            {!matches.length && <span className="note">Aucun namespace ne correspond.</span>}
          </div>
        </div>
      )}
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
