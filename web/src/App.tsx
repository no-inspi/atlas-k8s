import { useEffect } from 'react'
import { apiFetch } from './api/http'
import { connectStream } from './api/stream'
import type { Me } from './api/types'
import { Scene } from './scene/Scene'
import { useCluster } from './store/cluster'
import { Feed, Hint, Legend, Stats, TopBar } from './ui/Hud'
import { Inspector } from './inspector/Inspector'
import { syncRoute } from './ui/route'
import { Toasts } from './ui/Toasts'

export function App() {
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
      <TopBar />
      <Stats />
      <div className="bottom-left">
        <Feed />
        <Legend />
      </div>
      <Hint />
      <Inspector />
      <Toasts />
    </>
  )
}
