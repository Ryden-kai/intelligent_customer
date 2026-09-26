-- 006_jev_decision_layer.sql
-- v2.1 Jev 决策层 + LLM 三通道 + 行业模板 新表。
-- 设计原则（详见 docs/v2-roadmap.md / v2-jev-rollout.md / v2-llm-routing.md）：
--   * 模板按 tenant_id 隔离；同 tenant 下 name+version UNIQUE
--   * 决策日志带 input_hash（SHA-256(input_json)）用于回灌去重
--   * llm_call_log 用于按 tenant/model 归因成本
--   * industry_templates 是平台级共享资源（不按 tenant 隔离），由
--     loader.go Activate() 把具体模板拷到 jev_templates 表

-- ====== tenants ======
CREATE TABLE IF NOT EXISTS tenants (
    id           TEXT PRIMARY KEY,            -- 'tnt_default' / 'tnt_acme' / ...
    name         TEXT NOT NULL,
    region       TEXT NOT NULL DEFAULT 'cn', -- cn | intl — 决定 LLM 主通道
    status       TEXT NOT NULL DEFAULT 'active', -- active | suspended
    llm_primary  TEXT NOT NULL DEFAULT 'openai',
    llm_secondary TEXT NOT NULL DEFAULT 'openrouter',
    llm_tertiary TEXT NOT NULL DEFAULT 'minimax',
    config_json  TEXT NOT NULL DEFAULT '{}',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

-- 默认租户：v1 存量数据全部归 tnt_default
INSERT OR IGNORE INTO tenants(id, name, region, status, created_at, updated_at)
VALUES ('tnt_default', 'Default Tenant', 'cn', 'active',
        CAST(strftime('%s','now')*1000 AS INTEGER),
        CAST(strftime('%s','now')*1000 AS INTEGER));

-- ====== tenant_users ======
-- v2.2 才完整用（RBAC + 多 admin）；v2.1 先建表，预留扩展位
CREATE TABLE IF NOT EXISTS tenant_users (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    username    TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'agent', -- platform_admin | tenant_admin | agent | supervisor | viewer
    created_at  INTEGER NOT NULL,
    UNIQUE(tenant_id, username),
    FOREIGN KEY(tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_tenant_users_tenant ON tenant_users(tenant_id);

-- ====== jev_templates ======
-- 模板按 tenant_id 隔离；同 tenant 下 name+version UNIQUE。
-- definition_yaml 是完整 YAML（与 backend/templates/jev/*.yaml 同构）。
-- 优先级：DB.status='published' > FS YAML > 内置默认。
CREATE TABLE IF NOT EXISTS jev_templates (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    name            TEXT NOT NULL,             -- intent_routing | sensitive_check | ...
    version         INTEGER NOT NULL DEFAULT 1,
    trigger         TEXT NOT NULL,             -- message.entry | message.pre_ingest | ...
    output_type     TEXT NOT NULL,             -- choice | score | noul
    labels_json     TEXT NOT NULL DEFAULT '[]',
    instructions    TEXT NOT NULL,
    fallback_json   TEXT NOT NULL DEFAULT '{}',
    definition_yaml TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'draft', -- draft | pending | approved | published | archived
    industry_code   TEXT,                       -- 关联 industry_templates.code（可空）
    created_by      TEXT,
    created_at      INTEGER NOT NULL,
    published_at    INTEGER,
    archived_at     INTEGER,
    UNIQUE(tenant_id, name, version),
    FOREIGN KEY(tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_jev_t_tenant_status ON jev_templates(tenant_id, status, trigger);
CREATE INDEX IF NOT EXISTS idx_jev_t_industry ON jev_templates(industry_code);

-- ====== jev_decisions ======
-- 决策日志 + 效果回灌输入：每次 Orchestrator.Decide 写一行。
-- status 默认 'decided'；loopback 写入后变 'reviewed' / 'accepted' / 'rejected'。
CREATE TABLE IF NOT EXISTS jev_decisions (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL,
    template_name TEXT NOT NULL,                -- intent_routing | sensitive_check | ...
    template_version INTEGER NOT NULL DEFAULT 1,
    trigger       TEXT NOT NULL,                -- message.entry | ...
    input_hash    TEXT NOT NULL,                -- SHA-256(input_json) hex（64 chars）
    input_json    TEXT NOT NULL,
    output_json   TEXT NOT NULL,                -- {"choice":"...", "scores":{...}, "latency_ms":...}
    fallback      INTEGER NOT NULL DEFAULT 0,   -- 1 = 由 fallback 兜底产生
    confidence    REAL,
    latency_ms    INTEGER NOT NULL DEFAULT 0,
    cost_usd      REAL NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'decided', -- decided | reviewed | accepted | rejected
    ground_truth  TEXT,                          -- 人工标注或满意度反哺（JSON）
    reviewed_by   TEXT,
    reviewed_at   INTEGER,
    trace_id      TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jev_d_tenant_created   ON jev_decisions(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_jev_d_tenant_template  ON jev_decisions(tenant_id, template_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_jev_d_tenant_status    ON jev_decisions(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_jev_d_input_hash       ON jev_decisions(tenant_id, input_hash);

-- ====== industry_templates ======
-- 平台级（不按 tenant 隔离）：行业模板定义，启动时由 loader.go 加载到 jev_templates。
CREATE TABLE IF NOT EXISTS industry_templates (
    code         TEXT PRIMARY KEY,              -- 'general_v1' | 'medicine_gsp_v1' | ...
    name         TEXT NOT NULL,
    version      INTEGER NOT NULL DEFAULT 1,
    description  TEXT NOT NULL DEFAULT '',
    intent_labels_json TEXT NOT NULL DEFAULT '[]',
    sensitive_words_json TEXT NOT NULL DEFAULT '[]',
    priority_rules_json  TEXT NOT NULL DEFAULT '{}',
    config_yaml  TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'published', -- draft | published | archived
    created_at   INTEGER NOT NULL,
    UNIQUE(code, version)
);

-- ====== llm_call_log ======
-- LLM 三通道调用日志：按 tenant / model 归因成本、统计 fallback 触发率。
CREATE TABLE IF NOT EXISTS llm_call_log (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    channel         TEXT NOT NULL,             -- primary | secondary | tertiary
    provider        TEXT NOT NULL,             -- openai | openrouter | minimax
    model           TEXT NOT NULL,
    prompt_tokens   INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens    INTEGER NOT NULL DEFAULT 0,
    latency_ms      INTEGER NOT NULL DEFAULT 0,
    fallback        INTEGER NOT NULL DEFAULT 0, -- 1 = 此调用是 fallback 触发的
    error_class     TEXT,                       -- 当 status != ok 时填
    status          TEXT NOT NULL DEFAULT 'ok', -- ok | error | timeout
    trace_id        TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_llm_tenant_created ON llm_call_log(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_llm_tenant_channel ON llm_call_log(tenant_id, channel, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_llm_tenant_status  ON llm_call_log(tenant_id, status);

-- ====== agent_presence ======
-- 坐席在线状态：v2.1 预留位（Agent Workspace 文档已规划，本期不接前端）。
-- status: idle | ringing | active | wrap_up | offline
CREATE TABLE IF NOT EXISTS agent_presence (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    username        TEXT NOT NULL,
    display_name    TEXT NOT NULL DEFAULT '',
    skill_group     TEXT NOT NULL DEFAULT 'default',
    status          TEXT NOT NULL DEFAULT 'offline',
    max_concurrent  INTEGER NOT NULL DEFAULT 5,
    active_sessions INTEGER NOT NULL DEFAULT 0,
    last_heartbeat  INTEGER,
    UNIQUE(tenant_id, username),
    FOREIGN KEY(tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_presence_tenant_status ON agent_presence(tenant_id, status);
