/**
 * v2.2 PR3 — useTheme hook 单元测试。
 *
 * 覆盖（PR3 §4.7 用例 3）：
 *   - 默认 mode = 'system'
 *   - setMode('light') 写 localStorage + 移除 .dark class
 *   - setMode('dark') 加 .dark class
 *   - cycleMode 三态循环：system → light → dark → system
 *   - matchMedia 变化时 resolved 跟随（system 模式）
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useTheme } from './useTheme';

const STORAGE_KEY = 'ic.theme';

function clearStorage() {
  try {
    localStorage.clear();
  } catch {
    /* ignore */
  }
  document.documentElement.classList.remove('dark');
}

describe('useTheme', () => {
  beforeEach(() => {
    clearStorage();
  });
  afterEach(() => {
    clearStorage();
  });

  it('默认 mode = system，resolved = systemPref（默认 light）', () => {
    const { result } = renderHook(() => useTheme());
    expect(result.current.mode).toBe('system');
    expect(result.current.resolved).toBe('light');
    // 默认不写入 .dark class
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });

  it('setMode("dark")：写 localStorage + 加 .dark class + resolved=dark', () => {
    const { result } = renderHook(() => useTheme());
    act(() => result.current.setMode('dark'));
    expect(localStorage.getItem(STORAGE_KEY)).toBe('dark');
    expect(result.current.resolved).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
  });

  it('setMode("light")：移除 .dark class + resolved=light', () => {
    const { result } = renderHook(() => useTheme());
    act(() => result.current.setMode('dark'));
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    act(() => result.current.setMode('light'));
    expect(localStorage.getItem(STORAGE_KEY)).toBe('light');
    expect(result.current.resolved).toBe('light');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });

  it('cycleMode：system → light → dark → system 循环', () => {
    const { result } = renderHook(() => useTheme());
    expect(result.current.mode).toBe('system');
    act(() => result.current.cycleMode());
    expect(result.current.mode).toBe('light');
    act(() => result.current.cycleMode());
    expect(result.current.mode).toBe('dark');
    act(() => result.current.cycleMode());
    expect(result.current.mode).toBe('system');
  });

  it('挂载时读取已存在的 localStorage', () => {
    localStorage.setItem(STORAGE_KEY, 'dark');
    const { result } = renderHook(() => useTheme());
    expect(result.current.mode).toBe('dark');
    expect(result.current.resolved).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
  });

  it('matchMedia 触发 prefers-color-scheme 变化时（system 模式）resolved 跟随', () => {
    let listener: ((e: { matches: boolean }) => void) | null = null;
    const mq = {
      matches: false,
      media: '(prefers-color-scheme: dark)',
      addEventListener: vi.fn((_t: string, l: (e: { matches: boolean }) => void) => {
        listener = l;
      }),
      removeEventListener: vi.fn(),
    };
    // 替换全局 matchMedia（useTheme 内部读取 window.matchMedia）
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn(() => mq),
    });

    const { result } = renderHook(() => useTheme());
    expect(result.current.mode).toBe('system');
    expect(result.current.resolved).toBe('light');

    // 模拟 OS 切到 dark
    act(() => {
      listener?.({ matches: true });
    });
    expect(result.current.resolved).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
  });

  it('setMode 写入失败（Safari 隐私）时静默不抛错', () => {
    const { result } = renderHook(() => useTheme());
    const original = Storage.prototype.setItem;
    Storage.prototype.setItem = vi.fn(() => {
      throw new Error('QuotaExceeded');
    });
    try {
      expect(() => act(() => result.current.setMode('dark'))).not.toThrow();
      // state 仍然更新
      expect(result.current.mode).toBe('dark');
    } finally {
      Storage.prototype.setItem = original;
    }
  });
});
