import { useEffect, useState } from 'react'
import { deletePod, restart, scale } from '../api/actions'
import { workloadKey, type Pod } from '../api/types'
import { useCluster } from '../store/cluster'
import { Dialog } from '../ui/Dialog'
import { ActionButton } from './ActionButton'
import { deniedText, podChecks, useAccess } from './access'

type Pending = { kind: 'delete' } | { kind: 'scale'; to: number } | null

/** Actions sur un pod et son workload : supprimer, scale, rollout restart. */
export function PodActions({ p }: { p: Pod }) {
  const notify = useCluster((s) => s.notify)
  const workloads = useCluster.getState().workloads
  const checks = podChecks(p)
  const access = useAccess(checks)
  const [confirm, setConfirm] = useState<Pending>(null)
  const [busy, setBusy] = useState(false)
  const wl = p.owner.kind ? workloads.get(workloadKey({ kind: p.owner.kind, namespace: p.namespace, name: p.owner.name })) : undefined
  // Cible demandée, affichée jusqu'à ce que le flux confirme le nouveau nombre de replicas :
  // deux clics rapides font bien 2 → 1 → 0.
  const [target, setTarget] = useState<number | null>(null)
  const replicas = target ?? wl?.replicas ?? 0
  useEffect(() => { if (target !== null && wl?.replicas === target) setTarget(null) }, [target, wl?.replicas])

  const run = async (label: string, fn: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await fn()
      notify(label)
    } catch (e) {
      notify(`${label.split(' :')[0]} : échec — ${(e as Error).message}`, 'e')
    } finally {
      setBusy(false)
      setConfirm(null)
    }
  }
  const doScale = (to: number) => {
    setTarget(to)
    return run(`${p.owner.kind} ${p.owner.name} scalé à ${to}`, () => scale(p.namespace, p.owner.kind, p.owner.name, to).catch((e) => {
      setTarget(null)
      throw e
    }))
  }
  const requestScale = (to: number) => (to === 0 ? setConfirm({ kind: 'scale', to }) : doScale(to))

  return (
    <>
      <div className="actions">
        <ActionButton danger check={checks.delete} decision={access?.delete} busy={busy} onClick={() => setConfirm({ kind: 'delete' })}>
          Supprimer le pod
        </ActionButton>
        {checks.scale && (
          <span className="stepper" role="group" aria-label={`Replicas de ${p.owner.kind} ${p.owner.name}`}
            title={access?.scale && !access.scale.allowed ? deniedText(checks.scale) : undefined}>
            <button aria-label="Retirer un replica" disabled={!access?.scale?.allowed || busy || replicas <= 0} onClick={() => requestScale(replicas - 1)}>−</button>
            <span>{replicas} replica{replicas > 1 ? 's' : ''}{wl ? ` (${wl.readyReplicas} prêts)` : ''}</span>
            <button aria-label="Ajouter un replica" disabled={!access?.scale?.allowed || busy} onClick={() => requestScale(replicas + 1)}>+</button>
          </span>
        )}
        {checks.restart && (
          <ActionButton check={checks.restart} decision={access?.restart} busy={busy}
            onClick={() => run(`Rollout restart de ${p.owner.kind} ${p.owner.name} lancé`, () => restart(p.namespace, p.owner.kind, p.owner.name))}>
            Rollout restart
          </ActionButton>
        )}
      </div>
      {wl?.argocd && checks.scale && (
        <p className="note">
          {p.owner.kind} géré par ArgoCD (application {wl.argocd.application}) : si selfHeal est activé, un scale sera annulé à la prochaine synchronisation.
        </p>
      )}

      {confirm?.kind === 'delete' && (
        <Dialog title="Supprimer le pod ?" onClose={() => setConfirm(null)} actions={<>
          <button className="btn" onClick={() => setConfirm(null)}>Annuler</button>
          <button className="btn danger" disabled={busy} onClick={() => run(`Pod ${p.name} supprimé`, () => deletePod(p.namespace, p.name))}>Supprimer</button>
        </>}>
          <p><b>{p.name}</b> ({p.namespace}) sera supprimé.{' '}
            {p.owner.kind ? `Son ${p.owner.kind === 'Deployment' ? 'ReplicaSet' : p.owner.kind} le recréera.` : "Il n'a pas de contrôleur : il ne sera pas recréé."}</p>
        </Dialog>
      )}
      {confirm?.kind === 'scale' && (
        <Dialog title={`Scaler ${p.owner.name} à 0 ?`} onClose={() => setConfirm(null)} actions={<>
          <button className="btn" onClick={() => setConfirm(null)}>Annuler</button>
          <button className="btn danger" disabled={busy} onClick={() => doScale(0)}>Scaler à 0</button>
        </>}>
          <p>Tous les pods de {p.owner.kind} <b>{p.owner.name}</b> seront arrêtés.</p>
        </Dialog>
      )}
    </>
  )
}
