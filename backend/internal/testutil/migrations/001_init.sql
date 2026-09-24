-- 001_init.sql
-- Initial schema for the intelligent customer service.
-- All tables are append-only with surrogate UUID primary keys.

CREATE TABLE IF NOT EXISTS conversations (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL,
    title        TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'open',   -- open | handed_over | closed
    handed_over  INTEGER NOT NULL DEFAULT 0,     -- boolean
    created_at   INTEGER NOT NULL,               -- unix ms
    updated_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_conv_user      ON conversations(user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_conv_status    ON conversations(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS messages (
    id               TEXT PRIMARY KEY,
    conversation_id  TEXT NOT NULL,
    role             TEXT NOT NULL,              -- user | system | assistant | agent
    content          TEXT NOT NULL,
    intent           TEXT,                       -- refund | order | tech | other | unknown
    intent_confidence REAL,
    model            TEXT,                       -- which model produced this (jev | MiniMax | system)
    created_at       INTEGER NOT NULL,
    FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_msg_conv ON messages(conversation_id, created_at);

CREATE TABLE IF NOT EXISTS feedback (
    id               TEXT PRIMARY KEY,
    conversation_id  TEXT NOT NULL,
    rating           INTEGER NOT NULL,             -- 1..5
    comment          TEXT NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,
    UNIQUE(conversation_id),
    FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS faqs (
    id           TEXT PRIMARY KEY,
    category     TEXT NOT NULL,                  -- refund | order | tech | general
    question     TEXT NOT NULL,
    answer       TEXT NOT NULL,
    keywords     TEXT NOT NULL DEFAULT '',       -- comma-separated for quick scan
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_faq_cat ON faqs(category, enabled);

CREATE TABLE IF NOT EXISTS handover_signals (
    id               TEXT PRIMARY KEY,
    conversation_id  TEXT NOT NULL,
    source           TEXT NOT NULL,              -- jev | user_request | fallback
    detail           TEXT NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,
    FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_signal_conv ON handover_signals(conversation_id, created_at);