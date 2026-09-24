import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// All API endpoints are reached through the configured base URL. In
// development we proxy /api/* to the local Go backend; in production the
// same path is resolved against VITE_API_BASE_URL (an absolute URL on
// Cloudflare Pages, e.g. https://api.your-domain.com).
const devProxy = {
  '/api': {
    target: process.env.VITE_API_PROXY_TARGET || 'http://localhost:8080',
    changeOrigin: true,
  },
};

export default defineConfig(({ mode }) => ({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: devProxy,
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: mode !== 'production',
  },
}));
