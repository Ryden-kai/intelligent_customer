-- 003_admin_users.sql
-- Admin accounts live in their own table so passwords can be hashed with
-- Argon2id instead of living as plaintext in env vars. password_hash stores
-- the standard PHC string ($argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>)
-- produced by internal/security.HashPassword.
--
-- The original env-var credentials (ADMIN_USERNAME / ADMIN_PASSWORD) are
-- only used to bootstrap the very first admin row when the table is empty;
-- after that, passwords are managed via the `adminctl` CLI.

CREATE TABLE IF NOT EXISTS admin_users (
    id             TEXT PRIMARY KEY,
    username       TEXT NOT NULL UNIQUE,
    password_hash  TEXT NOT NULL,             -- Argon2id PHC encoded string
    role           TEXT NOT NULL DEFAULT 'admin',
    created_at     INTEGER NOT NULL,          -- unix ms
    updated_at     INTEGER NOT NULL,
    last_login_at  INTEGER                    -- nullable, unix ms
);
CREATE INDEX IF NOT EXISTS idx_admin_username ON admin_users(username);