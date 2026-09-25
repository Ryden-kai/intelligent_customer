/**
 * v2.2 PR4 — MessageActions 组件测试。
 *
 * 覆盖：
 *   - 渲染：4 个按钮（复制 / 重新生成 / 点赞 / 点踩）
 *   - 复制：调用 clipboard.writeText + 切换 ✓ 状态
 *   - regenerate：触发回调
 *   - 点赞 / 点踩：触发回调 + 切换激活态
 *   - 禁用态：按钮 disabled
 *   - 受控 feedback prop
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';

import { MessageActions } from './MessageActions';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

beforeEach(() => {
  Object.assign(navigator, {
    clipboard: {
      writeText: vi.fn().mockResolvedValue(undefined),
    },
  });
});

describe('MessageActions', () => {
  it('默认渲染 4 个按钮', () => {
    render(<MessageActions content="hello" />);
    expect(screen.getByTestId('msg-action-copy')).toBeDefined();
    expect(screen.getByTestId('msg-action-regenerate')).toBeDefined();
    expect(screen.getByTestId('msg-action-thumb-up')).toBeDefined();
    expect(screen.getByTestId('msg-action-thumb-down')).toBeDefined();
  });

  it('点击复制按钮调用 navigator.clipboard.writeText', async () => {
    const spy = navigator.clipboard.writeText as unknown as ReturnType<typeof vi.fn>;
    render(<MessageActions content="copy me" />);
    fireEvent.click(screen.getByTestId('msg-action-copy'));
    await vi.waitFor(() => {
      expect(spy).toHaveBeenCalledWith('copy me');
    });
  });

  it('复制成功按钮文案变 ✓', async () => {
    render(<MessageActions content="x" />);
    const btn = screen.getByTestId('msg-action-copy');
    fireEvent.click(btn);
    await vi.waitFor(() => {
      expect(btn.getAttribute('aria-label')).toBe('已复制');
    });
  });

  it('regenerate 点击触发 onRegenerate', () => {
    const onRegenerate = vi.fn();
    render(<MessageActions content="x" onRegenerate={onRegenerate} />);
    fireEvent.click(screen.getByTestId('msg-action-regenerate'));
    expect(onRegenerate).toHaveBeenCalledTimes(1);
  });

  it('regenerate 未传回调时按钮 disabled', () => {
    render(<MessageActions content="x" />);
    const btn = screen.getByTestId('msg-action-regenerate') as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
  });

  it('点赞触发 onThumbUp + 切换激活态（aria-pressed=true）', async () => {
    const onUp = vi.fn();
    render(<MessageActions content="x" onThumbUp={onUp} />);
    const btn = screen.getByTestId('msg-action-thumb-up');
    fireEvent.click(btn);
    await vi.waitFor(() => {
      expect(onUp).toHaveBeenCalledTimes(1);
      expect(btn.getAttribute('aria-pressed')).toBe('true');
    });
  });

  it('点踩触发 onThumbDown + 切换激活态', async () => {
    const onDown = vi.fn();
    render(<MessageActions content="x" onThumbDown={onDown} />);
    const btn = screen.getByTestId('msg-action-thumb-down');
    fireEvent.click(btn);
    await vi.waitFor(() => {
      expect(onDown).toHaveBeenCalledTimes(1);
      expect(btn.getAttribute('aria-pressed')).toBe('true');
    });
  });

  it('disabled=true 时按钮全部 disabled', () => {
    render(<MessageActions content="x" disabled />);
    expect((screen.getByTestId('msg-action-copy') as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByTestId('msg-action-regenerate') as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByTestId('msg-action-thumb-up') as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByTestId('msg-action-thumb-down') as HTMLButtonElement).disabled).toBe(true);
  });

  it('受控 feedback=up 时按钮 aria-pressed=true', () => {
    render(<MessageActions content="x" feedback="up" />);
    const btn = screen.getByTestId('msg-action-thumb-up');
    expect(btn.getAttribute('aria-pressed')).toBe('true');
  });

  it('再次点击激活态按钮可取消', async () => {
    const onUp = vi.fn();
    render(<MessageActions content="x" onThumbUp={onUp} />);
    const btn = screen.getByTestId('msg-action-thumb-up');
    fireEvent.click(btn);
    await vi.waitFor(() => {
      expect(btn.getAttribute('aria-pressed')).toBe('true');
    });
    fireEvent.click(btn);
    await vi.waitFor(() => {
      expect(btn.getAttribute('aria-pressed')).toBe('false');
    });
    // 取消时不应再次调 onThumbUp
    expect(onUp).toHaveBeenCalledTimes(1);
  });
});
