import { defineConfig, devices } from '@playwright/test'

// E2E en mode démo : le binaire compilé (make build) sert le front et le simulateur.
export default defineConfig({
  testDir: 'e2e',
  testIgnore: 'auth.spec.ts',
  timeout: 30_000,
  use: {
    baseURL: 'http://localhost:18080',
    // WebGL logiciel en headless.
    launchOptions: { args: ['--use-angle=swiftshader', '--enable-unsafe-swiftshader'] },
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } }],
  webServer: {
    command: '../bin/atlas --demo --addr :18080',
    url: 'http://localhost:18080/readyz',
    reuseExistingServer: false,
  },
})
