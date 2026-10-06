import { Suspense, lazy, useCallback, useState } from 'react'
import type { Pod } from '../api/types'
import { useCluster } from '../store/cluster'
import type { TermStatus } from './Terminal'
import { deniedText, podChecks, useAccess } from './access'

const Terminal = lazy(() => import('./Terminal'))

/** Onglet Terminal : vérifie d'abord ce qui empêcherait d'ouvrir un shell. */
export function TerminalTab({ p }: { p: Pod }) {
  const features = useCluster((s) => s.me?.features)
  const containers = p.containers.filter((c) => !c.init)
  const [container, setContainer] = useState(containers[0]?.name ?? '')
  const [session, setSession] = useState(0)
  const [status, setStatus] = useState<TermStatus>('connecting')
  const onStatus = useCallback((s: TermStatus) => setStatus(s), [])
  const check = podChecks(p).exec
  const access = useAccess({ exec: check })

  let blocked = ''
  if (features && !features.exec) blocked = 'Le terminal est désactivé sur cette installation.'
  else if (features?.execDeniedNamespaces.includes(p.namespace)) blocked = `Le terminal est interdit dans le namespace ${p.namespace}.`
  else if (p.displayStatus !== 'Running') blocked = `Container non démarré : ${p.displayStatus}.`
  else if (access && !access.exec.allowed) blocked = `${deniedText(check)}.`

  return (
    <>
      <div className="toolbar">
        <label>
          Container
          <select aria-label="Container" value={container} onChange={(e) => setContainer(e.target.value)}>
            {containers.map((c) => <option key={c.name}>{c.name}</option>)}
          </select>
        </label>
        <span className="sp" />
        {!blocked && status === 'closed' && <button className="btn small" onClick={() => setSession((n) => n + 1)}>Reconnecter</button>}
      </div>
      {blocked ? (
        <div className="term-blocked" role="status">{blocked}</div>
      ) : !access ? (
        <div className="term-blocked">Vérification des droits…</div>
      ) : (
        <Suspense fallback={<div className="term-blocked">Chargement du terminal…</div>}>
          <Terminal ns={p.namespace} pod={p.name} container={container} session={session} onStatus={onStatus} />
        </Suspense>
      )}
    </>
  )
}
