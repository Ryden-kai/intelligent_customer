import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import type { Permission, Role } from '../types';
import { Skeleton } from '../components/Skeleton';
import { EmptyState } from '../components/EmptyState';
import { ChartContainer, CHART_COLORS } from '../components/ChartContainer';
import { BarChart, Bar, XAxis, YAxis, Tooltip, CartesianGrid } from 'recharts';

interface EditState {
  id?: string;
  name: string;
  description: string;
  permissions: string[];
}

const EMPTY_EDIT: EditState = {
  name: '',
  description: '',
  permissions: [],
};

export default function RolesPage() {
  const [roles, setRoles] = useState<Role[]>([]);
  const [perms, setPerms] = useState<Permission[]>([]);
  const [groups, setGroups] = useState<Record<string, string[]>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<EditState | null>(null);

  async function reload() {
    setLoading(true);
    try {
      const [r, p] = await Promise.all([api.rbac.listRoles(), api.rbac.listPermissions()]);
      setRoles(r.items);
      setPerms(p.permissions);
      setGroups(p.groups);
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

  function startCreate() {
    setEditing({ ...EMPTY_EDIT });
  }

  function startEdit(role: Role) {
    setEditing({
      id: role.id,
      name: role.name,
      description: role.description,
      permissions: [...role.permissions],
    });
  }

  async function save() {
    if (!editing) return;
    try {
      if (editing.id) {
        await api.rbac.updateRole(editing.id, {
          name: editing.name,
          description: editing.description,
          permissions: editing.permissions,
        });
      } else {
        await api.rbac.createRole({
          name: editing.name,
          description: editing.description,
          permissions: editing.permissions,
        });
      }
      setEditing(null);
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '保存失败');
    }
  }

  async function del(role: Role) {
    if (role.is_system) {
      setError('系统预置角色不可删除');
      return;
    }
    if (!confirm(`确认删除角色 "${role.name}"？`)) return;
    try {
      await api.rbac.deleteRole(role.id);
      await reload();
    } catch (e: any) {
      setError(e?.response?.data?.message || '删除失败');
    }
  }

  function togglePerm(code: string) {
    if (!editing) return;
    const has = editing.permissions.includes(code);
    setEditing({
      ...editing,
      permissions: has
        ? editing.permissions.filter((p) => p !== code)
        : [...editing.permissions, code],
    });
  }

  const groupedPerms = useMemo(() => {
    const map: Record<string, Permission[]> = {};
    for (const p of perms) {
      if (!map[p.group_name]) map[p.group_name] = [];
      map[p.group_name].push(p);
    }
    return map;
  }, [perms]);

  // Role permissions count chart data
  const permCountData = useMemo(() => {
    return roles.slice(0, 12).map((r) => ({
      name: r.name,
      count: r.permissions.length,
      system: r.is_system,
    }));
  }, [roles]);

  return (
    <div className="space-y-6">
      <header className="flex items-center justify-between gap-2 flex-wrap">
        <div>
          <h2 className="text-xl font-semibold text-app-text">角色管理</h2>
          <p className="text-xs text-app-text-muted">
            管理平台角色与权限码映射。系统预置角色不可删除。
          </p>
        </div>
        <button
          type="button"
          onClick={startCreate}
          aria-label="新建角色"
          className="text-xs px-3 py-1 rounded bg-brand-500 hover:bg-brand-600 text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 focus-visible:ring-offset-2 focus-visible:ring-offset-app-bg"
        >
          + 新建角色
        </button>
      </header>

      {error && (
        <div
          role="alert"
          className="bg-danger-50 border border-danger-200 text-danger-700 text-sm rounded p-2"
        >
          {error}
        </div>
      )}

      {/* Role permission count chart */}
      {roles.length > 0 && (
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <div className="lg:col-span-2">
            <ChartContainer title="角色权限数（柱状图）" height={220}>
              <BarChart data={permCountData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="name" stroke="currentColor" />
                <YAxis stroke="currentColor" allowDecimals={false} />
                <Tooltip />
                <Bar dataKey="count" name="权限数" fill={CHART_COLORS.primary} radius={[4, 4, 0, 0]} />
              </BarChart>
            </ChartContainer>
          </div>
          <div className="bg-app-surface border border-app-border rounded-xl p-4 text-xs text-app-text-muted">
            <h3 className="text-sm font-semibold text-app-text mb-2">说明</h3>
            <p>每个角色被分配的权限码数量。*（全权限）算作 1 项。</p>
            <p>系统预置角色（admin / agent / user）不可删改，仅展示。</p>
          </div>
        </div>
      )}

      {loading ? (
        <div
          className="bg-app-surface border border-app-border rounded-xl p-4 space-y-3"
          aria-label="正在加载角色与权限"
        >
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} variant="text" height="1.25rem" />
          ))}
        </div>
      ) : roles.length === 0 ? (
        <EmptyState
          variant="noData"
          title="暂无角色"
          description="点击右上角「新建角色」开始。"
          action={{ label: '+ 新建角色', onClick: startCreate }}
        />
      ) : (
        <div className="bg-app-surface border border-app-border rounded-xl overflow-hidden">
          <div className="table-responsive">
            <table className="w-full text-sm">
              <thead className="bg-app-surface-muted text-xs text-app-text-muted uppercase">
                <tr>
                  <th className="text-left px-3 py-2">角色名</th>
                  <th className="text-left px-3 py-2">描述</th>
                  <th className="text-left px-3 py-2">权限数</th>
                  <th className="text-left px-3 py-2">用户数</th>
                  <th className="text-left px-3 py-2">系统</th>
                  <th className="text-left px-3 py-2 hidden md:table-cell">创建时间</th>
                  <th className="text-right px-3 py-2">操作</th>
                </tr>
              </thead>
              <tbody>
                {roles.map((r) => (
                  <tr key={r.id} className="border-t border-app-border">
                    <td className="px-3 py-2 font-medium text-app-text">{r.name}</td>
                    <td className="px-3 py-2 text-app-text-muted">{r.description}</td>
                    <td className="px-3 py-2 text-app-text">{r.permissions.length}</td>
                    <td className="px-3 py-2 text-app-text">{r.user_count ?? 0}</td>
                    <td className="px-3 py-2">
                      {r.is_system ? (
                        <span className="inline-block px-2 py-0.5 text-xs rounded bg-warning-100 text-warning-700">
                          系统
                        </span>
                      ) : (
                        <span className="inline-block px-2 py-0.5 text-xs rounded bg-slate-100 text-slate-500">
                          自定义
                        </span>
                      )}
                    </td>
                    <td className="px-3 py-2 text-xs text-app-text-muted hidden md:table-cell">
                      {new Date(r.created_at).toLocaleString('zh-CN')}
                    </td>
                    <td className="px-3 py-2 text-right space-x-2 whitespace-nowrap">
                      <button
                        type="button"
                        onClick={() => startEdit(r)}
                        aria-label={`编辑角色 ${r.name}`}
                        className="text-xs px-2 py-1 rounded border border-app-border text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                      >
                        ✏️ 编辑
                      </button>
                      <button
                        type="button"
                        onClick={() => del(r)}
                        disabled={r.is_system}
                        aria-label={`删除角色 ${r.name}`}
                        className="text-xs px-2 py-1 rounded border border-danger-200 text-danger-700 bg-app-surface hover:bg-danger-50 disabled:opacity-30 disabled:cursor-not-allowed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-danger-500"
                      >
                        🗑️ 删除
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
        <div
          role="dialog"
          aria-modal="true"
          aria-labelledby="role-edit-title"
          className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4"
          onClick={(e) => {
            if (e.target === e.currentTarget) setEditing(null);
          }}
        >
          <div className="bg-app-surface rounded-xl w-full max-w-2xl max-h-[85vh] overflow-auto shadow-xl border border-app-border">
            <div className="px-6 py-4 border-b border-app-border flex items-center justify-between">
              <h3 id="role-edit-title" className="font-semibold text-app-text">
                {editing.id ? `编辑角色 ${editing.name}` : '新建角色'}
              </h3>
              <button
                type="button"
                onClick={() => setEditing(null)}
                aria-label="关闭编辑"
                className="text-app-text-muted hover:text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand rounded"
              >
                ✕
              </button>
            </div>
            <div className="px-6 py-4 space-y-4">
              <div>
                <label htmlFor="role-name" className="block text-sm text-app-text mb-1">
                  角色名
                </label>
                <input
                  id="role-name"
                  value={editing.name}
                  onChange={(e) => setEditing({ ...editing, name: e.target.value })}
                  disabled={Boolean(editing.id && roles.find((r) => r.id === editing.id)?.is_system)}
                  className="w-full border border-app-border rounded-md px-3 py-2 text-sm bg-app-surface text-app-text disabled:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                  required
                />
                {editing.id && roles.find((r) => r.id === editing.id)?.is_system && (
                  <p className="text-xs text-warning-600 mt-1">系统预置角色不可改名</p>
                )}
              </div>
              <div>
                <label htmlFor="role-desc" className="block text-sm text-app-text mb-1">
                  描述
                </label>
                <textarea
                  id="role-desc"
                  value={editing.description}
                  onChange={(e) => setEditing({ ...editing, description: e.target.value })}
                  rows={2}
                  className="w-full border border-app-border rounded-md px-3 py-2 text-sm bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                />
              </div>
              <div>
                <div className="block text-sm text-app-text mb-2">权限矩阵</div>
                <div className="space-y-3 max-h-[40vh] overflow-y-auto border border-app-border rounded p-3 bg-app-surface-muted">
                  <label className="flex items-center gap-2 pb-2 border-b border-app-border">
                    <input
                      type="checkbox"
                      checked={editing.permissions.includes('*')}
                      onChange={() => togglePerm('*')}
                      className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                    />
                    <span className="font-semibold text-danger-700">* (全权限 — 仅 admin)</span>
                  </label>
                  {Object.entries(groupedPerms).map(([group, ps]) => (
                    <div key={group}>
                      <div className="text-xs font-semibold text-app-text-muted mb-1">{group}</div>
                      <div className="grid grid-cols-1 md:grid-cols-2 gap-1 pl-2">
                        {ps.map((p) => (
                          <label key={p.code} className="flex items-center gap-2 text-xs text-app-text">
                            <input
                              type="checkbox"
                              checked={editing.permissions.includes(p.code)}
                              onChange={() => togglePerm(p.code)}
                              disabled={editing.permissions.includes('*')}
                              className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
                            />
                            <span title={p.description}>{p.code}</span>
                          </label>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
            <div className="px-6 py-3 border-t border-app-border flex justify-end gap-2 bg-app-surface-muted">
              <button
                type="button"
                onClick={() => setEditing(null)}
                className="text-xs px-3 py-1.5 rounded border border-app-border text-app-text bg-app-surface hover:bg-app-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
              >
                取消
              </button>
              <button
                type="button"
                onClick={save}
                disabled={!editing.name || editing.permissions.length === 0}
                className="text-xs px-3 py-1.5 rounded bg-brand-500 hover:bg-brand-600 text-white disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500"
              >
                保存
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
