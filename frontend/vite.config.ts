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
    // v2.2 PR3：emptyOutDir 关闭，避免 sandbox 安全删除拦截；dist 内容由 npm script 在 build 前清理。
    emptyOutDir: false,
    sourcemap: mode !== 'production',
    // v2.2 PR4：manualChunks 拆包，确保主 bundle < 800KB（PR3=695KB + Markdown libs 增量）。
    //   - react-vendor：React 运行时，几乎所有页面共享。
    //   - markdown：Markdown 渲染栈（react-markdown + remark/rehype 插件 + highlight.js + dompurify）。
    //     集中到独立 chunk 后可在长缓存命中且不影响主 bundle 大小。
    rollupOptions: {
      output: {
        manualChunks: {
          'react-vendor': ['react', 'react-dom', 'react-router-dom'],
          markdown: [
            'react-markdown',
            'remark-gfm',
            'rehype-highlight',
            'rehype-sanitize',
            'highlight.js',
            'dompurify',
          ],
        },
      },
    },
  },
}));
