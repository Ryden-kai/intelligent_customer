import { useEffect, useState } from 'react';
import { Routes, Route, Navigate, useNavigate, Link } from 'react-router-dom';
import { api, getToken, setToken } from '../api';
import ConversationListPage from './ConversationListPage';
import ConversationDetailPage from './ConversationDetailPage';
import StatsPage from './StatsPage';

export default function AdminApp() {
  const [token, setTok] = useState<string | null>(getToken());
  const [user, setUser] = useState<{ username: string; role: string } | null>(null);
  const nav = useNavigate();

  useEffect(() => {
    if (!token) return;
    api.whoami()
      .then((u) => setUser(u))
      .catch(() => {
        setToken(null);
        setTok(null);
        nav('/admin/login', { replace: true });
      });
  }, [token]);

  if (!token) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage onLogin={(t) => { setTok(t); }} />} />
        <Route path="*" element={<Navigate to="/admin/login" replace />} />
      </Routes>
    );
  }

  return (
    <div className="min-h-full flex flex-col">
      <header className="bg-slate-800 text-white px-6 py-3 flex items-center justify-between">
        <div className="flex items-center gap-4">
          <div className="font-semibold">智能客服 · 管理后台</div>
          <nav className="flex gap-3 text-sm text-slate-200">
            <Link to="/admin/conversations" className="hover:text-white">会话</Link>
            <Link to="/admin/stats" className="hover:text-white">满意度</Link>
          </nav>
        </div>
        <div className="flex items-center gap-3 text-sm">
          {user && <span>{user.username}（{user.role}）</span>}
          <button
            onClick={() => {
              setToken(null);
              setTok(null);
              nav('/admin/login', { replace: true });
            }}
            className="text-slate-300 hover:text-white underline text-xs"
          >
            退出
          </button>
        </div>
      </header>
      <main className="flex-1 max-w-6xl mx-auto w-full p-6">
        <Routes>
          <Route path="/" element={<Navigate to="/admin/conversations" replace />} />
          <Route path="/conversations" element={<ConversationListPage />} />
          <Route path="/conversations/:id" element={<ConversationDetailPage />} />
          <Route path="/stats" element={<StatsPage />} />
          <Route path="*" element={<Navigate to="/admin/conversations" replace />} />
        </Routes>
      </main>
    </div>
  );
}

function LoginPage({ onLogin }: { onLogin: (t: string) => void }) {
  const [u, setU] = useState('');
  const [p, setP] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    setErr(null);
    try {
      const r = await api.login(u, p);
      setToken(r.token);
      onLogin(r.token);
    } catch (e: any) {
      setErr(e?.response?.data?.message || '登录失败');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-full flex items-center justify-center bg-slate-100">
      <form onSubmit={submit} className="bg-white shadow-md rounded-2xl p-8 w-full max-w-sm">
        <h1 className="text-xl font-semibold text-slate-800 mb-1">管理员登录</h1>
        <p className="text-xs text-slate-500 mb-6">初始账号见项目 .env 中的 ADMIN_USERNAME / ADMIN_PASSWORD</p>
        <label className="block text-sm text-slate-700 mb-1">用户名</label>
        <input
          value={u}
          onChange={(e) => setU(e.target.value)}
          className="w-full border border-slate-200 rounded-md px-3 py-2 mb-3 focus:outline-none focus:ring-2 focus:ring-brand-500/40"
          required
        />
        <label className="block text-sm text-slate-700 mb-1">密码</label>
        <input
          type="password"
          value={p}
          onChange={(e) => setP(e.target.value)}
          className="w-full border border-slate-200 rounded-md px-3 py-2 mb-4 focus:outline-none focus:ring-2 focus:ring-brand-500/40"
          required
        />
        {err && <div className="text-rose-700 text-sm mb-3">{err}</div>}
        <button
          type="submit"
          disabled={loading}
          className="w-full bg-brand-500 hover:bg-brand-600 text-white py-2 rounded-md text-sm font-medium disabled:opacity-40"
        >
          {loading ? '登录中…' : '登录'}
        </button>
      </form>
    </div>
  );
}