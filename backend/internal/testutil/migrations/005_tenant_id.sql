-- 005_tenant_id.sql
-- v2.1 第一波：把 tenant_id 一级索引注入到所有业务表。v1 单租户的所有数据
-- 统一 backfill 为 'tnt_default'。后续 v2.2 多租户接入时，由 TenantGuard
-- 中间件强制按 ctx 中的 tenant_id 过滤，未传 ctx 的查询默认只看到默认租户。
--
-- 设计要点：
-- 1. 所有 ALTER ADD COLUMN 都带 NOT NULL DEFAULT 'tnt_default'，存量数据
--    自动 backfill，无需 UPDATE。
-- 2. 复合索引 (tenant_id, ...) 放在最左列，配合 TenantGuard 走 index scan。
-- 3. conversations/user_id/skills.name 仍然保持 UNIQUE；多租户落地后
--    这些约束会演变为 UNIQUE(tenant_id, ...) 或 (tenant_id, name)，
--    本期先不动（v2.2 ADR-002 评估）。
-- 4. UNIQUE INDEX 改动要 drop 旧的、再 create 复合的；SQLite 不支持
--    ALTER TABLE 修改约束，只能 drop+recreate。

-- ===== conversations =====
ALTER TABLE conversations ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';
DROP INDEX IF EXISTS idx_conv_user;
DROP INDEX IF EXISTS idx_conv_status;
CREATE INDEX idx_conv_tenant_updated ON conversations(tenant_id, updated_at DESC);
CREATE INDEX idx_conv_tenant_user    ON conversations(tenant_id, user_id, updated_at DESC);
CREATE INDEX idx_conv_tenant_status  ON conversations(tenant_id, status, updated_at DESC);

-- ===== messages =====
ALTER TABLE messages ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';
-- Keep idx_msg_conv intact so existing ORDER BY conversation_id, created_at
-- queries stay stable; v1 tests rely on the rowid-from-index ordering. Add a
-- tenant-aware composite index alongside for v2.1 TenantGuard use cases.
CREATE INDEX IF NOT EXISTS idx_msg_tenant_conv ON messages(tenant_id, conversation_id, created_at);

-- ===== skills =====
ALTER TABLE skills ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';
CREATE INDEX idx_skills_tenant ON skills(tenant_id, enabled);

-- ===== skill_invocations =====
ALTER TABLE skill_invocations ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';
CREATE INDEX idx_inv_tenant_conv  ON skill_invocations(tenant_id, conversation_id, created_at);
CREATE INDEX idx_inv_tenant_trace ON skill_invocations(tenant_id, trace_id);

-- ===== skill_pending_tickets =====
ALTER TABLE skill_pending_tickets ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';
CREATE INDEX idx_ticket_tenant_conv    ON skill_pending_tickets(tenant_id, conversation_id, created_at);
CREATE INDEX idx_ticket_tenant_status  ON skill_pending_tickets(tenant_id, status, expires_at);
CREATE INDEX idx_ticket_tenant_user    ON skill_pending_tickets(tenant_id, user_id, created_at);

-- ===== mock_orders =====
ALTER TABLE mock_orders ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';
DROP INDEX IF EXISTS idx_mock_order_user;
CREATE INDEX idx_mock_order_tenant_user ON mock_orders(tenant_id, user_id);

-- ===== mock_coupons =====
ALTER TABLE mock_coupons ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';
DROP INDEX IF EXISTS idx_coupon_user;
CREATE INDEX idx_coupon_tenant_user ON mock_coupons(tenant_id, user_id, used);

-- ===== mock_points =====
ALTER TABLE mock_points ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';

-- ===== feedback =====
ALTER TABLE feedback ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';
CREATE INDEX idx_feedback_tenant ON feedback(tenant_id, created_at);

-- ===== faqs =====
ALTER TABLE faqs ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';
DROP INDEX IF EXISTS idx_faq_cat;
CREATE INDEX idx_faq_tenant_cat ON faqs(tenant_id, category, enabled);

-- ===== handover_signals =====
ALTER TABLE handover_signals ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';
CREATE INDEX idx_signal_tenant_conv ON handover_signals(tenant_id, conversation_id, created_at);

-- ===== admin_users =====
-- admin_users 不打 tenant_id 标签——admin 是平台级账号，可登录多个租户的
-- 管理后台（v2.2 加 rbac_user_roles 后再绑定 tenant）。这里只是补字段
-- 占位便于 schema 一致性；业务查询不按 tenant 过滤。
ALTER TABLE admin_users ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'tnt_default';
