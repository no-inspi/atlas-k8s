import { useEffect } from 'react'
import { workloadKey, type Node, type Pod } from '../api/types'
import { clusterColors } from '../scene/colors'
import { postureFor, type Antenna } from '../scene/posture'
import { useCluster } from '../store/cluster'
import { shortNode } from '../store/feed'
import { age, fmtCpu, fmtMem, pct } from './format'

// Inspecteur : panneau latéral sur desktop, feuille en bas sur mobile (CSS).
// Jalon 1 : onglet Aperçu en lecture seule. Logs, terminal, YAML et
// événements arrivent au jalon 5, les actions au jalon 6.

const BADGE: Record<Antenna, string> = { ok: 's-ok', warn: 's-warn', err: 's-err', mute: 's-mute', done: 's-mute' }

function Meter({ label, value, reference, ratio }: { label: string; value: string; reference: string; ratio: number }) {
  return (
    <div className="meter">
      <div className="meter-row"><span>{label} <b>{value}</b></span><span>{reference}</span></div>
      <div className="bar"><i className={ratio > 0.9 ? 'hot' : ''} style={{ width: `${Math.max(2, Math.min(100, ratio * 100))}%` }} /></div>
    </div>
  )
}

function Head({ kind, name, badge, badgeClass, color, onClose }: {
  kind: string; name: string; badge: string; badgeClass: string; color?: string; onClose: () => void
}) {
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
        <button className="tab" role="tab" aria-selected="true">Aperçu</button>
      </div>
    </>
  )
}

const goNode = (name: string) => () => useCluster.getState().select({ type: 'node', key: name })
const goPod = (uid: string) => () => useCluster.getState().select({ type: 'pod', key: uid })

function ownerChain(p: Pod): string[] {
  if (!p.owner.kind) return []
  if (p.owner.kind === 'Deployment') {
    const rs = p.name.split('-').slice(0, -1).join('-')
    return [`Deployment ${p.owner.name}`, `ReplicaSet ${rs}`]
  }
  return [`${p.owner.kind} ${p.owner.name}`]
}

