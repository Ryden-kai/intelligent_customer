-- 007_rbac_audit_ratelimit.sql
-- v2.2 PR1 基础设施层：5 张新表 + users 表新增 default_role_id。
-- 设计原则（详见 docs/v2-architecture-design.md §4 + docs/v2-ui-security-prd.md §7）：
--   1. roles / permissions / user_roles 是 RBAC 体系的载体；permissions 作为参考表存 25 个预置码；
--   2. role.permissions 用 JSON 数组存（v2.2 简化路径；多对多表 role_permissions 留 v2.2.1+ 升级）；
--   3. audit_logs 覆盖 8 类关键操作（auth / role / template / skills / conversation）；
--   4. rate_limit_configs 5 类端点（login / register / chat / feedback / admin write）；
--   5. users 表 default_role_id 兜底单租户场景的默认角色；
--   6. 全部时间戳字段统一 unix ms（与已有 migrations 风格一致）。

-- ====== roles ======
-- 角色定义。系统预置 3 个：admin / agent / user；后续可自定义。
-- permissions 是 JSON 数组，存该角色拥有的权限码（如 ["chat.use","feedback.submit"]）；
-- admin 用 ["*"] 标识全权限。
CREATE TABLE IF NOT EXISTS roles (
    id           TEXT PRIMARY KEY,                 -- 'role-admin-tnt_default' / 'role-custom-001'
    tenant_id    TEXT NOT NULL DEFAULT 'tnt_default',
    name         TEXT NOT NULL,                    -- 'admin' / 'agent' / 'user' / 'custom_role'
    description  TEXT NOT NULL DEFAULT '',
    permissions_json TEXT NOT NULL DEFAULT '[]',  -- JSON 数组存权限码
    is_system    INTEGER NOT NULL DEFAULT 0,       -- 1 = 系统预置不可删
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    UNIQUE(tenant_id, name)
);
CREATE INDEX IF NOT EXISTS idx_roles_tenant ON roles(tenant_id, created_at);

-- ====== permissions ======
-- 权限码参考表。25 个预置权限；code 是主键；group_name 聚合用（如 'jev' / 'conversation'）。
CREATE TABLE IF NOT EXISTS permissions (
    code         TEXT PRIMARY KEY,                  -- 'jev.template.read'
    description  TEXT NOT NULL,
    group_name   TEXT NOT NULL,                     -- 'jev' / 'conversation' / 'skills' / 'audit' / 'user' / 'role' / 'stats' / 'ratelimit'
    created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_permissions_group ON permissions(group_name, code);

-- ====== user_roles ======
-- 用户—角色 多对多关系。复合主键 (user_id, role_id, tenant_id) 支持一个用户在不同租户有不同角色。
CREATE TABLE IF NOT EXISTS user_roles (
    user_id     TEXT NOT NULL,
    role_id     TEXT NOT NULL,
    tenant_id   TEXT NOT NULL DEFAULT 'tnt_default',
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (user_id, role_id, tenant_id),
    FOREIGN KEY(role_id) REFERENCES roles(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_user_roles_user ON user_roles(user_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_role ON user_roles(role_id);

-- ====== audit_logs ======
-- 审计流水表。8 类操作：auth.login / auth.logout / role.create|update|delete|assign /
-- template.publish|archive|delete / skills.create|toggle|delete / conversation.delete。
-- payload_json 是 JSON 字符串，存额外上下文（如 {template_id, old_status, new_status}）。
CREATE TABLE IF NOT EXISTS audit_logs (
    id            TEXT PRIMARY KEY,                 -- 'log-20260925-143218-001'
    tenant_id     TEXT NOT NULL DEFAULT 'tnt_default',
    timestamp     INTEGER NOT NULL,                 -- unix ms
    actor_id      TEXT NOT NULL,                    -- user_id 或 admin_user_id
    actor_email   TEXT NOT NULL DEFAULT '',
    action        TEXT NOT NULL,                    -- 'template.publish' / 'auth.login' ...
    target_type   TEXT,                             -- 'template' / 'skill' / 'conversation' / 'user' / 'role'
    target_id     TEXT,
    ip            TEXT,
    user_agent    TEXT,
    payload_json  TEXT,
    created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant_time ON audit_logs(tenant_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor      ON audit_logs(actor_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action     ON audit_logs(action, timestamp DESC);

-- ====== rate_limit_configs ======
-- 限流配置。5 类端点：login / register / chat / feedback / admin write。
-- dimension: 'ip' / 'tenant_id' / 'actor_id'。
-- 限流 key = dimension:value:endpoint（详见 v2-architecture-design.md §3.6）。
CREATE TABLE IF NOT EXISTS rate_limit_configs (
    id            TEXT PRIMARY KEY,                 -- 'rlc-default-login-ip'
    tenant_id     TEXT NOT NULL DEFAULT 'tnt_default',
    endpoint      TEXT NOT NULL,                    -- 'POST:/api/auth/login'
    dimension     TEXT NOT NULL,                    -- 'ip' / 'tenant_id' / 'actor_id'
    per_minute    INTEGER NOT NULL DEFAULT 60,
    per_hour      INTEGER NOT NULL DEFAULT 1000,
    burst         INTEGER NOT NULL DEFAULT 10,
    enabled       INTEGER NOT NULL DEFAULT 1,
    description   TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    UNIQUE(tenant_id, endpoint, dimension)
);
CREATE INDEX IF NOT EXISTS idx_rate_limit_tenant ON rate_limit_configs(tenant_id, endpoint);

-- ====== admin_users.default_role_id ======
-- 给 admin_users 表加默认角色外键（v2.2 实际承载"用户—角色"的表）。
-- 业务上 admin_users 才是真正的"管理员账号"表（v1 bootstrap 用）；
-- tenant_users 是租户—账号多对多表（v2.1 预留），本 PR 不绑定。
-- SQLite ALTER ADD COLUMN 支持。SQLite 不支持 DROP COLUMN，
-- 故回滚时需要保留 admin_users.default_role_id 字段
-- （详见 008_rollback_rbac_audit_ratelimit.sql 的注释）。
ALTER TABLE admin_users ADD COLUMN default_role_id TEXT REFERENCES roles(id);