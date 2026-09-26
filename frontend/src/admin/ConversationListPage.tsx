import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, getToken } from '../api';
import type { ListConversationsResponse, Conversation } from '../types';
import { Skeleton } from '../components/Skeleton';
import { EmptyState } from '../components/EmptyState';
import { ChartContainer, CHART_COLORS } from '../components/ChartContainer';
import { FilterPanel, FilterField } from '../components/FilterPanel';
import { useBatchSelection } from '../hooks/useBatchSelection';
import { useExportCsv } from '../hooks/useExportCsv';
import { PieChart, Pie, Cell, Legend, Tooltip } from 'recharts';

interface Filter {
  status: string;
  user_id: string;
  keyword: string;
  from: string;
  to: string;
}

const EMPTY_FILTER: Filter = {
  status: '',
  user_id: '',
  keyword: '',
  from: '',
  to: '',
};

const STATUS_OPTIONS = [
  { value: 'open', label: '进行中' },
  { value: 'handed_over', label: '已转人工' },
  { value: 'closed', label: '已关闭' },
];

const FILTER_FIELDS: FilterField[] = [
  { id: 'from', label: '起始时间', type: 'datetime' },
  { id: 'to', label: '结束时间', type: 'datetime' },
  { id: 'status', label: '状态', type: 'select', options: STATUS_OPTIONS },
  { id: 'user_id', label: '用户 ID', type: 'text', placeholder: '搜索 user_id' },
];

const PALETTE = [CHART_COLORS.primary, CHART_COLORS.warning, CHART_COLORS.info];

