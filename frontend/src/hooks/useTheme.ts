/**
 * v2.2 PR3 — useTheme hook。
 *
 * 职责：
 *   - 监听 localStorage `ic.theme`（值：`system` | `light` | `dark`）。
 *   - 监听 OS prefers-color-scheme 变化（仅 system 模式响应）。
 *   - 把 resolved 主题写入 `<html>.classList`（添加或移除 `.dark`）。
 *   - 提供 setTheme / cycleTheme 接口。
 *
 * 与 PR1 pre-paint script 配合：
 *   - PR1 的 index.html 同步脚本已把 .dark class 写到 <html>（防 FOUC）。
 *   - 本 hook 在组件 mount 后接管维护，确保用户切换时实时响应。
 *
 * SSR 兼容：本 hook 不假设 window 存在；测试用 happy-dom / jsdom。
 */

import { useCallback, useEffect, useState } from 'react';

export type ThemeMode = 'system' | 'light' | 'dark';
export type ResolvedTheme = 'light' | 'dark';

const STORAGE_KEY = 'ic.theme';
const DARK_CLASS = 'dark';

function readStoredTheme(): ThemeMode {
  if (typeof window === 'undefined') return 'system';
  try {
    const v = window.localStorage.getItem(STORAGE_KEY);
    if (v === 'light' || v === 'dark' || v === 'system') return v;
  } catch {
    /* Safari private 模式：忽略 */
  }
  return 'system';
}

function readSystemPref(): ResolvedTheme {
  if (typeof window === 'undefined' || !window.matchMedia) return 'light';
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function applyDarkClass(resolved: ResolvedTheme) {
  if (typeof document === 'undefined') return;
  const root = document.documentElement;
  if (resolved === 'dark') {
    root.classList.add(DARK_CLASS);
  } else {
    root.classList.remove(DARK_CLASS);
  }
  // 设置 meta theme-color（移动浏览器顶栏颜色）
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) {
    meta.setAttribute('content', resolved === 'dark' ? '#0f172a' : '#ffffff');
  }
}

export interface UseThemeReturn {
  /** 用户选择的模式（含 system） */
  mode: ThemeMode;
  /** 实际生效的亮 / 暗（system 模式下跟随 OS） */
  resolved: ResolvedTheme;
  /** 写入用户偏好并立刻生效 */
  setMode: (mode: ThemeMode) => void;
  /** 三态循环：system → light → dark → system */
  cycleMode: () => void;
}

export function useTheme(): UseThemeReturn {
  const [mode, setModeState] = useState<ThemeMode>(() => readStoredTheme());
  const [systemPref, setSystemPref] = useState<ResolvedTheme>(() => readSystemPref());

  // 监听 OS prefers-color-scheme（system 模式下使用）。
  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return;
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const handler = (e: MediaQueryListEvent) => {
      setSystemPref(e.matches ? 'dark' : 'light');
    };
    // 新 API
    if (mq.addEventListener) {
      mq.addEventListener('change', handler);
      return () => mq.removeEventListener('change', handler);
    }
    // 兼容旧 Safari
    mq.addListener(handler);
    return () => mq.removeListener(handler);
  }, []);

  // 计算 resolved 主题 + 应用 class
  const resolved: ResolvedTheme = mode === 'system' ? systemPref : mode;

  useEffect(() => {
    applyDarkClass(resolved);
  }, [resolved]);

  const setMode = useCallback((m: ThemeMode) => {
    setModeState(m);
    if (typeof window !== 'undefined') {
      try {
        window.localStorage.setItem(STORAGE_KEY, m);
      } catch {
        /* ignore */
      }
    }
  }, []);

  const cycleMode = useCallback(() => {
    const order: ThemeMode[] = ['system', 'light', 'dark'];
    const idx = order.indexOf(mode);
    const next = order[(idx + 1) % order.length];
    setMode(next);
  }, [mode, setMode]);

  return { mode, resolved, setMode, cycleMode };
}

export default useTheme;
