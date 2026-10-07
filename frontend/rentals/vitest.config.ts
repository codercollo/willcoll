import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath } from 'node:url'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '~': fileURLToPath(new URL('./app', import.meta.url)),
      // Same #base alias the base layer declares for Nuxt (layers/base/nuxt.config.ts).
      '#base': fileURLToPath(new URL('../layers/base/app', import.meta.url)),
    },
  },
  test: { environment: 'happy-dom', include: ['tests/**/*.test.ts'] },
})
