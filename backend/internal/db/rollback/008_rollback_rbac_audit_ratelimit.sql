-- 008_rollback_rbac_audit_ratelimit.sql
-- v2.2 PR1 回滚脚本：撤销 007 的所有 DDL 变更。
--
-- ⚠️ **本脚本不默认执行**（db.Migrate 仍按文件名顺序执行，但 008 只在显式触发时调用）。
--    正常启动路径走 001 → 002 → 003 → 004 → 005 → 006 → 007，不会执行 008。
--    如需真正回滚，请人工执行：sqlite3 your.db < 008_rollback_rbac_audit_ratelimit.sql
--
-- 注意事项：
--   1. SQLite 不支持 DROP COLUMN，所以 users.default_role_id 字段会保留（v2.2 数据兼容）。
--      业务逻辑不再引用该字段即可，迁移到下一个 DB 版本（如 Postgres）时再 DROP。
--   2. 表的删除顺序遵循外键依赖的反向：先删依赖表，后删被引用表。
--   3. 索引先于表删除（虽然 DROP TABLE 会自动删除其索引，但显式 DROP INDEX
--      让脚本更易审计）。

-- ====== rate_limit_configs ======
DROP INDEX IF EXISTS idx_rate_limit_tenant;
DROP TABLE IF EXISTS rate_limit_configs;

-- ====== audit_logs ======
DROP INDEX IF EXISTS idx_audit_logs_action;
DROP INDEX IF EXISTS idx_audit_logs_actor;
DROP INDEX IF EXISTS idx_audit_logs_tenant_time;
DROP TABLE IF EXISTS audit_logs;

-- ====== user_roles ======
DROP INDEX IF EXISTS idx_user_roles_role;
DROP INDEX IF EXISTS idx_user_roles_user;
DROP TABLE IF EXISTS user_roles;

-- ====== permissions ======
DROP INDEX IF EXISTS idx_permissions_group;
DROP TABLE IF EXISTS permissions;

-- ====== roles ======
DROP INDEX IF EXISTS idx_roles_tenant;
DROP TABLE IF EXISTS roles;

-- ====== admin_users.default_role_id ======
-- SQLite 不支持 DROP COLUMN；该字段会保留在 admin_users 表中。
-- 应用层不再读取此字段即可视为回滚完成。
-- 若需要彻底清理：使用 SQLite 12.0+ 的 ALTER TABLE DROP COLUMN（见 SQLite 官方文档），
-- 或手动重建 admin_users 表（PRAGMA foreign_keys=OFF; CREATE TABLE admin_users_new ...; INSERT ...;）。
-- ALTER TABLE admin_users DROP COLUMN default_role_id;  -- 留手动