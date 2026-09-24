import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { api } from '../api';
import type { ConversationDetail, ChatMessage } from '../types';

export default function ConversationDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [data, setData] = useState<ConversationDetail | null>(null);
  const [reply, setReply] = useState('');
  const [sending, setSending] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function load() {
    if (!id) return;
    try {
      const r = await api.getConversation(id);
      setData(r);
    } catch (e: any) {
      setErr(e?.response?.data?.message || '加载失败');
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

  if (!data) return <div className="text-slate-500">{err || '加载中…'}</div>;

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-lg font-semibold text-slate-800">{data.conversation.title || data.conversation.id}</h2>
          <p className="text-xs text-slate-500">
            会话 ID：{data.conversation.id} · 用户：{data.conversation.userId} · 状态：{data.conversation.status}
          </p>
        </div>
        <button onClick={load} className="text-xs px-2 py-1 border border-slate-200 rounded-md hover:bg-slate-50">刷新</button>
      </div>

      {err && <div className="text-rose-700 bg-rose-50 px-3 py-2 rounded-md text-sm mb-3">{err}</div>}

      <div className="bg-white border border-slate-200 rounded-xl p-4 flex flex-col gap-3 max-h-[60vh] overflow-y-auto">
        {data.messages.map((m) => <DetailBubble key={m.id} m={m} />)}
      </div>

      {data.conversation.handedOver && (
        <div className="mt-4 flex gap-2">
          <textarea
            value={reply}
            onChange={(e) => setReply(e.target.value)}
            placeholder="输入人工回复..."
            className="flex-1 border border-slate-200 rounded-md px-3 py-2 text-sm"
            rows={2}
          />
          <button
            onClick={send}
            disabled={sending || !reply.trim()}
            className="bg-emerald-500 hover:bg-emerald-600 text-white text-sm px-4 rounded-md disabled:opacity-40"
          >
            发送
          </button>
        </div>
      )}
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
          {roleLabel(m.role)} {m.model ? `· ${m.model}` : ''} {m.intent && m.intent !== 'unknown' ? `· ${m.intent}` : ''}
        </div>
        <div>{m.content}</div>
      </div>
    </div>
  );
}

function roleLabel(r: string): string {
  return { user: '用户', assistant: 'AI', agent: '客服', system: '系统' }[r] || r;
}