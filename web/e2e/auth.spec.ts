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
  // Jalon 9 : PV sans PVC (hack/scenarios/30-storage.yaml) et HTTPRoute
  // (hack/scenarios-gateway), lisibles avec « view » sur tout le cluster.
  // PV Released créé par hack/scenarios/30-storage.yaml.
  await expect.poll(() => stream()).toContain('"name":"atlas-it-released"')
  await expect.poll(() => stream()).toContain('"source":"HTTPRoute"')
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
  // Témoin positif avant les absences : le snapshot de bob est bien arrivé.
  await expect.poll(() => stream()).toContain('"namespace":"staging"')
  const frames = stream()
  expect(frames).toContain('"namespace":"production"')
  // Rien de kube-system : ni pod, ni Service, ni Gateway, ni route.
  expect(frames).not.toContain('kube-system')
  expect(frames).not.toContain('"kind":"node"')
  // Les PV sont cluster-scoped : sans droit de les lister, bob n'en reçoit aucun.
  expect(frames).not.toContain('"persistentVolumes"')
  expect(frames).not.toContain('"kind":"persistentVolume"')
  // La HTTPRoute de production (rôle agrégé atlas-dev-gateway-view), oui.
  expect(frames).toContain('"source":"HTTPRoute"')
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

/** Snapshot du flux ; échoue vite (au lieu de rester bloqué) si Atlas redémarre. */
async function snapshotPods(page: Page): Promise<SnapPod[]> {
  return page.evaluate(() => new Promise<SnapPod[]>((resolve, reject) => {
    const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/stream`)
    const timer = setTimeout(() => { ws.close(); reject(new Error('snapshot : délai dépassé')) }, 5000)
    ws.onmessage = (e) => {
      const m = JSON.parse(e.data)
      if (m.type === 'snapshot') { clearTimeout(timer); ws.close(); resolve(m.pods) }
    }
    ws.onerror = () => { clearTimeout(timer); reject(new Error('snapshot : connexion impossible')) }
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

  // Les événements expirent après 1 h : table ou liste vide, mais jamais d'erreur.
  await panel.getByRole('tab', { name: 'Événements' }).click()
  await expect(panel.getByTestId('events').or(panel.getByText('Aucun événement récent pour ce pod.'))).toBeVisible()
  await expect(panel.locator('.p-body .s-err')).toHaveCount(0)
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

// --- Terminal et actions sur le vrai cluster (critères du jalon 6) ---

test('bob : terminal réel (sh), redimensionnement, Ctrl+C, Tab, flèches, exit', async ({ page }) => {
  await login(page, 'bob@example.com')
  const pods = await snapshotPods(page)
  const orders = pods.find((p) => p.namespace === 'staging' && p.owner.name === 'orders-service' && p.displayStatus === 'Running')!
  await page.goto(`/pods/staging/${orders.name}`)
  const panel = page.locator('aside.panel')
  await panel.getByRole('tab', { name: 'Terminal' }).click()
  const term = panel.getByTestId('terminal')
  await expect(term).toContainText('/ #', { timeout: 15_000 }) // invite de busybox sh (root)
  await term.click()

  await page.keyboard.type('echo hello-$((40+2))')
  await page.keyboard.press('Enter')
  await expect(term).toContainText('hello-42')

  // Redimensionnement : la taille du TTY vue dans le pod suit la fenêtre.
  // Sortie balisée (SZ:colonnes lignes) pour ne pas confondre avec d'autres nombres.
  const ttysize = async (n: number) => {
    await page.keyboard.type(`echo SZ${n}:$(ttysize)`)
    await page.keyboard.press('Enter')
    await expect(term).toContainText(new RegExp(`SZ${n}:\\d+ \\d+`))
    return (await term.innerText()).match(new RegExp(`SZ${n}:(\\d+ \\d+)`))![1]
  }
  const before = await ttysize(1)
  // Le panneau est plafonné en largeur : c'est la hauteur de la fenêtre qui change le nombre de lignes.
  await page.setViewportSize({ width: 1440, height: 650 })
  await page.waitForTimeout(800)
  const after = await ttysize(2)
  expect(before).not.toBe('0 0')
  expect(after).not.toBe(before)

  await page.keyboard.type('sleep 100')
  await page.keyboard.press('Enter')
  await page.keyboard.press('Control+C')
  await page.keyboard.type('echo apres-interruption')
  await page.keyboard.press('Enter')
  await expect(term).toContainText('apres-interruption')

  await page.keyboard.type('ech') // complétion par Tab
  await page.keyboard.press('Tab')
  await page.keyboard.type(' tab-ok')
  await page.keyboard.press('Enter')
  await expect(term).toContainText('tab-ok')
  await page.keyboard.press('ArrowUp') // historique : rejoue « echo tab-ok »
  await page.keyboard.press('Enter')
  await expect.poll(async () => ((await term.innerText()).match(/tab-ok/g) ?? []).length).toBeGreaterThanOrEqual(4)

  await page.keyboard.type('exit')
  await page.keyboard.press('Enter')
  await expect(term).toContainText('Session terminée')
})

test('alice (view) : actions et terminal désactivés avec explication, appels forcés refusés (403)', async ({ page }) => {
  await login(page, 'alice@example.com')
  const pods = await snapshotPods(page)
  const api = pods.find((p) => p.owner.name === 'api-gateway' && p.namespace === 'production')!
  await page.goto(`/pods/production/${api.name}`)
  const panel = page.locator('aside.panel')
  const del = panel.getByRole('button', { name: 'Supprimer le pod' })
  await expect(del).toBeDisabled()
  await expect(del).toHaveAttribute('title', "Vous n'avez pas le droit delete sur pods dans production")
  await expect(panel.getByRole('button', { name: 'Rollout restart' })).toBeDisabled()
  await panel.getByRole('tab', { name: 'Terminal' }).click()
  await expect(panel.getByText("Vous n'avez pas le droit create sur pods/exec dans production.")).toBeVisible()

  const forced = await page.request.delete(`/api/namespaces/production/pods/${api.name}`, { headers: { 'X-Atlas-Request': '1' } })
  expect(forced.status()).toBe(403)
  expect((await forced.json()).error).toContain('cannot delete resource "pods"')
  const exec = await page.evaluate((name) => new Promise<{ type: string; message: string }>((resolve) => {
    const ws = new WebSocket(`ws://${location.host}/api/namespaces/production/pods/${name}/exec?container=api-gateway`)
    ws.onmessage = (e) => { if (typeof e.data === 'string') resolve(JSON.parse(e.data)) }
  }), api.name)
  expect(exec.type).toBe('error')
  expect(exec.message).toMatch(/forbidden|cannot create resource "pods\/exec"/)
})

