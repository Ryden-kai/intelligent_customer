export type Intent = 'refund' | 'order' | 'tech' | 'other' | 'unknown';

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant' | 'agent' | 'system';
  content: string;
  model?: string;
  intent?: Intent;
  intentConfidence?: number;
  createdAt: string;
}

// ---------------------------------------------------------------------------
// Streaming protocol — backend writes one NDJSON event per line. Frontend
// collects them into a StreamEvent[] and renders the trace as the agent
// walks through steps.
// ---------------------------------------------------------------------------

export type StreamEventType =
  | 'step'
  | 'tool_call'
  | 'tool_result'
  | 'pending_human'
  | 'final'
  | 'handover'
  | 'done'
  | 'error';

export interface StreamEvent {
  type: StreamEventType;
  traceId?: string;
  step?: number;
  content?: string;
  tool?: string;
  args?: Record<string, unknown>;
  result?: unknown;
  summary?: string;
  status?: string;
  reason?: string;
  ticketId?: string;
  handedOver?: boolean;
  source?: string;
  durationMs?: number;
  error?: string;
}

export interface ChatRequest {
  conversationId?: string;
  userId?: string;
  content: string;
}

// Legacy single-shot response kept for the few call sites that still
// expect it (admin replay, tests). The runtime chat path uses
// streamChat() instead.
export interface ChatResponse {
  conversationId: string;
  message: ChatMessage;
  userMessage: ChatMessage;
  intent: Intent;
  intentConfidence: number;
  handedOver: boolean;
  source: 'faq' | 'llm' | 'handover' | string;
}

export interface FeedbackInput {
  conversationId: string;
  rating: number;
  comment?: string;
}

