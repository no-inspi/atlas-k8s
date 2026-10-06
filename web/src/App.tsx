import { useEffect } from 'react'
import { connectStream } from './api/stream'
import type { Me } from './api/types'
import { Scene } from './scene/Scene'
import { useCluster } from './store/cluster'
import { Feed, Hint, Legend, Stats, TopBar } from './ui/Hud'
import { Inspector } from './ui/Inspector'

export function App() {
  useEffect(() => {
    fetch('/api/me')
      .then((r) => (r.ok ? (r.json() as Promise<Me>) : null))
      .then((me) => me && useCluster.getState().setMe(me))
      .catch(() => {})
    return connectStream()
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
    </>
  )
}
