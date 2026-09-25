import { useEffect, useState } from 'react';
import { api } from '../api';
import type { AuditLog } from '../types';
import { AUDIT_ACTIONS } from '../types';

interface Filter {
  from: string;
  to: string;
  actor_id: string;
  action: string;
  target_type: string;
}

const EMPTY_FILTER: Filter = {
  from: '',
  to: '',
  actor_id: '',
  action: '',
  target_type: '',
};

const TARGET_TYPES = ['user', 'role', 'template', 'skill', 'conversation', 'ratelimit'];

export default function AuditLogPage() {
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<Filter>(EMPTY_FILTER);
  const [pageSize, setPageSize] = useState(50);
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState<AuditLog | null>(null);

  async function reload() {
    setLoading(true);
    try {
      const params: Record<string, string> = {};
      if (filter.from) params.from = new Date(filter.from).getTime().toString();
      if (filter.to) params.to = new Date(filter.to).getTime().toString();
      if (filter.actor_id) params.actor_id = filter.actor_id;
      if (filter.action) params.action = filter.action;
      if (filter.target_type) params.target_type = filter.target_type;
      params.limit = pageSize.toString();
      params.offset = offset.toString();
      const r = await api.audit.list(params);
      setLogs(r.items);
      setTotal(r.total);
      setError(null);
    } catch (e: any) {
      setError(e?.response?.data?.message || '加载失败');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pageSize, offset]);

  function applyFilter() {
    setOffset(0);
    reload();
  }

  function resetFilter() {
    setFilter(EMPTY_FILTER);
    setOffset(0);
    setTimeout(reload, 0);
  }

  function exportCsv() {
    const params: Record<string, string> = {};
    if (filter.from) params.from = new Date(filter.from).getTime().toString();
    if (filter.to) params.to = new Date(filter.to).getTime().toString();
    if (filter.actor_id) params.actor_id = filter.actor_id;
    if (filter.action) params.action = filter.action;
    const url = api.audit.exportUrl(params);
    // 通过 fetch 拿 blob 后下载，避免 401/403 时直接跳页面。
    fetch(url, { method: 'GET' })
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        return r.blob();
      })
      .then((blob) => {
        const a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = `audit_logs_${new Date().toISOString().slice(0, 10)}.csv`;
        a.click();
        URL.revokeObjectURL(a.href);
      })
      .catch((e: any) => setError(`导出失败: ${e.message}`));
  }

  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const currentPage = Math.floor(offset / pageSize) + 1;

  return (
    <div className="space-y-6">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-xl font-semibold text-slate-800">审计日志</h2>
          <p className="text-xs text-slate-500">8 类关键操作的流水记录。可按时间、操作者、动作、目标类型筛选。</p>
        </div>
        <button
          onClick={exportCsv}
          className="text-xs px-3 py-1 rounded border border-slate-300 hover:bg-slate-50"
        >
          📥 导出 CSV
        </button>
      </header>

      {error && (
        <div className="bg-rose-50 border border-rose-200 text-rose-700 text-sm rounded p-2">
          {error}
        </div>
      )}

      <div className="bg-white border border-slate-200 rounded-xl p-4">
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-3">
          <div>
            <label className="block text-xs text-slate-500 mb-1">起始时间</label>
            <input
              type="datetime-local"
              value={filter.from}
              onChange={(e) => setFilter({ ...filter, from: e.target.value })}
              className="w-full border border-slate-200 rounded px-2 py-1 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-slate-500 mb-1">结束时间</label>
            <input
              type="datetime-local"
              value={filter.to}
              onChange={(e) => setFilter({ ...filter, to: e.target.value })}
              className="w-full border border-slate-200 rounded px-2 py-1 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-slate-500 mb-1">操作者 ID</label>
            <input
              type="text"
              value={filter.actor_id}
              onChange={(e) => setFilter({ ...filter, actor_id: e.target.value })}
              placeholder="如 admin@demo"
              className="w-full border border-slate-200 rounded px-2 py-1 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-slate-500 mb-1">动作</label>
            <select
              value={filter.action}
              onChange={(e) => setFilter({ ...filter, action: e.target.value })}
              className="w-full border border-slate-200 rounded px-2 py-1 text-sm bg-white"
            >
              <option value="">全部</option>
              {AUDIT_ACTIONS.map((a) => (
                <option key={a} value={a}>
                  {a}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-xs text-slate-500 mb-1">目标类型</label>
            <select
              value={filter.target_type}
              onChange={(e) => setFilter({ ...filter, target_type: e.target.value })}
              className="w-full border border-slate-200 rounded px-2 py-1 text-sm bg-white"
            >
              <option value="">全部</option>
              {TARGET_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="flex gap-2 mt-3">
          <button
            onClick={applyFilter}
            className="text-xs px-3 py-1.5 rounded bg-brand-500 hover:bg-brand-600 text-white"
          >
            🔍 搜索
          </button>
          <button
            onClick={resetFilter}
            className="text-xs px-3 py-1.5 rounded border border-slate-300 hover:bg-slate-50"
          >
            ↺ 重置
          </button>
        </div>
      </div>

      <div className="bg-white border border-slate-200 rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-slate-50 text-xs text-slate-500 uppercase">
            <tr>
              <th className="text-left px-3 py-2">时间</th>
              <th className="text-left px-3 py-2">操作者</th>
              <th className="text-left px-3 py-2">动作</th>
              <th className="text-left px-3 py-2">目标</th>
              <th className="text-left px-3 py-2">IP</th>
              <th className="text-right px-3 py-2">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={6} className="px-3 py-6 text-center text-slate-500">加载中…</td>
              </tr>
            ) : logs.length === 0 ? (
              <tr>
                <td colSpan={6} className="px-3 py-6 text-center text-slate-500">无匹配记录</td>
              </tr>
            ) : (
              logs.map((lg) => (
                <tr
                  key={lg.id}
                  className="border-t border-slate-100 hover:bg-slate-50 cursor-pointer"
                  onClick={() => setSelected(lg)}
                >
                  <td className="px-3 py-2 text-xs text-slate-600">
                    {new Date(lg.timestamp).toLocaleString('zh-CN')}
                  </td>
                  <td className="px-3 py-2">
                    <div className="text-slate-800">{lg.actor_id}</div>
                    {lg.actor_email && (
                      <div className="text-xs text-slate-400">{lg.actor_email}</div>
                    )}
                  </td>
                  <td className="px-3 py-2">
                    <span className="inline-block px-2 py-0.5 text-xs rounded bg-slate-100 text-slate-700">
                      {lg.action}
                    </span>
                  </td>
                  <td className="px-3 py-2 text-xs">
                    {lg.target_type ? (
                      <span>
                        <span className="text-slate-500">{lg.target_type}:</span>
                        <span className="text-slate-800 ml-1">{lg.target_id}</span>
                      </span>
                    ) : (
                      <span className="text-slate-400">-</span>
                    )}
                  </td>
                  <td className="px-3 py-2 text-xs text-slate-500">{lg.ip || '-'}</td>
                  <td className="px-3 py-2 text-right">
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        setSelected(lg);
                      }}
                      className="text-xs text-brand-600 hover:underline"
                    >
                      查看
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <div className="flex items-center justify-between text-xs text-slate-500">
        <div>共 {total} 条</div>
        <div className="space-x-2">
          <button
            disabled={offset === 0}
            onClick={() => setOffset(Math.max(0, offset - pageSize))}
            className="px-2 py-1 rounded border border-slate-300 disabled:opacity-30"
          >
            ‹ 上一页
          </button>
          <span>
            {currentPage} / {totalPages}
          </span>
          <button
            disabled={offset + pageSize >= total}
            onClick={() => setOffset(offset + pageSize)}
            className="px-2 py-1 rounded border border-slate-300 disabled:opacity-30"
          >
            下一页 ›
          </button>
        </div>
      </div>

      {selected && (
        <div
          className="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
          onClick={(e) => {
            if (e.target === e.currentTarget) setSelected(null);
          }}
        >
          <div className="bg-white rounded-xl w-full max-w-lg max-h-[85vh] overflow-auto shadow-xl">
            <div className="px-6 py-4 border-b border-slate-200 flex items-center justify-between">
              <h3 className="font-semibold text-slate-800">审计记录详情</h3>
              <button
                onClick={() => setSelected(null)}
                className="text-slate-400 hover:text-slate-600"
              >
                ✕
              </button>
            </div>
            <div className="px-6 py-4 space-y-2 text-sm">
              <Row label="ID" value={selected.id} />
              <Row label="时间" value={new Date(selected.timestamp).toLocaleString('zh-CN')} />
              <Row label="操作者" value={`${selected.actor_id} (${selected.actor_email || '-'})`} />
              <Row label="动作" value={selected.action} />
              <Row
                label="目标"
                value={
                  selected.target_type
                    ? `${selected.target_type}: ${selected.target_id || '-'}`
                    : '-'
                }
              />
              <Row label="租户" value={selected.tenant_id} />
              <Row label="IP" value={selected.ip || '-'} />
              <Row label="User-Agent" value={selected.user_agent || '-'} />
              <div>
                <div className="text-xs text-slate-500 mb-1">Payload (JSON):</div>
                <pre className="text-xs bg-slate-50 border border-slate-200 rounded p-2 overflow-x-auto">
                  {selected.payload_json
                    ? JSON.stringify(selected.payload_json, null, 2)
                    : '(空)'}
                </pre>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-3 gap-2">
      <div className="text-xs text-slate-500">{label}</div>
      <div className="col-span-2 text-slate-800 break-all">{value}</div>
    </div>
  );
}