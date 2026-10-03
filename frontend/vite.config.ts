import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      // API http port from backend/RootAccess.Inventory.Api/Properties/launchSettings.json
      '/api': 'http://localhost:5030',
    },
  },
})
