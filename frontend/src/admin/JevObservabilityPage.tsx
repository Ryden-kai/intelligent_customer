import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import type { JevDecision, JevStats, SSEStatsSnapshot } from '../types';
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ScatterChart,
  Scatter,
  ZAxis,
  ResponsiveContainer,
  CartesianGrid,
  Legend,
  Cell,
} from 'recharts';
import { Skeleton } from '../components/Skeleton';
import { EmptyState } from '../components/EmptyState';
import { ChartContainer, CHART_COLORS } from '../components/ChartContainer';
import { LiveBadge } from '../components/LiveBadge';
import { FilterPanel, FilterField } from '../components/FilterPanel';
import { useBatchSelection } from '../hooks/useBatchSelection';
import { useExportCsv } from '../hooks/useExportCsv';
import { useSSE } from '../hooks/useSSE';
import { getToken } from '../api';

interface Filter {
  from: string;
  to: string;
  template: string;
  label: string;
  actor_id: string;
  score_min: string;
  score_max: string;
}

const EMPTY_FILTER: Filter = {
  from: '',
  to: '',
  template: '',
  label: '',
  actor_id: '',
  score_min: '',
  score_max: '',
};

const LABEL_OPTIONS = [
  { value: 'clean', label: 'clean (通过)' },
  { value: 'sensitive', label: 'sensitive (敏感)' },
  { value: 'violate', label: 'violate (违规)' },
];

const FILTER_FIELDS: FilterField[] = [
  { id: 'from', label: '起始时间', type: 'datetime' },
  { id: 'to', label: '结束时间', type: 'datetime' },
  { id: 'template', label: '模板', type: 'text', placeholder: '如 intent_routing' },
  { id: 'label', label: '标签', type: 'select', options: LABEL_OPTIONS },
  { id: 'actor_id', label: '操作者', type: 'text', placeholder: 'actor id' },
];

