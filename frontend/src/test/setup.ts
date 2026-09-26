/**
 * v2.2 PR3 — Vitest 测试 setup。
 *
 * - 加载 @testing-library/jest-dom（toBeInTheDocument / toHaveTextContent 等）
 * - 抑制 React 18 在 act() 警告之外的重复错误日志
 * - 清理 matchMedia polyfill（部分组件依赖 window.matchMedia）
 */

import '@testing-library/jest-dom/vitest';
import { afterEach, beforeAll, vi } from 'vitest';
import { cleanup } from '@testing-library/react';

// 每个用例后清理 React Testing Library 渲染残留。
afterEach(() => {
  cleanup();
});

// window.matchMedia polyfill（dark mode / useTheme 测试需要）。
// jsdom 24+ 提供默认 matchMedia 但接口不完整，强制覆盖。
if (typeof window !== 'undefined') {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

// localStorage polyfill（jsdom 自带但兜底写一份）。
if (typeof window !== 'undefined' && !window.localStorage) {
  const store = new Map<string, string>();
  Object.defineProperty(window, 'localStorage', {
    value: {
      getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
      clear: () => store.clear(),
      key: (i: number) => Array.from(store.keys())[i] ?? null,
      get length() {
        return store.size;
      },
    },
  });
}

// 抑制 React DOM 18 在测试中的 propTypes / act() 噪音。
const originalError = console.error;
beforeAll(() => {
  console.error = (...args: unknown[]) => {
    const msg = typeof args[0] === 'string' ? args[0] : '';
    if (msg.includes('not wrapped in act(')) return;
    if (msg.includes('Warning: ReactDOM.render')) return;
    originalError(...(args as Parameters<typeof console.error>));
  };
});

// ResizeObserver polyfill — recharts ResponsiveContainer needs it but jsdom
// doesn't provide one. Stubbing observe/unobserve is enough for tests
// (no actual layout measurements are required).
if (typeof window !== 'undefined' && typeof (window as any).ResizeObserver === 'undefined') {
  (window as any).ResizeObserver = class {
    observe() { /* no-op */ }
    unobserve() { /* no-op */ }
    disconnect() { /* no-op */ }
  };
}

// EventSource polyfill — useSSE hook creates an EventSource when available.
// Provide a minimal stub so tests don't open real connections.
if (typeof (globalThis as any).EventSource === 'undefined') {
  (globalThis as any).EventSource = class {
    url: string;
    onmessage: ((ev: MessageEvent) => void) | null = null;
    onerror: ((ev: Event) => void) | null = null;
    constructor(url: string) {
      this.url = url;
    }
    close() { /* no-op */ }
  };
}
