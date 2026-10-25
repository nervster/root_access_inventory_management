import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      // Go API port (backend/cmd/api, PORT defaults to 8080)
      '/api': 'http://localhost:8080',
    },
  },
})
