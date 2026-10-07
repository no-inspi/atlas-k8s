import { Suspense, lazy, useEffect, useState } from 'react'
import { getYaml, type Ref, type YamlDoc } from '../api/inspect'
import { useTheme } from '../scene/theme'

const MonacoYaml = lazy(() => import('./MonacoYaml'))

/** YAML en lecture seule d'un objet désigné directement (Service, route, PVC). */
export function RefYamlTab({ target }: { target: Ref }) {
  const theme = useTheme()
  const [doc, setDoc] = useState<YamlDoc | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    setDoc(null)
    setError('')
    getYaml(target).then((d) => live && setDoc(d)).catch((e: Error) => live && setError(e.message))
    return () => { live = false }
  }, [target.group, target.version, target.kind, target.namespace, target.name]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <>
      {error && <div className="log-banner"><span className="s-err">{error}</span></div>}
      {!error && !doc && <div className="p-body"><p className="note">Chargement…</p></div>}
      {doc && (
        <Suspense fallback={<div className="p-body"><p className="note">Chargement de l'éditeur…</p></div>}>
          <MonacoYaml value={doc.yaml} dark={theme.dark} />
        </Suspense>
      )}
      <div className="toolbar footer"><span className="note" style={{ margin: 0 }}>Lecture seule.</span></div>
    </>
  )
}
