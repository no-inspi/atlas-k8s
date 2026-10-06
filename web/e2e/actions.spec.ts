import { expect, test } from '@playwright/test'
import { snapshot, waitForPod } from './helpers'

// Terminal et actions en mode démo (shell simulé, cluster simulé).

test('terminal : commandes, Ctrl+C, flèches ignorées, exit', async ({ page }) => {
  const api = await waitForPod(page, (p) => p.owner.name === 'api-gateway' && p.displayStatus === 'Running')
  await page.goto(`/pods/production/${api.name}`)
  const panel = page.locator('aside.panel')
  await panel.getByRole('tab', { name: 'Terminal' }).click()
  const term = panel.getByTestId('terminal')
  await expect(term).toContainText('/app $', { timeout: 15_000 })
  await term.click()
  await page.keyboard.type('hostname')
  await page.keyboard.press('Enter')
  await expect(term).toContainText(api.name)
  await page.keyboard.type('sleep 100')
  await page.keyboard.press('Control+C')
  await expect(term).toContainText('^C')
  await page.keyboard.press('ArrowUp')
  await page.keyboard.press('Tab')
  await page.keyboard.type('echo hello-42')
  await page.keyboard.press('Enter')
  await expect(term).toContainText('hello-42')
  await page.keyboard.type('exit')
  await page.keyboard.press('Enter')
  await expect(term).toContainText('Session terminée')
  await expect(panel.getByRole('button', { name: 'Reconnecter' })).toBeVisible()
})

test('terminal refusé pour un pod non démarré', async ({ page }) => {
  const broken = await waitForPod(page, (p) => p.owner.name === 'checkout-preview' && p.displayStatus === 'ImagePullBackOff')
  await page.goto(`/pods/staging/${broken.name}`)
  await page.locator('aside.panel').getByRole('tab', { name: 'Terminal' }).click()
  await expect(page.getByText('Container non démarré : ImagePullBackOff.')).toBeVisible()
})

test('supprimer un pod, scaler, drainer un node', async ({ page }) => {
  const { nodes } = await snapshot(page)
  const panel = page.locator('aside.panel')
  const front = await waitForPod(page, (p) => p.owner.name === 'frontend' && p.displayStatus === 'Running')

  await page.goto(`/pods/production/${front.name}`)
  await panel.getByRole('button', { name: 'Supprimer le pod' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('Son ReplicaSet le recréera')
  await dialog.getByRole('button', { name: 'Supprimer' }).click()
  await expect(page.locator('.toast').first()).toContainText(`Pod ${front.name} supprimé`)
  await expect(panel.getByText('Ce pod a été supprimé.')).toBeVisible({ timeout: 10_000 })

  const other = await waitForPod(page, (p) => p.owner.name === 'grafana' && p.displayStatus === 'Running')
  await page.goto(`/pods/monitoring/${other.name}`)
  await panel.getByRole('button', { name: 'Ajouter un replica' }).click()
  await expect(page.locator('.toast').last()).toContainText('scalé à 2')
  await expect(panel.getByText('scale sera annulé à la prochaine synchronisation')).toBeVisible()
  // Le compteur peut afficher la cible demandée : on attend la confirmation du flux.
  await expect(panel.getByRole('group', { name: /Replicas de/ })).toContainText('2 replicas (2 prêts)', { timeout: 15_000 })
  await panel.getByRole('button', { name: 'Retirer un replica' }).click()
  await expect(page.locator('.toast').last()).toContainText('scalé à 1')
  await expect(panel.getByRole('group', { name: /Replicas de/ })).toContainText('1 replica (1 prêts)', { timeout: 15_000 })
  await panel.getByRole('button', { name: 'Retirer un replica' }).click()
  await expect(page.getByRole('dialog')).toContainText('Tous les pods')
  await page.getByRole('dialog').getByRole('button', { name: 'Annuler' }).click()

  const spot = nodes.find((n) => n.pool === 'spot-pool')!
  const short = spot.name.split('-').pop()!
  await page.goto(`/nodes/${spot.name}`)
  await panel.getByRole('button', { name: 'Drain' }).click()
  const drain = page.getByRole('dialog')
  await expect(drain).toContainText('Pods évincés')
  await expect(drain).toContainText('pod de DaemonSet')
  const confirm = drain.getByRole('button', { name: 'Drainer' })
  await expect(confirm).toBeDisabled()
  await drain.getByLabel('Nom court du node').fill(short)
  await confirm.click()
  await expect(page.locator('.toast').last()).toContainText(`Drain de ${short}`)
  await expect(panel.getByText('SchedulingDisabled')).toBeVisible()
  // Les robots évincés repartent ailleurs : il ne reste que le DaemonSet sur le node.
  await expect.poll(async () => (await panel.locator('.podlist li').count()), { timeout: 15_000 }).toBe(1)
  await page.screenshot({ path: 'e2e/__screenshots__/actions-drain.png' })
  // Remet le node en service pour les tests suivants.
  await panel.getByRole('button', { name: 'Uncordon' }).click()
  await expect(panel.getByText('SchedulingDisabled')).toHaveCount(0)
})
