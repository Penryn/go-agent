import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import Components from 'unplugin-vue-components/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'
import { defineConfig } from 'vite'

export default defineConfig({
  base: '/admin/',
  plugins: [vue(), tailwindcss(), Components({ resolvers: [ElementPlusResolver()] })],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    proxy: { '/admin/api': 'http://127.0.0.1:8088' },
  },
  build: {
    outDir: '../internal/app/adminui/dist',
    emptyOutDir: true,
  },
})
