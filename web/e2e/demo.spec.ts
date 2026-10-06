import { expect, test, type ConsoleMessage, type Page } from '@playwright/test'

function collectProblems(page: Page): string[] {
  const problems: string[] = []
  page.on('console', (m: ConsoleMessage) => {
    if (m.type() === 'error' || /Content Security Policy/i.test(m.text())) problems.push(m.text())
  })
  page.on('pageerror', (e) => problems.push(e.message))
  return problems
}

test('la ville simulée se charge sans erreur ni violation CSP', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/')
  await expect(page.locator('canvas')).toBeVisible()
  await expect(page.getByText('Données simulées')).toBeVisible()
  await expect(page.getByText('gke-prod-europe-west1')).toBeVisible()

  const running = page.getByTestId('pods-running')
  await expect(running).toHaveText(/^\d+\/\d+$/)
  await expect.poll(async () => Number((await running.textContent())!.split('/')[1])).toBeGreaterThan(20)

  await page.waitForTimeout(2500) // laisse le stream et les animations tourner
  await page.screenshot({ path: 'e2e/__screenshots__/demo-light.png' })
  expect(problems).toEqual([])
})

test('les chips de namespace filtrent la vue', async ({ page }) => {
  await page.goto('/')
  const chip = page.getByRole('button', { name: 'production' })
  await expect(chip).toBeVisible()
  await chip.click()
  await expect(chip).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByTestId('ns-stat')).toContainText(/\d+ pods sur \d+ nodes/)
  await chip.click()
  await expect(page.getByTestId('ns-stat')).toHaveCount(0)
})

test('un clic dans la ville ouvre l’inspecteur, qui navigue entre node et pod', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/')
  await expect(page.getByTestId('pods-running')).toHaveText(/\d+\/\d+/)
  await page.waitForTimeout(1500)
  // Le centre de l'écran tombe sur la ville : on balaie une grille jusqu'à toucher un objet.
  const box = (await page.locator('canvas').boundingBox())!
  const panel = page.locator('aside.panel')
  let opened = false
  for (let dy = -0.25; dy <= 0.25 && !opened; dy += 0.05)
    for (let dx = -0.3; dx <= 0.3 && !opened; dx += 0.05) {
      await page.mouse.click(box.x + box.width * (0.5 + dx), box.y + box.height * (0.5 + dy))
      opened = await panel.evaluate((el) => el.classList.contains('open'))
    }
  expect(opened).toBe(true)
  await expect(panel.getByText(/^(Pod|Node) · /)).toBeVisible()
  await page.waitForTimeout(1500) // fin de la transition du panneau (lente en WebGL logiciel)
  await page.screenshot({ path: 'e2e/__screenshots__/demo-inspector.png' })

  // Depuis un node, la liste de ses pods mène à l'aperçu d'un pod (ou l'inverse).
  if (await panel.getByText(/^Node · /).isVisible()) {
    await panel.locator('.podlist button').first().click()
    await expect(panel.getByText(/^Pod · /)).toBeVisible()
  }
  await expect(panel.getByRole('heading', { name: 'Containers' })).toBeVisible()
  await expect(panel.getByRole('heading', { name: 'Ressources' })).toBeVisible()
  await panel.getByRole('button', { name: /^gke-prod-/ }).click()
  await expect(panel.getByText(/^Node · /)).toBeVisible()

  await page.keyboard.press('Escape')
  await expect(panel).not.toHaveClass(/open/)
  expect(problems).toEqual([])
})

test('thème sombre', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.goto('/')
  await expect(page.getByTestId('pods-running')).toHaveText(/\d+\/\d+/)
  await page.waitForTimeout(2500)
  await page.screenshot({ path: 'e2e/__screenshots__/demo-dark.png' })
})
