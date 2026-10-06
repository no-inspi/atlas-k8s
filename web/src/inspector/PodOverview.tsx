import { useEffect, useState } from 'react'
import { getOwners, type Ref } from '../api/inspect'
import { workloadKey, type Pod } from '../api/types'
import { useCluster } from '../store/cluster'
import { shortNode } from '../store/feed'
import { age, fmtCpu, fmtMem } from '../ui/format'
import { Meter, goNode } from './common'

/** Chaîne de propriétaires du pod (Deployment › ReplicaSet › Pod), lue au nom de l'utilisateur. */
export function useOwners(p: Pod): Ref[] | null {
  const [chain, setChain] = useState<Ref[] | null>(null)
  useEffect(() => {
    let live = true
    setChain(null)
    getOwners(p.namespace, p.name).then((c) => live && setChain(c)).catch(() => live && setChain([]))
    return () => { live = false }
  }, [p.namespace, p.name])
  return chain
}

export function PodOverview({ p }: { p: Pod }) {
  const { pods, workloads, metrics, openYaml } = useCluster.getState()
  const chain = useOwners(p)
  const siblings = [...pods.values()].filter((q) => q.namespace === p.namespace && q.owner.kind === p.owner.kind && q.owner.name === p.owner.name && q.displayStatus !== 'Terminating')
  const onNodes = [...new Set(siblings.filter((q) => q.nodeName).map((q) => q.nodeName))].sort()
  const waiting = siblings.filter((q) => !q.nodeName).length
  const wl = p.owner.kind ? workloads.get(workloadKey({ kind: p.owner.kind, namespace: p.namespace, name: p.owner.name })) : undefined
  const usage = metrics.pods[p.uid]
  const owners = (chain ?? []).filter((r) => r.kind !== 'Pod')

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
      <div className="chain" data-testid="owner-chain">
        {chain === null && p.owner.kind && <span className="later">{p.owner.kind} {p.owner.name}</span>}
        {owners.map((r) => (
          <span key={r.kind + r.name} style={{ display: 'contents' }}>
            <button className="link" onClick={() => openYaml({ kind: r.kind, name: r.name })} title="Voir le YAML">{r.kind} {r.name}</button>
            <span className="sep">›</span>
          </span>
        ))}
        <button className="link cur" onClick={() => openYaml({ kind: 'Pod', name: p.name })} title="Voir le YAML">ce pod</button>
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
