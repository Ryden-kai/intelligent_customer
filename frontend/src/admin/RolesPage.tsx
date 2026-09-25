import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import type { Permission, Role } from '../types';

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

  return (
    <div className="space-y-6">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-xl font-semibold text-slate-800">角色管理</h2>
          <p className="text-xs text-slate-500">管理平台角色与权限码映射。系统预置角色不可删除。</p>
        </div>
        <button
          onClick={startCreate}
          className="text-xs px-3 py-1 rounded bg-brand-500 hover:bg-brand-600 text-white"
        >
          + 新建角色
        </button>
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
              <th className="text-left px-3 py-2">角色名</th>
              <th className="text-left px-3 py-2">描述</th>
              <th className="text-left px-3 py-2">权限数</th>
              <th className="text-left px-3 py-2">用户数</th>
              <th className="text-left px-3 py-2">系统</th>
              <th className="text-left px-3 py-2">创建时间</th>
              <th className="text-right px-3 py-2">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={7} className="px-3 py-6 text-center text-slate-500">加载中…</td>
              </tr>
            ) : roles.length === 0 ? (
              <tr>
                <td colSpan={7} className="px-3 py-6 text-center text-slate-500">暂无角色</td>
              </tr>
            ) : (
              roles.map((r) => (
                <tr key={r.id} className="border-t border-slate-100">
                  <td className="px-3 py-2 font-medium text-slate-800">{r.name}</td>
                  <td className="px-3 py-2 text-slate-600">{r.description}</td>
                  <td className="px-3 py-2">{r.permissions.length}</td>
                  <td className="px-3 py-2">{r.user_count ?? 0}</td>
                  <td className="px-3 py-2">
                    {r.is_system ? (
                      <span className="inline-block px-2 py-0.5 text-xs rounded bg-amber-100 text-amber-700">
                        系统
                      </span>
                    ) : (
                      <span className="inline-block px-2 py-0.5 text-xs rounded bg-slate-100 text-slate-500">
                        自定义
                      </span>
                    )}
                  </td>
                  <td className="px-3 py-2 text-xs text-slate-500">
                    {new Date(r.created_at).toLocaleString('zh-CN')}
                  </td>
                  <td className="px-3 py-2 text-right space-x-2">
                    <button
                      onClick={() => startEdit(r)}
                      className="text-xs px-2 py-1 rounded border border-slate-300 hover:bg-slate-50"
                    >
                      ✏️ 编辑
                    </button>
                    <button
                      onClick={() => del(r)}
                      disabled={r.is_system}
                      className="text-xs px-2 py-1 rounded border border-rose-200 text-rose-700 hover:bg-rose-50 disabled:opacity-30 disabled:cursor-not-allowed"
                    >
                      🗑️ 删除
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {editing && (
        <div
          className="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
          onClick={(e) => {
            if (e.target === e.currentTarget) setEditing(null);
          }}
        >
          <div className="bg-white rounded-xl w-full max-w-2xl max-h-[85vh] overflow-auto shadow-xl">
            <div className="px-6 py-4 border-b border-slate-200 flex items-center justify-between">
              <h3 className="font-semibold text-slate-800">
                {editing.id ? `编辑角色 ${editing.name}` : '新建角色'}
              </h3>
              <button
                onClick={() => setEditing(null)}
                className="text-slate-400 hover:text-slate-600"
              >
                ✕
              </button>
            </div>
            <div className="px-6 py-4 space-y-4">
              <div>
                <label className="block text-sm text-slate-700 mb-1">角色名</label>
                <input
                  value={editing.name}
                  onChange={(e) => setEditing({ ...editing, name: e.target.value })}
                  disabled={Boolean(editing.id && roles.find((r) => r.id === editing.id)?.is_system)}
                  className="w-full border border-slate-200 rounded-md px-3 py-2 text-sm disabled:bg-slate-100"
                  required
                />
                {editing.id && roles.find((r) => r.id === editing.id)?.is_system && (
                  <p className="text-xs text-amber-600 mt-1">系统预置角色不可改名</p>
                )}
              </div>
              <div>
                <label className="block text-sm text-slate-700 mb-1">描述</label>
                <textarea
                  value={editing.description}
                  onChange={(e) => setEditing({ ...editing, description: e.target.value })}
                  rows={2}
                  className="w-full border border-slate-200 rounded-md px-3 py-2 text-sm"
                />
              </div>
              <div>
                <label className="block text-sm text-slate-700 mb-2">权限矩阵</label>
                <div className="space-y-3 max-h-[40vh] overflow-y-auto border border-slate-200 rounded p-3 bg-slate-50">
                  <label className="flex items-center gap-2 pb-2 border-b border-slate-200">
                    <input
                      type="checkbox"
                      checked={editing.permissions.includes('*')}
                      onChange={() => togglePerm('*')}
                      className="rounded"
                    />
                    <span className="font-semibold text-rose-700">* (全权限 — 仅 admin)</span>
                  </label>
                  {Object.entries(groupedPerms).map(([group, ps]) => (
                    <div key={group}>
                      <div className="text-xs font-semibold text-slate-600 mb-1">{group}</div>
                      <div className="grid grid-cols-1 md:grid-cols-2 gap-1 pl-2">
                        {ps.map((p) => (
                          <label key={p.code} className="flex items-center gap-2 text-xs">
                            <input
                              type="checkbox"
                              checked={editing.permissions.includes(p.code)}
                              onChange={() => togglePerm(p.code)}
                              disabled={editing.permissions.includes('*')}
                              className="rounded"
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
            <div className="px-6 py-3 border-t border-slate-200 flex justify-end gap-2 bg-slate-50">
              <button
                onClick={() => setEditing(null)}
                className="text-xs px-3 py-1.5 rounded border border-slate-300 hover:bg-white"
              >
                取消
              </button>
              <button
                onClick={save}
                disabled={!editing.name || editing.permissions.length === 0}
                className="text-xs px-3 py-1.5 rounded bg-brand-500 hover:bg-brand-600 text-white disabled:opacity-40"
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