export interface Conversation {
  id: string;
  userId: string;
  title: string;
  status: 'open' | 'handed_over' | 'closed';
  handedOver: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface ConversationDetail {
  conversation: Conversation;
  messages: ChatMessage[];
  feedback?: { rating: number; comment: string; createdAt: string };
  skillInvocations?: SkillInvocation[];
}

export interface SkillInvocation {
  id: string;
  conversationId: string;
  skillName: string;
  argsJson: string;
  resultJson?: string;
  status: 'ok' | 'pending_human' | 'error' | 'timeout' | string;
  pendingTicket?: string;
  traceId?: string;
  stepIndex: number;
  durationMs: number;
  createdAt: string;
}

export interface ListConversationsResponse {
  total: number;
  items: Conversation[];
  page: number;
  pageSize: number;
}

export interface SatisfactionStat {
  total: number;
  average: number;
  distribution: [number, number, number, number, number];
  byDay: { day: string; count: number; avg: number }[];
}

export interface LoginResponse {
  token: string;
  expiresIn: number;
  username: string;
  role: string;
  // v2.2 PR1：JWT claims 扩展。Permissions 与后端 auth.Claims.Permissions 对齐：
  //   - 老 token 兼容路径下：admin → ["*"]，非 admin → ["chat.use","feedback.submit"]
  //   - 新 token：精确的权限码列表
  permissions?: string[];
  // v2.2 PR1：可选 email / tenant_id 字段。
  email?: string;
  tenant_id?: string;
}

// WhoamiResponse 是 GET /api/admin/me 的返回结构。v2.2 PR1 扩展了
// permissions / email / tenant_id 三个字段。
export interface WhoamiResponse {
  username: string;
  role: string;
  permissions?: string[];
  email?: string;
  tenant_id?: string;
}

// ---------------------------------------------------------------------------
// Skill admin
// ---------------------------------------------------------------------------

export interface Skill {
  id: string;
  name: string;
  description: string;
  category: string;
  parametersJson: string;
  handlerKind: 'http' | 'echo' | string;
  handlerConfig: string;
  enabled: boolean;
  requiresHuman: boolean;
  readOnly: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface SkillMeta {
  name: string;
  description: string;
  category: string;
  enabled: boolean;
  requiresHuman: boolean;
  readOnly: boolean;
  source: 'builtin' | 'fs' | 'db' | string;
}

export interface SkillListResponse {
  total: number;
  items: Skill[];
  meta: SkillMeta[];
}

export interface ConfirmRequest {
  ticketId: string;
  userId: string;
}

// ---------------------------------------------------------------------------
// Jev decision layer (v2.1)
// ---------------------------------------------------------------------------

export type JevTrigger =
  | 'message.entry'
  | 'message.pre_ingest'
  | 'reply.pre_ingest'
  | 'handover.pre'
  | 'ticket.create'
  | 'agent.assign';

export type JevOutputType = 'choice' | 'score' | 'noul';

export type JevTemplateStatus =
  | 'draft'
  | 'pending'
  | 'approved'
  | 'published'
  | 'archived';

export interface JevTemplate {
  id: string;
  tenantId: string;
  name: string;
  version: number;
  trigger: JevTrigger;
  outputType: JevOutputType;
  labels: string[];
  instructions: string;
  fallback: { type: string; value?: string; score?: Record<string, unknown> };
  definitionYaml: string;
  status: JevTemplateStatus;
  industryCode?: string;
  createdBy?: string;
  createdAt: string;
  publishedAt?: string;
  archivedAt?: string;
}

export interface JevTemplateListResponse {
  total: number;
  items: JevTemplate[];
}

export interface JevDecision {
  id: string;
  tenantId: string;
  templateName: string;
  templateVersion: number;
  trigger: JevTrigger;
  inputHash: string;
  inputJson: string;
  outputJson: string;
  fallback: boolean;
  confidence?: number;
  latencyMs: number;
  costUsd: number;
  status: 'decided' | 'reviewed' | 'accepted' | 'rejected';
  groundTruth?: string;
  reviewedBy?: string;
  reviewedAt?: string;
  traceId: string;
  createdAt: string;
}

export interface JevDecisionListResponse {
  total: number;
  items: JevDecision[];
  limit: number;
  offset: number;
}

export interface JevTemplateStat {
  template: string;
  count: number;
  fallback: number;
  fallbackRatio: number;
}

export interface JevStats {
  total: number;
  fallbackRate: number;
  p95LatencyMs: number;
  byTemplate: JevTemplateStat[];
}

// ---------------------------------------------------------------------------
// v2.2 PR2: RBAC / Audit / Rate limit types
// ---------------------------------------------------------------------------

// Permission 码（与后端 rbac/permission.go 一一对应）。
export interface Permission {
  code: string;
  description: string;
  group_name: string;
}

// Role 角色记录。
export interface Role {
  id: string;
  tenant_id: string;
  name: string;
  description: string;
  permissions: string[];
  is_system: boolean;
  user_count?: number;
  created_at: string;
  updated_at: string;
}

// AuditLog 审计日志记录。
export interface AuditLog {
  id: string;
  tenant_id: string;
  timestamp: string;
  actor_id: string;
  actor_email: string;
  action: string;
  target_type?: string;
  target_id?: string;
  ip?: string;
  user_agent?: string;
  payload_json?: Record<string, unknown>;
}

export interface AuditListResponse {
  items: AuditLog[];
  total: number;
  limit: number;
  offset: number;
}

// RateLimitConfig 限流配置。
export interface RateLimitConfig {
  id: string;
  tenant_id: string;
  endpoint: string;
  dimension: 'ip' | 'tenant_id' | 'actor_id' | string;
  per_minute: number;
  per_hour: number;
  burst: number;
  enabled: boolean;
  description: string;
}

export interface RateLimitListResponse {
  items: RateLimitConfig[];
  total: number;
}

// 8 类 action 常量（与后端 audit/action.go 对齐）。
export const AUDIT_ACTIONS = [
  'auth.login',
  'auth.logout',
  'role.create',
  'role.update',
  'role.delete',
  'role.assign',
  'template.create',
  'template.publish',
  'template.archive',
  'template.delete',
  'skills.create',
  'skills.toggle',
  'skills.delete',
  'conversation.delete',
  'ratelimit.config.update',
] as const;
export type AuditAction = (typeof AUDIT_ACTIONS)[number];

// ---------------------------------------------------------------------------
// v2.2 PR5: SSE / CSV export / bulk operations
// ---------------------------------------------------------------------------

/** Payload of the "stats_update" SSE event. */
export interface SSEStatsSnapshot {
  timestamp: string;
  total_decisions: number;
  fallback_rate: number;
  p95_ms: number;
  accept_rate: number;
  by_template?: { template: string; count: number; fallback: number }[];
}

/** Result of a bulk operation (success / partial failure). */
export interface BulkResult {
  id: string;
  status: 'ok' | 'archived' | 'tagged' | 'not_found' | 'unchanged' | 'error';
  error?: string;
}

export interface BulkResponse {
  total: number;
  succeeded: number;
  results: BulkResult[];
  /** Some bulk endpoints return extra fields (e.g. `enabled` for skills). */
  enabled?: boolean;
}