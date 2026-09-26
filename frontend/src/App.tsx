import { useEffect, useRef, useState } from 'react';
import { api, streamChat } from './api';
import type { ChatMessage, StreamEvent } from './types';
import { ErrorBoundary } from './components/ErrorBoundary';
import { Skeleton } from './components/Skeleton';
import { EmptyState } from './components/EmptyState';
import { ThemeToggle } from './components/ThemeToggle';
import { Markdown } from './components/Markdown';
import { ConversationList } from './components/ConversationList';
import { Drawer } from './components/Drawer';
import { MessageActions, type FeedbackState } from './components/MessageActions';
import { useConversations, type ConversationMessage } from './hooks/useConversations';
import { useTypewriter } from './hooks/useTypewriter';

const USER_ID_KEY = 'ic.user_id';
const CONV_KEY = 'ic.conversation_id';

function loadOrCreateUserId(): string {
  let id = localStorage.getItem(USER_ID_KEY);
  if (!id) {
    id = 'u-' + Math.random().toString(36).slice(2, 10);
    localStorage.setItem(USER_ID_KEY, id);
  }
  return id;
}

interface UiMsg extends ChatMessage {
  pending?: boolean;
  feedback?: FeedbackState;
}

interface TraceStep {
  type: string;
  tool?: string;
  summary?: string;
  status?: string;
  ticketId?: string;
  args?: Record<string, unknown>;
  result?: unknown;
  durationMs?: number;
}

