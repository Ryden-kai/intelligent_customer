import { useEffect, useState } from 'react';
import { api } from '../api';
import type { RateLimitConfig } from '../types';

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
        <h2 className="text-xl font-semibold text-slate-800">限流配置</h2>
        <p className="text-xs text-slate-500">
          接口级 token bucket 限流。修改后立即生效，无需重启。
        </p>
      </header>

      {error && (
        <div className="bg-rose-50 border border-rose-200 text-rose-700 text-sm rounded p-2">
          {error}
        </div>
      )}

      <div className="bg-white border border-slate-200 rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-slate-50 text-xs text-slate-500 uppercase">
            <tr>
              <th className="text-left px-3 py-2">端点</th>
              <th className="text-left px-3 py-2">维度</th>
              <th className="text-right px-3 py-2">每分钟</th>
              <th className="text-right px-3 py-2">每小时</th>
              <th className="text-right px-3 py-2">突发</th>
              <th className="text-left px-3 py-2">启用</th>
              <th className="text-left px-3 py-2">描述</th>
              <th className="text-right px-3 py-2">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={8} className="px-3 py-6 text-center text-slate-500">加载中…</td>
              </tr>
            ) : configs.length === 0 ? (
              <tr>
                <td colSpan={8} className="px-3 py-6 text-center text-slate-500">暂无配置</td>
              </tr>
            ) : (
              configs.map((c) => {
                const isEditing = editing === c.id;
                return (
                  <tr key={c.id} className="border-t border-slate-100">
                    <td className="px-3 py-2 font-mono text-xs">{c.endpoint}</td>
                    <td className="px-3 py-2">
                      <span className="inline-block px-2 py-0.5 text-xs rounded bg-slate-100 text-slate-700">
                        {c.dimension}
                      </span>
                    </td>
                    <td className="px-3 py-2 text-right">
                      {isEditing ? (
                        <input
                          type="number"
                          min={1}
                          value={editDraft.per_minute}
                          onChange={(e) =>
                            setEditDraft({ ...editDraft, per_minute: parseInt(e.target.value) || 0 })
                          }
                          className="w-20 border border-slate-200 rounded px-2 py-1 text-right text-sm"
                        />
                      ) : (
                        c.per_minute
                      )}
                    </td>
                    <td className="px-3 py-2 text-right">
                      {isEditing ? (
                        <input
                          type="number"
                          min={1}
                          value={editDraft.per_hour}
                          onChange={(e) =>
                            setEditDraft({ ...editDraft, per_hour: parseInt(e.target.value) || 0 })
                          }
                          className="w-20 border border-slate-200 rounded px-2 py-1 text-right text-sm"
                        />
                      ) : (
                        c.per_hour
                      )}
                    </td>
                    <td className="px-3 py-2 text-right">
                      {isEditing ? (
                        <input
                          type="number"
                          min={1}
                          value={editDraft.burst}
                          onChange={(e) =>
                            setEditDraft({ ...editDraft, burst: parseInt(e.target.value) || 0 })
                          }
                          className="w-20 border border-slate-200 rounded px-2 py-1 text-right text-sm"
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
                          />
                          <span className="text-xs">启用</span>
                        </label>
                      ) : c.enabled ? (
                        <span className="inline-block px-2 py-0.5 text-xs rounded bg-emerald-100 text-emerald-700">
                          ✓ 启用
                        </span>
                      ) : (
                        <span className="inline-block px-2 py-0.5 text-xs rounded bg-slate-100 text-slate-500">
                          禁用
                        </span>
                      )}
                    </td>
                    <td className="px-3 py-2 text-xs text-slate-600">
                      {isEditing ? (
                        <input
                          type="text"
                          value={editDraft.description}
                          onChange={(e) =>
                            setEditDraft({ ...editDraft, description: e.target.value })
                          }
                          className="w-full border border-slate-200 rounded px-2 py-1 text-sm"
                        />
                      ) : (
                        c.description
                      )}
                    </td>
                    <td className="px-3 py-2 text-right space-x-2">
                      {isEditing ? (
                        <>
                          <button
                            onClick={() => setEditing(null)}
                            className="text-xs px-2 py-1 rounded border border-slate-300 hover:bg-slate-50"
                          >
                            取消
                          </button>
                          <button
                            onClick={save}
                            className="text-xs px-2 py-1 rounded bg-brand-500 hover:bg-brand-600 text-white"
                          >
                            💾 保存
                          </button>
                        </>
                      ) : (
                        <button
                          onClick={() => startEdit(c)}
                          className="text-xs px-2 py-1 rounded border border-slate-300 hover:bg-slate-50"
                        >
                          ✏️ 编辑
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}