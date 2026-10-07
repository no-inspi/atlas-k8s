import { world } from '../scene/world'
import { useCluster } from '../store/cluster'

const LABELS: [string, string, string][] = [['gate', 'porte', 'portes'], ['service', 'Service', 'Services'], ['pod', 'pod', 'pods'], ['volume', 'PVC', 'PVC']]

/**
 * Résumé du chemin allumé par la sélection d'une porte, d'une route, d'un
 * Service ou d'un PVC : ce que la ville montre, dit aussi en texte (lecteurs
 * d'écran, tests).
 */
export function PathSummary() {
  const selection = useCluster((s) => s.selection)
  useCluster((s) => s.version)
  if (!selection || selection.type === 'pod' || selection.type === 'node') return null
  world.update(useCluster.getState())
  const path = world.focusFor(selection, null, null).path
  if (!path) return null
  const parts = LABELS.map(([type, one, many]) => {
    const n = [...path].filter((k) => k.startsWith(`${type}:`)).length
    return n ? `${n} ${n === 1 ? one : many}` : ''
  }).filter(Boolean)
  return <div className="path-summary" role="status" data-testid="path-summary">Chemin : {parts.join(' · ') || 'aucun lien'}</div>
}