export default function App() {
  const [userId] = useState(loadOrCreateUserId);
  const [input, setInput] = useState('');
  const [loading, setLoading] = useState(false);
  const [trace, setTrace] = useState<TraceStep[]>([]);
  const [pendingTicket, setPendingTicket] = useState<string | undefined>();
  const [feedbackSubmitted, setFeedbackSubmitted] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [handedOver, setHandedOver] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);

  const conv = useConversations();
  const { list, currentId, setCurrentId, createNew, save, loadMessages } = conv;

  // 从 localStorage 同步后端 conversationId（每次 send 后会被 streamChat 更新）
  const [serverConversationId, setServerConversationId] = useState<string | undefined>(
    () => localStorage.getItem(CONV_KEY) || undefined,
  );

  // 当前会话的消息
  const [messages, setMessages] = useState<UiMsg[]>(() => {
    const cid = localStorage.getItem(CONV_KEY) || undefined;
    if (cid) {
      // 尝试从 useConversations 恢复（首次 mount 时 list 可能未加载）
      try {
        const raw = localStorage.getItem('ic.conversations');
        if (raw) {
          const parsed = JSON.parse(raw);
          const found = parsed.find((c: { id: string }) => c.id === cid);
          if (found?.messages) return found.messages as UiMsg[];
        }
      } catch {
        /* ignore */
      }
    }
    return [];
  });

  // 切会话时恢复消息
  useEffect(() => {
    if (currentId) {
      const msgs = loadMessages(currentId);
      // 把 ConversationMessage 转换为 UiMsg（补充 id / createdAt）
      const restored: UiMsg[] = msgs.map((m, idx) => ({
        id: `restored-${currentId}-${idx}`,
        role: m.role,
        content: m.content,
        createdAt: m.timestamp,
      }));
      setMessages(restored);
    } else {
      setMessages([]);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentId]);

  // 持久化：当 messages 变化时，自动 save（节流：每次 setMessages 后立即 save）
  useEffect(() => {
    if (messages.length === 0) return;
    const cid = currentId;
    if (!cid) return;
    // 把 UiMsg 简化为 ConversationMessage（仅 role/content/timestamp）
    const simple: ConversationMessage[] = messages.map((m) => ({
      role: m.role,
      content: m.content,
      timestamp: m.createdAt || new Date().toISOString(),
    }));
    save(cid, simple, { conversationId: serverConversationId });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messages]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages.length, trace.length]);

  async function send(textOverride?: string) {
    const text = (textOverride ?? input).trim();
    if (!text || loading) return;
    setError(null);
    setInput('');
    setLoading(true);
    setTrace([]);
    setPendingTicket(undefined);

    // 确保有 currentId
    let activeId = currentId;
    if (!activeId) {
      activeId = createNew();
    }

    const optimistic: UiMsg = {
      id: 'pending-' + Date.now(),
      role: 'user',
      content: text,
      createdAt: new Date().toISOString(),
      pending: true,
    };
    setMessages((m) => [...m, optimistic]);
    try {
      const result = await streamChat(
        { conversationId: serverConversationId, userId, content: text },
        (ev) => onStreamEvent(ev),
      );
      const { final, ticketId } = result;
      if (final && final.content) {
        const assistantMsg: UiMsg = {
          id: 'assistant-' + Date.now(),
          role: 'assistant',
          content: final.content!,
          model: final.source,
          createdAt: new Date().toISOString(),
        };
        setMessages((m) => [
          ...m.filter((x) => x.id !== optimistic.id),
          assistantMsg,
        ]);
      } else {
        setMessages((m) => m.filter((x) => x.id !== optimistic.id));
      }
      if (final?.handedOver) setHandedOver(true);
      if (ticketId) setPendingTicket(ticketId);
      // 保存后端返回的 conversationId
      if (result.events.length > 0) {
        // 真实场景下服务端会在首个事件里返回 conversationId
        // 这里用 ev.conversationId 或沿用 userId 生成稳定 id
        const serverId =
          (result.events.find((e) => e.traceId)?.traceId as string | undefined) ||
          localStorage.getItem(CONV_KEY) ||
          'conv-' + Date.now();
        setServerConversationId(serverId);
        try {
          localStorage.setItem(CONV_KEY, serverId);
        } catch {
          /* ignore */
        }
      }
      setFeedbackSubmitted(false);
    } catch (e) {
      const err = e as { message?: string };
      setError(err?.message || '请求失败');
      setMessages((m) => m.filter((x) => x.id !== optimistic.id));
    } finally {
      setLoading(false);
    }
  }

  function onStreamEvent(_ev: StreamEvent) {
    // 流式事件统一打到 trace 面板（保持原有可视化）
    setTrace((t) => {
      const ev = _ev;
      switch (ev.type) {
        case 'step':
          return [...t, { type: '思考', content: ev.content }];
        case 'tool_call':
          return [
            ...t,
            {
              type: '调用 skill',
              tool: ev.tool,
              args: ev.args,
              status: 'in_flight',
            },
          ];
        case 'tool_result':
          return t.map((s, i) =>
            i === t.length - 1 && s.type === '调用 skill' && s.tool === ev.tool
              ? {
                  ...s,
                  status: ev.status || 'ok',
                  summary: ev.summary,
                  result: ev.result,
                  ticketId: ev.ticketId,
                  durationMs: ev.durationMs,
                }
              : s,
          );
        case 'pending_human':
          return [
            ...t,
            {
              type: '等待人工确认',
              tool: ev.tool,
              summary: ev.summary,
              ticketId: ev.ticketId,
              status: 'pending_human',
            },
          ];
        case 'handover':
          return [
            ...t,
            { type: '转人工', summary: ev.reason || 'agent decided', status: 'handover' },
          ];
        case 'final':
          return [
            ...t,
            { type: '最终回复', summary: ev.content, status: 'final', durationMs: ev.durationMs },
          ];
        case 'error':
          return [...t, { type: '错误', summary: ev.error, status: 'error' }];
        default:
          return t;
      }
    });
  }

  async function submitFeedback(rating: number, comment: string) {
    if (!serverConversationId) return;
    try {
      await api.feedback({ conversationId: serverConversationId, rating, comment });
      setFeedbackSubmitted(true);
    } catch (e) {
      const err = e as { response?: { data?: { message?: string } } };
      setError(err?.response?.data?.message || '反馈失败');
    }
  }

  async function confirmTicket() {
    if (!pendingTicket) return;
    try {
      await api.confirmSkill({ ticketId: pendingTicket, userId });
      setPendingTicket(undefined);
      setMessages((m) => [
        ...m,
        {
          id: 'system-' + Date.now(),
          role: 'system',
          content: '✅ 已确认，工单已提交。',
          createdAt: new Date().toISOString(),
        },
      ]);
    } catch (e) {
      const err = e as { response?: { data?: { message?: string } } };
      setError(err?.response?.data?.message || '确认失败');
    }
  }

  async function cancelTicket() {
    if (!pendingTicket) return;
    try {
      await api.cancelSkill({ ticketId: pendingTicket, userId });
      setPendingTicket(undefined);
    } catch (e) {
      const err = e as { response?: { data?: { message?: string } } };
      setError(err?.response?.data?.message || '取消失败');
    }
  }

  function handleNewConversation() {
    createNew();
    setMessages([]);
    setTrace([]);
    setHandedOver(false);
    setPendingTicket(undefined);
    setFeedbackSubmitted(false);
    setServerConversationId(undefined);
    try {
      localStorage.removeItem(CONV_KEY);
    } catch {
      /* ignore */
    }
  }

  /**
   * ConversationList 调用：当用户点删除某个会话时触发。
   * 清空聊天窗口的所有状态，避免"删了侧栏还能看到历史"的混乱体验。
   */
  function handleDeleteConversation(_id: string) {
    setMessages([]);
    setTrace([]);
    setHandedOver(false);
    setPendingTicket(undefined);
    setFeedbackSubmitted(false);
    setServerConversationId(undefined);
    try {
      localStorage.removeItem(CONV_KEY);
    } catch {
      /* ignore */
    }
  }

  function handleSwitchConversation(id: string) {
    setCurrentId(id);
    const msgs = loadMessages(id);
    const restored: UiMsg[] = msgs.map((m, idx) => ({
      id: `restored-${id}-${idx}`,
      role: m.role,
      content: m.content,
      createdAt: m.timestamp,
    }));
    setMessages(restored);
    setTrace([]);
    setHandedOver(false);
    setPendingTicket(undefined);
    setFeedbackSubmitted(false);
    setDrawerOpen(false);
  }

  function handleRegenerate() {
    // 找最后一条 user 消息重新发送
    const lastUser = [...messages].reverse().find((m) => m.role === 'user');
    if (!lastUser) return;
    // 移除最后一条 assistant 消息（如果有）
    setMessages((m) => {
      const lastAssistantIdx = [...m].reverse().findIndex((x) => x.role === 'assistant');
      if (lastAssistantIdx === -1) return m;
      const realIdx = m.length - 1 - lastAssistantIdx;
      return [...m.slice(0, realIdx), ...m.slice(realIdx + 1)];
    });
    void send(lastUser.content);
  }

  async function handleThumbUp() {
    if (!serverConversationId) return;
    try {
      await api.feedback({ conversationId: serverConversationId, rating: 5 });
    } catch (e) {
      console.warn('thumbUp feedback failed', e);
    }
  }

  async function handleThumbDown() {
    if (!serverConversationId) return;
    try {
      await api.feedback({ conversationId: serverConversationId, rating: 2 });
    } catch (e) {
      console.warn('thumbDown feedback failed', e);
    }
  }

  function handleMessageFeedbackChange(id: string, next: FeedbackState) {
    setMessages((m) => m.map((x) => (x.id === id ? { ...x, feedback: next } : x)));
  }

  return (
    <ErrorBoundary scope="user">
      <div className="flex h-full w-full">
        {/* 桌面端侧栏（≥ lg） */}
        <div className="hidden lg:block w-[280px] flex-shrink-0">
          <ConversationList onSelect={handleSwitchConversation} onDelete={handleDeleteConversation} />
        </div>

        {/* 移动端抽屉 */}
        <Drawer
          open={drawerOpen}
          onClose={() => setDrawerOpen(false)}
          title="历史会话"
          titleId="drawer-title"
        >
          <ConversationList onSelect={handleSwitchConversation} onDelete={handleDeleteConversation} />
        </Drawer>

        {/* 主区 */}
        <div className="flex flex-col flex-1 min-w-0 h-full max-w-3xl mx-auto w-full p-3 sm:p-4 gap-3 sm:gap-4">
          <header className="flex items-center justify-between gap-2">
            <div className="flex items-center gap-2 min-w-0">
              {/* 移动端汉堡菜单 */}
              <button
                type="button"
                onClick={() => setDrawerOpen(true)}
                aria-label="打开侧栏"
                className="lg:hidden text-app-text-muted hover:text-app-text p-2 rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
              >
                ☰
              </button>
              <div className="min-w-0">
                <h1 className="text-lg sm:text-xl font-semibold text-app-text">
                  智能客服 Agent
                </h1>
                <p className="text-xs text-app-text-muted hidden sm:block">
                  Tool Calling · Skill 自动化 · 流式步骤可视化
                </p>
              </div>
            </div>
            <div className="flex items-center gap-2 flex-shrink-0">
              <ThemeToggle aria-label="切换主题" />
              <button
                onClick={handleNewConversation}
                aria-label="开启新会话"
                className="text-xs text-app-text-muted hover:text-brand-600 underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand rounded"
              >
                新会话
              </button>
            </div>
          </header>

          <main className="flex-1 bg-app-surface rounded-2xl shadow-sm border border-app-border p-3 sm:p-4 flex flex-col gap-3 overflow-hidden">
            <div
              className="flex-1 overflow-y-auto flex flex-col gap-3 pr-2"
              aria-live="polite"
            >
              {messages.length === 0 && !loading && (
                <EmptyState
                  variant="noData"
                  icon="💬"
                  title="您好，我是您的智能客服「小助理」"
                  description="可以问我关于退款、订单、技术问题等。开始输入即可。"
                />
              )}
              {messages.map((m) => (
                <Bubble
                  key={m.id}
                  m={m}
                  onRegenerate={m.role === 'assistant' ? handleRegenerate : undefined}
                  onThumbUp={
                    m.role === 'assistant' && serverConversationId
                      ? () => {
                          handleMessageFeedbackChange(m.id, 'up');
                          return handleThumbUp();
                        }
                      : undefined
                  }
                  onThumbDown={
                    m.role === 'assistant' && serverConversationId
                      ? () => {
                          handleMessageFeedbackChange(m.id, 'down');
                          return handleThumbDown();
                        }
                      : undefined
                  }
                />
              ))}
              {loading && messages.length === 0 && (
                <div className="flex flex-col gap-2 my-auto" aria-label="正在加载首条消息">
                  <Skeleton variant="text" width="60%" />
                  <Skeleton variant="text" width="80%" />
                  <Skeleton variant="text" width="40%" />
                </div>
              )}
              {loading && messages.length > 0 && (
                <div
                  className="self-start bg-app-surface border border-app-border rounded-2xl rounded-bl-sm px-4 py-2 text-sm text-app-text-muted animate-pulse"
                  aria-label="正在思考"
                >
                  正在思考…
                </div>
              )}
              <div ref={bottomRef} />
            </div>

            {error && (
              <div
                role="alert"
                className="text-danger-700 bg-danger-50 border border-danger-200 px-3 py-2 rounded-lg text-sm"
              >
                {error}
              </div>
            )}

            {pendingTicket && (
              <div
                role="alertdialog"
                aria-label="需要确认的操作"
                className="bg-warning-50 border border-warning-200 rounded-xl p-3 text-sm"
              >
                <div className="font-medium text-warning-700 mb-1">检测到需要您确认的操作</div>
                <div className="text-warning-700 text-xs mb-2">工单 id: {pendingTicket}</div>
                <div className="flex gap-2">
                  <button
                    onClick={confirmTicket}
                    className="bg-warning-500 hover:bg-warning-600 text-white text-xs px-3 py-1 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-warning-500"
                  >
                    确认
                  </button>
                  <button
                    onClick={cancelTicket}
                    className="bg-app-surface border border-warning-300 text-warning-700 text-xs px-3 py-1 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-warning-500"
                  >
                    取消
                  </button>
                </div>
              </div>
            )}

            {(loading || trace.length > 0) && <TracePanel trace={trace} />}

            {handedOver && !feedbackSubmitted && (
              <FeedbackPanel onSubmit={submitFeedback} />
            )}
            {feedbackSubmitted && (
              <div
                role="status"
                className="text-success-700 bg-success-50 border border-success-200 px-3 py-2 rounded-lg text-sm"
              >
                已收到您的评价，感谢反馈！
              </div>
            )}

            <div className="flex items-end gap-2 border-t border-app-border pt-3">
              <label htmlFor="chat-input" className="sr-only">
                输入消息
              </label>
              <textarea
                id="chat-input"
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey) {
                    e.preventDefault();
                    void send();
                  }
                }}
                placeholder="输入您的问题，Enter 发送，Shift+Enter 换行"
                aria-label="聊天输入框"
                className="flex-1 resize-none border border-app-border rounded-xl px-3 py-2 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                rows={2}
                disabled={loading}
              />
              <button
                onClick={() => void send()}
                disabled={loading || !input.trim()}
                aria-label="发送消息"
                className="bg-brand-500 hover:bg-brand-600 text-white text-sm font-medium px-3 sm:px-4 py-2 rounded-xl disabled:opacity-40 disabled:cursor-not-allowed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 focus-visible:ring-offset-2 focus-visible:ring-offset-app-bg"
              >
                发送
              </button>
            </div>
          </main>

          <footer className="text-center text-xs text-app-text-muted">
            Powered by Tool-Calling Agent · {new Date().toLocaleDateString('zh-CN')}
          </footer>
        </div>
      </div>
    </ErrorBoundary>
  );
}

