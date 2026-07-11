import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    proxy: {
      // Admin API — keep Host/Origin intact so the backend same-origin check passes.
      '/api': {
        target: 'http://127.0.0.1:9090',
        changeOrigin: false,
      },
    },
  },
})
