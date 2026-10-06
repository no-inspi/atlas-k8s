import { expect, type Page } from '@playwright/test'

// Lecture du flux depuis la page (mêmes cookies, même origine). Les tests
// partagent un cluster simulé que certains modifient (drain, delete) : on
// attend qu'un pod voulu existe plutôt que de supposer l'état.

export type SnapPod = {
  uid: string; name: string; namespace: string; nodeName: string
  owner: { kind: string; name: string }; displayStatus: string; restarts: number
}
export type SnapNode = { name: string; pool: string; unschedulable: boolean }

export async function snapshot(page: Page): Promise<{ pods: SnapPod[]; nodes: SnapNode[] }> {
  if (!page.url().startsWith('http')) await page.goto('/')
  return page.evaluate(() => new Promise((resolve, reject) => {
    const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/stream`)
    const timer = setTimeout(() => { ws.close(); reject(new Error('snapshot : délai dépassé')) }, 5000)
    ws.onmessage = (e) => {
      const m = JSON.parse(e.data)
      if (m.type === 'snapshot') { clearTimeout(timer); ws.close(); resolve({ pods: m.pods, nodes: m.nodes ?? [] }) }
    }
    ws.onerror = () => { clearTimeout(timer); reject(new Error('snapshot : connexion impossible')) }
  }))
}

/** Attend (jusqu'à 30 s) qu'un pod satisfasse la condition et le renvoie. */
export async function waitForPod(page: Page, match: (p: SnapPod) => boolean): Promise<SnapPod> {
  let found: SnapPod | undefined
  await expect.poll(async () => {
    found = (await snapshot(page).catch(() => ({ pods: [] as SnapPod[] }))).pods.find(match)
    return !!found
  }, { timeout: 30_000, intervals: [500, 1000] }).toBe(true)
  return found!
}
