/**
 * v2.2 PR4 — useTypewriter hook 测试。
 *
 * 覆盖：
 *   - 初始状态：空 + 未完成
 *   - text 变化后逐步推进
 *   - skip() 立即显示完整 text
 *   - reset() 清空
 *   - disabled 模式：直接显示完整
 *   - text 缩短：立刻清空
 *   - 完成后不再 tick
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';

import { useTypewriter } from './useTypewriter';

afterEach(() => {
  vi.useRealTimers();
});

describe('useTypewriter', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  it('初始：空 + done=false', () => {
    const { result } = renderHook(() => useTypewriter('hello', { speedMs: 20 }));
    expect(result.current.displayed).toBe('');
    expect(result.current.done).toBe(false);
  });

  it('text 变化后逐步推进（speedMs=20）', () => {
    const { result, rerender } = renderHook(
      ({ text }) => useTypewriter(text, { speedMs: 20 }),
      { initialProps: { text: '' } },
    );
    rerender({ text: 'hello' });
    // 初始空
    expect(result.current.displayed).toBe('');
    // 推足够长时间让所有 char 写完
    act(() => {
      vi.advanceTimersByTime(20 * 10);
    });
    expect(result.current.displayed.length).toBe(5);
  });

  it('到达 text 长度后 done=true', () => {
    const { result, rerender } = renderHook(
      ({ text }) => useTypewriter(text, { speedMs: 20 }),
      { initialProps: { text: '' } },
    );
    rerender({ text: 'hi' });
    act(() => {
      vi.advanceTimersByTime(20 * 10);
    });
    expect(result.current.displayed).toBe('hi');
    expect(result.current.done).toBe(true);
  });

  it('skip() 立即显示完整 text 并 done=true', () => {
    const { result, rerender } = renderHook(
      ({ text }) => useTypewriter(text, { speedMs: 20 }),
      { initialProps: { text: '' } },
    );
    rerender({ text: 'world' });
    act(() => {
      result.current.skip();
    });
    expect(result.current.displayed).toBe('world');
    expect(result.current.done).toBe(true);
  });

  it('reset() 清空 + done=false', () => {
    const { result, rerender } = renderHook(
      ({ text }) => useTypewriter(text, { speedMs: 20 }),
      { initialProps: { text: '' } },
    );
    rerender({ text: 'abc' });
    act(() => {
      vi.advanceTimersByTime(20 * 10);
    });
    expect(result.current.displayed).toBe('abc');
    act(() => {
      result.current.reset();
    });
    expect(result.current.displayed).toBe('');
    expect(result.current.done).toBe(false);
  });

  it('disabled=true 直接显示完整', () => {
    const { result } = renderHook(() =>
      useTypewriter('immediate', { disabled: true }),
    );
    expect(result.current.displayed).toBe('immediate');
    expect(result.current.done).toBe(true);
  });

  it('text 缩短立刻清空', () => {
    const { result, rerender } = renderHook(
      ({ text }) => useTypewriter(text, { speedMs: 20 }),
      { initialProps: { text: 'long text here' } },
    );
    act(() => {
      vi.advanceTimersByTime(20 * 30);
    });
    expect(result.current.displayed.length).toBeGreaterThan(0);
    // text 缩短
    rerender({ text: 'x' });
    act(() => {
      vi.advanceTimersByTime(20 * 5);
    });
    expect(result.current.displayed).toBe('x');
    expect(result.current.done).toBe(true);
  });

  it('完成后再改变 text（更长）会重新开始打字', () => {
    const { result, rerender } = renderHook(
      ({ text }) => useTypewriter(text, { speedMs: 20 }),
      { initialProps: { text: '' } },
    );
    rerender({ text: 'abc' });
    act(() => {
      vi.advanceTimersByTime(20 * 10);
    });
    expect(result.current.displayed).toBe('abc');
    expect(result.current.done).toBe(true);
    // 变长：重新推进
    rerender({ text: 'abcdef' });
    act(() => {
      vi.advanceTimersByTime(20 * 10);
    });
    expect(result.current.displayed).toBe('abcdef');
    expect(result.current.done).toBe(true);
  });

  it('空 text 不抛错', () => {
    const { result } = renderHook(() => useTypewriter('', { speedMs: 20 }));
    act(() => {
      vi.advanceTimersByTime(20 * 10);
    });
    expect(result.current.displayed).toBe('');
    expect(result.current.done).toBe(true);
  });
});
