import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import type { JevTemplate, JevOutputType, JevTrigger, JevTemplateStatus } from '../types';
import { Skeleton } from '../components/Skeleton';
import { EmptyState } from '../components/EmptyState';
import { ChartContainer, CHART_COLORS } from '../components/ChartContainer';
import { useBatchSelection } from '../hooks/useBatchSelection';
import { BarChart, Bar, XAxis, YAxis, Tooltip, CartesianGrid } from 'recharts';

const TRIGGERS: JevTrigger[] = [
  'message.entry',
  'message.pre_ingest',
  'reply.pre_ingest',
  'handover.pre',
  'ticket.create',
  'agent.assign',
];

const OUTPUT_TYPES: JevOutputType[] = ['choice', 'score', 'noul'];

export default function JevTemplatesPage() {
  const [items, setItems] = useState<JevTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Partial<JevTemplate> | null>(null);

  const selection = useBatchSelection();

  async function reload() {
    setLoading(true);
    try {
      const r = await api.listJevTemplates();
      setItems(r.items);
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
      const body: Partial<JevTemplate> = {
        ...editing,
        labels: typeof editing.labels === 'string'
          ? (editing.labels as unknown as string).split(',').map((s) => s.trim()).filter(Boolean)
          : editing.labels || [],
      };
      await api.createJevTemplate(body);
      setEditing(null);
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '保存失败');
    }
  }

  async function publish(id: string) {
    try {
      await api.publishJevTemplate(id);
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '发布失败');
    }
  }

  async function archive(id: string) {
    if (!confirm('确认归档该模板？')) return;
    try {
      await api.archiveJevTemplate(id);
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '归档失败');
    }
  }

  async function del(id: string) {
    if (!confirm('确认删除该模板？')) return;
    try {
      await api.deleteJevTemplate(id);
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '删除失败');
    }
  }

  async function hotReload() {
    try {
      await api.reloadJevTemplates();
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '热重载失败');
    }
  }

  async function bulkArchive() {
    if (selection.count === 0) return;
    if (!confirm(`确认归档选中的 ${selection.count} 个模板？`)) return;
    try {
      const r = await api.jev.archiveBatch(selection.ids);
      selection.clear();
      await reload();
      if (r.succeeded < r.total) {
        setError(`已归档 ${r.succeeded}/${r.total} 个模板`);
      }
    } catch (e: any) {
      setError(e?.response?.data?.message || '批量归档失败');
    }
  }

  // version distribution: count templates per version
  const versionDistData = useMemo(() => {
    const map = new Map<number, number>();
    for (const t of items) {
      map.set(t.version, (map.get(t.version) ?? 0) + 1);
    }
    return Array.from(map.entries())
      .map(([version, count]) => ({ name: `v${version}`, version, count }))
      .sort((a, b) => a.version - b.version)
      .slice(0, 12);
  }, [items]);

  return (
    <div className="space-y-6">
      <header className="flex items-center justify-between gap-2 flex-wrap">
        <div>
          <h2 className="text-xl font-semibold text-app-text">Jev 决策模板</h2>
          <p className="text-xs text-app-text-muted">
            管理 Jev 模型决策的 5 类触发点模板（意图 / 敏感词 / 情绪 / 工单优先级 / 坐席分配）。
          </p>
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={hotReload}
            aria-label="热重载模板"
            className="text-xs px-3 py-1 rounded border border-app-border text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          >
            热重载
          </button>
          <button
            type="button"
            onClick={() =>
              setEditing({
                name: '',
                version: 1,
                trigger: 'message.entry',
                outputType: 'choice',
                labels: [],
                instructions: '',
                status: 'draft',
              })
            }
            aria-label="新建模板"
            className="text-xs px-3 py-1 rounded bg-brand-500 hover:bg-brand-600 text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500"
          >
            + 新建模板
          </button>
        </div>
      </header>

      {error && (
        <div role="alert" className="bg-danger-50 border border-danger-200 text-danger-700 text-sm rounded p-2">
          {error}
        </div>
      )}

      {/* Version distribution chart */}
      {items.length > 0 && (
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <div className="lg:col-span-2">
            <ChartContainer title="模板版本分布" height={240}>
              <BarChart data={versionDistData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="name" stroke="currentColor" />
                <YAxis stroke="currentColor" allowDecimals={false} />
                <Tooltip />
                <Bar dataKey="count" name="模板数" fill={CHART_COLORS.primary} radius={[4, 4, 0, 0]} />
              </BarChart>
            </ChartContainer>
          </div>
          <div className="bg-app-surface border border-app-border rounded-xl p-4 text-xs text-app-text-muted">
            <h3 className="text-sm font-semibold text-app-text mb-2">说明</h3>
            <p>按版本号（v1 / v2 / v3...）统计当前模板数。</p>
            <p>同一 (name) 可能有多个版本；归档操作会保留所有版本。</p>
          </div>
        </div>
      )}

      {/* Bulk action bar */}
      <div className="flex items-center gap-2 flex-wrap">
        <span className="text-xs text-app-text-muted">
          {selection.count > 0 ? `已选 ${selection.count} 条` : '勾选多行后可批量归档'}
        </span>
        <button
          type="button"
          onClick={bulkArchive}
          disabled={selection.count === 0}
          aria-label="批量归档选中"
          className="text-xs px-3 py-1 rounded bg-warning-500 hover:bg-warning-600 text-white disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-warning-500"
        >
          ⛔ 批量归档选中（{selection.count}）
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
          aria-label="正在加载模板"
        >
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} variant="text" height="1.5rem" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <EmptyState variant="noData" title="暂无模板" description="还没有任何 Jev 决策模板。" />
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
                      checked={selection.count > 0 && selection.count === items.length}
                      onChange={(e) => {
                        if (e.target.checked) selection.selectAll(items.map((t) => t.id));
                        else selection.clear();
                      }}
                      className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                    />
                  </th>
                  <th className="text-left px-3 py-2">name</th>
                  <th className="text-left px-3 py-2 hidden md:table-cell">trigger</th>
                  <th className="text-left px-3 py-2">output</th>
                  <th className="text-left px-3 py-2 hidden sm:table-cell">version</th>
                  <th className="text-left px-3 py-2">status</th>
                  <th className="text-left px-3 py-2 hidden lg:table-cell">industry</th>
                  <th className="text-right px-3 py-2">actions</th>
                </tr>
              </thead>
              <tbody>
                {items.map((t) => (
                  <tr key={t.id} className="border-t border-app-border hover:bg-app-surface-muted">
                    <td className="px-3 py-2">
                      <input
                        type="checkbox"
                        aria-label={`选择 ${t.name}`}
                        checked={selection.isSelected(t.id)}
                        onChange={() => selection.toggle(t.id)}
                        className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                      />
                    </td>
                    <td className="px-3 py-2">
                      <div className="font-medium text-app-text">{t.name}</div>
                    </td>
                    <td className="px-3 py-2 text-xs font-mono text-app-text-muted hidden md:table-cell">{t.trigger}</td>
                    <td className="px-3 py-2 text-xs">
                      <span className="px-2 py-0.5 rounded-full bg-app-surface-muted text-app-text">
                        {t.outputType}
                      </span>
                    </td>
                    <td className="px-3 py-2 text-xs text-app-text-muted hidden sm:table-cell">v{t.version}</td>
                    <td className="px-3 py-2 text-xs">
                      <StatusBadge status={t.status} />
                    </td>
                    <td className="px-3 py-2 text-xs text-app-text-muted hidden lg:table-cell">
                      {t.industryCode || '—'}
                    </td>
                    <td className="px-3 py-2 text-right space-x-1 text-xs whitespace-nowrap">
                      {t.status !== 'published' && (
                        <button
                          type="button"
                          onClick={() => publish(t.id)}
                          aria-label={`发布模板 ${t.name}`}
                          className="text-success-600 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand rounded"
                        >
                          发布
                        </button>
                      )}
                      {t.status !== 'archived' && (
                        <button
                          type="button"
                          onClick={() => archive(t.id)}
                          aria-label={`归档模板 ${t.name}`}
                          className="text-warning-600 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand rounded"
                        >
                          归档
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => del(t.id)}
                        aria-label={`删除模板 ${t.name}`}
                        className="text-danger-600 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-danger-500 rounded"
                      >
                        删除
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {editing && (
        <TemplateEditor
          template={editing}
          onChange={setEditing}
          onCancel={() => setEditing(null)}
          onSave={save}
        />
      )}
    </div>
  );
}

function StatusBadge({ status }: { status: JevTemplateStatus }) {
  const map: Record<JevTemplateStatus, string> = {
    draft: 'bg-slate-100 text-slate-700',
    pending: 'bg-warning-100 text-warning-700',
    approved: 'bg-blue-100 text-blue-700',
    published: 'bg-success-100 text-success-700',
    archived: 'bg-slate-200 text-slate-500',
  };
  const label: Record<JevTemplateStatus, string> = {
    draft: '草稿',
    pending: '审批中',
    approved: '已批准',
    published: '已发布',
    archived: '已归档',
  };
  return (
    <span className={`px-2 py-0.5 rounded-full text-[10px] ${map[status]}`}>
      {label[status]}
    </span>
  );
}

function TemplateEditor({
  template,
  onChange,
  onCancel,
  onSave,
}: {
  template: Partial<JevTemplate>;
  onChange: (t: Partial<JevTemplate>) => void;
  onCancel: () => void;
  onSave: () => void;
}) {
  function update<K extends keyof JevTemplate>(k: K, v: JevTemplate[K]) {
    onChange({ ...template, [k]: v });
  }
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="template-editor-title"
      className="fixed inset-0 bg-slate-900/40 flex items-center justify-center z-50 p-4"
    >
      <div className="bg-app-surface rounded-xl shadow-lg w-full max-w-2xl p-6 space-y-4 max-h-[90vh] overflow-y-auto border border-app-border">
        <h3 id="template-editor-title" className="font-semibold text-app-text">
          新建 Jev 模板
        </h3>
        <Field id="tpl-name" label="name">
          <input
            id="tpl-name"
            value={template.name || ''}
            onChange={(e) => update('name', e.target.value)}
            className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
          />
        </Field>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <Field id="tpl-trigger" label="trigger">
            <select
              id="tpl-trigger"
              value={template.trigger || 'message.entry'}
              onChange={(e) => update('trigger', e.target.value as JevTrigger)}
              className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            >
              {TRIGGERS.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </Field>
          <Field id="tpl-output" label="output_type">
            <select
              id="tpl-output"
              value={template.outputType || 'choice'}
              onChange={(e) => update('outputType', e.target.value as JevOutputType)}
              className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            >
              {OUTPUT_TYPES.map((o) => (
                <option key={o} value={o}>
                  {o}
                </option>
              ))}
            </select>
          </Field>
          <Field id="tpl-version" label="version">
            <input
              id="tpl-version"
              type="number"
              min={1}
              value={template.version || 1}
              onChange={(e) => update('version', parseInt(e.target.value || '1', 10))}
              className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            />
          </Field>
        </div>
        <Field id="tpl-labels" label="labels (逗号分隔)">
          <input
            id="tpl-labels"
            value={Array.isArray(template.labels) ? template.labels.join(', ') : ''}
            onChange={(e) => update('labels', e.target.value as unknown as string[])}
            className="w-full border border-app-border rounded px-2 py-1 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            placeholder="order, refund, coupon, faq"
          />
        </Field>
        <Field id="tpl-instructions" label="instructions (text/template body, 支持 {{.key}})">
          <textarea
            id="tpl-instructions"
            value={template.instructions || ''}
            onChange={(e) => update('instructions', e.target.value)}
            rows={6}
            className="w-full border border-app-border rounded px-2 py-1 text-xs font-mono bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            placeholder={'判断用户消息的意图：\n{{.message}}'}
          />
        </Field>
        <div className="text-xs text-warning-700 bg-warning-50 border border-warning-200 rounded p-2">
          提示：模板保存后 status=draft，需在列表点"发布"才能被 Orchestrator 使用。
          优先使用 backend/data/industry_templates/* 行业模板一键 Activate。
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
            保存草稿
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