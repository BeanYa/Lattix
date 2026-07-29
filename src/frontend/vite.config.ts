import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    rolldownOptions: {
      output: {
        codeSplitting: {
          includeDependenciesRecursively: false,
          minSize: 20_000,
          maxSize: 400_000,
          groups: [
            {
              name: 'three',
              test: /node_modules[\\/]three[\\/]/,
              priority: 2,
            },
            {
              name: 'globe',
              test:
                /node_modules[\\/](?:@tweenjs[\\/]tween\.js|accessor-fn|d3-[^\\/]+|data-bind-mapper|float-tooltip|frame-ticker|globe\.gl|h3-js|index-array-by|kapsule|polished|react-globe\.gl|react-kapsule|three-[^\\/]+|tinycolor2)[\\/]/,
              priority: 1,
            },
          ],
        },
      },
    },
  },
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
