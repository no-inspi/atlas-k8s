import { expect, test } from '@playwright/test'
import { waitForPod } from './helpers'

// Recherche, vue Liste au clavier, choix du thème (mode démo).

test('recherche (/) : ouvre le pod et suit le lien profond', async ({ page }) => {
  const api = await waitForPod(page, (p) => p.owner.name === 'api-gateway' && p.namespace === 'production' && p.displayStatus === 'Running')
  await page.goto('/')
  await page.locator('canvas').waitFor()
  await page.keyboard.press('/')
  const box = page.getByRole('combobox', { name: /Rechercher/ })
  await expect(box).toBeFocused()
  await box.fill(api.name.slice(0, 22))
  await expect(page.getByRole('option').first()).toContainText(api.name)
  await page.keyboard.press('Enter')
  await expect(page.locator('aside.panel')).toContainText(api.name)
  await expect(page).toHaveURL(new RegExp(`/pods/production/${api.name}$`))
})

test('vue Liste : arbre accessible au clavier, même inspecteur', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: 'Liste' }).click()
  const tree = page.getByRole('tree', { name: 'Cluster' })
  await expect(tree).toBeVisible()
  const first = tree.getByRole('treeitem').first()
  await expect(first).toHaveAttribute('aria-expanded', 'true')
  await first.focus()
  // Namespaces › premier namespace › premier workload › premier pod.
  for (const key of ['ArrowDown', 'ArrowRight', 'ArrowDown', 'ArrowRight', 'ArrowDown']) await page.keyboard.press(key)
  const focused = page.locator('.tree-item:focus')
  await expect(focused).toHaveAttribute('aria-level', '4')
  const podName = (await focused.locator('.nm').textContent())!
  await page.keyboard.press('Enter')
  await expect(page.locator('aside.panel')).toContainText(podName)
  await expect(focused).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('button', { name: 'Ville 3D' }).click()
  await expect(tree).toHaveCount(0)
})

test('thème : système, clair, sombre, mémorisé', async ({ page }) => {
  await page.goto('/')
  const btn = page.getByRole('button', { name: /Thème : système/ })
  await btn.click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  await page.getByRole('button', { name: /Thème : clair/ }).click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await page.waitForTimeout(1500)
  await page.screenshot({ path: 'e2e/__screenshots__/theme-dark-forced.png' })
  await page.getByRole('button', { name: /Thème : sombre/ }).click()
  await expect(page.locator('html')).not.toHaveAttribute('data-theme', /./)
})
