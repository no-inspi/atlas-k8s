import { useEffect } from 'react'
import type { Pod } from '../api/types'
import { clusterColors } from '../scene/colors'
import { postureFor } from '../scene/posture'
import { useCluster, type InspectorTab } from '../store/cluster'
import { BADGE } from './common'
import { EventsTab } from './EventsTab'
import { LogsTab } from './LogsTab'
import { GhostNode, NodeOverview } from './NodeOverview'
import { PodOverview } from './PodOverview'
import { YamlTab } from './YamlTab'

// Inspecteur : panneau latéral sur desktop, feuille en bas sur mobile (CSS).
// Il garde son onglet actif quand on passe d'un pod à l'autre.

const POD_TABS: [InspectorTab, string][] = [['overview', 'Aperçu'], ['logs', 'Logs'], ['yaml', 'YAML'], ['events', 'Événements']]

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
  switch (tab) {
    case 'logs':
      return <div className="p-body flush"><LogsTab p={p} /></div>
    case 'yaml':
      return <div className="p-body flush"><YamlTab p={p} /></div>
    case 'events':
      return <EventsTab p={p} />
    default:
      return <PodOverview p={p} />
  }
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
      if (e.key === 'Escape' && !t.closest('input, select, textarea, .monaco-host')) useCluster.getState().select(null)
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
  }

  return <aside className={`panel ${selection ? 'open' : ''}`} aria-label="Inspecteur" aria-hidden={!selection}>{content}</aside>
}
