/**
 * v2.2 PR4 — 多会话历史管理 hook。
 *
 * 数据存储：
 *   - localStorage key=`ic.conversations`，JSON 数组。
 *   - 单元素结构（Q9 拍板）：不含 metadata，仅 { role, content, timestamp }。
 *
 * 容量策略（Q2 拍板）：
 *   - 最多保留 50 条 FIFO（First-In First-Out）。
 *   - 超出 50 条时按 updatedAt 升序淘汰最旧。
 *
 * 写入时机：
 *   - 每次 send() 后 / final 事件到达后 / regenerate 后，调用 save()。
 *
 * 错误处理：
 *   - localStorage 不可用（Safari 隐私模式 / 配额超限）时降级为内存 state。
 *   - 解析失败时打 console.warn 并清空损坏数据。
 *
 * 数据一致性：
 *   - 暴露 list / currentId / setCurrentId / save / delete / getById / createNew。
 *   - save 会自动新建（id 不存在）或更新（id 已存在）。
 */

import { useCallback, useEffect, useRef, useState } from 'react';

export const CONVERSATIONS_KEY = 'ic.conversations';
/** PRD Q2 拍板：FIFO 50 条 */
export const MAX_CONVERSATIONS = 50;
/** 标题截断长度 */
const TITLE_MAX = 30;
/** 预览截断长度 */
const PREVIEW_MAX = 50;

export interface ConversationMessage {
  role: 'user' | 'assistant' | 'agent' | 'system';
  content: string;
  /** 写入时刻（ISO string） */
  timestamp: string;
}

export interface StoredConversation {
  id: string;
  /** 首条 user 消息前 30 字 */
  title: string;
  /** 最后一条消息前 50 字 */
  preview: string;
  /** 完整消息数组（不含 metadata） */
  messages: ConversationMessage[];
  createdAt: number;
  updatedAt: number;
}

export interface UseConversationsReturn {
  /** 所有会话（按 updatedAt 倒序） */
  list: StoredConversation[];
  /** 当前激活会话 id（undefined 表示新建中） */
  currentId: string | undefined;
  /** 切换会话（仅设置 currentId，不修改 messages） */
  setCurrentId: (id: string | undefined) => void;
  /** 创建新会话（写入空记录，返回 id；同时设为 current） */
  createNew: () => string;
  /** 保存会话（id 不存在则新建；存在则更新 messages + updatedAt） */
  save: (
    id: string | undefined,
    messages: ConversationMessage[],
    options?: { conversationId?: string },
  ) => string;
  /** 删除会话 */
  remove: (id: string) => void;
  /** 按 id 查 */
  getById: (id: string) => StoredConversation | undefined;
  /** 当前会话的全部消息（与 list[currentId]?.messages 等价） */
  currentMessages: ConversationMessage[];
  /** 加载指定 id 的 messages（用于切换会话后恢复对话） */
  loadMessages: (id: string) => ConversationMessage[];
  /** 是否降级到内存（localStorage 不可用） */
  degraded: boolean;
}

function generateId(): string {
  // 类似 'c-' + 8 位 base36 随机
  return 'c-' + Math.random().toString(36).slice(2, 10) + Date.now().toString(36).slice(-4);
}

function deriveTitle(messages: ConversationMessage[]): string {
  const firstUser = messages.find((m) => m.role === 'user');
  const text = firstUser?.content ?? messages[0]?.content ?? '新会话';
  return text.length > TITLE_MAX ? text.slice(0, TITLE_MAX) + '…' : text;
}

function derivePreview(messages: ConversationMessage[]): string {
  const last = messages[messages.length - 1];
  const text = last?.content ?? '';
  return text.length > PREVIEW_MAX ? text.slice(0, PREVIEW_MAX) + '…' : text;
}

function readAll(): StoredConversation[] {
  if (typeof window === 'undefined') return [];
  try {
    const raw = window.localStorage.getItem(CONVERSATIONS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (c): c is StoredConversation =>
        c !== null &&
        typeof c === 'object' &&
        typeof (c as StoredConversation).id === 'string',
    );
  } catch (e) {
    console.warn('useConversations: readAll failed', e);
    return [];
  }
}

function writeAll(list: StoredConversation[]): boolean {
  if (typeof window === 'undefined') return false;
  try {
    window.localStorage.setItem(CONVERSATIONS_KEY, JSON.stringify(list));
    return true;
  } catch (e) {
    console.warn('useConversations: writeAll failed', e);
    return false;
  }
}

