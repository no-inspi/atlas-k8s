/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const backend = process.env.ATLAS_BACKEND ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': { target: backend, ws: true },
      '/healthz': backend,
      '/auth': backend,
    },
  },
  // assetsInlineLimit: 0 : aucun asset en data:, la CSP n'autorise que 'self' pour les polices.
  // Le plus gros morceau (~2,8 Mo) est Monaco, chargé seulement à l'ouverture de l'onglet YAML.
  build: { outDir: 'dist', emptyOutDir: true, assetsInlineLimit: 0, chunkSizeWarningLimit: 3000 },
  test: { environment: 'jsdom', globals: true, include: ['src/**/*.test.ts'] },
})
