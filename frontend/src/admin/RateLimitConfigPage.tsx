import { useEffect, useState } from 'react';
import { api } from '../api';
import type { RateLimitConfig } from '../types';
import { Skeleton } from '../components/Skeleton';
import { EmptyState } from '../components/EmptyState';

export default function RateLimitConfigPage() {
  const [configs, setConfigs] = useState<RateLimitConfig[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [editDraft, setEditDraft] = useState<{
    per_minute: number;
    per_hour: number;
    burst: number;
    enabled: boolean;
    description: string;
  }>({
    per_minute: 60,
    per_hour: 1000,
    burst: 10,
    enabled: true,
    description: '',
  });

  async function reload() {
    setLoading(true);
    try {
      const r = await api.ratelimit.list();
      setConfigs(r.items);
      setError(null);
    } catch (e: any) {
      setError(e?.response?.data?.message || '加载失败');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    reload();
  }, []);

  function startEdit(c: RateLimitConfig) {
    setEditing(c.id);
    setEditDraft({
      per_minute: c.per_minute,
      per_hour: c.per_hour,
      burst: c.burst,
      enabled: c.enabled,
      description: c.description,
    });
  }

  async function save() {
    if (!editing) return;
    try {
      await api.ratelimit.update(editing, editDraft);
      setEditing(null);
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '保存失败');
    }
  }

  return (
    <div className="space-y-6">
      <header>
        <h2 className="text-xl font-semibold text-app-text">限流配置</h2>
        <p className="text-xs text-app-text-muted">
          接口级 token bucket 限流。修改后立即生效，无需重启。
        </p>
      </header>

      {error && (
        <div role="alert" className="bg-danger-50 border border-danger-200 text-danger-700 text-sm rounded p-2">
          {error}
        </div>
      )}

      {loading ? (
        <div
          className="bg-app-surface border border-app-border rounded-xl p-4 space-y-3"
          aria-label="正在加载限流配置"
        >
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} variant="text" height="1.5rem" />
          ))}
        </div>
      ) : configs.length === 0 ? (
        <EmptyState variant="noData" title="暂无配置" description="未配置任何限流项。" />
      ) : (
        <div className="bg-app-surface border border-app-border rounded-xl overflow-hidden">
          <div className="table-responsive">
            <table className="w-full text-sm">
              <thead className="bg-app-surface-muted text-xs text-app-text-muted uppercase">
                <tr>
                  <th className="text-left px-3 py-2">端点</th>
                  <th className="text-left px-3 py-2 hidden sm:table-cell">维度</th>
                  <th className="text-right px-3 py-2">每分钟</th>
                  <th className="text-right px-3 py-2 hidden md:table-cell">每小时</th>
                  <th className="text-right px-3 py-2 hidden md:table-cell">突发</th>
                  <th className="text-left px-3 py-2">启用</th>
                  <th className="text-left px-3 py-2 hidden lg:table-cell">描述</th>
                  <th className="text-right px-3 py-2">操作</th>
                </tr>
              </thead>
              <tbody>
                {configs.map((c) => {
                  const isEditing = editing === c.id;
                  return (
                    <tr key={c.id} className="border-t border-app-border">
                      <td className="px-3 py-2 font-mono text-xs text-app-text">{c.endpoint}</td>
                      <td className="px-3 py-2 hidden sm:table-cell">
                        <span className="inline-block px-2 py-0.5 text-xs rounded bg-app-surface-muted text-app-text">
                          {c.dimension}
                        </span>
                      </td>
                      <td className="px-3 py-2 text-right text-app-text">
                        {isEditing ? (
                          <label className="sr-only" htmlFor={`pm-${c.id}`}>
                            每分钟
                          </label>
                        ) : null}
                        {isEditing ? (
                          <input
                            id={`pm-${c.id}`}
                            type="number"
                            min={1}
                            value={editDraft.per_minute}
                            onChange={(e) =>
                              setEditDraft({ ...editDraft, per_minute: parseInt(e.target.value) || 0 })
                            }
                            className="w-20 border border-app-border rounded px-2 py-1 text-right text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                          />
                        ) : (
                          c.per_minute
                        )}
                      </td>
                      <td className="px-3 py-2 text-right text-app-text hidden md:table-cell">
                        {isEditing ? (
                          <input
                            type="number"
                            min={1}
                            value={editDraft.per_hour}
                            onChange={(e) =>
                              setEditDraft({ ...editDraft, per_hour: parseInt(e.target.value) || 0 })
                            }
                            className="w-20 border border-app-border rounded px-2 py-1 text-right text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                          />
                        ) : (
                          c.per_hour
                        )}
                      </td>
                      <td className="px-3 py-2 text-right text-app-text hidden md:table-cell">
                        {isEditing ? (
                          <input
                            type="number"
                            min={1}
                            value={editDraft.burst}
                            onChange={(e) =>
                              setEditDraft({ ...editDraft, burst: parseInt(e.target.value) || 0 })
                            }
                            className="w-20 border border-app-border rounded px-2 py-1 text-right text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                          />
                        ) : (
                          c.burst
                        )}
                      </td>
                      <td className="px-3 py-2">
                        {isEditing ? (
                          <label className="inline-flex items-center gap-2">
                            <input
                              type="checkbox"
                              checked={editDraft.enabled}
                              onChange={(e) => setEditDraft({ ...editDraft, enabled: e.target.checked })}
                              className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                            />
                            <span className="text-xs text-app-text">启用</span>
                          </label>
                        ) : c.enabled ? (
                          <span className="inline-block px-2 py-0.5 text-xs rounded bg-success-100 text-success-700">
                            ✓ 启用
                          </span>
                        ) : (
                          <span className="inline-block px-2 py-0.5 text-xs rounded bg-slate-100 text-slate-500">
                            禁用
                          </span>
                        )}
                      </td>
                      <td className="px-3 py-2 text-xs text-app-text-muted hidden lg:table-cell">
                        {isEditing ? (
                          <input
                            type="text"
                            value={editDraft.description}
                            onChange={(e) =>
                              setEditDraft({ ...editDraft, description: e.target.value })
                            }
                            className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                          />
                        ) : (
                          c.description
                        )}
                      </td>
                      <td className="px-3 py-2 text-right space-x-2 whitespace-nowrap">
                        {isEditing ? (
                          <>
                            <button
                              type="button"
                              onClick={() => setEditing(null)}
                              className="text-xs px-2 py-1 rounded border border-app-border text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                            >
                              取消
                            </button>
                            <button
                              type="button"
                              onClick={save}
                              className="text-xs px-2 py-1 rounded bg-brand-500 hover:bg-brand-600 text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500"
                            >
                              💾 保存
                            </button>
                          </>
                        ) : (
                          <button
                            type="button"
                            onClick={() => startEdit(c)}
                            aria-label={`编辑 ${c.endpoint}`}
                            className="text-xs px-2 py-1 rounded border border-app-border text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                          >
                            ✏️ 编辑
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
