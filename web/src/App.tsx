import { useEffect } from 'react'
import { apiFetch } from './api/http'
import { connectStream } from './api/stream'
import type { Me } from './api/types'
import { Scene } from './scene/Scene'
import { useCluster } from './store/cluster'
import { Feed, Hint, Legend, Stats, TopBar } from './ui/Hud'
import { PodControls, PodTooltip } from './ui/PodControls'
import { Inspector } from './inspector/Inspector'
import { syncRoute } from './ui/route'
import { Toasts } from './ui/Toasts'
import { ListView } from './ui/ListView'
import { perfEnabled } from './scene/PerfMeter'

export function App() {
  const view = useCluster((s) => s.view)
  useEffect(() => {
    apiFetch('/api/me')
      .then((r) => (r.ok ? (r.json() as Promise<Me>) : null))
      .then((me) => me && useCluster.getState().setMe(me))
      .catch(() => {})
    const stopRoute = syncRoute()
    const stopStream = connectStream()
    return () => { stopRoute(); stopStream() }
  }, [])

  return (
    <>
      <div className="stage"><Scene /></div>
      {view === 'list' && <ListView />}
      <TopBar />
      <Stats />
      <div className="bottom-left">
        <Feed />
        {view === '3d' && <PodControls />}
        <Legend />
      </div>
      <Hint />
      {view === '3d' && <PodTooltip />}
      <Inspector />
      <Toasts />
      {perfEnabled() && <div id="perf-meter" className="perf-meter" aria-hidden="true" />}
    </>
  )
}
