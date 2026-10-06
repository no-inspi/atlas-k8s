import type { Antenna } from '../scene/posture'
import { useCluster } from '../store/cluster'

export const BADGE: Record<Antenna, string> = { ok: 's-ok', warn: 's-warn', err: 's-err', mute: 's-mute', done: 's-mute' }

export function Meter({ label, value, reference, ratio }: { label: string; value: string; reference: string; ratio: number }) {
  return (
    <div className="meter">
      <div className="meter-row"><span>{label} <b>{value}</b></span><span>{reference}</span></div>
      <div className="bar"><i className={ratio > 0.9 ? 'hot' : ''} style={{ width: `${Math.max(2, Math.min(100, ratio * 100))}%` }} /></div>
    </div>
  )
}

export const goNode = (name: string) => () => useCluster.getState().select({ type: 'node', key: name })
export const goPod = (uid: string) => () => useCluster.getState().select({ type: 'pod', key: uid })
