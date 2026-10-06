import { expect, test, type Page } from '@playwright/test'

// Parcours d'authentification réel : Dex (hack/dex/config.yaml) et RBAC de
// hack/dev-rbac.yaml. alice (oidc:sre) a « view » partout ; bob (oidc:dev) a
// « edit » dans production et staging seulement.

async function login(page: Page, email: string) {
  await page.goto('/')
  await page.waitForURL(/\/dex\/auth/)
  await page.locator('#login').fill(email)
  await page.locator('#password').fill('password')
  await page.locator('#submit-login').click()
  await page.waitForURL((u) => u.pathname === '/' && !u.host.startsWith('dex') && !u.port.includes('5556'))
  await expect(page.getByTestId('pods-running')).toHaveText(/\d+\/\d+/)
}

/** Concatène les messages reçus sur /api/stream. */
function recordStream(page: Page): () => string {
  const frames: string[] = []
  page.on('websocket', (ws) => {
    if (ws.url().includes('/api/stream')) ws.on('framereceived', (f) => frames.push(String(f.payload)))
  })
  return () => frames.join('\n')
}

test('sans session, l’API répond 401 et la page renvoie vers l’IdP', async ({ request, page }) => {
  expect((await request.get('/api/me', { maxRedirects: 0 })).status()).toBe(401)
  await page.goto('/')
  await expect(page).toHaveURL(/\/dex\/auth/)
})

test('alice (view sur tout le cluster) voit kube-system et les nodes', async ({ page }) => {
  const stream = recordStream(page)
  await login(page, 'alice@example.com')
  await expect(page.getByText('alice@example.com')).toBeVisible()
  await expect(page.getByRole('button', { name: 'kube-system' })).toBeVisible()
  await expect(page.locator('.stat').first()).toContainText(/\d+\/\d+/)
  await expect.poll(() => stream()).toContain('"namespace":"kube-system"')
  await page.waitForTimeout(2000) // premier rendu WebGL (logiciel en headless)
  await page.screenshot({ path: 'e2e/__screenshots__/auth-alice.png' })
})

test('bob (edit dans production et staging) ne reçoit rien de kube-system', async ({ page }) => {
  const stream = recordStream(page)
  await login(page, 'bob@example.com')
  await expect(page.getByRole('button', { name: 'production' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'staging' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'kube-system' })).toHaveCount(0)
  // Pas le droit de lister les nodes : bâtiments anonymes, compteur indisponible.
  await expect(page.locator('.stat').first()).toContainText('—')
  await page.waitForTimeout(2000)
  const frames = stream()
  expect(frames).toContain('"namespace":"production"')
  expect(frames).not.toContain('kube-system')
  expect(frames).not.toContain('"kind":"node"')
  await page.screenshot({ path: 'e2e/__screenshots__/auth-bob.png' })

  // Revue d'accès au nom de bob : delete dans production oui, dans kube-system non.
  const res = await page.evaluate(async () => {
    const r = await fetch('/api/access-review', {
      method: 'POST', headers: { 'X-Atlas-Request': '1', 'Content-Type': 'application/json' },
      body: JSON.stringify({ checks: [
        { verb: 'delete', resource: 'pods', namespace: 'production' },
        { verb: 'delete', resource: 'pods', namespace: 'kube-system' },
        { verb: 'list', resource: 'nodes' },
      ] }),
    })
    return (await r.json()).results.map((x: { allowed: boolean }) => x.allowed)
  })
  expect(res).toEqual([true, false, false])

  await page.goto('/auth/logout')
  await expect(page.getByText('Vous êtes déconnecté')).toBeVisible()
  expect((await page.request.get('/api/me')).status()).toBe(401)
})

// --- Inspecteur sur le vrai cluster (critères d'acceptation du jalon 5) ---

type SnapPod = { name: string; namespace: string; owner: { kind: string; name: string }; displayStatus: string; restarts: number }

async function snapshotPods(page: Page): Promise<SnapPod[]> {
  return page.evaluate(() => new Promise<SnapPod[]>((resolve) => {
    const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/stream`)
    ws.onmessage = (e) => {
      const m = JSON.parse(e.data)
      if (m.type === 'snapshot') { ws.close(); resolve(m.pods) }
    }
  }))
}

const editorText = (page: Page) =>
  page.getByTestId('yaml-editor').locator('.view-lines').innerText().then((t) => t.replace(/ /g, ' '))

test('alice : logs en direct, instance précédente, YAML du Deployment avec ArgoCD', async ({ page }) => {
  await login(page, 'alice@example.com')
  const pods = await snapshotPods(page)
  const panel = page.locator('aside.panel')

  // Logs en direct : orders-service écrit une ligne par seconde.
  const orders = pods.find((p) => p.namespace === 'staging' && p.owner.name === 'orders-service' && p.displayStatus === 'Running')!
  await page.goto(`/pods/staging/${orders.name}`)
  await panel.getByRole('tab', { name: 'Logs' }).click()
  const logs = panel.getByTestId('logs')
  await expect(logs).toContainText('GET /api/v1/orders')
  const before = await logs.locator('.log-line').count()
  await expect.poll(() => logs.locator('.log-line').count(), { timeout: 10_000 }).toBeGreaterThan(before)

  // Instance précédente : les logs d'avant le crash de payment-worker.
  const crashy = pods.find((p) => p.owner.name === 'payment-worker' && p.restarts > 0)!
  await page.goto(`/pods/production/${crashy.name}`)
  await panel.getByRole('tab', { name: 'Logs' }).click()
  await panel.getByLabel('Instance précédente').check()
  await expect(logs).toContainText('connection refused')

  // YAML du Deployment propriétaire, sans managedFields, badge ArgoCD.
  const api = pods.find((p) => p.owner.name === 'api-gateway' && p.namespace === 'production')!
  await page.goto(`/pods/production/${api.name}`)
  await panel.getByRole('tab', { name: 'YAML' }).click()
  await expect(panel.getByLabel('Objet')).toHaveValue('Deployment/api-gateway')
  await expect.poll(() => editorText(page), { timeout: 15_000 }).toContain('kind: Deployment')
  expect(await editorText(page)).not.toContain('managedFields')
  await expect(panel.getByTestId('argocd-badge')).toContainText('prod-apps')

  await panel.getByRole('tab', { name: 'Événements' }).click()
  await expect(panel.getByTestId('events')).toContainText(/Scheduled|Pulled|Started|Created/)
})

test('bob : l’inspecteur renvoie le refus de l’API server pour kube-system', async ({ page }) => {
  await login(page, 'bob@example.com')
  const yaml = await page.request.get('/api/yaml/apps/v1/Deployment/kube-system/coredns')
  expect(yaml.status()).toBe(403)
  expect((await yaml.json()).error).toContain('cannot get resource "deployments"')
  expect((await page.request.get('/api/namespaces/kube-system/pods/coredns/events')).status()).toBe(403)

  const logsError = await page.evaluate(() => new Promise<{ status: number; message: string }>((resolve) => {
    const ws = new WebSocket(`ws://${location.host}/api/namespaces/kube-system/pods/coredns/logs`)
    ws.onmessage = (e) => resolve(JSON.parse(e.data))
  }))
  expect(logsError.status).toBe(403)
  expect(logsError.message).toContain('forbidden')
})