interface BubbleProps {
  m: UiMsg;
  onRegenerate?: () => void;
  onThumbUp?: () => void | Promise<void>;
  onThumbDown?: () => void | Promise<void>;
}

function Bubble({ m, onRegenerate, onThumbUp, onThumbDown }: BubbleProps) {
  const isUser = m.role === 'user';
  const isSystem = m.role === 'system';
  if (isSystem) {
    return (
      <div
        role="status"
        className="self-center text-app-text-muted text-xs bg-app-surface-muted px-3 py-1 rounded-full"
      >
        {m.content}
      </div>
    );
  }
  const cls = isUser
    ? 'bubble-user'
    : m.role === 'agent'
      ? 'bubble-agent'
      : 'bubble-assistant';
  // assistant 消息走打字机 + Markdown
  const showTypewriter = m.role === 'assistant' && !m.pending;
  return (
    <div className={`flex ${isUser ? 'justify-end' : 'justify-start'} group`}>
      <div className={cls + ' relative'}>
        {isUser ? (
          <div className="whitespace-pre-wrap">{m.content}</div>
        ) : (
          <AssistantContent content={m.content} animated={showTypewriter} />
        )}
        {m.role === 'assistant' && m.model && (
          <div className="text-[10px] text-app-text-muted mt-1">via {m.model}</div>
        )}
        {m.role === 'assistant' && (
          <MessageActions
            content={m.content}
            onRegenerate={onRegenerate}
            onThumbUp={onThumbUp}
            onThumbDown={onThumbDown}
            feedback={m.feedback}
          />
        )}
      </div>
    </div>
  );
}

