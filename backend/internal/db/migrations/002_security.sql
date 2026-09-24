-- 002_security.sql
-- Adds the request_nonces table used by the HMAC anti-replay check.
-- Entries expire automatically; we prune with a best-effort cleanup at
-- every successful verification (cheap; bounded by TTL).

CREATE TABLE IF NOT EXISTS request_nonces (
    nonce      TEXT PRIMARY KEY,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_nonce_expires ON request_nonces(expires_at);
