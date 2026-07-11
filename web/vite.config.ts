import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// Embed target — Vite writes the production bundle straight into the Go
// package that //go:embed's it. dist/ is fully gitignored (no placeholder to
// conflict with). There is no copy step and no content-hashed entry names:
// the binary is the cache key.
const embedOutDir = fileURLToPath(new URL('../internal/webui/dist', import.meta.url))

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
  build: {
    outDir: embedOutDir,
    // Wipe the embed dir on every build so stale files cannot linger next to
    // the stable names (safe: dist/ is gitignored entirely).
    emptyOutDir: true,
    // One JS + one CSS for the admin SPA. Hash-free names keep index.html
    // stable; the Go binary is redeployed as a unit so long-lived asset
    // caching is unnecessary (see internal/webui/embed.go).
    cssCodeSplit: false,
    rollupOptions: {
      output: {
        inlineDynamicImports: true,
        entryFileNames: 'assets/app.js',
        chunkFileNames: 'assets/app.js',
        assetFileNames: (asset) => {
          if (asset.name && asset.name.endsWith('.css')) return 'assets/app.css'
          // favicon.svg and any other static assets keep their basename.
          return 'assets/[name][extname]'
        },
      },
    },
  },
})
