import { useEffect, useRef, useState } from 'react';
import { api } from './api';
import type { ChatMessage, ChatResponse, FeedbackInput, Intent } from './types';

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

const INTENT_LABEL: Record<Intent, string> = {
  refund: '退款',
  order: '订单',
  tech: '技术支持',
  other: '其他',
  unknown: '未识别',
};

const INTENT_COLOR: Record<Intent, string> = {
  refund: 'bg-rose-100 text-rose-700',
  order: 'bg-amber-100 text-amber-700',
  tech: 'bg-violet-100 text-violet-700',
  other: 'bg-slate-100 text-slate-700',
  unknown: 'bg-slate-100 text-slate-500',
};

interface UiMsg extends ChatMessage {
  pending?: boolean;
  intent?: Intent;
  confidence?: number;
  source?: string;
}

export default function App() {
  const [userId] = useState(loadOrCreateUserId);
  const [conversationId, setConversationId] = useState<string | undefined>(() => localStorage.getItem(CONV_KEY) || undefined);
  const [messages, setMessages] = useState<UiMsg[]>([]);
  const [input, setInput] = useState('');
  const [loading, setLoading] = useState(false);
  const [lastResp, setLastResp] = useState<ChatResponse | null>(null);
  const [feedbackSubmitted, setFeedbackSubmitted] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages.length]);

  async function send() {
    if (!input.trim() || loading) return;
    setError(null);
    const userText = input.trim();
    setInput('');
    setLoading(true);
    const optimistic: UiMsg = {
      id: 'pending-' + Date.now(),
      role: 'user',
      content: userText,
      createdAt: new Date().toISOString(),
      pending: true,
    };
    setMessages((m) => [...m, optimistic]);
    try {
      const resp = await api.chat({
        conversationId,
        userId,
        content: userText,
      });
      setLastResp(resp);
      setConversationId(resp.conversationId);
      localStorage.setItem(CONV_KEY, resp.conversationId);
      setMessages((m) => [
        ...m.filter((x) => x.id !== optimistic.id),
        {
          ...resp.userMessage,
          intent: resp.intent,
          confidence: resp.intentConfidence,
          source: resp.source,
        },
        { ...resp.message },
      ]);
      setFeedbackSubmitted(false);
    } catch (e: any) {
      setError(e?.response?.data?.message || e?.message || '请求失败');
      setMessages((m) => m.filter((x) => x.id !== optimistic.id));
    } finally {
      setLoading(false);
    }
  }

  async function submitFeedback(rating: number, comment: string) {
    if (!conversationId) return;
    try {
      await api.feedback({ conversationId, rating, comment });
      setFeedbackSubmitted(true);
    } catch (e: any) {
      setError(e?.response?.data?.message || '反馈失败');
    }
  }

  function reset() {
    setConversationId(undefined);
    setMessages([]);
    setLastResp(null);
    setFeedbackSubmitted(false);
    localStorage.removeItem(CONV_KEY);
  }

  return (
    <div className="flex flex-col h-full max-w-3xl mx-auto w-full p-4 gap-4">
      <header className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-slate-800">智能客服 Agent</h1>
          <p className="text-xs text-slate-500">基于 Jev 意图识别 + LLM 自由回答 + 自动转人工</p>
        </div>
        <button onClick={reset} className="text-xs text-slate-500 hover:text-rose-600 underline">新会话</button>
      </header>

      <main className="flex-1 bg-white rounded-2xl shadow-sm border border-slate-200 p-4 flex flex-col gap-3 overflow-hidden">
        <div className="flex-1 overflow-y-auto flex flex-col gap-3 pr-2">
          {messages.length === 0 && (
            <div className="text-slate-400 text-sm text-center my-auto">
              您好，我是您的智能客服「小助理」。可以问我关于退款、订单、技术问题等。
            </div>
          )}
          {messages.map((m) => (
            <Bubble key={m.id} m={m} />
          ))}
          {loading && (
            <div className="self-start bg-white border border-slate-200 rounded-2xl rounded-bl-sm px-4 py-2 text-sm text-slate-400 animate-pulse">
              正在思考…
            </div>
          )}
          <div ref={bottomRef} />
        </div>

        {error && (
          <div className="text-rose-700 bg-rose-50 border border-rose-200 px-3 py-2 rounded-lg text-sm">
            {error}
          </div>
        )}

        {lastResp?.handedOver && !feedbackSubmitted && (
          <FeedbackPanel onSubmit={submitFeedback} />
        )}
        {feedbackSubmitted && (
          <div className="text-emerald-700 bg-emerald-50 border border-emerald-200 px-3 py-2 rounded-lg text-sm">
            已收到您的评价，感谢反馈！
          </div>
        )}

        <div className="flex items-end gap-2 border-t border-slate-100 pt-3">
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                send();
              }
            }}
            placeholder="输入您的问题，Enter 发送，Shift+Enter 换行"
            className="flex-1 resize-none border border-slate-200 rounded-xl px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-brand-500/40"
            rows={2}
            disabled={loading}
          />
          <button
            onClick={send}
            disabled={loading || !input.trim()}
            className="bg-brand-500 hover:bg-brand-600 text-white text-sm font-medium px-4 py-2 rounded-xl disabled:opacity-40 disabled:cursor-not-allowed"
          >
            发送
          </button>
        </div>
      </main>

      <footer className="text-center text-xs text-slate-400">Powered by Jev · {new Date().toLocaleDateString('zh-CN')}</footer>
    </div>
  );
}

