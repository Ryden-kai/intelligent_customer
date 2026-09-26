#!/usr/bin/env bash
# 把一个 readonly skill 插入 SQLite `skills` 表，演示后台 CRUD 路径。
# 用法: bash scripts/seed_demo_skills.sh [db_path]
#
# 默认 DB_PATH 走 ./data/intelligent_customer.db；若 .env 的 DB_PATH 不同请用参数。
# 用 python 的 sqlite3 标准库（沙箱里 sqlite3 命令行会被 vec0 shim 替换）。

set -euo pipefail
cd "$(dirname "$0")/.." || exit 1

DB_PATH="${1:-${DB_PATH:-./data/intelligent_customer.db}}"

if [[ ! -f "$DB_PATH" ]]; then
  echo "DB not found: $DB_PATH" >&2
  echo "  先跑一次 ./bin/intelligent_customer 让它迁移 + 建表" >&2
  exit 1
fi

NOW_MS=$(date +%s%3N)
ID="db-demo-$(date +%s)"

python3 - "$DB_PATH" "$ID" "$NOW_MS" <<'PY'
import sys, sqlite3
db, sid, now_ms = sys.argv[1], sys.argv[2], sys.argv[3]
con = sqlite3.connect(db)
con.execute("""
INSERT INTO skills(id, name, description, category, parameters_json, handler_kind, handler_config,
                  enabled, requires_human, read_only, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 1, 0, 1, ?, ?)
""", (
    sid,
    'demo_db_currency',
    '示例 skill（db loader）：演示通过 SQLite CRUD 创建的动态 skill，回显入参。',
    'general',
    '{"type":"object","properties":{"amount":{"type":"number"},"from":{"type":"string"}},"required":["amount"]}',
    'echo',
    '{}',
    now_ms, now_ms,
))
con.commit()
con.close()
PY

echo "✓ inserted skill id=${ID} name=demo_db_currency into ${DB_PATH}"
echo "  在管理后台 /admin/skills 可见，或 curl POST /api/admin/skills/reload 热加载"