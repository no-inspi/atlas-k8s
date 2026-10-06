import { expect, test, type Page } from '@playwright/test'

// Inspecteur en mode démo : logs en streaming, instance précédente, YAML du
// workload racine (Monaco), événements, chaîne de propriétaires cliquable.

type SnapPod = { name: string; namespace: string; owner: { kind: string; name: string }; displayStatus: string; restarts: number }

/** Lit le snapshot de /api/stream depuis la page (mêmes cookies, même origine). */
async function snapshotPods(page: Page): Promise<SnapPod[]> {
  await page.goto('/')
  return page.evaluate(() => new Promise<SnapPod[]>((resolve) => {
    const ws = new WebSocket(`ws://${location.host}/api/stream`)
    ws.onmessage = (e) => {
      const m = JSON.parse(e.data)
      if (m.type === 'snapshot') { ws.close(); resolve(m.pods) }
    }
  }))
}

const editorText = (page: Page) =>
  page.getByTestId('yaml-editor').locator('.view-lines').innerText().then((t) => t.replace(/ /g, ' '))

test('logs en direct, YAML du Deployment et événements', async ({ page }) => {
  const problems: string[] = []
  page.on('console', (m) => { if (m.type() === 'error') problems.push(m.text()) })
  page.on('pageerror', (e) => problems.push(e.message))

  const pods = await snapshotPods(page)
  const api = pods.find((p) => p.owner.name === 'api-gateway' && p.namespace === 'production' && p.displayStatus === 'Running')!
  await page.goto(`/pods/production/${api.name}`)
  const panel = page.locator('aside.panel')
  await expect(panel.getByText(api.name)).toBeVisible()

  // Chaîne de propriétaires cliquable : ouvre le YAML du Deployment.
  await panel.getByTestId('owner-chain').getByRole('button', { name: 'Deployment api-gateway' }).click()
  await expect(panel.getByRole('tab', { name: 'YAML' })).toHaveAttribute('aria-selected', 'true')
  await expect.poll(() => editorText(page), { timeout: 15_000 }).toContain('kind: Deployment')
  const yaml = await editorText(page)
  expect(yaml).not.toContain('managedFields')
  await expect(panel.getByTestId('argocd-badge')).toContainText('production-apps')
  await expect(panel.getByText('les modifications passent par Git')).toBeVisible()

  await panel.getByLabel('Objet').selectOption({ label: `Pod/${api.name}` })
  await expect.poll(() => editorText(page)).toContain('kind: Pod')

  // Logs : lignes initiales puis nouvelles lignes en direct.
  await panel.getByRole('tab', { name: 'Logs' }).click()
  const logs = panel.getByTestId('logs')
  await expect(logs).toContainText('Starting api-gateway')
  await expect(logs.locator('.lv-i').first()).toBeVisible()
  const count = () => logs.locator('.log-line').count()
  const before = await count()
  await expect.poll(count, { timeout: 15_000 }).toBeGreaterThan(before)
  await panel.getByLabel('Filtrer les logs').fill('healthz')
  await expect.poll(async () => (await logs.innerText()).split('\n').filter((l) => l.trim() && !l.includes('healthz')).length).toBe(0)

  // Événements.
  await panel.getByRole('tab', { name: 'Événements' }).click()
  await expect(panel.getByTestId('events')).toContainText('Started')

  // L'onglet reste actif en passant à un autre pod.
  const other = pods.find((p) => p.owner.name === 'orders-service' && p.namespace === 'production')!
  await page.goto(`/pods/production/${other.name}`)
  await page.getByText(other.name).first().waitFor()
  expect(problems).toEqual([])
})

test('instance précédente : les logs d’avant le crash', async ({ page }) => {
  const pods = await snapshotPods(page)
  const crashy = pods.find((p) => p.owner.name === 'payment-worker' && p.restarts > 0)!
  await page.goto(`/pods/production/${crashy.name}`)
  const panel = page.locator('aside.panel')
  await panel.getByRole('tab', { name: 'Logs' }).click()
  await panel.getByLabel('Instance précédente').check()
  const logs = panel.getByTestId('logs')
  await expect(logs).toContainText('connection refused')
  await expect(logs.locator('.lv-e').first()).toBeVisible()
  await expect(panel.getByText("Fin des logs de l'instance précédente.")).toBeVisible()
  await page.screenshot({ path: 'e2e/__screenshots__/inspector-logs.png' })
})
