import { iconShape } from '../scene/Pods'
import { kindOf, SORT_LABEL, sizeMetric, sortKinds, SYSTEM_NAMESPACES, type PodSort } from '../scene/podView'
import { world } from '../scene/world'
import { useCluster } from '../store/cluster'
import { fmtCpu, fmtMem } from './format'

/** Pictogramme de l'icône d'état pour chaque type de workload. */
const ICON_GLYPH = { iconSquare: '■', iconDome: '◗', iconDisk: '◉', iconPyramid: '▲' } as const

/**
 * Réglages d'affichage des pods : ordre et hauteur des blocs, namespaces
 * système et types de workload masqués.
 */
export function PodControls() {
  useCluster((s) => s.version)
  const view = useCluster((s) => s.podView)
  const setView = useCluster((s) => s.setPodView)
  useCluster((s) => s.nsFilter) // un namespace système filtré s'affiche malgré le masquage
  const st = useCluster.getState()
  world.update(st)
  const { pods } = st

  const kindCounts = new Map<string, number>()
  let system = 0
  for (const p of pods.values()) {
    kindCounts.set(kindOf(p), (kindCounts.get(kindOf(p)) ?? 0) + 1)
    if (SYSTEM_NAMESPACES.has(p.namespace)) system++
  }
  // Un type masqué reste proposé même s'il n'a plus de pods, pour pouvoir le réafficher.
  const kinds = sortKinds([...kindCounts.keys(), ...view.hiddenKinds])
  const toggleKind = (k: string) =>
    setView({ hiddenKinds: view.hiddenKinds.includes(k) ? view.hiddenKinds.filter((x) => x !== k) : [...view.hiddenKinds, k] })

  return (
    <div className="pod-controls" role="group" aria-label="Affichage des pods">
      <label className="pc-sort">
        <span>Trier par</span>
        <select value={view.sort} onChange={(e) => setView({ sort: e.target.value as PodSort })}>
          {(Object.keys(SORT_LABEL) as PodSort[]).map((s) => <option key={s} value={s}>{SORT_LABEL[s]}</option>)}
        </select>
      </label>
      <span className="pc-note" title="La hauteur d'un bloc est la part de la capacité du node demandée par le pod">
        hauteur = request {sizeMetric(view.sort) === 'cpu' ? 'CPU' : 'RAM'}
      </span>
      {system > 0 && (
        <button className="ns" aria-pressed={view.hideSystem} onClick={() => setView({ hideSystem: !view.hideSystem })}
          title="kube-system, kube-public, kube-node-lease">
          Masquer système ({system})
        </button>
      )}
      {kinds.map((k) => {
        const shown = !view.hiddenKinds.includes(k)
        return (
          <button key={k} className="ns kind" aria-pressed={shown} onClick={() => toggleKind(k)}
            title={shown ? `Masquer les pods ${k}` : `Afficher les pods ${k}`}>
            <span className="glyph" aria-hidden="true">{ICON_GLYPH[iconShape(k)]}</span>
            {k} <span className="count">{kindCounts.get(k) ?? 0}</span>
          </button>
        )
      })}
      {world.filtered > 0 && <span className="pc-note">{world.filtered} pods masqués</span>}
    </div>
  )
}

/** Infobulle du pod sous la souris : de quoi identifier un bloc sans cliquer. */
export function PodTooltip() {
  const hover = useCluster((s) => s.hover)
  useCluster((s) => s.version)
  if (!hover) return null
  const pod = useCluster.getState().pods.get(hover.uid)
  if (!pod) return null
  const stack = world.stacks.find((s) => s.uid === pod.uid)
  const owner = pod.owner.kind ? `${pod.owner.kind}/${pod.owner.name}` : 'Pod sans propriétaire'
  const flipX = hover.x > window.innerWidth - 300
  return (
    <div className="pod-tip" role="tooltip"
      style={{ left: flipX ? undefined : hover.x + 14, right: flipX ? window.innerWidth - hover.x + 14 : undefined, top: hover.y + 14 }}>
      <strong>{pod.name}</strong>
      <span>{pod.namespace} · {owner}</span>
      <span>
        {pod.displayStatus} · CPU {pod.requests.cpu ? fmtCpu(pod.requests.cpu) : '—'} · RAM {pod.requests.memory ? fmtMem(pod.requests.memory) : '—'}
        {pod.restarts > 0 && ` · ${pod.restarts} redémarrages`}
      </span>
      {stack && <span className="pc-note">Pile de {stack.count} pods ({stack.owner})</span>}
    </div>
  )
}
