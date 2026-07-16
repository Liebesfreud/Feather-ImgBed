import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '')
  const apiTarget = env.FEATHER_API_PROXY || 'http://127.0.0.1:8080'
  return {
    plugins: [vue()],
    build: {
      outDir: '../internal/app/web/dist',
      emptyOutDir: true,
      sourcemap: false,
    },
    server: {
      port: 5173,
      proxy: {
        '/api': apiTarget,
        '/files': apiTarget,
        '/healthz': apiTarget,
      },
    },
  }
})
