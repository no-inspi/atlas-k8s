import type { Health, PersistentVolume, Volume } from '../api/types'
import { isGatewayGate, type Gate } from '../store/net'

// Couleur des voyants (relais, portes, citernes), dans le vocabulaire des badges.

export type Signal = 'ok' | 'warn' | 'err' | 'mute'

const RANK: Record<Signal, number> = { mute: 0, ok: 1, warn: 2, err: 3 }

export const healthSignal = (h: Health): Signal => (h === 'ok' ? 'ok' : h === 'degraded' ? 'warn' : h === 'down' ? 'err' : 'mute')
export const volumeSignal = (v: Pick<Volume, 'phase'>): Signal => (v.phase === 'Bound' ? 'ok' : v.phase === 'Pending' ? 'warn' : 'err')
/** Citerne vide : seule une PV en échec est signalée. */
export const pvSignal = (p: Pick<PersistentVolume, 'phase'>): Signal => (p.phase === 'Failed' ? 'err' : 'mute')

/**
 * Porte déduite (IngressClass, traefik) : orange si une route est cassée ou
 * refusée. Porte Gateway : rouge si le Gateway n'est pas programmé ; orange si
 * un listener n'est pas prêt ou une route cassée ou refusée ; grise sans statut
 * écrit ou sans Gateway visible (une route en échec, orange, l'emporte sur
 * l'état inconnu, gris) ; verte sinon.
 */
export function gateSignal(g: Pick<Gate, 'name' | 'broken'> & Partial<Pick<Gate, 'refused' | 'gateway'>>): Signal {
  const troubled = g.broken > 0 || (g.refused ?? 0) > 0
  if (!isGatewayGate(g.name)) return troubled ? 'warn' : 'ok'
  const gw = g.gateway
  if (!gw) return troubled ? 'warn' : 'mute'
  if (gw.programmed === 'false') return 'err'
  if (troubled || gw.listeners.some((l) => l.ready === 'false')) return 'warn'
  return gw.programmed === 'unknown' ? 'mute' : 'ok'
}

export const worst = (xs: Signal[]): Signal => xs.reduce<Signal>((w, x) => (RANK[x] > RANK[w] ? x : w), 'mute')

export const HEALTH_LABEL: Record<Health, string> = {
  ok: 'Sain', degraded: 'Dégradé', down: 'Aucun endpoint ready', external: 'ExternalName',
}
