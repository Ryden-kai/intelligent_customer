import axios, { AxiosInstance } from 'axios';
import type {
  ChatRequest,
  ChatResponse,
  ConversationDetail,
  FeedbackInput,
  ListConversationsResponse,
  LoginResponse,
  SatisfactionStat,
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
  // whether to enforce them via `SIGNATURE_REQUIRED`:
  //   - SIGNATURE_REQUIRED=true  → server 401s unsigned requests
  //   - SIGNATURE_REQUIRED=false → server also accepts unsigned requests,
  //                                so curl/Postman can debug without
  //                                wiring signing through every ad-hoc test
  // This split keeps the SPA bundle stable across environments while
  // letting developers toggle strictness with one env var.
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
      // Signing is best-effort; never throw — the server will 401 if
      // SIGNATURE_REQUIRED=true and headers are missing or invalid.
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
};

export type Api = typeof api;
export type ApiClient = AxiosInstance;
