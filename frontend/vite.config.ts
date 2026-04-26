import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/philosophers': 'http://localhost:8000',
      '/debates': 'http://localhost:8000',
    },
  },
})
