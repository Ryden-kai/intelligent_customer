import axios, { AxiosInstance } from 'axios';
import type {
  ChatRequest,
  ChatResponse,
  ConfirmRequest,
  ConversationDetail,
  FeedbackInput,
  JevDecision,
  JevDecisionListResponse,
  JevStats,
  JevTemplate,
  JevTemplateListResponse,
  ListConversationsResponse,
  LoginResponse,
  SatisfactionStat,
  Skill,
  SkillListResponse,
  StreamEvent,
  Role,
  Permission,
  AuditLog,
  AuditListResponse,
  RateLimitConfig,
  RateLimitListResponse,
} from './types';
import { sign } from './security';

const TOKEN_KEY = 'ic.admin.token';

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}
export function setToken(t: string | null) {
  if (t) localStorage.setItem(TOKEN_KEY, t);
  else localStorage.removeItem(TOKEN_KEY);
}

// `VITE_API_BASE_URL` is set at build time. Leave it empty in dev (we proxy
// /api → backend via vite.config) and set it to the absolute backend URL
// when deploying the SPA (e.g. https://api.your-domain.com).
const API_BASE: string =
  (import.meta.env.VITE_API_BASE_URL as string | undefined)?.replace(/\/+$/, '') || '';

// `VITE_API_SIGN_SECRET` is the shared HMAC secret with the backend.
// Default fallback lets `npm run dev` produce signed requests out of
// the box — match this string in backend/.env's JWT_SECRET during
// development. Production builds must override via the env var.
const DEFAULT_DEV_SIGN_SECRET = 'dev-secret-please-change-in-production-must-be-32b';
const SIGN_SECRET: string =
  (import.meta.env.VITE_API_SIGN_SECRET as string | undefined)?.trim() ||
  DEFAULT_DEV_SIGN_SECRET;

const base = axios.create({ baseURL: API_BASE });

base.interceptors.request.use(async (cfg) => {
  const t = getToken();
  if (t) cfg.headers.Authorization = `Bearer ${t}`;

  // Frontend ALWAYS attaches signature headers. The backend decides
  // whether to enforce them via `SIGNATURE_REQUIRED`.
  if (cfg.method && cfg.url) {
    try {
      const body =
        typeof cfg.data === 'string'
          ? cfg.data
          : cfg.data
          ? JSON.stringify(cfg.data)
          : '';
      const fullUrl = cfg.url.startsWith('http') ? cfg.url : `${API_BASE}${cfg.url}`;
      const headers = await sign(cfg.method.toUpperCase(), fullUrl, body);
      Object.assign(cfg.headers, headers);
    } catch (e) {
      console.warn('api: request signing failed', e);
    }
  }
  return cfg;
});

