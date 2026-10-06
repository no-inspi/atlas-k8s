import { expect, test, type Page } from '@playwright/test'

// Parcours d'authentification réel : Dex (hack/dex/config.yaml) et RBAC de
// hack/dev-rbac.yaml. alice (oidc:sre) a « view » partout ; bob (oidc:dev) a
// « edit » dans production et staging seulement.

async function login(page: Page, email: string) {
  await page.goto('/')
  await page.waitForURL(/localhost:5556\/dex/)
  await page.locator('#login').fill(email)
  await page.locator('#password').fill('password')
  await page.locator('#submit-login').click()
  await page.waitForURL('http://localhost:8080/')
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
  await expect(page).toHaveURL(/localhost:5556\/dex/)
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
