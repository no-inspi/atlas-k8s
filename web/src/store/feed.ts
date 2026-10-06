import type { Node, Pod } from '../api/types'

export type FeedLevel = '' | 'w' | 'e'

export interface FeedItem {
  id: number
  at: number
  text: string
  level: FeedLevel
}

export type FeedDraft = Pick<FeedItem, 'text' | 'level'>

const ERROR_STATUSES = new Set(['CrashLoopBackOff', 'Error', 'OOMKilled', 'ImagePullBackOff', 'ErrImagePull'])

export const shortNode = (name: string) => name.split('-').pop() ?? name

/** Ligne du bandeau d'événements pour la transition prev → next d'un pod, ou null. */
export function feedForPod(prev: Pod | undefined, next: Pod | undefined): FeedDraft | null {
  if (!prev && next)
    return { text: next.owner.kind ? `${next.owner.kind} ${next.owner.name} a créé ${next.name}` : `Pod ${next.name} créé`, level: '' }
  if (prev && !next) return prev.displayStatus === 'Terminating' ? null : { text: `Pod supprimé : ${prev.name}`, level: '' }
  if (!prev || !next) return null

  if (!prev.nodeName && next.nodeName) return { text: `Scheduler : ${next.name} → ${shortNode(next.nodeName)}`, level: '' }
  if (next.restarts > prev.restarts) return { text: `${next.name} a redémarré (${next.restarts} redémarrages)`, level: 'e' }
  if (next.displayStatus !== prev.displayStatus) {
    if (ERROR_STATUSES.has(next.displayStatus) && !ERROR_STATUSES.has(prev.displayStatus))
      return { text: `${next.name} : ${next.displayStatus}`, level: 'e' }
    if (next.displayStatus === 'Terminating') return { text: `Arrêt de ${next.name}`, level: '' }
    if (next.displayStatus === 'Completed') return { text: `${next.name} terminé`, level: '' }
  }
  if (!next.nodeName && next.statusMessage && next.statusMessage !== prev.statusMessage)
    return { text: `${next.name} ne peut pas être placé`, level: 'w' }
  return null
}

export function feedForNode(prev: Node | undefined, next: Node | undefined): FeedDraft | null {
  if (!prev || !next || prev.unschedulable === next.unschedulable) return null
  return next.unschedulable
    ? { text: `Node ${shortNode(next.name)} cordonné`, level: 'w' }
    : { text: `Node ${shortNode(next.name)} réactivé`, level: '' }
}
