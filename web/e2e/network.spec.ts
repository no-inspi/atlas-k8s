import { expect, test, type Page } from '@playwright/test'

// Réseau et stockage en mode démo : portes nginx et traefik, Service en panne,
// chemin allumé depuis un relais, route cassée, PVC en attente, recherche par hôte.

const panel = (page: Page) => page.locator('aside.panel')
const editorText = (page: Page) =>
  page.getByTestId('yaml-editor').locator('.view-lines').innerText().then((t) => t.replace(/\u00a0/g, ' '))

function collectProblems(page: Page) {
  const problems: string[] = []
  page.on('console', (m) => { if (m.type() === 'error') problems.push(m.text()) })
  page.on('pageerror', (e) => problems.push(e.message))
  return problems
}

test('vue Liste : Entrées, Services et Stockage', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/')
  await page.getByRole('button', { name: 'Liste' }).click()
  const tree = page.getByRole('tree', { name: 'Cluster' })
  for (const group of ['Entrées', 'Services', 'Stockage']) await tree.getByRole('treeitem', { name: new RegExp(`^${group}`) }).click()
  await expect(tree.getByRole('treeitem', { name: /^nginx/ })).toBeVisible()
  await expect(tree.getByRole('treeitem', { name: /^traefik/ })).toBeVisible()
  await expect(tree.getByRole('treeitem', { name: /^standard-rwo/ })).toBeVisible()
  expect(problems).toEqual([])
})

test('un Service en panne, puis le chemin d’un Service sain', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/services/staging/checkout-preview')
  await expect(panel(page).getByText('Service · staging')).toBeVisible()
  await expect(panel(page).getByText('Aucun endpoint ready')).toBeVisible()

  await page.goto('/services/production/api-gateway')
  await expect(panel(page).getByText(/\d+\/\d+ ready/).first()).toBeVisible()
  await expect(panel(page).getByTestId('endpoints').getByRole('button').first()).toBeVisible()
  // api-gateway est atteint par deux portes (nginx et traefik) dans la démo.
  await expect(page.getByTestId('path-summary')).toContainText('2 portes')
  await page.waitForTimeout(1500)
  await page.screenshot({ path: 'e2e/__screenshots__/network-service.png' })

  await panel(page).getByRole('tab', { name: 'YAML' }).click()
  await expect.poll(() => editorText(page), { timeout: 15_000 }).toContain('kind: Service')
  expect(problems).toEqual([])
})

test('porte traefik : une route vers un Service introuvable', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/gates/traefik')
  const p = panel(page)
  await expect(p.getByText("Porte d'entrée")).toBeVisible()
  await p.getByTestId('gate-routes').getByRole('button', { name: /admin/ }).click()
  await expect(p.getByTestId('rules')).toContainText('Service introuvable')
  await expect(page).toHaveURL(/\/routes\/ingressroute\/production\/admin$/)
  expect(problems).toEqual([])
})

test('PVC en attente : la raison est affichée', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/volumes/staging/uploads-preview')
  await expect(panel(page).getByText('PVC · staging')).toBeVisible()
  await expect(panel(page).getByTestId('pvc-why')).toContainText('WaitForFirstConsumer')
  expect(problems).toEqual([])
})

test('recherche d’une route par son hôte', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/')
  await expect(page.getByTestId('pods-running')).toHaveText(/\d+\/\d+/)
  await page.keyboard.press('/')
  await page.getByRole('combobox', { name: /Rechercher/ }).fill('grafana.example')
  await expect(page.getByRole('option').first()).toContainText('grafana')
  await page.keyboard.press('Enter')
  await expect(panel(page).getByText('IngressRoute · monitoring')).toBeVisible()
  await expect(page).toHaveURL(/\/routes\/ingressroute\/monitoring\/grafana$/)
  expect(problems).toEqual([])
})
