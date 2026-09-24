import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../api';
import type { ListConversationsResponse } from '../types';

export default function ConversationListPage() {
  const [data, setData] = useState<ListConversationsResponse | null>(null);
  const [page, setPage] = useState(1);
  const [status, setStatus] = useState<string>('');
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    setErr(null);
    try {
      const r = await api.listConversations({ page, size: 30, status: status || undefined });
      setData(r);
    } catch (e: any) {
      setErr(e?.response?.data?.message || '加载失败');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, status]);

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold text-slate-800">会话记录</h2>
        <div className="flex items-center gap-2 text-sm">
          <select
            value={status}
            onChange={(e) => { setStatus(e.target.value); setPage(1); }}
            className="border border-slate-200 rounded-md px-2 py-1"
          >
            <option value="">全部</option>
            <option value="open">进行中</option>
            <option value="handed_over">已转人工</option>
            <option value="closed">已关闭</option>
          </select>
          <button onClick={load} className="text-xs px-2 py-1 border border-slate-200 rounded-md hover:bg-slate-50">刷新</button>
        </div>
      </div>

      {err && <div className="text-rose-700 bg-rose-50 px-3 py-2 rounded-md text-sm mb-3">{err}</div>}

      <div className="bg-white border border-slate-200 rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-slate-50 text-slate-600">
            <tr>
              <th className="text-left px-4 py-2">会话</th>
              <th className="text-left px-4 py-2">用户</th>
              <th className="text-left px-4 py-2">状态</th>
              <th className="text-left px-4 py-2">更新时间</th>
            </tr>
          </thead>
          <tbody>
            {data?.items?.length ? data.items.map((c) => (
              <tr key={c.id} className="border-t border-slate-100 hover:bg-slate-50">
                <td className="px-4 py-2">
                  <Link to={`/admin/conversations/${c.id}`} className="text-brand-600 hover:underline">
                    {c.title || c.id.slice(0, 8)}
                  </Link>
                </td>
                <td className="px-4 py-2 text-slate-500">{c.userId}</td>
                <td className="px-4 py-2">
                  <StatusBadge status={c.status} />
                </td>
                <td className="px-4 py-2 text-slate-500">{new Date(c.updatedAt).toLocaleString('zh-CN')}</td>
              </tr>
            )) : (
              <tr><td colSpan={4} className="text-center text-slate-400 py-6">{loading ? '加载中…' : '暂无数据'}</td></tr>
            )}
          </tbody>
        </table>
      </div>

      {data && (
        <div className="flex items-center justify-between mt-3 text-xs text-slate-500">
          <span>共 {data.total} 条 · 第 {data.page} 页</span>
          <div className="flex gap-2">
            <button disabled={page <= 1} onClick={() => setPage((p) => p - 1)} className="px-2 py-1 border border-slate-200 rounded disabled:opacity-40">上一页</button>
            <button disabled={page * data.pageSize >= data.total} onClick={() => setPage((p) => p + 1)} className="px-2 py-1 border border-slate-200 rounded disabled:opacity-40">下一页</button>
          </div>
        </div>
      )}
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  const map: Record<string, string> = {
    open: 'bg-emerald-100 text-emerald-700',
    handed_over: 'bg-amber-100 text-amber-700',
    closed: 'bg-slate-100 text-slate-500',
  };
  const label: Record<string, string> = {
    open: '进行中',
    handed_over: '已转人工',
    closed: '已关闭',
  };
  return <span className={`tag ${map[status] || 'bg-slate-100 text-slate-500'}`}>{label[status] || status}</span>;
}