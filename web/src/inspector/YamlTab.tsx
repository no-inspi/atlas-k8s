import { Suspense, lazy, useEffect, useState } from 'react'
import { getYaml, type Ref, type YamlDoc } from '../api/inspect'
import type { Pod } from '../api/types'
import { useTheme } from '../scene/theme'
import { useCluster } from '../store/cluster'
import { useOwners } from './PodOverview'

const MonacoYaml = lazy(() => import('./MonacoYaml'))

/**
 * YAML du workload racine du pod par défaut, avec bascule vers le ReplicaSet ou
 * le pod. Lecture seule : si l'objet est géré par ArgoCD, les modifications
 * passent par Git.
 */
export function YamlTab({ p }: { p: Pod }) {
  const chain = useOwners(p)
  const target = useCluster((s) => s.yamlTarget)
  const openYaml = useCluster((s) => s.openYaml)
  const theme = useTheme()
  const [doc, setDoc] = useState<YamlDoc | null>(null)
  const [error, setError] = useState('')

  const options: Ref[] = chain ?? []
  const current: Ref | undefined = options.find((r) => target && r.kind === target.kind && r.name === target.name) ?? options[0]

  useEffect(() => {
    if (!current) return
    let live = true
    setDoc(null)
    setError('')
    getYaml(current).then((d) => live && setDoc(d)).catch((e: Error) => live && setError(e.message))
    return () => { live = false }
  }, [current?.kind, current?.name, current?.namespace])

  return (
    <>
      <div className="toolbar">
        <label>
          Objet
          <select aria-label="Objet" value={current ? `${current.kind}/${current.name}` : ''}
            onChange={(e) => { const [kind, name] = e.target.value.split('/'); openYaml({ kind, name }) }}>
            {options.map((r) => <option key={r.kind + r.name} value={`${r.kind}/${r.name}`}>{r.kind}/{r.name}</option>)}
          </select>
        </label>
        <span className="sp" />
        {doc?.argocd && (
          <span className="argo" data-testid="argocd-badge">
            ArgoCD{doc.argocd.syncStatus ? ` : ${doc.argocd.syncStatus}` : ''} · {doc.argocd.application}
          </span>
        )}
      </div>
      {error && <div className="log-banner"><span className="s-err">{error}</span></div>}
      {!error && !doc && <div className="p-body"><p className="note">Chargement…</p></div>}
      {doc && (
        <Suspense fallback={<div className="p-body"><p className="note">Chargement de l'éditeur…</p></div>}>
          <MonacoYaml value={doc.yaml} dark={theme.dark} />
        </Suspense>
      )}
      <div className="toolbar footer">
        <span className="note" style={{ margin: 0 }}>
          {doc?.argocd
            ? "Lecture seule : cet objet est géré par ArgoCD, les modifications passent par Git (sinon le selfHeal les écrase)."
            : 'Lecture seule.'}
        </span>
      </div>
    </>
  )
}
