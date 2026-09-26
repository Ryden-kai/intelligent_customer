import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import type { AuditLog } from '../types';
import { AUDIT_ACTIONS } from '../types';
import { Skeleton } from '../components/Skeleton';
import { EmptyState } from '../components/EmptyState';
import { ChartContainer, CHART_COLORS } from '../components/ChartContainer';
import { PieChart, Pie, Cell, Legend, Tooltip, ResponsiveContainer } from 'recharts';

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

  // Action distribution (top 8) derived from the current page's logs.
  const actionDist = useMemo(() => {
    const map = new Map<string, number>();
    for (const lg of logs) {
      map.set(lg.action, (map.get(lg.action) ?? 0) + 1);
    }
    return Array.from(map.entries())
      .map(([action, count], i) => ({
        name: action,
        value: count,
        color: [
          CHART_COLORS.primary,
          CHART_COLORS.secondary,
          CHART_COLORS.warning,
          CHART_COLORS.danger,
          CHART_COLORS.info,
          CHART_COLORS.pink,
          CHART_COLORS.violet,
          CHART_COLORS.cyan,
        ][i % 8],
      }))
      .sort((a, b) => b.value - a.value)
      .slice(0, 8);
  }, [logs]);

  return (
    <div className="space-y-6">
      <header className="flex items-center justify-between gap-2 flex-wrap">
        <div>
          <h2 className="text-xl font-semibold text-app-text">审计日志</h2>
          <p className="text-xs text-app-text-muted">8 类关键操作的流水记录。可按时间、操作者、动作、目标类型筛选。</p>
        </div>
        <button
          type="button"
          onClick={exportCsv}
          aria-label="导出 CSV"
          className="text-xs px-3 py-1 rounded border border-app-border text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
        >
          📥 导出 CSV
        </button>
      </header>

      {error && (
        <div role="alert" className="bg-danger-50 border border-danger-200 text-danger-700 text-sm rounded p-2">
          {error}
        </div>
      )}

      <div className="bg-app-surface border border-app-border rounded-xl p-4">
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-3">
          <div>
            <label htmlFor="audit-from" className="block text-xs text-app-text-muted mb-1">起始时间</label>
            <input
              id="audit-from"
              type="datetime-local"
              value={filter.from}
              onChange={(e) => setFilter({ ...filter, from: e.target.value })}
              className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            />
          </div>
          <div>
            <label htmlFor="audit-to" className="block text-xs text-app-text-muted mb-1">结束时间</label>
            <input
              id="audit-to"
              type="datetime-local"
              value={filter.to}
              onChange={(e) => setFilter({ ...filter, to: e.target.value })}
              className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            />
          </div>
          <div>
            <label htmlFor="audit-actor" className="block text-xs text-app-text-muted mb-1">操作者 ID</label>
            <input
              id="audit-actor"
              type="text"
              value={filter.actor_id}
              onChange={(e) => setFilter({ ...filter, actor_id: e.target.value })}
              placeholder="如 admin@demo"
              className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            />
          </div>
          <div>
            <label htmlFor="audit-action" className="block text-xs text-app-text-muted mb-1">动作</label>
            <select
              id="audit-action"
              value={filter.action}
              onChange={(e) => setFilter({ ...filter, action: e.target.value })}
              className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
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
            <label htmlFor="audit-target" className="block text-xs text-app-text-muted mb-1">目标类型</label>
            <select
              id="audit-target"
              value={filter.target_type}
              onChange={(e) => setFilter({ ...filter, target_type: e.target.value })}
              className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
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
            type="button"
            onClick={applyFilter}
            className="text-xs px-3 py-1.5 rounded bg-brand-500 hover:bg-brand-600 text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500"
          >
            🔍 搜索
          </button>
          <button
            type="button"
            onClick={resetFilter}
            className="text-xs px-3 py-1.5 rounded border border-app-border text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          >
            ↺ 重置
          </button>
        </div>
      </div>

      {loading ? (
        <div
          className="bg-app-surface border border-app-border rounded-xl p-4 space-y-3"
          aria-label="正在加载审计日志"
        >
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="flex gap-3 items-center">
              <Skeleton variant="text" width="30%" />
              <Skeleton variant="text" width="20%" />
              <Skeleton variant="text" width="20%" />
            </div>
          ))}
        </div>
      ) : actionDist.length > 0 ? (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div className="md:col-span-1">
            <ChartContainer title="Action 分布（Top 8）" height={280}>
              <PieChart>
                <Pie
                  data={actionDist}
                  dataKey="value"
                  nameKey="name"
                  cx="50%"
                  cy="50%"
                  outerRadius={90}
                  label
                >
                  {actionDist.map((entry, idx) => (
                    <Cell key={idx} fill={entry.color} />
                  ))}
                </Pie>
                <Tooltip />
                <Legend />
              </PieChart>
            </ChartContainer>
          </div>
          <div className="md:col-span-2 bg-app-surface border border-app-border rounded-xl p-4 text-xs text-app-text-muted">
            <h3 className="text-sm font-semibold text-app-text mb-2">说明</h3>
            <p>该饼图统计当前页查询结果内每种 action 的数量（最多 Top 8）。</p>
            <p>完整的 Action 分布请通过 CSV 导出后用 Excel / Pandas 做透视。</p>
          </div>
        </div>
      ) : null}

      {logs.length === 0 && !loading ? (
        <EmptyState
          variant={filter.actor_id || filter.action || filter.target_type || filter.from || filter.to ? 'noResult' : 'noData'}
          title={filter.actor_id || filter.action || filter.target_type || filter.from || filter.to ? '无匹配记录' : '暂无审计日志'}
          description={filter.actor_id || filter.action || filter.target_type || filter.from || filter.to ? '试试调整筛选条件，或者清空筛选。' : '还没有任何审计记录。'}
          action={
            filter.actor_id || filter.action || filter.target_type || filter.from || filter.to
              ? { label: '↺ 重置筛选', onClick: resetFilter, variant: 'secondary' }
              : undefined
          }
        />
      ) : (
        <div className="bg-app-surface border border-app-border rounded-xl overflow-hidden">
          <div className="table-responsive">
            <table className="w-full text-sm">
              <thead className="bg-app-surface-muted text-xs text-app-text-muted uppercase">
                <tr>
                  <th className="text-left px-3 py-2">时间</th>
                  <th className="text-left px-3 py-2">操作者</th>
                  <th className="text-left px-3 py-2">动作</th>
                  <th className="text-left px-3 py-2 hidden md:table-cell">目标</th>
                  <th className="text-left px-3 py-2 hidden lg:table-cell">IP</th>
                  <th className="text-right px-3 py-2">操作</th>
                </tr>
              </thead>
              <tbody>
                {logs.map((lg) => (
                  <tr
                    key={lg.id}
                    className="border-t border-app-border hover:bg-app-surface-muted cursor-pointer"
                    onClick={() => setSelected(lg)}
                  >
                    <td className="px-3 py-2 text-xs text-app-text">
                      {new Date(lg.timestamp).toLocaleString('zh-CN')}
                    </td>
                    <td className="px-3 py-2">
                      <div className="text-app-text">{lg.actor_id}</div>
                      {lg.actor_email && (
                        <div className="text-xs text-app-text-muted">{lg.actor_email}</div>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      <span className="inline-block px-2 py-0.5 text-xs rounded bg-app-surface-muted text-app-text">
                        {lg.action}
                      </span>
                    </td>
                    <td className="px-3 py-2 text-xs hidden md:table-cell">
                      {lg.target_type ? (
                        <span>
                          <span className="text-app-text-muted">{lg.target_type}:</span>
                          <span className="text-app-text ml-1">{lg.target_id}</span>
                        </span>
                      ) : (
                        <span className="text-app-text-muted">-</span>
                      )}
                    </td>
                    <td className="px-3 py-2 text-xs text-app-text-muted hidden lg:table-cell">{lg.ip || '-'}</td>
                    <td className="px-3 py-2 text-right">
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          setSelected(lg);
                        }}
                        aria-label={`查看审计记录 ${lg.id}`}
                        className="text-xs text-brand-600 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand rounded"
                      >
                        查看
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {!loading && logs.length > 0 && (
        <div className="flex items-center justify-between text-xs text-app-text-muted">
          <div>共 {total} 条</div>
          <div className="space-x-2">
            <button
              type="button"
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - pageSize))}
              aria-label="上一页"
              className="px-2 py-1 rounded border border-app-border text-app-text bg-app-surface disabled:opacity-30 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            >
              ‹ 上一页
            </button>
            <span>
              {currentPage} / {totalPages}
            </span>
            <button
              type="button"
              disabled={offset + pageSize >= total}
              onClick={() => setOffset(offset + pageSize)}
              aria-label="下一页"
              className="px-2 py-1 rounded border border-app-border text-app-surface bg-app-surface disabled:opacity-30 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            >
              下一页 ›
            </button>
          </div>
        </div>
      )}

      {selected && (
        <div
          role="dialog"
          aria-modal="true"
          aria-labelledby="audit-detail-title"
          className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4"
          onClick={(e) => {
            if (e.target === e.currentTarget) setSelected(null);
          }}
        >
          <div className="bg-app-surface rounded-xl w-full max-w-lg max-h-[85vh] overflow-auto shadow-xl border border-app-border">
            <div className="px-6 py-4 border-b border-app-border flex items-center justify-between">
              <h3 id="audit-detail-title" className="font-semibold text-app-text">审计记录详情</h3>
              <button
                type="button"
                onClick={() => setSelected(null)}
                aria-label="关闭详情"
                className="text-app-text-muted hover:text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand rounded"
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
                <div className="text-xs text-app-text-muted mb-1">Payload (JSON):</div>
                <pre className="text-xs bg-app-surface-muted border border-app-border rounded p-2 overflow-x-auto text-app-text">
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
      <div className="text-xs text-app-text-muted">{label}</div>
      <div className="col-span-2 text-app-text break-all">{value}</div>
    </div>
  );
}
