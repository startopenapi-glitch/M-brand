import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
export default defineConfig(({ mode }) => {
  // Optional development proxy for the developer's own backend only.
  // Set MBRAND_DEV_API_ORIGIN and MBRAND_DEV_API_PREFIX in web/.env.local.
  // No backend address or internal prefix is selected by default.
  const env = loadEnv(mode, process.cwd(), 'MBRAND_')
  const target = env.MBRAND_DEV_API_ORIGIN
  const prefix = (env.MBRAND_DEV_API_PREFIX || '').replace(/\/$/, '')
  return {
    base: '/', plugins: [react(), tailwindcss()],
    server: { proxy: target ? {
      '/api': { target, rewrite: (path: string) => prefix + path },
      '/gen': { target, rewrite: (path: string) => prefix + path },
    } : undefined },
  }
})
