import { useEffect, useState } from 'react'
import { cordon, drain, drainPlan, type DrainPlan } from '../api/actions'
import type { Node } from '../api/types'
import { useCluster } from '../store/cluster'
import { shortNode } from '../store/feed'
import { Dialog } from '../ui/Dialog'
import { ActionButton } from './ActionButton'
import { nodeChecks, useAccess } from './access'

/** Cordon, uncordon et drain (avec récapitulatif et confirmation par le nom du node). */
export function NodeActions({ n }: { n: Node }) {
  const notify = useCluster((s) => s.notify)
  const checks = nodeChecks(n.name)
  const access = useAccess(checks)
  const [busy, setBusy] = useState(false)
  const [draining, setDraining] = useState(false)
  const short = shortNode(n.name)
  // Le drain cordonne puis évince : il faut les deux droits. On explique le premier qui manque.
  const drainNeeds = access?.cordon?.allowed === false ? 'cordon' : 'drain'

  const toggle = async () => {
    setBusy(true)
    try {
      await cordon(n.name, !n.unschedulable)
      notify(`Node ${short} ${n.unschedulable ? 'réactivé' : 'cordonné'}`, n.unschedulable ? '' : 'w')
    } catch (e) {
      notify(`Cordon de ${short} : échec — ${(e as Error).message}`, 'e')
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <div className="actions">
        <ActionButton check={checks.cordon} decision={access?.cordon} busy={busy} onClick={toggle}>
          {n.unschedulable ? 'Uncordon' : 'Cordon'}
        </ActionButton>
        <ActionButton danger check={checks[drainNeeds]} decision={access?.[drainNeeds]} busy={busy} onClick={() => setDraining(true)}>
          Drain
        </ActionButton>
      </div>
      {draining && <DrainDialog node={n.name} onClose={() => setDraining(false)} />}
    </>
  )
}

function DrainDialog({ node, onClose }: { node: string; onClose: () => void }) {
  const notify = useCluster((s) => s.notify)
  const short = shortNode(node)
  const [plan, setPlan] = useState<DrainPlan | null>(null)
  const [error, setError] = useState('')
  const [typed, setTyped] = useState('')
  const [running, setRunning] = useState(false)

  useEffect(() => {
    drainPlan(node).then(setPlan).catch((e: Error) => setError(e.message))
  }, [node])

  const run = async () => {
    setRunning(true)
    try {
      const res = await drain(node)
      const evicted = res.evictions.filter((e) => e.result === 'evicted').length
      const failed = res.evictions.filter((e) => e.result !== 'evicted')
      notify(`Drain de ${short} : ${evicted} pod${evicted > 1 ? 's' : ''} évincé${evicted > 1 ? 's' : ''}` +
        (failed.length ? `, ${failed.length} en échec (${failed.map((f) => `${f.name} : ${f.result}`).join(', ')})` : ''), failed.length ? 'e' : 'w')
      onClose()
    } catch (e) {
      notify(`Drain de ${short} : échec — ${(e as Error).message}`, 'e')
      setRunning(false)
    }
  }

  return (
    <Dialog title={`Drainer ${short} ?`} onClose={onClose} actions={<>
      <button className="btn" onClick={onClose}>Annuler</button>
      <button className="btn danger" disabled={!plan || typed !== short || running} onClick={run}>{running ? 'Drain en cours…' : 'Drainer'}</button>
    </>}>
      {error && <p className="s-err">{error}</p>}
      {!plan && !error && <p className="note">Calcul du récapitulatif…</p>}
      {plan && (
        <div className="drain-plan">
          <p>Le node sera cordonné, puis ses pods évincés par l'API Eviction (les PodDisruptionBudgets sont respectés).</p>
          <h3>Pods évincés ({plan.evict.length})</h3>
          <ul>{plan.evict.map((p) => <li key={p.namespace + p.name}>{p.namespace}/{p.name}{p.owner ? ` · ${p.owner}` : ''}{p.emptyDir ? ' · données emptyDir perdues' : ''}</li>)}</ul>
          {plan.ignored.length > 0 && <>
            <h3>Ignorés ({plan.ignored.length})</h3>
            <ul>{plan.ignored.map((p) => <li key={p.namespace + p.name}>{p.namespace}/{p.name} — {p.reason}</li>)}</ul>
          </>}
          {plan.blocking.length > 0 && <>
            <h3 className="s-warn">PodDisruptionBudgets qui bloqueraient</h3>
            <ul>{plan.blocking.map((b) => <li key={b.namespace + b.name}>{b.namespace}/{b.name} : {b.disruptionsAllowed} interruption(s) autorisée(s) pour {b.pods.join(', ')}</li>)}</ul>
          </>}
          {plan.pdbUnknown && <p className="note">{plan.pdbUnknown}</p>}
          <label className="confirm-name">Tapez <b>{short}</b> pour confirmer
            <input value={typed} onChange={(e) => setTyped(e.target.value)} aria-label="Nom court du node" autoComplete="off" spellCheck={false} />
          </label>
        </div>
      )}
    </Dialog>
  )
}
