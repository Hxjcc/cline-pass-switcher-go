import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'
import path from 'node:path'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:3123',
      '/v1': 'http://127.0.0.1:3123',
    },
  },
  build: {
    outDir: '../internal/webassets/dist',
    emptyOutDir: true,
  },
})