test('carol : le drain respecte le PDB et ignore le DaemonSet, les pods repartent ailleurs', async ({ page }) => {
  await login(page, 'carol@example.com')
  // Un worker qui porte un pod api-gateway (protégé par le PDB) et d'autres pods.
  const pods = await snapshotPods(page) as (SnapPod & { nodeName?: string })[]
  const apiNode = pods.find((p) => p.owner.name === 'api-gateway')!.nodeName!
  const short = apiNode.split('-').pop()!
  // Pods censés repartir ailleurs (les Jobs du CronJob changent de nom à chaque exécution).
  const movable = pods.filter((p) => p.nodeName === apiNode && p.owner.name !== 'api-gateway' &&
    !['DaemonSet', 'Job', 'Node'].includes(p.owner.kind) && p.displayStatus !== 'Completed')

  test.info().annotations.push({ type: 'node', description: apiNode })
  try {
    await drainAndCheck(page, apiNode, short, movable)
  } finally {
    // Remet le node en service, même si une vérification a échoué.
    await page.request.post(`/api/nodes/${apiNode}/uncordon`, { headers: { 'X-Atlas-Request': '1' } })
  }
})

async function drainAndCheck(page: Page, apiNode: string, short: string, movable: (SnapPod & { nodeName?: string })[]) {
  const panel = page.locator('aside.panel')
  await page.goto(`/nodes/${apiNode}`)
  await panel.getByRole('button', { name: 'Drain' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('PodDisruptionBudgets qui bloqueraient')
  await expect(dialog).toContainText('production/api-gateway')
  await expect(dialog).toContainText('pod de DaemonSet')
  await dialog.getByLabel('Nom court du node').fill(short)
  await dialog.getByRole('button', { name: 'Drainer' }).click()
  // L'API Eviction refuse (429) tant que le PDB l'exige : le drain réessaie jusqu'à 60 s.
  await expect(page.locator('.toast').last()).toContainText(`Drain de ${short}`, { timeout: 90_000 })
  await expect(page.locator('.toast').last()).toContainText('blocked')

  // Les pods évincés réapparaissent sur d'autres nodes.
  await expect.poll(async () => {
    const now = await snapshotPods(page).catch(() => []) as (SnapPod & { nodeName?: string })[]
    return now.length > 0 && movable.every((m) => now.some((p) => p.owner.name === m.owner.name && p.namespace === m.namespace && p.nodeName && p.nodeName !== apiNode))
  }, { timeout: 90_000, intervals: [2000] }).toBe(true)
  await page.screenshot({ path: 'e2e/__screenshots__/auth-drain.png' })

  await page.goto(`/nodes/${apiNode}`)
  await panel.getByRole('button', { name: 'Uncordon' }).click()
  await expect(page.locator('.toast').last()).toContainText(`Node ${short} réactivé`)
}