export default function ConversationListPage() {
  const [data, setData] = useState<ListConversationsResponse | null>(null);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [filter, setFilter] = useState<Filter>(EMPTY_FILTER);
  const [tagLabel, setTagLabel] = useState('priority');

  const selection = useBatchSelection();
  const exporter = useExportCsv();

  async function load() {
    setLoading(true);
    setErr(null);
    try {
      const params: Record<string, string | number> = {
        page,
        size: 50,
      };
      if (filter.status) params.status = filter.status;
      const r = await api.listConversations(params);
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
  }, [page, filter.status]);

  // donut chart: status distribution
  const statusDist = useMemo(() => {
    if (!data) return [];
    const map = new Map<string, number>();
    for (const c of data.items) {
      map.set(c.status, (map.get(c.status) ?? 0) + 1);
    }
    return Array.from(map.entries()).map(([status, value], i) => ({
      name: status,
      value,
      color: PALETTE[i % PALETTE.length],
    }));
  }, [data]);

  function doExport() {
    const params: Record<string, string> = {};
    if (filter.status) params.status = filter.status;
    exporter.trigger(api.conversations.exportUrl(params), {
      filenamePrefix: 'conversations',
      token: getToken(),
    });
  }

  async function applyTag() {
    if (selection.count === 0) return;
    const label = tagLabel.trim();
    if (!label) return;
    if (!confirm(`确认给选中的 ${selection.count} 条会话打标 "${label}"？`)) return;
    try {
      const r = await api.conversations.tagBatch(selection.ids, label);
      selection.clear();
      await load();
      if (r.succeeded < r.total) {
        setErr(`已打标 ${r.succeeded}/${r.total} 条`);
      }
    } catch (e: any) {
      setErr(e?.response?.data?.message || '批量打标失败');
    }
  }

  // local keyword/user_id filter (client-side; backend currently doesn't support)
  const visibleItems = useMemo<Conversation[]>(() => {
    if (!data) return [];
    return data.items.filter((c) => {
      if (filter.user_id && !c.userId.toLowerCase().includes(filter.user_id.toLowerCase())) return false;
      if (filter.keyword) {
        const kw = filter.keyword.toLowerCase();
        if (
          !c.title.toLowerCase().includes(kw) &&
          !c.userId.toLowerCase().includes(kw)
        ) {
          return false;
        }
      }
      return true;
    });
  }, [data, filter.user_id, filter.keyword]);

  return (
    <div>
      <div className="flex items-center justify-between mb-4 gap-2 flex-wrap">
        <h2 className="text-lg font-semibold text-app-text">会话记录</h2>
        <div className="flex items-center gap-2 text-sm flex-wrap">
          <button
            type="button"
            onClick={doExport}
            disabled={exporter.loading}
            aria-label="导出 CSV"
            className="text-xs px-2 py-1 border border-app-border rounded-md text-app-text bg-app-surface hover:bg-app-surface-muted disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          >
            📥 导出 CSV
          </button>
          <button
            type="button"
            onClick={load}
            aria-label="刷新会话列表"
            className="text-xs px-2 py-1 border border-app-border rounded-md text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          >
            刷新
          </button>
        </div>
      </div>

      {err && (
        <div
          role="alert"
          className="text-danger-700 bg-danger-50 border border-danger-200 px-3 py-2 rounded-md text-sm mb-3"
        >
          {err}
        </div>
      )}
      {exporter.error && (
        <div
          role="alert"
          className="text-danger-700 bg-danger-50 border border-danger-200 px-3 py-2 rounded-md text-sm mb-3"
        >
          导出失败：{exporter.error}
        </div>
      )}

      {/* Status distribution donut */}
      {data && data.items.length > 0 && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-4">
          <div className="md:col-span-1">
            <ChartContainer title="会话状态分布">
              {statusDist.length > 0 ? (
                <PieChart>
                  <Pie
                    data={statusDist}
                    dataKey="value"
                    nameKey="name"
                    cx="50%"
                    cy="50%"
                    innerRadius={50}
                    outerRadius={80}
                    label
                  >
                    {statusDist.map((entry, idx) => (
                      <Cell key={idx} fill={entry.color} />
                    ))}
                  </Pie>
                  <Tooltip />
                  <Legend />
                </PieChart>
              ) : (
                <div className="flex items-center justify-center h-full text-sm text-app-text-muted">
                  暂无数据
                </div>
              )}
            </ChartContainer>
          </div>
          <div className="md:col-span-2 bg-app-surface border border-app-border rounded-xl p-4 text-xs text-app-text-muted">
            <h3 className="text-sm font-semibold text-app-text mb-2">说明</h3>
            <p>本页展示当前筛选下的会话环形图（按 status）。</p>
            <p>勾选多行后可在右上角下拉选择标签进行批量打标（写入会话标题前缀）。</p>
            <p>CSV 导出包含当前 status 筛选的全部命中记录（上限 10000 行）。</p>
          </div>
        </div>
      )}

      <FilterPanel
        filters={filter}
        fields={FILTER_FIELDS}
        onApply={(next) => {
          setFilter(next);
          setPage(1);
        }}
        onReset={() => {
          setFilter(EMPTY_FILTER);
          setPage(1);
        }}
      />

      {/* Local keyword (client-side) */}
      <div className="mt-3">
        <label htmlFor="conv-kw" className="sr-only">
          关键词
        </label>
        <input
          id="conv-kw"
          type="text"
          value={filter.keyword}
          onChange={(e) => setFilter({ ...filter, keyword: e.target.value })}
          placeholder="关键词（客户端过滤：title / userId）"
          className="w-full md:w-1/2 border border-app-border rounded-md px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
        />
      </div>

      {/* Bulk action bar */}
      <div className="flex items-center gap-2 mt-3 flex-wrap">
        <span className="text-xs text-app-text-muted">
          {selection.count > 0 ? `已选 ${selection.count} 条` : '勾选多行后可批量打标'}
        </span>
        <input
          type="text"
          value={tagLabel}
          onChange={(e) => setTagLabel(e.target.value)}
          placeholder="标签名"
          className="border border-app-border rounded px-2 py-1 text-xs bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
        />
        <button
          type="button"
          onClick={applyTag}
          disabled={selection.count === 0 || !tagLabel.trim()}
          aria-label="批量打标"
          className="text-xs px-3 py-1 rounded bg-brand-500 hover:bg-brand-600 text-white disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500"
        >
          🏷️ 批量打标（{selection.count}）
        </button>
        {selection.count > 0 && (
          <button type="button" onClick={selection.clear} className="text-xs underline text-app-text-muted">
            清除
          </button>
        )}
      </div>

      {loading ? (
        <div
          className="bg-app-surface border border-app-border rounded-xl p-4 space-y-3 mt-4"
          aria-label="正在加载会话列表"
        >
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="flex items-center gap-3">
              <Skeleton variant="circle" width={32} height={32} />
              <div className="flex-1 space-y-1">
                <Skeleton variant="text" width="40%" />
                <Skeleton variant="text" width="80%" />
              </div>
            </div>
          ))}
        </div>
      ) : visibleItems.length === 0 ? (
        <div className="mt-4">
          <EmptyState
            variant={filter.user_id || filter.keyword || filter.status ? 'noResult' : 'noData'}
            title="暂无会话记录"
            description={filter.user_id || filter.keyword || filter.status ? '试试调整筛选条件' : '还没有任何会话'}
          />
        </div>
      ) : (
        <div className="bg-app-surface border border-app-border rounded-xl overflow-hidden mt-4">
          <div className="table-responsive">
            <table className="w-full text-sm">
              <thead className="bg-app-surface-muted text-app-text-muted">
                <tr>
                  <th className="px-4 py-2 w-8">
                    <input
                      type="checkbox"
                      aria-label="全选"
                      checked={selection.count > 0 && selection.count === visibleItems.length}
                      onChange={(e) => {
                        if (e.target.checked) selection.selectAll(visibleItems.map((c) => c.id));
                        else selection.clear();
                      }}
                      className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                    />
                  </th>
                  <th className="text-left px-4 py-2">会话</th>
                  <th className="text-left px-4 py-2">用户</th>
                  <th className="text-left px-4 py-2">状态</th>
                  <th className="text-left px-4 py-2">更新时间</th>
                </tr>
              </thead>
              <tbody>
                {visibleItems.map((c) => (
                  <tr key={c.id} className="border-t border-app-border hover:bg-app-surface-muted">
                    <td className="px-4 py-2">
                      <input
                        type="checkbox"
                        aria-label={`选择会话 ${c.id}`}
                        checked={selection.isSelected(c.id)}
                        onChange={() => selection.toggle(c.id)}
                        className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                      />
                    </td>
                    <td className="px-4 py-2">
                      <Link
                        to={`/admin/conversations/${c.id}`}
                        className="text-brand-600 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand rounded"
                      >
                        {c.title || c.id.slice(0, 8)}
                      </Link>
                    </td>
                    <td className="px-4 py-2 text-app-text-muted">{c.userId}</td>
                    <td className="px-4 py-2">
                      <StatusBadge status={c.status} />
                    </td>
                    <td className="px-4 py-2 text-app-text-muted">
                      {new Date(c.updatedAt).toLocaleString('zh-CN')}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {data && data.items && data.items.length > 0 && (
        <div className="flex items-center justify-between mt-3 text-xs text-app-text-muted">
          <span>共 {data.total} 条 · 第 {data.page} 页</span>
          <div className="flex gap-2">
            <button
              type="button"
              disabled={page <= 1}
              onClick={() => setPage((p) => p - 1)}
              aria-label="上一页"
              className="px-2 py-1 border border-app-border rounded text-app-text bg-app-surface disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            >
              上一页
            </button>
            <button
              type="button"
              disabled={page * data.pageSize >= data.total}
              onClick={() => setPage((p) => p + 1)}
              aria-label="下一页"
              className="px-2 py-1 border border-app-border rounded text-app-text bg-app-surface disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            >
              下一页
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  const map: Record<string, string> = {
    open: 'bg-success-100 text-success-700',
    handed_over: 'bg-warning-100 text-warning-700',
    closed: 'bg-slate-100 text-slate-500',
  };
  const label: Record<string, string> = {
    open: '进行中',
    handed_over: '已转人工',
    closed: '已关闭',
  };
  return (
    <span className={`tag ${map[status] || 'bg-slate-100 text-slate-500'}`}>
      {label[status] || status}
    </span>
  );
}