/**
 * AssistantContent — assistant 消息内容：
 *   - 若 animated=true：使用打字机逐字显示（≤ 20ms/token）。
 *   - 否则：直接渲染 Markdown 全文。
 *   - 打字机进行中显示光标（▍）。
 *   - 用户可点 skip 立即显示全文（按钮由 MessageActions 提供 regenerate 复用 skip 思路，但这里为了简单直接显示完成态）。
 */
function AssistantContent({ content, animated }: { content: string; animated: boolean }) {
  const { displayed, done, skip } = useTypewriter(content, {
    speedMs: 20,
    disabled: !animated,
  });
  const finalContent = animated ? displayed : content;
  // 不使用 skip UI（由 MessageActions regenerate 触发整体重新生成）；
  // 但保留 skip 函数以便外部调用扩展。
  void skip;

  return (
    <div data-testid="assistant-content">
      <Markdown content={finalContent} />
      {animated && !done && <span className="ic-typewriter-cursor" aria-hidden="true">▍</span>}
    </div>
  );
}

function TracePanel({ trace }: { trace: TraceStep[] }) {
  if (trace.length === 0) return null;
  return (
    <section
      aria-label="Agent 执行轨迹"
      className="bg-app-surface-muted border border-app-border rounded-xl p-3 text-xs"
    >
      <div className="text-app-text-muted mb-2 font-medium">Agent 执行轨迹</div>
      <ol className="space-y-1">
        {trace.map((s, i) => (
          <li key={i} className="flex items-start gap-2">
            <span className="text-app-text-muted w-6 inline-block">{i + 1}.</span>
            <div className="flex-1 min-w-0">
              <span className="font-medium text-app-text">{s.type}</span>
              {s.tool && <span className="ml-1 text-violet-700 dark:text-violet-400">· {s.tool}</span>}
              {s.status === 'in_flight' && (
                <span className="ml-1 text-warning-600 animate-pulse">调用中…</span>
              )}
              {s.status && s.status !== 'in_flight' && (
                <span
                  className={`ml-1 ${
                    s.status === 'ok'
                      ? 'text-success-600'
                      : s.status === 'error'
                      ? 'text-danger-600'
                      : 'text-app-text-muted'
                  }`}
                >
                  [{s.status}]
                </span>
              )}
              {s.summary && <span className="text-app-text ml-1">{s.summary}</span>}
              {s.args && (
                <pre className="mt-1 bg-app-surface border border-app-border rounded px-2 py-1 overflow-x-auto">
                  {JSON.stringify(s.args)}
                </pre>
              )}
              {s.result != null && (
                <pre className="mt-1 bg-app-surface border border-app-border rounded px-2 py-1 overflow-x-auto text-[11px] text-app-text-muted">
                  {JSON.stringify(s.result)}
                </pre>
              )}
              {typeof s.durationMs === 'number' && (
                <span className="ml-1 text-app-text-muted">{s.durationMs}ms</span>
              )}
            </div>
          </li>
        ))}
      </ol>
    </section>
  );
}

