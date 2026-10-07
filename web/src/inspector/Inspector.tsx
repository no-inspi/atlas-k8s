import { useEffect, type ReactNode } from 'react'
import { eventsResource, routeRef, serviceRef, volumeRef, type Ref } from '../api/inspect'
import type { Pod } from '../api/types'
import { clusterColors } from '../scene/colors'
import { HEALTH_LABEL, healthSignal, volumeSignal } from '../scene/health'
import { postureFor } from '../scene/posture'
import { useCluster, type InspectorTab, type Selection } from '../store/cluster'
import { gatesOf, readyCount, routeBroken } from '../store/net'
import { BADGE } from './common'
import { EventsTab } from './EventsTab'
import { LogsTab } from './LogsTab'
import { GateOverview, RouteOverview, ServiceOverview, VolumeOverview } from './NetOverview'
import { GhostNode, NodeOverview } from './NodeOverview'
import { PodOverview } from './PodOverview'
import { RefYamlTab } from './RefYamlTab'
import { TerminalTab } from './TerminalTab'
import { YamlTab } from './YamlTab'

// Inspecteur : panneau latéral sur desktop, feuille en bas sur mobile (CSS).
// Il garde son onglet actif quand on passe d'un pod à l'autre.

const POD_TABS: [InspectorTab, string][] = [['overview', 'Aperçu'], ['logs', 'Logs'], ['terminal', 'Terminal'], ['yaml', 'YAML'], ['events', 'Événements']]

function Head({ kind, name, badge, badgeClass, color, tabs, onClose }: {
  kind: string; name: string; badge: string; badgeClass: string; color?: string; tabs: [InspectorTab, string][]; onClose: () => void
}) {
  const active = useCluster((s) => s.inspectorTab)
  const setTab = useCluster((s) => s.setInspectorTab)
  const current = tabs.some(([k]) => k === active) ? active : 'overview'
  return (
    <>
      <div className="p-head">
        <div className="p-title">
          <div className="p-kind">{color && <i style={{ background: color }} />}{kind}</div>
          <div className="p-name">{name}</div>
          <div style={{ marginTop: 6 }}><span className={`badge ${badgeClass}`}>{badge}</span></div>
        </div>
        <button className="close" onClick={onClose} aria-label="Fermer">✕</button>
      </div>
      <div className="tabs" role="tablist">
        {tabs.map(([k, label]) => (
          <button key={k} className="tab" role="tab" aria-selected={current === k} onClick={() => setTab(k)}>{label}</button>
        ))}
      </div>
    </>
  )
}

function PodBody({ p }: { p: Pod }) {
  const tab = useCluster((s) => s.inspectorTab)
  // key : un autre pod repart d'un état neuf (container, filtre), sans ouvrir de
  // session avec le container du pod précédent.
  switch (tab) {
    case 'logs':
      return <div className="p-body flush"><LogsTab key={p.uid} p={p} /></div>
    case 'terminal':
      return <div className="p-body flush"><TerminalTab key={p.uid} p={p} /></div>
    case 'yaml':
      return <div className="p-body flush"><YamlTab p={p} /></div>
    case 'events':
      return <EventsTab ns={p.namespace} name={p.name} />
    default:
      return <PodOverview p={p} />
  }
}

const NET_TABS: [InspectorTab, string][] = [['overview', 'Aperçu'], ['yaml', 'YAML'], ['events', 'Événements']]

function NetBody({ target, children }: { target: Ref; children: ReactNode }) {
  const tab = useCluster((s) => s.inspectorTab)
  if (tab === 'yaml') return <div className="p-body flush"><RefYamlTab target={target} /></div>
  if (tab === 'events') {
    return <EventsTab ns={target.namespace} name={target.name} resource={eventsResource(target.kind)} empty="Aucun événement récent pour cet objet." />
  }
  return <>{children}</>
}