export const api = {
  // Public
  chat: (req: ChatRequest) =>
    base.post<ChatResponse>('/api/chat', req).then((r) => r.data),
  feedback: (req: FeedbackInput) =>
    base.post('/api/feedback', req).then((r) => r.data),

  // Admin
  login: (username: string, password: string) =>
    base.post<LoginResponse>('/api/admin/login', { username, password }).then((r) => r.data),
  listConversations: (params?: { status?: string; page?: number; size?: number }) =>
    base.get<ListConversationsResponse>('/api/admin/conversations', { params }).then((r) => r.data),
  getConversation: (id: string) =>
    base.get<ConversationDetail>(`/api/admin/conversations/${id}`).then((r) => r.data),
  postAgentReply: (id: string, content: string) =>
    base.post(`/api/admin/conversations/${id}/reply`, { content }).then((r) => r.data),
  statsSatisfaction: (days = 7) =>
    base.get<SatisfactionStat>('/api/admin/stats/satisfaction', { params: { days } }).then((r) => r.data),
  whoami: () => base.get('/api/admin/me').then((r) => r.data),

  // Skills
  listSkills: () =>
    base.get<SkillListResponse>('/api/admin/skills').then((r) => r.data),
  createSkill: (s: Partial<Skill>) =>
    base.post<Skill>('/api/admin/skills', s).then((r) => r.data),
  updateSkill: (id: string, s: Partial<Skill>) =>
    base.put<Skill>(`/api/admin/skills/${id}`, s).then((r) => r.data),
  deleteSkill: (id: string) =>
    base.delete(`/api/admin/skills/${id}`).then((r) => r.data),
  reloadSkills: () =>
    base.post<{ reloaded: string[] }>('/api/admin/skills/reload').then((r) => r.data),

  // Refund / skill confirm (public; auth is ticket id + user id pair)
  confirmSkill: (req: ConfirmRequest) =>
    base.post('/api/skills/confirm', req).then((r) => r.data),
  cancelSkill: (req: ConfirmRequest) =>
    base.post('/api/skills/cancel', req).then((r) => r.data),

  // Jev decision layer (v2.1) ------------------------------------------
  listJevTemplates: () =>
    base.get<JevTemplateListResponse>('/api/admin/jev/templates').then((r) => r.data),
  getJevTemplate: (id: string) =>
    base.get<JevTemplate>(`/api/admin/jev/templates/${id}`).then((r) => r.data),
  createJevTemplate: (t: Partial<JevTemplate>) =>
    base.post<JevTemplate>('/api/admin/jev/templates', t).then((r) => r.data),
  publishJevTemplate: (id: string) =>
    base.post<{ id: string; status: string }>(
      `/api/admin/jev/templates/publish/${id}`,
    ).then((r) => r.data),
  archiveJevTemplate: (id: string) =>
    base.post<{ id: string; status: string }>(
      `/api/admin/jev/templates/archive/${id}`,
    ).then((r) => r.data),
  deleteJevTemplate: (id: string) =>
    base.delete(`/api/admin/jev/templates/${id}`).then((r) => r.data),
  reloadJevTemplates: () =>
    base.post<{ status: string }>('/api/admin/jev/templates/reload').then((r) => r.data),
  listJevDecisions: (params?: {
    template?: string;
    trigger?: string;
    status?: string;
    limit?: number;
    offset?: number;
  }) =>
    base
      .get<JevDecisionListResponse>('/api/admin/jev/decisions', { params })
      .then((r) => r.data),
  reviewJevDecision: (id: string, body: { status: string; groundTruth?: string }) =>
    base
      .post<{ id: string; status: string }>(`/api/admin/jev/decisions/${id}/review`, body)
      .then((r) => r.data),
  getJevStats: () => base.get<JevStats>('/api/admin/jev/stats').then((r) => r.data),

  // ---- v2.2 PR2: RBAC ----
  rbac: {
    listRoles: () => base.get<{ items: Role[]; total: number }>('/api/admin/roles').then((r) => r.data),
    getRole: (id: string) => base.get<Role>(`/api/admin/roles/${id}`).then((r) => r.data),
    createRole: (body: { name: string; description: string; permissions: string[]; tenant_id?: string }) =>
      base.post<Role>('/api/admin/roles', body).then((r) => r.data),
    updateRole: (id: string, body: { name?: string; description?: string; permissions?: string[] }) =>
      base.put<Role>(`/api/admin/roles/${id}`, body).then((r) => r.data),
    deleteRole: (id: string) => base.delete(`/api/admin/roles/${id}`).then((r) => r.data),
    listPermissions: () =>
      base.get<{ permissions: Permission[]; groups: Record<string, string[]>; total: number }>(
        '/api/admin/permissions',
      ).then((r) => r.data),
    listUserRoles: (userID: string) =>
      base.get<{ user_id: string; roles: Role[]; total: number }>(
        `/api/admin/users/${userID}/roles`,
      ).then((r) => r.data),
    setUserRoles: (userID: string, roleIDs: string[], tenantID?: string) =>
      base.put<{ user_id: string; roles: string[] }>(`/api/admin/users/${userID}/roles`, {
        role_ids: roleIDs,
        ...(tenantID ? { tenant_id: tenantID } : {}),
      }).then((r) => r.data),
  },

  // ---- v2.2 PR2: Audit ----
  audit: {
    list: (params?: {
      from?: string;
      to?: string;
      actor_id?: string;
      action?: string;
      target_type?: string;
      tenant_id?: string;
      limit?: number;
      offset?: number;
    }) => base.get<AuditListResponse>('/api/admin/audit', { params }).then((r) => r.data),
    get: (id: string) => base.get<AuditLog>(`/api/admin/audit/${id}`).then((r) => r.data),
    exportUrl: (params?: Record<string, string>) => {
      const q = new URLSearchParams(params || {}).toString();
      // 返回带 token 的完整 URL；前端 fetch blob + 下载。
      const tok = getToken();
      const sep = q ? '&' : '';
      const authPart = tok ? `Authorization=Bearer%20${encodeURIComponent(tok)}` : '';
      return `${API_BASE}/api/admin/audit/export${q ? '?' + q : ''}${tok ? (q ? '&' : '?') + authPart : ''}`;
    },
  },

  // ---- v2.2 PR2: Rate Limit ----
  ratelimit: {
    list: (params?: { tenant_id?: string }) =>
      base.get<RateLimitListResponse>('/api/admin/ratelimit/configs', { params }).then((r) => r.data),
    update: (
      id: string,
      body: {
        per_minute?: number;
        per_hour?: number;
        burst?: number;
        enabled?: boolean;
        description?: string;
      },
    ) => base.put<RateLimitConfig>(`/api/admin/ratelimit/configs/${id}`, body).then((r) => r.data),
  },
};