export default function JevObservabilityPage() {
  const [stats, setStats] = useState<JevStats | null>(null);
  const [decisions, setDecisions] = useState<JevDecision[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<Filter>(EMPTY_FILTER);

  const selection = useBatchSelection();
  const exporter = useExportCsv();

  const sse = useSSE(api.stream.url(), {
    retryMs: 3000,
    maxRetries: 5,
    pollingUrl: '/api/admin/jev/stats',
    pollingIntervalMs: 5000,
    token: getToken(),
  });

  const sseSnap: SSEStatsSnapshot | undefined = useMemo(() => {
    if (!sse.lastEvent?.payload) return undefined;
    try {
      return JSON.parse(sse.lastEvent.payload) as SSEStatsSnapshot;
    } catch {
      return undefined;
    }
  }, [sse.lastEvent]);

  async function reload() {
    setLoading(true);
    try {
      const params: Record<string, string | number> = {
        limit: 100,
      };
      if (filter.template) params.template = filter.template;
      // trigger/label/actor/score filters are placeholder — server side
      // currently only filters by template/trigger/status; pass the ones
      // that are wired so admins still see the right slice.
      const [s, d] = await Promise.all([
        api.getJevStats(),
        api.listJevDecisions(params),
      ]);
      setStats(s);
      setDecisions(d.items);
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
  }, [filter.template]);

  async function review(id: string, status: 'accepted' | 'rejected') {
    try {
      await api.reviewJevDecision(id, { status });
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '标记失败');
    }
  }

  async function archiveSelected() {
    if (selection.count === 0) return;
    if (!confirm(`确认归档选中的 ${selection.count} 个模板？`)) return;
    try {
      const r = await api.jev.archiveBatch(selection.ids);
      selection.clear();
      await reload();
      if (r.succeeded < r.total) {
        setError(`已归档 ${r.succeeded}/${r.total} 个模板，部分失败`);
      }
    } catch (e: any) {
      setError(e?.response?.data?.message || '批量归档失败');
    }
  }

  function doExport() {
    const params: Record<string, string> = {};
    if (filter.template) params.template = filter.template;
    exporter.trigger(api.jev.exportDecisionsUrl(params), {
      filenamePrefix: 'jev_decisions',
      token: getToken(),
    });
  }

  // ---- charts ----
  const acceptRateByTemplate = useMemo(() => {
    if (!stats) return [];
    return stats.byTemplate.map((t) => ({
      name: t.template,
      rate: 1 - t.fallbackRatio,
      count: t.count,
    }));
  }, [stats]);

  const decisionScatter = useMemo(() => {
    if (decisions.length === 0) return [];
    return decisions.slice(0, 100).map((d, idx) => {
      let label: 'clean' | 'sensitive' | 'violate' | 'other' = 'other';
      try {
        const o = JSON.parse(d.outputJson);
        const out = typeof o?.label === 'string' ? o.label.toLowerCase() : '';
        if (out.includes('violate')) label = 'violate';
        else if (out.includes('sensitive')) label = 'sensitive';
        else if (out) label = 'clean';
      } catch {
        /* ignore */
      }
      return {
        x: idx,
        y: d.latencyMs,
        ts: new Date(d.createdAt).getTime(),
        z: d.costUsd * 1000 + 1,
        label,
      };
    });
  }, [decisions]);

  const liveP95 = sseSnap?.p95_ms ?? stats?.p95LatencyMs ?? 0;
  const liveTotal = sseSnap?.total_decisions ?? stats?.total ?? 0;
  const liveAcceptRate = sseSnap ? sseSnap.accept_rate : stats ? 1 - stats.fallbackRate : 0;

  return (
    <div className="space-y-6">
      <header className="flex items-center justify-between gap-2 flex-wrap">
        <div>
          <h2 className="text-xl font-semibold text-app-text">Jev 决策可观测性</h2>
          <p className="text-xs text-app-text-muted">
            决策占比 / 接受率 / P95 延迟 / 决策日志 + 人工标注（v2.3 闭环自动调优预留）。
          </p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <LiveBadge
            connected={sse.connected}
            polling={sse.polling}
            error={sse.error}
            lastUpdate={sseSnap?.timestamp}
          />
          <button
            type="button"
            onClick={doExport}
            disabled={exporter.loading}
            aria-label="导出决策日志 CSV"
            className="text-xs px-3 py-1 rounded border border-app-border text-app-text bg-app-surface hover:bg-app-surface-muted disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          >
            📥 导出 CSV
          </button>
        </div>
      </header>

      {error && (
        <div role="alert" className="bg-danger-50 border border-danger-200 text-danger-700 text-sm rounded p-2">
          {error}
        </div>
      )}
      {exporter.error && (
        <div role="alert" className="bg-danger-50 border border-danger-200 text-danger-700 text-sm rounded p-2">
          导出失败：{exporter.error}
        </div>
      )}

      <FilterPanel
        filters={filter}
        fields={FILTER_FIELDS}
        onApply={(next) => setFilter(next)}
        onReset={() => setFilter(EMPTY_FILTER)}
      />

      {/* KPI cards */}
      {loading && !stats ? (
        <div className="grid grid-cols-1 md:grid-cols-4 gap-4" aria-label="正在加载 Jev 统计">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="bg-app-surface border border-app-border rounded-xl p-4 space-y-2">
              <Skeleton variant="text" width="50%" />
              <Skeleton variant="text" width="70%" height="1.75rem" />
            </div>
          ))}
        </div>
      ) : stats ? (
        <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
          <Stat label="总决策数（实时）" value={liveTotal} />
          <Stat label="接受率（实时）" value={`${(liveAcceptRate * 100).toFixed(1)}%`} />
          <Stat label="P95 延迟" value={`${liveP95} ms`} />
          <Stat label="降级率" value={`${((sseSnap?.fallback_rate ?? stats.fallbackRate) * 100).toFixed(1)}%`} />
        </div>
      ) : null}

      {/* Charts */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <ChartContainer title="按模板接受率（水平柱状图）">
          {acceptRateByTemplate.length > 0 ? (
            <BarChart data={acceptRateByTemplate} layout="vertical">
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis type="number" domain={[0, 1]} stroke="currentColor" tickFormatter={(v) => `${(v * 100).toFixed(0)}%`} />
              <YAxis type="category" dataKey="name" stroke="currentColor" width={130} />
              <Tooltip formatter={(v: number) => `${(v * 100).toFixed(1)}%`} />
              <Bar dataKey="rate" name="接受率" fill={CHART_COLORS.secondary} radius={[0, 4, 4, 0]} />
            </BarChart>
          ) : (
            <div className="flex items-center justify-center h-full text-sm text-app-text-muted">
              暂无模板
            </div>
          )}
        </ChartContainer>

        <ChartContainer title="决策时间热力图（x=序号, y=延迟ms, 颜色=标签）">
          {decisionScatter.length > 0 ? (
            <ScatterChart>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis type="number" dataKey="x" name="序号" stroke="currentColor" />
              <YAxis type="number" dataKey="y" name="延迟ms" stroke="currentColor" />
              <ZAxis type="number" dataKey="z" range={[40, 200]} />
              <Tooltip cursor={{ strokeDasharray: '3 3' }} />
              <Legend />
              {(['clean', 'sensitive', 'violate', 'other'] as const).map((lab, idx) => (
                <Scatter
                  key={lab}
                  name={lab}
                  data={decisionScatter.filter((d) => d.label === lab)}
                  fill={[CHART_COLORS.secondary, CHART_COLORS.warning, CHART_COLORS.danger, CHART_COLORS.info][idx]}
                />
              ))}
            </ScatterChart>
          ) : (
            <div className="flex items-center justify-center h-full text-sm text-app-text-muted">
              等待决策数据…
            </div>
          )}
        </ChartContainer>
      </div>

      {/* by-template table */}
      {stats && stats.byTemplate.length > 0 && (
        <div className="bg-app-surface border border-app-border rounded-xl p-4">
          <h3 className="text-sm font-semibold text-app-text mb-3">按模板分布</h3>
          <div className="table-responsive">
            <table className="w-full text-sm">
              <thead className="text-xs text-app-text-muted uppercase">
                <tr>
                  <th className="text-left py-1">template</th>
                  <th className="text-right py-1">count</th>
                  <th className="text-right py-1">fallback</th>
                  <th className="text-right py-1">fallback ratio</th>
                </tr>
              </thead>
              <tbody>
                {stats.byTemplate.map((row) => (
                  <tr key={row.template} className="border-t border-app-border">
                    <td className="py-1 font-mono text-xs text-app-text">{row.template}</td>
                    <td className="text-right text-app-text">{row.count}</td>
                    <td className="text-right text-app-text">{row.fallback}</td>
                    <td className="text-right text-app-text">
                      {(row.fallbackRatio * 100).toFixed(1)}%
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* decision log table with checkboxes (batch archive) */}
      <div className="flex items-center justify-between flex-wrap gap-2">
        <div className="flex items-center gap-2 text-xs text-app-text-muted">
          {selection.count > 0 ? (
            <>
              <span>已选 {selection.count} 条</span>
              <button
                type="button"
                onClick={selection.clear}
                className="underline"
              >
                清除选择
              </button>
            </>
          ) : (
            <span>勾选多行后可批量归档（仅 DB 模板）</span>
          )}
        </div>
        <button
          type="button"
          onClick={archiveSelected}
          disabled={selection.count === 0}
          aria-label="批量归档选中"
          className="text-xs px-3 py-1 rounded bg-warning-500 hover:bg-warning-600 text-white disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-warning-500"
        >
          ⛔ 批量归档选中（{selection.count}）
        </button>
      </div>

      {loading ? (
        <div
          className="bg-app-surface border border-app-border rounded-xl p-4 space-y-3"
          aria-label="正在加载决策日志"
        >
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} variant="text" height="1.5rem" />
          ))}
        </div>
      ) : decisions.length === 0 ? (
        <EmptyState variant="noData" title="暂无决策记录" description="还没有任何 Jev 决策日志。" />
      ) : (
        <div className="bg-app-surface border border-app-border rounded-xl overflow-hidden">
          <div className="table-responsive">
            <table className="w-full text-sm">
              <thead className="bg-app-surface-muted text-xs text-app-text-muted uppercase">
                <tr>
                  <th className="px-3 py-2 w-8">
                    <input
                      type="checkbox"
                      aria-label="全选"
                      checked={selection.count > 0 && selection.count === decisions.length}
                      onChange={(e) => {
                        if (e.target.checked) selection.selectAll(decisions.map((d) => d.id));
                        else selection.clear();
                      }}
                      className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                    />
                  </th>
                  <th className="text-left px-3 py-2">time</th>
                  <th className="text-left px-3 py-2">template</th>
                  <th className="text-left px-3 py-2 hidden md:table-cell">trigger</th>
                  <th className="text-right px-3 py-2">latency</th>
                  <th className="text-left px-3 py-2 hidden sm:table-cell">fallback</th>
                  <th className="text-left px-3 py-2 hidden md:table-cell">status</th>
                  <th className="text-right px-3 py-2">actions</th>
                </tr>
              </thead>
              <tbody>
                {decisions.map((d) => (
                  <tr key={d.id} className="border-t border-app-border hover:bg-app-surface-muted">
                    <td className="px-3 py-2">
                      <input
                        type="checkbox"
                        aria-label={`选择 ${d.id}`}
                        checked={selection.isSelected(d.id)}
                        onChange={() => selection.toggle(d.id)}
                        className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                      />
                    </td>
                    <td className="px-3 py-2 text-xs text-app-text-muted">
                      {new Date(d.createdAt).toLocaleString()}
                    </td>
                    <td className="px-3 py-2 text-xs font-mono text-app-text">{d.templateName}</td>
                    <td className="px-3 py-2 text-xs font-mono text-app-text-muted hidden md:table-cell">
                      {d.trigger}
                    </td>
                    <td className="px-3 py-2 text-xs text-right text-app-text">{d.latencyMs} ms</td>
                    <td className="px-3 py-2 text-xs hidden sm:table-cell">
                      {d.fallback ? (
                        <span className="px-2 py-0.5 rounded-full bg-warning-100 text-warning-700">是</span>
                      ) : (
                        <span className="px-2 py-0.5 rounded-full bg-success-100 text-success-700">否</span>
                      )}
                    </td>
                    <td className="px-3 py-2 text-xs text-app-text hidden md:table-cell">{d.status}</td>
                    <td className="px-3 py-2 text-right text-xs space-x-1 whitespace-nowrap">
                      {d.status === 'decided' && (
                        <>
                          <button
                            type="button"
                            onClick={() => review(d.id, 'accepted')}
                            aria-label={`接受决策 ${d.id}`}
                            className="text-success-600 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand rounded"
                          >
                            接受
                          </button>
                          <button
                            type="button"
                            onClick={() => review(d.id, 'rejected')}
                            aria-label={`拒绝决策 ${d.id}`}
                            className="text-danger-600 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-danger-500 rounded"
                          >
                            拒绝
                          </button>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="bg-app-surface border border-app-border rounded-xl p-4">
      <div className="text-xs text-app-text-muted">{label}</div>
      <div className="text-2xl font-semibold text-app-text mt-1">{value}</div>
    </div>
  );
}

// keep Cell import alive to avoid "imported but not used" if tree-shaken
void Cell;