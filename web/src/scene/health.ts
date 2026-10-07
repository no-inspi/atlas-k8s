import type { Health, Volume } from '../api/types'
import type { Gate } from '../store/net'

// Couleur des voyants (relais, portes, citernes), dans le vocabulaire des badges.

export type Signal = 'ok' | 'warn' | 'err' | 'mute'

const RANK: Record<Signal, number> = { mute: 0, ok: 1, warn: 2, err: 3 }

export const healthSignal = (h: Health): Signal => (h === 'ok' ? 'ok' : h === 'degraded' ? 'warn' : h === 'down' ? 'err' : 'mute')
export const volumeSignal = (v: Pick<Volume, 'phase'>): Signal => (v.phase === 'Bound' ? 'ok' : v.phase === 'Pending' ? 'warn' : 'err')
export const gateSignal = (g: Pick<Gate, 'broken'>): Signal => (g.broken ? 'warn' : 'ok')
export const worst = (xs: Signal[]): Signal => xs.reduce<Signal>((w, x) => (RANK[x] > RANK[w] ? x : w), 'mute')

export const HEALTH_LABEL: Record<Health, string> = {
  ok: 'Sain', degraded: 'Dégradé', down: 'Aucun endpoint ready', external: 'ExternalName',
}