/**
 * FIFO 裁剪：超出 MAX_CONVERSATIONS 时按 updatedAt 升序淘汰最旧。
 */
function fifoTrim(list: StoredConversation[]): StoredConversation[] {
  if (list.length <= MAX_CONVERSATIONS) return list;
  // 按 updatedAt 升序
  const sorted = [...list].sort((a, b) => a.updatedAt - b.updatedAt);
  return sorted.slice(sorted.length - MAX_CONVERSATIONS);
}

/**
 * 按 updatedAt 倒序排序（最新在前）。
 */
function sortDesc(list: StoredConversation[]): StoredConversation[] {
  return [...list].sort((a, b) => b.updatedAt - a.updatedAt);
}

export function useConversations(): UseConversationsReturn {
  const [list, setList] = useState<StoredConversation[]>(() => readAll());
  const [currentId, setCurrentId] = useState<string | undefined>(undefined);
  const [degraded, setDegraded] = useState<boolean>(false);
  // 持久化最新 list 引用，避免闭包陈旧
  const listRef = useRef<StoredConversation[]>(list);
  listRef.current = list;

  // 检测 localStorage 可用性
  useEffect(() => {
    try {
      const probe = '__ic_probe__';
      window.localStorage.setItem(probe, probe);
      window.localStorage.removeItem(probe);
      setDegraded(false);
    } catch {
      setDegraded(true);
    }
  }, []);

  // 同步降级模式到内存
  useEffect(() => {
    if (degraded) {
      // 尝试一次性把已有数据迁移到内存（不需要写入 localStorage）
      const existing = readAll();
      if (existing.length > 0 && listRef.current.length === 0) {
        setList(existing);
      }
    }
  }, [degraded]);

  const persist = useCallback(
    (updater: StoredConversation[] | ((prev: StoredConversation[]) => StoredConversation[])) => {
      setList((prev) => {
        const next = typeof updater === 'function'
          ? (updater as (p: StoredConversation[]) => StoredConversation[])(prev)
          : updater;
        const sorted = sortDesc(fifoTrim(next));
        if (!degraded) {
          writeAll(sorted);
        }
        return sorted;
      });
    },
    [degraded],
  );

  const createNew = useCallback((): string => {
    const id = generateId();
    const now = Date.now();
    const fresh: StoredConversation = {
      id,
      title: '新会话',
      preview: '',
      messages: [],
      createdAt: now,
      updatedAt: now,
    };
    persist((prev) => [...prev, fresh]);
    setCurrentId(id);
    return id;
  }, [persist]);

  const save = useCallback<UseConversationsReturn['save']>(
    (id, messages, options) => {
      const now = Date.now();
      let realId = id;
      const exists = id ? listRef.current.find((c) => c.id === id) : undefined;

      if (!exists) {
        // 新建
        realId = id ?? generateId();
        const fresh: StoredConversation = {
          id: realId,
          title: deriveTitle(messages),
          preview: derivePreview(messages),
          messages,
          createdAt: now,
          updatedAt: now,
        };
        persist((prev) => [...prev, fresh]);
      } else {
        // 更新
        const updated: StoredConversation = {
          ...exists,
          title: deriveTitle(messages),
          preview: derivePreview(messages),
          messages,
          updatedAt: now,
        };
        persist((prev) => prev.map((c) => (c.id === realId ? updated : c)));
      }

      // 同步 conversationId（来自后端的"正式 id"，用于后续请求透传）
      if (options?.conversationId && typeof window !== 'undefined') {
        try {
          window.localStorage.setItem('ic.conversation_id', options.conversationId);
        } catch {
          /* ignore */
        }
      }
      return realId!;
    },
    [persist],
  );

  const remove = useCallback(
    (id: string) => {
      persist((prev) => prev.filter((c) => c.id !== id));
      if (currentId === id) setCurrentId(undefined);
    },
    [persist, currentId],
  );

  const getById = useCallback(
    (id: string) => listRef.current.find((c) => c.id === id),
    [],
  );

  const loadMessages = useCallback(
    (id: string) => listRef.current.find((c) => c.id === id)?.messages ?? [],
    [],
  );

  const current = list.find((c) => c.id === currentId);

  return {
    list,
    currentId,
    setCurrentId,
    createNew,
    save,
    remove,
    getById,
    currentMessages: current?.messages ?? [],
    loadMessages,
    degraded,
  };
}

export default useConversations;
