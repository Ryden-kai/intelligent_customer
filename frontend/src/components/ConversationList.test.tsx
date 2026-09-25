/**
 * v2.2 PR4 — ConversationList 组件测试。
 *
 * 覆盖：
 *   - 渲染：新建按钮 + 空态
 *   - 列出已有会话
 *   - 点击切换 currentId
 *   - hover 删除（confirm mock）
 *   - 当前会话高亮
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';

import { ConversationList } from './ConversationList';
import { CONVERSATIONS_KEY } from '../hooks/useConversations';

beforeEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});

afterEach(() => cleanup());

describe('ConversationList', () => {
  it('空态：渲染新建按钮 + 提示文案', () => {
    render(<ConversationList />);
    expect(screen.getByTestId('conversation-new')).toBeDefined();
    expect(screen.getByText(/暂无历史会话/)).toBeDefined();
  });

  it('渲染已有会话 + 显示标题', () => {
    const seed = [
      {
        id: 'c-1',
        title: 'first question',
        preview: 'first preview',
        messages: [],
        createdAt: Date.now() - 2000,
        updatedAt: Date.now() - 1000,
      },
      {
        id: 'c-2',
        title: 'second question',
        preview: 'second preview',
        messages: [],
        createdAt: Date.now() - 4000,
        updatedAt: Date.now() - 500,
      },
    ];
    localStorage.setItem(CONVERSATIONS_KEY, JSON.stringify(seed));
    render(<ConversationList />);
    expect(screen.getByTestId('conversation-row-c-1')).toBeDefined();
    expect(screen.getByTestId('conversation-row-c-2')).toBeDefined();
    expect(screen.getByText('first question')).toBeDefined();
    expect(screen.getByText('second question')).toBeDefined();
  });

  it('点击新建按钮触发 createNew（localStorage 写入新会话）', () => {
    render(<ConversationList />);
    fireEvent.click(screen.getByTestId('conversation-new'));
    const raw = localStorage.getItem(CONVERSATIONS_KEY);
    expect(raw).not.toBeNull();
    const parsed = JSON.parse(raw!);
    expect(Array.isArray(parsed)).toBe(true);
    expect(parsed.length).toBe(1);
  });

  it('点击会话触发 onSelect 回调', () => {
    const seed = [
      {
        id: 'c-1',
        title: 'first',
        preview: 'preview',
        messages: [],
        createdAt: Date.now(),
        updatedAt: Date.now(),
      },
    ];
    localStorage.setItem(CONVERSATIONS_KEY, JSON.stringify(seed));
    const onSelect = vi.fn();
    render(<ConversationList onSelect={onSelect} />);
    // 点击行内的主按钮（li 上 data-testid，按钮是其后代）
    const row = screen.getByTestId('conversation-row-c-1');
    const selectBtn = row.querySelector('button:not([aria-label*="删除"])') as HTMLElement;
    fireEvent.click(selectBtn);
    expect(onSelect).toHaveBeenCalledWith('c-1');
  });

  it('删除按钮（confirm=true 时从 localStorage 删除）', () => {
    const seed = [
      {
        id: 'c-1',
        title: 'first',
        preview: 'preview',
        messages: [],
        createdAt: Date.now(),
        updatedAt: Date.now(),
      },
    ];
    localStorage.setItem(CONVERSATIONS_KEY, JSON.stringify(seed));
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    render(<ConversationList />);
    const row = screen.getByTestId('conversation-row-c-1');
    const deleteBtn = row.querySelector('button[aria-label*="删除"]') as HTMLElement;
    fireEvent.click(deleteBtn);
    const after = JSON.parse(localStorage.getItem(CONVERSATIONS_KEY)!);
    expect(after.length).toBe(0);
  });

  it('删除按钮（confirm=false 时不删）', () => {
    const seed = [
      {
        id: 'c-1',
        title: 'first',
        preview: 'preview',
        messages: [],
        createdAt: Date.now(),
        updatedAt: Date.now(),
      },
    ];
    localStorage.setItem(CONVERSATIONS_KEY, JSON.stringify(seed));
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    render(<ConversationList />);
    const row = screen.getByTestId('conversation-row-c-1');
    const deleteBtn = row.querySelector('button[aria-label*="删除"]') as HTMLElement;
    fireEvent.click(deleteBtn);
    const after = JSON.parse(localStorage.getItem(CONVERSATIONS_KEY)!);
    expect(after.length).toBe(1);
  });

  it('当前会话有 aria-current="true" 高亮', () => {
    const seed = [
      {
        id: 'c-1',
        title: 'first',
        preview: 'preview',
        messages: [],
        createdAt: Date.now(),
        updatedAt: Date.now(),
      },
    ];
    localStorage.setItem(CONVERSATIONS_KEY, JSON.stringify(seed));
    render(<ConversationList />);
    const row = screen.getByTestId('conversation-row-c-1');
    const selectBtn = row.querySelector('button:not([aria-label*="删除"])') as HTMLElement;
    fireEvent.click(selectBtn);
    expect(row.getAttribute('aria-current')).toBe('true');
  });
});
