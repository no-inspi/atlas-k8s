import { chromium } from '@playwright/test'
const b = await chromium.launch({ args: ['--enable-gpu', '--use-angle=metal', '--ignore-gpu-blocklist'] })
const p = await b.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 })
await p.goto('http://localhost:18200/?perf=1')
await p.waitForTimeout(5000)
// Compteur de frames réellement dessinées (rendu à la demande).
await p.evaluate(() => { window.__frames = 0; const loop = () => { window.__frames++; requestAnimationFrame(loop) }; })
const sample = async (label) => {
  const before = await p.evaluate(() => performance.now())
  const meter = []
  for (let i = 0; i < 5; i++) { await p.waitForTimeout(1000); meter.push(await p.evaluate(() => document.getElementById('perf-meter').textContent)) }
  console.log(label.padEnd(26), meter[meter.length - 1])
}
// Vue d'ensemble, animation continue forcée par des mouvements de caméra.
const box = await p.locator('canvas').boundingBox()
const drag = async () => { for (let i = 0; i < 40; i++) { await p.mouse.move(box.x + 700 + Math.sin(i / 5) * 200, box.y + 450); await p.waitForTimeout(25) } }
await p.mouse.move(box.x + 700, box.y + 450); await p.mouse.down({ button: 'left' })
const dragging = drag()
await sample('vue d\'ensemble (rotation)')
await dragging; await p.mouse.up({ button: 'left' })
await p.waitForTimeout(3000)
await sample('vue d\'ensemble (repos)')
// Zoom rapproché : robots complets et animés.
for (let i = 0; i < 12; i++) { await p.mouse.wheel(0, -300); await p.waitForTimeout(50) }
await p.waitForTimeout(2000)
await sample('zoom rapproché')
await p.screenshot({ path: process.env.SHOT_CLOSE })
await p.mouse.wheel(0, 3600); await p.waitForTimeout(1500)
await p.screenshot({ path: process.env.SHOT_FAR })
await b.close()
