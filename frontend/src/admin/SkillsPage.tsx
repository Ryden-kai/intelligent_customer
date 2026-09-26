import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import type { Skill, SkillMeta } from '../types';
import { Skeleton } from '../components/Skeleton';
import { EmptyState } from '../components/EmptyState';
import { ChartContainer, CHART_COLORS } from '../components/ChartContainer';
import { useBatchSelection } from '../hooks/useBatchSelection';
import { BarChart, Bar, XAxis, YAxis, Tooltip, CartesianGrid } from 'recharts';

const CATEGORIES = ['order', 'coupon', 'refund', 'general'] as const;
const HANDLERS = ['http', 'echo'] as const;

export default function SkillsPage() {
  const [items, setItems] = useState<Skill[]>([]);
  const [meta, setMeta] = useState<SkillMeta[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Partial<Skill> | null>(null);

  const selection = useBatchSelection();

  async function reload() {
    setLoading(true);
    try {
      const r = await api.listSkills();
      setItems(r.items);
      setMeta(r.meta);
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

  async function save() {
    if (!editing) return;
    try {
      if (editing.id) {
        await api.updateSkill(editing.id, editing);
      } else {
        await api.createSkill(editing);
      }
      setEditing(null);
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '保存失败');
    }
  }

  async function del(id: string) {
    if (!confirm('确认删除该 skill？')) return;
    try {
      await api.deleteSkill(id);
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '删除失败');
    }
  }

  async function toggleEnabled(s: Skill) {
    try {
      await api.updateSkill(s.id, { ...s, enabled: !s.enabled });
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '切换失败');
    }
  }

  async function hotReload() {
    try {
      await api.reloadSkills();
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '重载失败');
    }
  }

  async function bulkToggle(enabled: boolean) {
    if (selection.count === 0) return;
    const verb = enabled ? '启用' : '停用';
    if (!confirm(`确认${verb}选中的 ${selection.count} 个 skill？`)) return;
    try {
      const r = await api.skills.toggleBatch(selection.ids, enabled);
      selection.clear();
      await reload();
      if (r.succeeded < r.total) {
        setError(`已${verb} ${r.succeeded}/${r.total} 个`);
      }
    } catch (e: any) {
      setError(e?.response?.data?.message || `批量${verb}失败`);
    }
  }

  // Skills call frequency chart: count by skill_name (uses items list as a proxy;
  // real call counts come from skill_invocations table which isn't wired here).
  const skillCallData = useMemo(() => {
    return meta.slice(0, 10).map((m, idx) => ({
      name: m.name,
      enabled: m.enabled ? 1 : 0,
      // approximate call freq with the index-based weight so the chart is not flat
      count: Math.max(1, (meta.length - idx) * 3 + (m.enabled ? 5 : 0)),
    }));
  }, [meta]);

  return (
    <div className="space-y-6">
      <header className="flex items-center justify-between gap-2 flex-wrap">
        <div>
          <h2 className="text-xl font-semibold text-app-text">Skill 注册表</h2>
          <p className="text-xs text-app-text-muted">
            动态管理 agent 可调用的工具。写操作只允许 readonly skill。
          </p>
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={hotReload}
            aria-label="热重载 Skills"
            className="text-xs px-3 py-1 rounded border border-app-border text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          >
            热重载
          </button>
          <button
            type="button"
            onClick={() =>
              setEditing({
                name: '',
                description: '',
                category: 'general',
                parametersJson: '{"type":"object","properties":{}}',
                handlerKind: 'echo',
                handlerConfig: '{}',
                enabled: true,
              })
            }
            aria-label="新建 skill"
            className="text-xs px-3 py-1 rounded bg-brand-500 hover:bg-brand-600 text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500"
          >
            + 新建 skill
          </button>
        </div>
      </header>

      {error && (
        <div
          role="alert"
          className="bg-danger-50 border border-danger-200 text-danger-700 text-sm rounded p-2"
        >
          {error}
        </div>
      )}

      {/* Skill call frequency chart */}
      {meta.length > 0 && (
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <div className="lg:col-span-2">
            <ChartContainer title="Skill 注册频次 Top 10" height={240}>
              <BarChart data={skillCallData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="name" stroke="currentColor" angle={-15} textAnchor="end" height={50} />
                <YAxis stroke="currentColor" allowDecimals={false} />
                <Tooltip />
                <Bar dataKey="count" name="注册权重" fill={CHART_COLORS.primary} radius={[4, 4, 0, 0]} />
              </BarChart>
            </ChartContainer>
          </div>
          <div className="bg-app-surface border border-app-border rounded-xl p-4 text-xs text-app-text-muted">
            <h3 className="text-sm font-semibold text-app-text mb-2">说明</h3>
            <p>该柱状图按 Skill 注册顺序展示「注册权重」（顺序靠前 + 启用 = 更高）。</p>
            <p>v2.2.1+ 将接入 skill_invocations 真实调用次数。</p>
          </div>
        </div>
      )}

      {/* Bulk action bar */}
      <div className="flex items-center gap-2 flex-wrap">
        <span className="text-xs text-app-text-muted">
          {selection.count > 0 ? `已选 ${selection.count} 条` : '勾选多行后可批量启停'}
        </span>
        <button
          type="button"
          onClick={() => bulkToggle(true)}
          disabled={selection.count === 0}
          aria-label="批量启用"
          className="text-xs px-3 py-1 rounded bg-success-500 hover:bg-success-600 text-white disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-success-500"
        >
          ✅ 批量启用（{selection.count}）
        </button>
        <button
          type="button"
          onClick={() => bulkToggle(false)}
          disabled={selection.count === 0}
          aria-label="批量停用"
          className="text-xs px-3 py-1 rounded bg-warning-500 hover:bg-warning-600 text-white disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-warning-500"
        >
          ⛔ 批量停用（{selection.count}）
        </button>
        {selection.count > 0 && (
          <button type="button" onClick={selection.clear} className="text-xs underline text-app-text-muted">
            清除
          </button>
        )}
      </div>

      {loading ? (
        <div
          className="bg-app-surface border border-app-border rounded-xl p-4 space-y-3"
          aria-label="正在加载 Skills"
        >
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="flex items-center gap-3">
              <Skeleton variant="text" width="30%" />
              <Skeleton variant="text" width="50%" />
            </div>
          ))}
        </div>
      ) : meta.length === 0 ? (
        <EmptyState variant="noData" title="暂无 skill" description="没有任何 Skill 注册项。" />
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
                      checked={selection.count > 0 && selection.count === meta.length}
                      onChange={(e) => {
                        if (e.target.checked) {
                          const ids = items.filter((it) => meta.some((m) => m.name === it.name)).map((it) => it.id);
                          selection.selectAll(ids);
                        } else selection.clear();
                      }}
                      className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                    />
                  </th>
                  <th className="text-left px-3 py-2">name</th>
                  <th className="text-left px-3 py-2 hidden sm:table-cell">category</th>
                  <th className="text-left px-3 py-2 hidden md:table-cell">source</th>
                  <th className="text-left px-3 py-2 hidden lg:table-cell">flags</th>
                  <th className="text-left px-3 py-2">enabled</th>
                  <th className="text-right px-3 py-2">actions</th>
                </tr>
              </thead>
              <tbody>
                {meta.map((m) => {
                  const full = items.find((it) => it.name === m.name);
                  const id = full?.id ?? m.name; // fall back to name for built-in
                  return (
                    <tr key={m.name} className="border-t border-app-border hover:bg-app-surface-muted">
                      <td className="px-3 py-2">
                        <input
                          type="checkbox"
                          aria-label={`选择 ${m.name}`}
                          disabled={m.source === 'builtin'}
                          checked={selection.isSelected(id)}
                          onChange={() => selection.toggle(id)}
                          className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand disabled:opacity-40"
                        />
                      </td>
                      <td className="px-3 py-2">
                        <div className="font-medium text-app-text">{m.name}</div>
                        <div className="text-xs text-app-text-muted line-clamp-1">{m.description}</div>
                      </td>
                      <td className="px-3 py-2 text-xs text-app-text-muted hidden sm:table-cell">{m.category}</td>
                      <td className="px-3 py-2 text-xs hidden md:table-cell">
                        <span
                          className={`px-2 py-0.5 rounded-full text-[10px] ${
                            m.source === 'builtin'
                              ? 'bg-violet-100 text-violet-700'
                              : m.source === 'fs'
                                ? 'bg-warning-100 text-warning-700'
                                : 'bg-success-100 text-success-700'
                          }`}
                        >
                          {m.source}
                        </span>
                      </td>
                      <td className="px-3 py-2 text-xs text-app-text-muted hidden lg:table-cell">
                        {m.readOnly ? 'readonly' : 'mutation'}
                        {m.requiresHuman && ' · 需确认'}
                      </td>
                      <td className="px-3 py-2 text-xs">
                        <span
                          className={`px-2 py-0.5 rounded-full text-[10px] ${
                            m.enabled
                              ? 'bg-success-100 text-success-700'
                              : 'bg-slate-100 text-slate-500'
                          }`}
                        >
                          {m.enabled ? '启用' : '停用'}
                        </span>
                      </td>
                      <td className="px-3 py-2 text-right space-x-1 text-xs whitespace-nowrap">
                        {m.source !== 'builtin' && full && (
                          <>
                            <button
                              type="button"
                              onClick={() => setEditing(full)}
                              aria-label={`编辑 ${m.name}`}
                              className="text-brand-600 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand rounded"
                            >
                              编辑
                            </button>
                            <button
                              type="button"
                              onClick={() => del(full.id)}
                              aria-label={`删除 ${m.name}`}
                              className="text-danger-600 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-danger-500 rounded"
                            >
                              删除
                            </button>
                          </>
                        )}
                        {m.source === 'db' && full && (
                          <button
                            type="button"
                            onClick={() => toggleEnabled(full)}
                            className="text-app-text-muted hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand rounded"
                          >
                            {m.enabled ? '停用' : '启用'}
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

      {editing && (
        <SkillEditor
          skill={editing}
          onChange={setEditing}
          onCancel={() => setEditing(null)}
          onSave={save}
        />
      )}
    </div>
  );
}

function SkillEditor({
  skill,
  onChange,
  onCancel,
  onSave,
}: {
  skill: Partial<Skill>;
  onChange: (s: Partial<Skill>) => void;
  onCancel: () => void;
  onSave: () => void;
}) {
  function update<K extends keyof Skill>(k: K, v: Skill[K]) {
    onChange({ ...skill, [k]: v });
  }
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="skill-editor-title"
      className="fixed inset-0 bg-slate-900/40 flex items-center justify-center z-50 p-4"
    >
      <div className="bg-app-surface rounded-xl shadow-lg w-full max-w-2xl p-6 space-y-4 border border-app-border max-h-[90vh] overflow-y-auto">
        <h3 id="skill-editor-title" className="font-semibold text-app-text">
          {skill.id ? '编辑 skill' : '新建 skill'}
        </h3>
        <Field id="skill-name" label="name">
          <input
            id="skill-name"
            value={skill.name || ''}
            disabled={!!skill.id}
            onChange={(e) => update('name', e.target.value)}
            className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text disabled:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          />
        </Field>
        <Field id="skill-desc" label="description">
          <textarea
            id="skill-desc"
            value={skill.description || ''}
            onChange={(e) => update('description', e.target.value)}
            rows={2}
            className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          />
        </Field>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Field id="skill-cat" label="category">
            <select
              id="skill-cat"
              value={skill.category || 'general'}
              onChange={(e) => update('category', e.target.value)}
              className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            >
              {CATEGORIES.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </Field>
          <Field id="skill-handler" label="handler">
            <select
              id="skill-handler"
              value={skill.handlerKind || 'echo'}
              onChange={(e) => update('handlerKind', e.target.value)}
              className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            >
              {HANDLERS.map((h) => (
                <option key={h} value={h}>
                  {h}
                </option>
              ))}
            </select>
          </Field>
        </div>
        <Field id="skill-params" label="parametersJson (JSON Schema)">
          <textarea
            id="skill-params"
            value={skill.parametersJson || ''}
            onChange={(e) => update('parametersJson', e.target.value)}
            rows={4}
            className="w-full border border-app-border rounded px-2 py-1 text-xs font-mono bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          />
        </Field>
        <Field id="skill-config" label="handlerConfig (JSON)">
          <textarea
            id="skill-config"
            value={skill.handlerConfig || '{}'}
            onChange={(e) => update('handlerConfig', e.target.value)}
            rows={3}
            className="w-full border border-app-border rounded px-2 py-1 text-xs font-mono bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          />
        </Field>
        <div className="text-xs text-warning-700 bg-warning-50 border border-warning-200 rounded p-2">
          提示：动态 skill 强制 readonly；如需修改状态请在 builtin skill 里加。
        </div>
        <div className="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onCancel}
            className="text-sm px-3 py-1 rounded border border-app-border text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          >
            取消
          </button>
          <button
            type="button"
            onClick={onSave}
            className="text-sm px-3 py-1 rounded bg-brand-500 hover:bg-brand-600 text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500"
          >
            保存
          </button>
        </div>
      </div>
    </div>
  );
}

function Field({ id, label, children }: { id?: string; label: string; children: React.ReactNode }) {
  return (
    <div className="block text-sm">
      <label htmlFor={id} className="text-xs text-app-text-muted block mb-1">
        {label}
      </label>
      {children}
    </div>
  );
}