// ---------------------------------------------------------------------------
// Streaming chat — bypasses axios because we need ReadableStream + NDJSON
// parsing. Same signature scheme as axios via the `sign()` helper.
// ---------------------------------------------------------------------------

export interface StreamChatResult {
  events: StreamEvent[];
  final?: StreamEvent;
  ticketId?: string;
}

/**
 * Send a chat message and stream the agent's NDJSON events back.
 *
 * Each line in the response body is one JSON object with at least
 * `{ "type": ... }`. The callback fires once per line as it arrives.
 * The returned promise resolves with the full event list once the
 * stream closes (final {type:"done"} or server-side error).
 */
export async function streamChat(
  req: ChatRequest,
  onEvent: (ev: StreamEvent) => void,
  signal?: AbortSignal,
): Promise<StreamChatResult> {
  const body = JSON.stringify(req);
  const url = `${API_BASE}/api/chat`;
  const headers: Record<string, string> = { ...(await sign('POST', url, body)) };
  headers['Content-Type'] = 'application/json; charset=utf-8';
  const t = getToken();
  if (t) headers['Authorization'] = `Bearer ${t}`;

  const resp = await fetch(url, {
    method: 'POST',
    headers,
    body,
    signal,
  });
  if (!resp.ok) {
    let text = '';
    try {
      text = await resp.text();
    } catch {}
    throw new Error(`streamChat HTTP ${resp.status}: ${text}`);
  }
  if (!resp.body) {
    throw new Error('streamChat: empty body');
  }

  const events: StreamEvent[] = [];
  let final: StreamEvent | undefined;
  let ticketId: string | undefined;

  const reader = resp.body.getReader();
  const decoder = new TextDecoder('utf-8');
  let buf = '';
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let nl = buf.indexOf('\n');
    while (nl >= 0) {
      const line = buf.slice(0, nl).trim();
      buf = buf.slice(nl + 1);
      if (line) {
        try {
          const ev = JSON.parse(line) as StreamEvent;
          events.push(ev);
          if (ev.ticketId) ticketId = ev.ticketId;
          if (ev.type === 'final' || ev.type === 'handover') {
            final = ev;
          }
          onEvent(ev);
        } catch (e) {
          // Bad line — surface but keep going.
          console.warn('streamChat: bad ndjson line', line, e);
        }
      }
      nl = buf.indexOf('\n');
    }
  }
  // Trailing line (no newline at end).
  if (buf.trim()) {
    try {
      const ev = JSON.parse(buf.trim()) as StreamEvent;
      events.push(ev);
      if (ev.ticketId) ticketId = ev.ticketId;
      if (ev.type === 'final' || ev.type === 'handover') final = ev;
      onEvent(ev);
    } catch (e) {
      console.warn('streamChat: bad trailing line', buf, e);
    }
  }

  return { events, final, ticketId };
}

export type Api = typeof api;
export type ApiClient = AxiosInstance;