/** Panneau d'un Service, d'une route, d'une porte ou d'un PVC. */
function NetPanel({ selection, onClose }: { selection: NonNullable<Selection>; onClose: () => void }) {
  const st = useCluster.getState()
  const colors = clusterColors(st)
  const gone = (kind: string, text: string) => (
    <>
      <Head kind={kind} name={selection.name} badge="Supprimé" badgeClass="s-mute" tabs={[['overview', 'Aperçu']]} onClose={onClose} />
      <div className="gone">{text}</div>
    </>
  )
  switch (selection.type) {
    case 'service': {
      const s = st.services.get(selection.key)
      if (!s) return gone('Service', 'Ce Service a été supprimé.')
      const badge = s.health === 'external' || s.health === 'down' ? HEALTH_LABEL[s.health] : `${readyCount(s)}/${s.endpoints.length} ready`
      return (
        <>
          <Head kind={`Service · ${s.namespace}`} name={s.name} badge={badge} badgeClass={BADGE[healthSignal(s.health)]}
            color={colors.get(s.namespace)} tabs={NET_TABS} onClose={onClose} />
          <NetBody target={serviceRef(s)}><ServiceOverview s={s} /></NetBody>
        </>
      )
    }
    case 'route': {
      const r = st.routes.get(selection.key)
      if (!r) return gone('Route', 'Cette route a été supprimée.')
      const broken = routeBroken(r)
      return (
        <>
          <Head kind={`${r.source} · ${r.namespace}`} name={r.name} badge={broken ? 'Service introuvable' : `Porte ${r.gate}`}
            badgeClass={broken ? 's-err' : 's-ok'} color={colors.get(r.namespace)} tabs={NET_TABS} onClose={onClose} />
          <NetBody target={routeRef(r)}><RouteOverview r={r} /></NetBody>
        </>
      )
    }
    case 'volume': {
      const v = st.volumes.get(selection.key)
      if (!v) return gone('PVC', 'Ce PVC a été supprimé.')
      return (
        <>
          <Head kind={`PVC · ${v.namespace}`} name={v.name} badge={v.phase} badgeClass={BADGE[volumeSignal(v)]}
            color={colors.get(v.namespace)} tabs={NET_TABS} onClose={onClose} />
          <NetBody target={volumeRef(v)}><VolumeOverview v={v} /></NetBody>
        </>
      )
    }
    case 'gate': {
      const g = gatesOf(st.routes.values()).find((x) => x.name === selection.key)
      if (!g) return gone("Porte d'entrée", 'Plus aucune route ne passe par cette porte.')
      return (
        <>
          <Head kind="Porte d'entrée" name={g.name} badge={g.broken ? `${g.broken} route(s) cassée(s)` : `${g.routes.length} route(s)`}
            badgeClass={g.broken ? 's-warn' : 's-ok'} tabs={[['overview', 'Aperçu']]} onClose={onClose} />
          <GateOverview g={g} />
        </>
      )
    }
  }
  return null
}

export function Inspector() {
  useCluster((s) => s.version)
  useCluster((s) => s.metrics)
  const selection = useCluster((s) => s.selection)
  const { pods, nodes, select } = useCluster.getState()
  const close = () => select(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // Échap ferme l'inspecteur, sauf depuis un champ (filtre des logs, éditeur).
      const t = e.target as HTMLElement
      if (e.key === 'Escape' && !t.closest('input, select, textarea, .monaco-host, .term-host, .dialog')) useCluster.getState().select(null)
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  let content = null
  if (selection?.type === 'pod') {
    const p = pods.get(selection.key)
    const color = p ? clusterColors(useCluster.getState()).get(p.namespace) : undefined
    content = p ? (
      <>
        <Head kind={`Pod · ${p.namespace}`} name={p.name} badge={p.displayStatus + (p.displayStatus === 'Running' && !p.ready ? ' (non ready)' : '')}
          badgeClass={BADGE[postureFor(p).antenna]} color={color} tabs={POD_TABS} onClose={close} />
        <PodBody p={p} />
      </>
    ) : (
      <>
        <Head kind="Pod" name={selection.name} badge="Supprimé" badgeClass="s-mute" tabs={[['overview', 'Aperçu']]} onClose={close} />
        <div className="gone">Ce pod a été supprimé.</div>
      </>
    )
  } else if (selection?.type === 'node') {
    const n = nodes.get(selection.key)
    const ready = n?.conditions.some((c) => c.type === 'Ready' && c.status === 'True')
    const ghost = !n && [...pods.values()].some((p) => p.nodeName === selection.key)
    const tabs: [InspectorTab, string][] = [['overview', 'Aperçu']]
    content = n ? (
      <>
        <Head kind={`Node · ${n.pool}`} name={n.name}
          badge={`${ready ? 'Ready' : 'NotReady'}${n.unschedulable ? ', SchedulingDisabled' : ''}`}
          badgeClass={!ready ? 's-err' : n.unschedulable ? 's-warn' : 's-ok'} tabs={tabs} onClose={close} />
        <NodeOverview n={n} />
      </>
    ) : ghost ? (
      <>
        <Head kind="Node" name={selection.key} badge="Non visible" badgeClass="s-mute" tabs={tabs} onClose={close} />
        <GhostNode name={selection.key} />
      </>
    ) : (
      <>
        <Head kind="Node" name={selection.name} badge="Supprimé" badgeClass="s-mute" tabs={tabs} onClose={close} />
        <div className="gone">Ce node n'existe plus.</div>
      </>
    )
  } else if (selection) {
    content = <NetPanel selection={selection} onClose={close} />
  }

  return <aside className={`panel ${selection ? 'open' : ''}`} aria-label="Inspecteur" aria-hidden={!selection}>{content}</aside>
}
