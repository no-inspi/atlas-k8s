import { expect, test, type Page } from '@playwright/test'

// Suite du réseau et du stockage en mode démo : Gateways (programmé, non
// programmé), HTTPRoute pondérée, route refusée, TraefikService pondéré avec
// miroir, IngressRouteTCP, PV orphelin, liens profonds et vue Liste.

const panel = (page: Page) => page.locator('aside.panel')
const editorText = (page: Page) =>
  page.getByTestId('yaml-editor').locator('.view-lines').innerText().then((t) => t.replace(/ /g, ' '))

function collectProblems(page: Page) {
  const problems: string[] = []
  page.on('console', (m) => { if (m.type() === 'error') problems.push(m.text()) })
  page.on('pageerror', (e) => problems.push(e.message))
  return problems
}

test('Gateway programmé : listeners, puis sa HTTPRoute pondérée 90/10', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/gateways/infra/public')
  const p = panel(page)
  await expect(p.getByText('Gateway · infra')).toBeVisible()
  // Orange : la route refusée staging/preview s'y rattache.
  await expect(p.locator('.badge.s-warn')).toHaveText('2/2 listeners prêts')
  await expect(p.getByTestId('listeners')).toContainText('HTTPS · 443')
  await expect(p.getByTestId('gateway-routes')).toContainText('Refusée')
  await expect(page.getByTestId('path-summary')).toContainText('1 porte')

  await p.getByTestId('gateway-routes').getByRole('button', { name: /storefront/ }).click()
  await expect(page).toHaveURL(/\/routes\/httproute\/production\/storefront$/)
  await expect(p.getByTestId('parents')).toContainText('infra/public')
  await expect(p.getByTestId('rules')).toContainText('90 %')
  await expect(p.getByTestId('rules')).toContainText('10 %')
  await expect(page.getByTestId('path-summary')).toContainText('Répartition : frontend 90 %, frontend-canary 10 %')
  await page.waitForTimeout(1500) // premier rendu WebGL (logiciel en headless) avant la capture
  await page.screenshot({ path: 'e2e/__screenshots__/gateway-route.png' })

  await p.getByRole('tab', { name: 'YAML' }).click()
  await expect.poll(() => editorText(page), { timeout: 15_000 }).toContain('kind: HTTPRoute')
  expect(problems).toEqual([])
})

test('Gateway non programmé : badge rouge et raison', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/gateways/infra/internal')
  const p = panel(page)
  await expect(p.locator('.badge.s-err')).toHaveText('Non programmé')
  await expect(p.getByTestId('gateway-why')).toContainText('AddressNotAssigned')
  await expect(p.getByTestId('gateway-routes')).toContainText('orders-grpc')
  expect(problems).toEqual([])
})

test('lien /gates/infra%2Fpublic : ouvre le Gateway et réécrit l’URL', async ({ page }) => {
  await page.goto('/gates/infra%2Fpublic')
  await expect(panel(page).getByText('Gateway · infra')).toBeVisible()
  await expect(page).toHaveURL(/\/gateways\/infra\/public$/)
})

test('route refusée par son Gateway', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/routes/httproute/staging/preview')
  const p = panel(page)
  await expect(p.locator('.badge.s-err')).toHaveText('Refusée')
  await expect(p.getByTestId('parents')).toContainText('NotAllowedByListeners')
  await expect(p.getByTestId('rules')).toContainText('Refusée')
  expect(problems).toEqual([])
})

test('IngressRoute via un TraefikService pondéré avec miroir', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/routes/ingressroute/production/checkout')
  const rules = panel(page).getByTestId('rules')
  await expect(rules).toContainText('75 %')
  await expect(rules).toContainText('25 %')
  await expect(rules).toContainText('miroir 10 %')
  await expect(rules).toContainText('production/checkout-split')
  expect(problems).toEqual([])
})

test('IngressRouteTCP : porte traefik et match HostSNI', async ({ page }) => {
  await page.goto('/routes/ingressroutetcp/production/postgres')
  const p = panel(page)
  await expect(p.getByText('IngressRouteTCP · production')).toBeVisible()
  await expect(p.locator('.badge')).toHaveText('Porte traefik')
  await expect(p.getByTestId('rules')).toContainText('HostSNI')
  await expect(p.getByTestId('rules')).toContainText('postgres-payments')
})

test('PV libéré : inspectable, YAML compris', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/persistentvolumes/pv-old-uploads')
  const p = panel(page)
  await expect(p.getByText('PersistentVolume', { exact: true })).toBeVisible()
  await expect(p.locator('.badge')).toHaveText('Released')
  await expect(p.getByTestId('pv-why')).toContainText('Libéré')
  await expect(p).toContainText('staging/old-uploads')
  await p.getByRole('tab', { name: 'YAML' }).click()
  await expect.poll(() => editorText(page), { timeout: 15_000 }).toContain('kind: PersistentVolume')
  expect(problems).toEqual([])
})

test('vue Liste : Gateways dans les entrées, PV dans le stockage', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/')
  await page.getByRole('button', { name: 'Liste' }).click()
  const tree = page.getByRole('tree', { name: 'Cluster' })
  await tree.getByRole('treeitem', { name: /^Entrées/ }).click()
  await expect(tree.getByRole('treeitem', { name: /^infra\/public.*route refusée/ })).toBeVisible()
  await expect(tree.getByRole('treeitem', { name: /^infra\/internal.*non programmé/ })).toBeVisible()
  await tree.getByRole('treeitem', { name: /^Stockage/ }).click()
  await tree.getByRole('treeitem', { name: /^standard-rwo/ }).click()
  await tree.getByRole('treeitem', { name: /^pv-old-uploads/ }).click()
  await expect(panel(page).getByText('PersistentVolume', { exact: true })).toBeVisible()
  await expect(page).toHaveURL(/\/persistentvolumes\/pv-old-uploads$/)
  expect(problems).toEqual([])
})

test('recherche d’un Gateway par son nom court', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByTestId('pods-running')).toHaveText(/\d+\/\d+/)
  await page.keyboard.press('/')
  await page.getByRole('combobox', { name: /Rechercher/ }).fill('internal')
  // Entrée n'agit que sur un résultat affiché : attendre la liste évite la course.
  await expect(page.getByRole('option').first()).toContainText('infra/internal')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/gateways\/infra\/internal$/)
})