function PodOverview({ p }: { p: Pod }) {
  const { pods, workloads, metrics } = useCluster.getState()
  const siblings = [...pods.values()].filter((q) => q.namespace === p.namespace && q.owner.kind === p.owner.kind && q.owner.name === p.owner.name && q.displayStatus !== 'Terminating')
  const onNodes = [...new Set(siblings.filter((q) => q.nodeName).map((q) => q.nodeName))].sort()
  const waiting = siblings.filter((q) => !q.nodeName).length
  const wl = p.owner.kind ? workloads.get(workloadKey({ kind: p.owner.kind, namespace: p.namespace, name: p.owner.name })) : undefined
  const usage = metrics.pods[p.uid]

  return (
    <div className="p-body">
      <dl className="kv">
        <dt>Namespace</dt><dd>{p.namespace}</dd>
        <dt>Node</dt><dd>{p.nodeName ? <button className="link" onClick={goNode(p.nodeName)}>{p.nodeName}</button> : 'non assigné'}</dd>
        <dt>IP</dt><dd>{p.podIP || '—'}</dd>
        <dt>Âge</dt><dd>{age(p.createdAt)}</dd>
        <dt>QoS</dt><dd>{p.qosClass}</dd>
        <dt>Redémarrages</dt><dd>{p.restarts}</dd>
      </dl>
      {p.statusMessage && <p className="note">{p.statusMessage}</p>}

      <h3>Containers</h3>
      <ul className="containers">
        {p.containers.map((c) => (
          <li key={c.name}>
            <span><b>{c.name}</b>{c.init ? ' (init)' : ''} · {c.reason || c.state} · {c.ready ? 'ready' : 'non ready'}</span>
            <code>{c.image}</code>
          </li>
        ))}
      </ul>

      {siblings.length > 0 && p.owner.kind && (
        <>
          <h3>Répartition des replicas</h3>
          <p style={{ margin: 0, fontSize: 14 }}>
            {siblings.length} replica{siblings.length > 1 ? 's' : ''} sur {onNodes.length} node{onNodes.length > 1 ? 's' : ''}
            {waiting ? `, ${waiting} en attente` : ''}
            {onNodes.length > 0 && ' : '}
            {onNodes.map((n, i) => (
              <span key={n}>{i > 0 && ', '}<button className="link" onClick={goNode(n)}>{shortNode(n)}</button></span>
            ))}
          </p>
          <p className="note">Les arcs dans la vue relient ce pod à ses replicas.</p>
        </>
      )}

      <h3>Géré par</h3>
      <div className="chain">
        {ownerChain(p).map((o) => (
          <span key={o} style={{ display: 'contents' }}><span>{o}</span><span className="sep">›</span></span>
        ))}
        <span className="cur">ce pod</span>
      </div>
      {wl?.argocd && (
        <p className="argo-line">
          <span className="argo">{wl.argocd.syncStatus ? `ArgoCD : ${wl.argocd.syncStatus}` : 'Géré par ArgoCD'}</span>
          <span className="later">application {wl.argocd.application}</span>
        </p>
      )}

      <h3>Ressources</h3>
      {usage ? (
        <>
          <Meter label="CPU" value={fmtCpu(usage.cpu)} reference={`request ${fmtCpu(p.requests.cpu)}`} ratio={usage.cpu / Math.max(1, p.requests.cpu)} />
          <Meter
            label="Mémoire" value={fmtMem(usage.memory)}
            reference={p.limits.memory ? `limit ${fmtMem(p.limits.memory)}` : `request ${fmtMem(p.requests.memory)}`}
            ratio={usage.memory / Math.max(1, p.limits.memory || p.requests.memory)}
          />
        </>
      ) : (
        <p className="note">Usage indisponible (metrics-server absent ou pod non démarré). Requests : {fmtCpu(p.requests.cpu)} CPU, {fmtMem(p.requests.memory)}.</p>
      )}
    </div>
  )
}

function NodeOverview({ n }: { n: Node }) {
  const { pods, metrics } = useCluster.getState()
  const onNode = [...pods.values()].filter((p) => p.nodeName === n.name).sort((a, b) => a.name.localeCompare(b.name))
  const colors = clusterColors(useCluster.getState())
  const usage = metrics.nodes[n.name]
  const allocPods = n.allocatable.pods ?? 110

  return (
    <div className="p-body">
      <dl className="kv">
        <dt>Pool</dt><dd>{n.pool}</dd>
        <dt>Machine</dt><dd>{n.instanceType}{n.spot ? ' (Spot)' : ''}</dd>
        <dt>Zone</dt><dd>{n.zone}</dd>
        <dt>Kubelet</dt><dd>{n.kubeletVersion}</dd>
        <dt>Âge</dt><dd>{age(n.createdAt)}</dd>
      </dl>

      <h3>Conditions</h3>
      <div className="tags">
        {n.conditions.map((c) => {
          const bad = c.type === 'Ready' ? c.status !== 'True' : c.status === 'True'
          return <span key={c.type} className={`tag ${bad ? 's-err' : ''}`}>{c.type}={c.status}</span>
        })}
      </div>
      {n.taints.length > 0 && (
        <>
          <h3>Taints</h3>
          <div className="tags">{n.taints.map((t) => <span key={t.key + t.effect} className="tag">{t.key}{t.value ? `=${t.value}` : ''}:{t.effect}</span>)}</div>
        </>
      )}

      <h3>Capacité allouée</h3>
      <Meter label="CPU demandé" value={fmtCpu(n.requested.cpu)} reference={`sur ${fmtCpu(n.allocatable.cpu)}`} ratio={pct(n.requested.cpu, n.allocatable.cpu) / 100} />
      <Meter label="Mémoire demandée" value={fmtMem(n.requested.memory)} reference={`sur ${fmtMem(n.allocatable.memory)}`} ratio={pct(n.requested.memory, n.allocatable.memory) / 100} />
      <Meter label="Pods" value={String(onNode.length)} reference={`sur ${allocPods}`} ratio={onNode.length / allocPods} />
      {usage && <p className="note">Usage réel : {fmtCpu(usage.cpu)} CPU, {fmtMem(usage.memory)} (metrics-server).</p>}

      <h3>Pods ({onNode.length})</h3>
      {onNode.length ? (
        <ul className="podlist">
          {onNode.map((p) => (
            <li key={p.uid}>
              <button onClick={goPod(p.uid)}>
                <i style={{ background: colors.get(p.namespace) }} />
                <span className="nm">{p.name}</span>
                <span className={BADGE[postureFor(p).antenna]}>{p.displayStatus}</span>
              </button>
            </li>
          ))}
        </ul>
      ) : <p className="note">Aucun pod sur ce node.</p>}
    </div>
  )
}

