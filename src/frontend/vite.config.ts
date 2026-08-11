import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

const apiProxyTarget = process.env.VITE_API_PROXY_TARGET || 'http://localhost:8080'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: [
      { find: '@', replacement: path.resolve(__dirname, './src') },
      { find: /^three$/, replacement: path.resolve(__dirname, './node_modules/three/src/Three.js') },
      { find: 'three/webgpu', replacement: path.resolve(__dirname, './node_modules/three/src/Three.WebGPU.js') },
      { find: 'three/tsl', replacement: path.resolve(__dirname, './node_modules/three/src/nodes/TSL.js') },
    ],
  },
  server: {
    proxy: {
      // The backend validates Origin against Host. Preserve the browser-facing
      // host during development so login has the same semantics as production.
      '/api': {
        target: apiProxyTarget,
        changeOrigin: false,
      },
      '/sub/': {
        target: apiProxyTarget,
        changeOrigin: false,
        bypass(request) {
          const url = new URL(request.url ?? '/', 'http://vite.local')
          const acceptsHTML = request.headers.accept?.includes('text/html')
          const isLandingPage = /^\/sub\/[^/]+\/?$/.test(url.pathname)
          if (acceptsHTML && isLandingPage && !url.searchParams.has('format')) {
            return '/index.html'
          }
        },
      },
    },
  },
})
