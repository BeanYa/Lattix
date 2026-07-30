import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

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
      '/api': 'http://localhost:8080',
      '/sub': 'http://localhost:8080',
    },
  },
})