function Bubble({ m }: { m: UiMsg }) {
  const isUser = m.role === 'user';
  const cls = isUser ? 'bubble-user' : m.role === 'agent' ? 'bubble-agent' : 'bubble-assistant';
  return (
    <div className={`flex ${isUser ? 'justify-end' : 'justify-start'}`}>
      <div className={cls + ' relative'}>
        {m.intent && m.intent !== 'unknown' && isUser && (
          <div className="text-xs mb-1 opacity-80">
            <span className={`tag ${INTENT_COLOR[m.intent]}`}>{INTENT_LABEL[m.intent]}</span>
            {typeof m.confidence === 'number' && (
              <span className="text-white/70 ml-1">置信度 {(m.confidence * 100).toFixed(0)}%</span>
            )}
          </div>
        )}
        <div>{m.content}</div>
        {m.role === 'assistant' && m.source && (
          <div className="text-[10px] text-slate-400 mt-1">
            via {m.source}
          </div>
        )}
      </div>
    </div>
  );
}

function FeedbackPanel({ onSubmit }: { onSubmit: (rating: number, comment: string) => void }) {
  const [rating, setRating] = useState(5);
  const [comment, setComment] = useState('');
  return (
    <div className="bg-amber-50 border border-amber-200 rounded-xl p-3 text-sm">
      <div className="font-medium text-amber-800 mb-1">已为您转接人工客服。在等待期间，您可以为本次服务打个分：</div>
      <div className="flex items-center gap-2 mt-2">
        {[1, 2, 3, 4, 5].map((n) => (
          <button
            key={n}
            onClick={() => setRating(n)}
            className={`px-2 py-1 rounded-md border ${
              rating === n ? 'bg-amber-500 text-white border-amber-500' : 'bg-white text-slate-700 border-slate-200'
            }`}
          >
            {'★'.repeat(n)}
          </button>
        ))}
      </div>
      <textarea
        value={comment}
        onChange={(e) => setComment(e.target.value)}
        placeholder="有什么想说的？（可选）"
        className="mt-2 w-full border border-amber-200 rounded-md px-2 py-1 text-xs"
        rows={2}
      />
      <button
        onClick={() => onSubmit(rating, comment)}
        className="mt-2 bg-amber-500 hover:bg-amber-600 text-white text-xs px-3 py-1 rounded-md"
      >
        提交评价
      </button>
    </div>
  );
}