import { defineConfig, devices } from '@playwright/test'

// E2E avec authentification : binaire en mode OIDC contre le cluster kind et
// Dex (make kind-up scenarios, puis make e2e-auth qui lance Dex). Avec
// ATLAS_URL, les mêmes parcours visent une installation in-cluster
// (ATLAS_URL=http://atlas.localtest.me, après make kind-oidc helm-kind-oidc).
const inCluster = process.env.ATLAS_URL

export default defineConfig({
  testDir: 'e2e',
  testMatch: 'auth.spec.ts',
  timeout: 150_000,
  workers: 1,
  use: {
    baseURL: inCluster ?? 'http://localhost:8080',
    launchOptions: { args: ['--use-angle=swiftshader', '--enable-unsafe-swiftshader'] },
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } }],
  webServer: inCluster
    ? undefined
    : { command: 'cd .. && make run-dev', url: 'http://localhost:8080/readyz', reuseExistingServer: true, timeout: 120_000 },
})
