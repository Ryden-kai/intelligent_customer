import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { api } from '../api';
import type { ChatMessage, ConversationDetail, SkillInvocation } from '../types';
import { Skeleton } from '../components/Skeleton';
import { EmptyState } from '../components/EmptyState';

export default function ConversationDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [data, setData] = useState<ConversationDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [reply, setReply] = useState('');
  const [sending, setSending] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function load() {
    if (!id) return;
    setLoading(true);
    try {
      const r = await api.getConversation(id);
      setData(r);
      setErr(null);
    } catch (e: any) {
      setErr(e?.response?.data?.message || '加载失败');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  async function send() {
    if (!id || !reply.trim() || sending) return;
    setSending(true);
    setErr(null);
    try {
      await api.postAgentReply(id, reply.trim());
      setReply('');
      await load();
    } catch (e: any) {
      setErr(e?.response?.data?.message || '发送失败');
    } finally {
      setSending(false);
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4 gap-2 flex-wrap">
        <div className="min-w-0">
          <h2 className="text-lg font-semibold text-app-text truncate">
            {data?.conversation.title || data?.conversation.id || '会话详情'}
          </h2>
          {data && (
            <p className="text-xs text-app-text-muted">
              会话 ID：{data.conversation.id} · 用户：{data.conversation.userId} · 状态：
              {data.conversation.status}
            </p>
          )}
        </div>
        <button
          type="button"
          onClick={load}
          aria-label="刷新会话详情"
          className="text-xs px-2 py-1 border border-app-border rounded-md text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
        >
          刷新
        </button>
      </div>

      {err && (
        <div role="alert" className="text-danger-700 bg-danger-50 border border-danger-200 px-3 py-2 rounded-md text-sm mb-3">
          {err}
        </div>
      )}

      {loading && !data ? (
        <div className="bg-app-surface border border-app-border rounded-xl p-4 space-y-3" aria-label="正在加载会话">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="flex gap-2">
              <Skeleton variant="circle" width={32} height={32} />
              <div className="flex-1 space-y-1">
                <Skeleton variant="text" width="30%" />
                <Skeleton variant="text" width="80%" />
              </div>
            </div>
          ))}
        </div>
      ) : data ? (
        <>
          <div
            className="bg-app-surface border border-app-border rounded-xl p-4 flex flex-col gap-3 max-h-[60vh] overflow-y-auto"
            aria-label="会话消息列表"
          >
            {data.messages.length === 0 ? (
              <EmptyState variant="noData" title="暂无消息" description="该会话还没有任何消息记录。" />
            ) : (
              data.messages.map((m) => <DetailBubble key={m.id} m={m} />)
            )}
          </div>

          {data.skillInvocations && data.skillInvocations.length > 0 && (
            <section
              aria-label="Skill 调用记录"
              className="mt-4 bg-app-surface border border-app-border rounded-xl p-4"
            >
              <div className="flex items-center justify-between mb-2">
                <h3 className="text-sm font-semibold text-app-text">Skill 调用记录</h3>
                <span className="text-xs text-app-text-muted">{data.skillInvocations.length} 条</span>
              </div>
              <div className="space-y-2">
                {data.skillInvocations.map((inv) => (
                  <InvocationRow key={inv.id} inv={inv} />
                ))}
              </div>
            </section>
          )}

          {data.conversation.handedOver && (
            <div className="mt-4 flex gap-2">
              <label htmlFor="agent-reply" className="sr-only">
                人工回复
              </label>
              <textarea
                id="agent-reply"
                value={reply}
                onChange={(e) => setReply(e.target.value)}
                placeholder="输入人工回复..."
                aria-label="人工回复"
                className="flex-1 border border-app-border rounded-md px-3 py-2 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                rows={2}
              />
              <button
                type="button"
                onClick={send}
                disabled={sending || !reply.trim()}
                aria-label="发送回复"
                className="bg-success-500 hover:bg-success-600 text-white text-sm px-4 rounded-md disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-success-500"
              >
                发送
              </button>
            </div>
          )}
        </>
      ) : null}
    </div>
  );
}

function DetailBubble({ m }: { m: ChatMessage }) {
  const isUser = m.role === 'user';
  const cls = isUser ? 'bubble-user' : m.role === 'agent' ? 'bubble-agent' : 'bubble-assistant';
  return (
    <div className={`flex ${isUser ? 'justify-end' : 'justify-start'}`}>
      <div className={cls}>
        <div className="text-[10px] opacity-70 mb-1">
          {roleLabel(m.role)} {m.model ? `· ${m.model}` : ''}{' '}
          {m.intent && m.intent !== 'unknown' ? `· ${m.intent}` : ''}
        </div>
        <div>{m.content}</div>
      </div>
    </div>
  );
}

function roleLabel(r: string): string {
  return { user: '用户', assistant: 'AI', agent: '客服', system: '系统' }[r] || r;
}

function InvocationRow({ inv }: { inv: SkillInvocation }) {
  const statusColor: Record<string, string> = {
    ok: 'bg-success-100 text-success-700',
    pending_human: 'bg-warning-100 text-warning-700',
    error: 'bg-danger-100 text-danger-700',
    timeout: 'bg-slate-100 text-slate-600',
  };
  const color = statusColor[inv.status] || 'bg-slate-100 text-slate-600';
  return (
    <div className="border border-app-border rounded-md p-2 text-xs">
      <div className="flex items-center gap-2 mb-1 flex-wrap">
        <span className={`px-2 py-0.5 rounded-full text-[10px] ${color}`}>{inv.status}</span>
        <span className="font-mono font-medium text-app-text">{inv.skillName}</span>
        <span className="text-app-text-muted">step #{inv.stepIndex}</span>
        <span className="text-app-text-muted ml-auto">{inv.durationMs}ms</span>
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
        <div>
          <div className="text-app-text-muted mb-0.5">args</div>
          <pre className="bg-app-surface-muted border border-app-border rounded px-2 py-1 overflow-x-auto font-mono text-[11px] text-app-text">
            {tryFormatJson(inv.argsJson)}
          </pre>
        </div>
        <div>
          <div className="text-app-text-muted mb-0.5">result</div>
          <pre className="bg-app-surface-muted border border-app-border rounded px-2 py-1 overflow-x-auto font-mono text-[11px] text-app-text">
            {tryFormatJson(inv.resultJson || '(empty)')}
          </pre>
        </div>
      </div>
      {inv.pendingTicket && (
        <div className="mt-1 text-warning-700 text-[11px]">
          待人工确认 ticket: <span className="font-mono">{inv.pendingTicket}</span>
        </div>
      )}
      {inv.traceId && (
        <div className="mt-1 text-app-text-muted text-[11px] font-mono">trace_id: {inv.traceId}</div>
      )}
    </div>
  );
}

function tryFormatJson(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}
