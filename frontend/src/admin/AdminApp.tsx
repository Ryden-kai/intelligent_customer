import { useEffect, useState } from 'react';
import { Routes, Route, Navigate, useNavigate, Link } from 'react-router-dom';
import { api, getToken, setToken } from '../api';
import ConversationListPage from './ConversationListPage';
import ConversationDetailPage from './ConversationDetailPage';
import StatsPage from './StatsPage';
import SkillsPage from './SkillsPage';
import JevTemplatesPage from './JevTemplatesPage';
import JevObservabilityPage from './JevObservabilityPage';
import RolesPage from './RolesPage';
import AuditLogPage from './AuditLogPage';
import RateLimitConfigPage from './RateLimitConfigPage';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { ThemeToggle } from '../components/ThemeToggle';

interface WhoamiExt {
  username: string;
  role: string;
  permissions?: string[];
}

function hasPerm(perms: string[] | undefined, code: string): boolean {
  if (!perms || perms.length === 0) return false;
  if (perms.includes('*')) return true;
  return perms.includes(code);
}

export default function AdminApp() {
  const [token, setTok] = useState<string | null>(getToken());
  const [user, setUser] = useState<WhoamiExt | null>(null);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
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

  const perms = user?.permissions;
  const showRoles = hasPerm(perms, 'role.read');
  const showAudit = hasPerm(perms, 'audit.read');
  const showRateLimit = hasPerm(perms, 'ratelimit.manage');

  return (
    <ErrorBoundary scope="admin">
      <div className="min-h-full flex flex-col bg-app-bg text-app-text">
        <header className="bg-slate-800 dark:bg-slate-950 text-white px-4 sm:px-6 py-3 flex items-center justify-between gap-2">
          <div className="flex items-center gap-3 sm:gap-4 min-w-0">
            <button
              type="button"
              aria-label={mobileNavOpen ? '关闭导航菜单' : '打开导航菜单'}
              aria-expanded={mobileNavOpen}
              onClick={() => setMobileNavOpen((v) => !v)}
              className="lg:hidden text-slate-200 hover:text-white p-1 rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
            >
              {mobileNavOpen ? '✕' : '☰'}
            </button>
            <div className="font-semibold text-sm sm:text-base">智能客服 · 管理后台</div>
            <nav
              aria-label="主导航"
              className="hidden lg:flex gap-3 text-sm text-slate-200"
            >
              <Link to="/admin/conversations" className="hover:text-white focus-visible:outline-none focus-visible:underline">会话</Link>
              <Link to="/admin/stats" className="hover:text-white focus-visible:outline-none focus-visible:underline">满意度</Link>
              <Link to="/admin/skills" className="hover:text-white focus-visible:outline-none focus-visible:underline">Skill</Link>
              <Link to="/admin/jev/templates" className="hover:text-white focus-visible:outline-none focus-visible:underline">Jev 模板</Link>
              <Link to="/admin/jev/observability" className="hover:text-white focus-visible:outline-none focus-visible:underline">Jev 可观测</Link>
              {showRoles && <Link to="/admin/roles" className="hover:text-white focus-visible:outline-none focus-visible:underline">角色</Link>}
              {showAudit && <Link to="/admin/audit" className="hover:text-white focus-visible:outline-none focus-visible:underline">审计日志</Link>}
              {showRateLimit && <Link to="/admin/ratelimit" className="hover:text-white focus-visible:outline-none focus-visible:underline">限流配置</Link>}
            </nav>
          </div>
          <div className="flex items-center gap-2 sm:gap-3 text-sm flex-shrink-0">
            <ThemeToggle className="border-slate-600 hover:bg-slate-700 text-white" />
            {user && (
              <span className="hidden sm:inline text-slate-200">
                {user.username}（{user.role}）
              </span>
            )}
            <button
              type="button"
              onClick={() => {
                setToken(null);
                setTok(null);
                nav('/admin/login', { replace: true });
              }}
              className="text-slate-300 hover:text-white underline text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand rounded px-1"
            >
              退出
            </button>
          </div>
        </header>
        {mobileNavOpen && (
          <nav
            aria-label="移动端导航"
            className="lg:hidden bg-slate-700 dark:bg-slate-900 text-white px-4 py-2 flex flex-col gap-1 text-sm"
          >
            <Link to="/admin/conversations" onClick={() => setMobileNavOpen(false)} className="py-2 hover:bg-slate-600 rounded px-2">会话</Link>
            <Link to="/admin/stats" onClick={() => setMobileNavOpen(false)} className="py-2 hover:bg-slate-600 rounded px-2">满意度</Link>
            <Link to="/admin/skills" onClick={() => setMobileNavOpen(false)} className="py-2 hover:bg-slate-600 rounded px-2">Skill</Link>
            <Link to="/admin/jev/templates" onClick={() => setMobileNavOpen(false)} className="py-2 hover:bg-slate-600 rounded px-2">Jev 模板</Link>
            <Link to="/admin/jev/observability" onClick={() => setMobileNavOpen(false)} className="py-2 hover:bg-slate-600 rounded px-2">Jev 可观测</Link>
            {showRoles && <Link to="/admin/roles" onClick={() => setMobileNavOpen(false)} className="py-2 hover:bg-slate-600 rounded px-2">角色</Link>}
            {showAudit && <Link to="/admin/audit" onClick={() => setMobileNavOpen(false)} className="py-2 hover:bg-slate-600 rounded px-2">审计日志</Link>}
            {showRateLimit && <Link to="/admin/ratelimit" onClick={() => setMobileNavOpen(false)} className="py-2 hover:bg-slate-600 rounded px-2">限流配置</Link>}
          </nav>
        )}
        <main className="flex-1 max-w-6xl mx-auto w-full p-4 sm:p-6">
          <Routes>
            <Route path="/" element={<Navigate to="/admin/conversations" replace />} />
            <Route path="/conversations" element={<ConversationListPage />} />
            <Route path="/conversations/:id" element={<ConversationDetailPage />} />
            <Route path="/stats" element={<StatsPage />} />
            <Route path="/skills" element={<SkillsPage />} />
            <Route path="/jev/templates" element={<JevTemplatesPage />} />
            <Route path="/jev/observability" element={<JevObservabilityPage />} />
            <Route path="/roles" element={<RolesPage />} />
            <Route path="/audit" element={<AuditLogPage />} />
            <Route path="/ratelimit" element={<RateLimitConfigPage />} />
            <Route path="*" element={<Navigate to="/admin/conversations" replace />} />
          </Routes>
        </main>
      </div>
    </ErrorBoundary>
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
    <div className="min-h-full flex items-center justify-center bg-app-bg px-4">
      <form
        onSubmit={submit}
        aria-labelledby="login-title"
        className="bg-app-surface shadow-md rounded-2xl p-6 sm:p-8 w-full max-w-sm border border-app-border"
      >
        <h1 id="login-title" className="text-xl font-semibold text-app-text mb-1">
          管理员登录
        </h1>
        <p className="text-xs text-app-text-muted mb-6">
          初始账号见项目 .env 中的 ADMIN_USERNAME / ADMIN_PASSWORD
        </p>
        <div className="space-y-3">
          <div>
            <label htmlFor="login-username" className="block text-sm text-app-text mb-1">
              用户名
            </label>
            <input
              id="login-username"
              value={u}
              onChange={(e) => setU(e.target.value)}
              autoComplete="username"
              className="w-full border border-app-border rounded-md px-3 py-2 bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
              required
            />
          </div>
          <div>
            <label htmlFor="login-password" className="block text-sm text-app-text mb-1">
              密码
            </label>
            <input
              id="login-password"
              type="password"
              value={p}
              onChange={(e) => setP(e.target.value)}
              autoComplete="current-password"
              className="w-full border border-app-border rounded-md px-3 py-2 bg-app-surface text-app-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
              required
            />
          </div>
        </div>
        {err && (
          <div
            role="alert"
            className="text-danger-700 bg-danger-50 text-sm mt-3 mb-3 px-3 py-2 rounded-md border border-danger-200"
          >
            {err}
          </div>
        )}
        <button
          type="submit"
          disabled={loading}
          className="mt-4 w-full bg-brand-500 hover:bg-brand-600 text-white py-2 rounded-md text-sm font-medium disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 focus-visible:ring-offset-2 focus-visible:ring-offset-app-bg"
        >
          {loading ? '登录中…' : '登录'}
        </button>
      </form>
    </div>
  );
}