function FeedbackPanel({
  onSubmit,
}: {
  onSubmit: (rating: number, comment: string) => void;
}) {
  const [rating, setRating] = useState(5);
  const [comment, setComment] = useState('');
  return (
    <section
      aria-label="服务评价"
      className="bg-warning-50 border border-warning-200 rounded-xl p-3 text-sm"
    >
      <div className="font-medium text-warning-700 mb-1">
        已为您转接人工客服。在等待期间，您可以为本次服务打个分：
      </div>
      <div className="flex items-center gap-2 mt-2" role="radiogroup" aria-label="评分">
        {[1, 2, 3, 4, 5].map((n) => (
          <button
            key={n}
            type="button"
            role="radio"
            aria-checked={rating === n}
            aria-label={`${n} 星`}
            onClick={() => setRating(n)}
            className={`px-2 py-1 rounded-md border focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-warning-500 ${
              rating === n
                ? 'bg-warning-500 text-white border-warning-500'
                : 'bg-app-surface text-app-text border-app-border'
            }`}
          >
            {'★'.repeat(n)}
          </button>
        ))}
      </div>
      <label htmlFor="feedback-comment" className="sr-only">
        评价留言
      </label>
      <textarea
        id="feedback-comment"
        value={comment}
        onChange={(e) => setComment(e.target.value)}
        placeholder="有什么想说的？（可选）"
        aria-label="评价留言"
        className="mt-2 w-full border border-warning-200 rounded-md px-2 py-1 text-xs bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-warning-500"
        rows={2}
      />
      <button
        type="button"
        onClick={() => onSubmit(rating, comment)}
        className="mt-2 bg-warning-500 hover:bg-warning-600 text-white text-xs px-3 py-1 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-warning-500"
      >
        提交评价
      </button>
    </section>
  );
}

// useMemo / useCallback 未在本文件中使用；如需新增请同步更新此处 import。
