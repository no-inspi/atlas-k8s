import type { Node } from '../api/types'
import { clusterColors } from '../scene/colors'
import { postureFor } from '../scene/posture'
import { useCluster } from '../store/cluster'
import { age, fmtCpu, fmtMem, pct } from '../ui/format'
import { BADGE, Meter, goPod } from './common'

export function NodeOverview({ n }: { n: Node }) {
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
export function GhostNode({ name }: { name: string }) {
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
