import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/models': 'http://localhost:8000',
      '/philosophers': 'http://localhost:8000',
      '/debates': 'http://localhost:8000',
    },
  },
})
