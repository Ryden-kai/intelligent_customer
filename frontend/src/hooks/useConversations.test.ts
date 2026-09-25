/**
 * v2.2 PR4 — useConversations hook 测试。
 *
 * 覆盖：
 *   - 初始空列表
 *   - createNew 生成新会话
 *   - save 写入 / 更新 / FIFO 50 条裁剪
 *   - remove 删除会话
 *   - loadMessages / getById 查询
 *   - localStorage 不可用时降级到内存
 *   - 切换 currentId
 *   - 标题 / 预览自动派生
 *   - 损坏 JSON 不崩
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';

import {
  useConversations,
  CONVERSATIONS_KEY,
  MAX_CONVERSATIONS,
  type ConversationMessage,
} from './useConversations';

beforeEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});

afterEach(() => {
  vi.useRealTimers();
  localStorage.clear();
});

function msg(role: ConversationMessage['role'], content: string): ConversationMessage {
  return { role, content, timestamp: new Date().toISOString() };
}

describe('useConversations — 基本 CRUD', () => {
  it('初始：空列表 + currentId=undefined', () => {
    const { result } = renderHook(() => useConversations());
    expect(result.current.list).toEqual([]);
    expect(result.current.currentId).toBeUndefined();
    expect(result.current.currentMessages).toEqual([]);
  });

  it('createNew 生成新会话 + 设为当前', () => {
    const { result } = renderHook(() => useConversations());
    let id = '';
    act(() => {
      id = result.current.createNew();
    });
    expect(id).toMatch(/^c-/);
    expect(result.current.list.length).toBe(1);
    expect(result.current.list[0].id).toBe(id);
    expect(result.current.currentId).toBe(id);
  });

  it('save 创建新会话（id 不存在）', () => {
    const { result } = renderHook(() => useConversations());
    let savedId = '';
    act(() => {
      savedId = result.current.save(undefined, [msg('user', 'hello world')]);
    });
    expect(savedId).toMatch(/^c-/);
    expect(result.current.list.length).toBe(1);
    expect(result.current.list[0].title).toBe('hello world');
    expect(result.current.list[0].preview).toBe('hello world');
    expect(result.current.list[0].messages.length).toBe(1);
  });

  it('save 更新已存在会话（id 匹配）', () => {
    const { result } = renderHook(() => useConversations());
    let savedId = '';
    act(() => {
      savedId = result.current.save(undefined, [msg('user', 'first')]);
    });
    act(() => {
      result.current.save(
        savedId,
        [msg('user', 'first'), msg('assistant', 'reply')],
      );
    });
    expect(result.current.list.length).toBe(1);
    expect(result.current.list[0].messages.length).toBe(2);
    expect(result.current.list[0].title).toBe('first');
    expect(result.current.list[0].preview).toBe('reply');
  });

  it('save 自动派生标题 / 预览（按第一条 user / 最后一条）', () => {
    const { result } = renderHook(() => useConversations());
    act(() => {
      result.current.save(undefined, [
        msg('system', 'system msg'),
        msg('user', 'I want to ask about refund'),
        msg('assistant', 'OK please provide order id'),
        msg('user', 'order 12345'),
      ]);
    });
    expect(result.current.list[0].title).toBe('I want to ask about refund');
    expect(result.current.list[0].preview).toBe('order 12345');
  });

  it('save 标题截断 30 字符', () => {
    const { result } = renderHook(() => useConversations());
    const long = 'a'.repeat(50);
    act(() => {
      result.current.save(undefined, [msg('user', long)]);
    });
    expect(result.current.list[0].title.length).toBeLessThanOrEqual(31); // 30 + '…'
    expect(result.current.list[0].title.endsWith('…')).toBe(true);
  });

  it('save 预览截断 50 字符', () => {
    const { result } = renderHook(() => useConversations());
    const long = 'b'.repeat(80);
    act(() => {
      result.current.save(undefined, [msg('user', 'hi'), msg('assistant', long)]);
    });
    expect(result.current.list[0].preview.length).toBeLessThanOrEqual(51);
    expect(result.current.list[0].preview.endsWith('…')).toBe(true);
  });

  it('remove 删除会话 + 若删除当前则清空 currentId', () => {
    const { result } = renderHook(() => useConversations());
    let id = '';
    act(() => {
      id = result.current.createNew();
    });
    act(() => {
      result.current.remove(id);
    });
    expect(result.current.list.length).toBe(0);
    expect(result.current.currentId).toBeUndefined();
  });

  it('remove 非当前会话不修改 currentId', () => {
    const { result } = renderHook(() => useConversations());
    let idA = '';
    let idB = '';
    act(() => {
      idA = result.current.createNew();
      idB = result.current.createNew();
    });
    // currentId = idB
    act(() => {
      result.current.remove(idA);
    });
    expect(result.current.currentId).toBe(idB);
  });

  it('getById / loadMessages 查询', () => {
    const { result } = renderHook(() => useConversations());
    let id = '';
    act(() => {
      id = result.current.save(undefined, [
        msg('user', 'q'),
        msg('assistant', 'a'),
      ]);
    });
    const got = result.current.getById(id);
    expect(got?.messages.length).toBe(2);
    expect(result.current.loadMessages(id).length).toBe(2);
    expect(result.current.loadMessages('nonexistent')).toEqual([]);
    expect(result.current.getById('nonexistent')).toBeUndefined();
  });

  it('setCurrentId 切换（不影响 list）', () => {
    const { result } = renderHook(() => useConversations());
    let idA = '';
    let idB = '';
    act(() => {
      idA = result.current.createNew();
      idB = result.current.createNew();
    });
    act(() => {
      result.current.setCurrentId(idA);
    });
    expect(result.current.currentId).toBe(idA);
    expect(result.current.list.length).toBe(2);
  });
});

describe('useConversations — FIFO 50 条', () => {
  it('超过 50 条时自动淘汰最旧（PRD Q2 拍板）', () => {
    const { result } = renderHook(() => useConversations());
    // 插入 60 条（每条 updatedAt 递增）
    const ids: string[] = [];
    act(() => {
      for (let i = 0; i < 60; i++) {
        const id = result.current.save(undefined, [msg('user', `msg-${i}`)]);
        ids.push(id);
      }
    });
    expect(result.current.list.length).toBe(MAX_CONVERSATIONS);
    // 保留的应是后 50 条（updatedAt 最新）
    const titles = result.current.list.map((c) => c.title);
    expect(titles).toContain('msg-59');
    expect(titles).toContain('msg-10');
    expect(titles).not.toContain('msg-9');
    expect(titles).not.toContain('msg-0');
  });

  it('FIFO 50 条边界：正好 50 条不裁剪', () => {
    const { result } = renderHook(() => useConversations());
    act(() => {
      for (let i = 0; i < 50; i++) {
        result.current.save(undefined, [msg('user', `msg-${i}`)]);
      }
    });
    expect(result.current.list.length).toBe(50);
  });
});

describe('useConversations — localStorage 降级', () => {
  it('localStorage 抛错时降级到内存（degraded=true）', () => {
    const original = window.localStorage;
    const failingStorage = {
      ...original,
      setItem: vi.fn(() => {
        throw new Error('QuotaExceeded');
      }),
      getItem: vi.fn(() => null),
      removeItem: vi.fn(),
    };
    Object.defineProperty(window, 'localStorage', {
      value: failingStorage,
      configurable: true,
    });

    const { result } = renderHook(() => useConversations());
    act(() => {
      result.current.save(undefined, [msg('user', 'in-memory only')]);
    });
    expect(result.current.degraded).toBe(true);
    expect(result.current.list.length).toBe(1);

    // 恢复
    Object.defineProperty(window, 'localStorage', {
      value: original,
      configurable: true,
    });
  });

  it('损坏 JSON 不崩', () => {
    localStorage.setItem(CONVERSATIONS_KEY, '{invalid json[[[');
    const { result } = renderHook(() => useConversations());
    expect(result.current.list).toEqual([]);
  });

  it('非数组数据不崩', () => {
    localStorage.setItem(CONVERSATIONS_KEY, JSON.stringify({ not: 'array' }));
    const { result } = renderHook(() => useConversations());
    expect(result.current.list).toEqual([]);
  });
});

describe('useConversations — 跨实例持久化', () => {
  it('保存后再 mount 可恢复', () => {
    const first = renderHook(() => useConversations());
    act(() => {
      first.result.current.save(undefined, [msg('user', 'persist me')]);
    });
    first.unmount();

    const second = renderHook(() => useConversations());
    expect(second.result.current.list.length).toBe(1);
    expect(second.result.current.list[0].title).toBe('persist me');
  });
});
