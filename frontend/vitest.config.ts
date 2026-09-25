import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import path from 'node:path';

// v2.2 PR3 前端 Vitest 配置。
// - jsdom 环境（UI 组件需要 DOM API）
// - alias @ → src/（与 vite.config 一致，便于测试引用）
// - setup.ts 加载 jest-dom matcher（toBeInTheDocument 等）
// - 覆盖：src 下所有 .test / .spec 文件

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}', 'src/**/*.spec.{ts,tsx}'],
    exclude: ['node_modules', 'dist'],
    css: false,
    reporters: ['default'],
    cache: false,
    // 单 fork 顺序执行（避免 sandbox EPERM 问题）
    pool: 'forks',
    poolOptions: {
      forks: { singleFork: true },
    },
  },
});
