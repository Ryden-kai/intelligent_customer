-- 004_skills.sql
-- Skill registry persistence. Built-in skills live as Go constants; this
-- table stores the dynamic ones so admins can create / edit / disable them
-- without redeploying. Disabled skills are kept in the table for audit, but
-- the agent never exposes them to the LLM.

CREATE TABLE IF NOT EXISTS skills (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,         -- tool name exposed to LLM (e.g. "query_order")
    description     TEXT NOT NULL,                -- shown to LLM in tool list
    category        TEXT NOT NULL DEFAULT 'general', -- order | coupon | refund | general
    parameters_json TEXT NOT NULL DEFAULT '{}',   -- JSON Schema for tool arguments
    handler_kind    TEXT NOT NULL DEFAULT 'http', -- http | builtin_ref
    handler_config  TEXT NOT NULL DEFAULT '{}',   -- for http: {"url":"...","method":"POST"}; for builtin_ref: {"ref":"..."}
    enabled         INTEGER NOT NULL DEFAULT 1,
    requires_human  INTEGER NOT NULL DEFAULT 0,    -- 1 = must be human-confirmed before commit
    read_only       INTEGER NOT NULL DEFAULT 1,    -- 1 = cannot mutate state (enforced on dynamic skills)
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_skills_enabled ON skills(enabled);
CREATE INDEX IF NOT EXISTS idx_skills_name    ON skills(name);

-- Audit table: every skill invocation the agent performed. Lets us
-- reconstruct "what did the agent do for this conversation" without
-- scraping logs.
CREATE TABLE IF NOT EXISTS skill_invocations (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    skill_name      TEXT NOT NULL,
    args_json       TEXT NOT NULL DEFAULT '{}',
    result_json     TEXT,
    status          TEXT NOT NULL DEFAULT 'ok',   -- ok | pending_human | error | timeout
    pending_ticket  TEXT,                          -- when status=pending_human, ticket id
    trace_id        TEXT NOT NULL DEFAULT '',
    step_index      INTEGER NOT NULL DEFAULT 0,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_inv_conv  ON skill_invocations(conversation_id, created_at);
CREATE INDEX IF NOT EXISTS idx_inv_trace ON skill_invocations(trace_id);

-- Pending human-confirmation tickets (refund etc.). The agent never
-- applies the mutation until the user calls /api/skills/confirm with the
-- matching ticket id. Tickets auto-expire after REFUND_TICKET_TTL.
CREATE TABLE IF NOT EXISTS skill_pending_tickets (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT,                           -- nullable: agent creates the ticket before knowing conv id, then back-fills
    user_id         TEXT NOT NULL,
    skill_name      TEXT NOT NULL,
    payload_json    TEXT NOT NULL,                 -- args that will be applied on confirm
    summary         TEXT NOT NULL,                 -- human-readable preview shown to user
    status          TEXT NOT NULL DEFAULT 'pending', -- pending | confirmed | expired | cancelled
    expires_at      INTEGER NOT NULL,
    created_at      INTEGER NOT NULL,
    confirmed_at    INTEGER,
    FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_ticket_conv    ON skill_pending_tickets(conversation_id, created_at);
CREATE INDEX IF NOT EXISTS idx_ticket_status  ON skill_pending_tickets(status, expires_at);

-- Mock data the order/coupon skills read from. Replace with real upstream
-- calls in production; the skill contract doesn't change.
CREATE TABLE IF NOT EXISTS mock_orders (
    order_id     TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL,
    status       TEXT NOT NULL,        -- pending | paid | shipped | delivered | refunding | refunded
    amount_cents INTEGER NOT NULL,
    carrier      TEXT NOT NULL DEFAULT '',
    tracking_no  TEXT NOT NULL DEFAULT '',
    paid_at      INTEGER,
    shipped_at   INTEGER,
    delivered_at INTEGER,
    created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mock_order_user ON mock_orders(user_id);

CREATE TABLE IF NOT EXISTS mock_coupons (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL,
    code        TEXT NOT NULL,
    amount_cents INTEGER NOT NULL,
    min_order_cents INTEGER NOT NULL DEFAULT 0,
    expires_at  INTEGER NOT NULL,
    used        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_coupon_user ON mock_coupons(user_id, used);

CREATE TABLE IF NOT EXISTS mock_points (
    user_id     TEXT PRIMARY KEY,
    balance     INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL
);