/** Node que l'utilisateur ne peut pas lister : seuls ses propres pods sont montrés. */
function GhostNode({ name }: { name: string }) {
  const { pods } = useCluster.getState()
  const onNode = [...pods.values()].filter((p) => p.nodeName === name).sort((a, b) => a.name.localeCompare(b.name))
  const colors = clusterColors(useCluster.getState())
  return (
    <div className="p-body">
      <p className="note" style={{ marginTop: 0 }}>
        Vous n'avez pas le droit <code>list</code> sur <code>nodes</code> : capacité, conditions et autres pods de ce node ne sont pas affichés.
      </p>
      <h3>Vos pods sur ce node ({onNode.length})</h3>
      <ul className="podlist">
        {onNode.map((p) => (
          <li key={p.uid}>
            <button onClick={goPod(p.uid)}>
              <i style={{ background: colors.get(p.namespace) }} />
              <span className="nm">{p.name}</span>
              <span className={BADGE[postureFor(p).antenna]}>{p.displayStatus}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function Inspector() {
  useCluster((s) => s.version)
  useCluster((s) => s.metrics)
  const selection = useCluster((s) => s.selection)
  const { pods, nodes, select } = useCluster.getState()
  const close = () => select(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') useCluster.getState().select(null) }
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
          badgeClass={BADGE[postureFor(p).antenna]} color={color} onClose={close} />
        <PodOverview p={p} />
      </>
    ) : (
      <>
        <Head kind="Pod" name={selection.name} badge="Supprimé" badgeClass="s-mute" onClose={close} />
        <div className="gone">Ce pod a été supprimé.</div>
      </>
    )
  } else if (selection?.type === 'node') {
    const n = nodes.get(selection.key)
    const ready = n?.conditions.some((c) => c.type === 'Ready' && c.status === 'True')
    const ghost = !n && [...pods.values()].some((p) => p.nodeName === selection.key)
    content = n ? (
      <>
        <Head kind={`Node · ${n.pool}`} name={n.name}
          badge={`${ready ? 'Ready' : 'NotReady'}${n.unschedulable ? ', SchedulingDisabled' : ''}`}
          badgeClass={!ready ? 's-err' : n.unschedulable ? 's-warn' : 's-ok'} onClose={close} />
        <NodeOverview n={n} />
      </>
    ) : ghost ? (
      <>
        <Head kind="Node" name={selection.key} badge="Non visible" badgeClass="s-mute" onClose={close} />
        <GhostNode name={selection.key} />
      </>
    ) : (
      <>
        <Head kind="Node" name={selection.name} badge="Supprimé" badgeClass="s-mute" onClose={close} />
        <div className="gone">Ce node n'existe plus.</div>
      </>
    )
  }

  return <aside className={`panel ${selection ? 'open' : ''}`} aria-label="Inspecteur" aria-hidden={!selection}>{content}</aside>
}
