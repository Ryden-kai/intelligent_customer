#!/usr/bin/env bash
# 端到端 demo（不需要真 LLM key）。
#
# 流程：
#   1. 准备临时 SQLite + 跑迁移
#   2. 启动 ./bin/intelligent_customer
#   3. 通过 /api/admin/skills/reload 热加载 data/skills/*.json
#   4. 验证 3 builtin + 1 fs + 1 db（seed）共 5 条
#   5. 退款 happy path：不走 LLM，直接调 apply_refund skill → 拿 ticket
#      → 调 /api/skills/confirm → 验证 mock_orders.status='refunding'
#   6. cancel ticket 路径
#   7. 清理
#
# 用法: bash scripts/demo.sh

set -o pipefail
cd "$(dirname "$0")/.." || exit 1

LOG_PREFIX="[demo]"
say()  { printf "%s %s\n" "$LOG_PREFIX" "$*"; }
fail() {
  printf "%s FAIL: %s\n" "$LOG_PREFIX" "$*" >&2
  printf "%s --- server log (last 40 lines) ---\n" "$LOG_PREFIX" >&2
  if [[ -f "${DB_DIR:-/dev/null}/server.log" ]]; then
    tail -40 "${DB_DIR}/server.log" >&2 || true
  fi
  printf "%s -------------------------------\n" "$LOG_PREFIX" >&2
  exit 1
}

# ---- 1. 准备二进制 + 临时 DB ----------------------------------------
BIN="./bin/intelligent_customer"
if [[ ! -x "$BIN" ]]; then
  say "构建后端二进制..."
  go build -trimpath -o "$BIN" ./cmd/server
fi

DEMO_BIN="./bin/demo-apply-refund"
if [[ ! -x "$DEMO_BIN" ]]; then
  say "构建 demo-apply-refund 二进制..."
  go build -trimpath -o "$DEMO_BIN" ./cmd/demo-apply-refund
fi

DB_DIR="$(mktemp -d)"
DB_PATH="${DB_DIR}/demo.db"
say "使用临时 DB: ${DB_PATH}"

# env 必须满足 config.validate()
export JWT_SECRET="dev-secret-please-change-in-production-must-be-32b"
export LLM_PROVIDER="openai"
export OPENAI_API_KEY="sk-demo"            # 占位
export OPENAI_BASE_URL="http://127.0.0.1:1" # 不可达 → 触发 llm_error，但 demo 不依赖 chat 路径
export OPENAI_MODEL="gpt-4o-mini"
export SIGNATURE_REQUIRED="false"
export LOG_LEVEL="info"
export LOG_PRETTY="false"
export DB_PATH
export AGENT_MAX_STEPS="2"
export AGENT_MAX_WALLCLOCK="5s"
export CORS_ORIGINS="http://localhost:5173"
export SKILL_DEMO_USER_ID="demo-user"
# 加快后台 sweeper，让 §8 验证自动 expired 不必等满 1 分钟
export TICKET_SWEEP_INTERVAL="500ms"

# ---- 2. 启动服务 -----------------------------------------------------
say "启动服务（后台）..."
"${BIN}" > "${DB_DIR}/server.log" 2>&1 &
SERVER_PID=$!
cleanup() {
  kill "$SERVER_PID" 2>/dev/null || true
  wait "$SERVER_PID" 2>/dev/null || true
  rm -rf "$DB_DIR"
}
trap cleanup EXIT

# 等 /health 通
for i in {1..30}; do
  if curl -sf http://127.0.0.1:8080/health >/dev/null; then
    say "✓ 服务就绪"
    break
  fi
  sleep 0.3
done
curl -sf http://127.0.0.1:8080/health >/dev/null || fail "服务启动超时"

# ---- 3. 登录拿 JWT --------------------------------------------------
say "登录 admin..."
LOGIN=$(curl -sS -X POST http://127.0.0.1:8080/api/admin/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}')
TOKEN=$(printf '%s' "$LOGIN" | python3 -c "import sys,json; print(json.loads(sys.stdin.read())['token'])" 2>/dev/null) || fail "登录失败：${LOGIN}"
[[ -n "$TOKEN" ]] || fail "没拿到 token"
say "✓ JWT 拿到"

# ---- 4. 验证 fs 热加载（启动时已自动 LoadFromFS） ----------------
say "列所有 skill（启动时已加载 data/skills/*.json）..."
LIST=$(curl -sS http://127.0.0.1:8080/api/admin/skills \
  -H "Authorization: Bearer $TOKEN")
if echo "$LIST" | grep -q '"code"'; then
  fail "/api/admin/skills 报错：${LIST}"
fi

# 应看到 demo_echo + demo_currency（fs 加载器） + 3 builtin
DYNAMIC_FS_COUNT=$(echo "$LIST" | python3 -c "import sys,json; m=json.loads(sys.stdin.read())['meta']; print(sum(1 for x in m if x['source']=='fs'))")
BUILTIN_COUNT=$(echo "$LIST" | python3 -c "import sys,json; m=json.loads(sys.stdin.read())['meta']; print(sum(1 for x in m if x['source']=='builtin'))")
say "  builtin=${BUILTIN_COUNT} fs=${DYNAMIC_FS_COUNT}"
[[ "$BUILTIN_COUNT" == "3" ]] || fail "期望 3 个 builtin，实际 ${BUILTIN_COUNT}"
[[ "$DYNAMIC_FS_COUNT" == "2" ]] || fail "期望 2 个 fs (echo + currency)，实际 ${DYNAMIC_FS_COUNT}"

# ---- 5. seed 一个 DB skill ----------------------------------------
say "通过 SQLite 直接 seed demo_db_currency skill..."
SKILL_ID="db-demo-$$"
NOW_MS=$(date +%s%3N)
python3 - "$DB_PATH" "$SKILL_ID" "$NOW_MS" <<'PY'
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
say "✓ inserted ${SKILL_ID}"

# 再 reload 让 in-memory registry 拿到
RELOAD=$(curl -sS -X POST http://127.0.0.1:8080/api/admin/skills/reload \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}')
say "reload: $(echo "$RELOAD" | python3 -c "import sys,json; print(','.join(json.loads(sys.stdin.read())['reloaded']))")"

# 再次确认
LIST2=$(curl -sS http://127.0.0.1:8080/api/admin/skills -H "Authorization: Bearer $TOKEN")
BUILTIN_COUNT=$(echo "$LIST2" | python3 -c "import sys,json; m=json.loads(sys.stdin.read())['meta']; print(sum(1 for x in m if x['source']=='builtin'))")
FS_COUNT=$(echo "$LIST2" | python3 -c "import sys,json; m=json.loads(sys.stdin.read())['meta']; print(sum(1 for x in m if x['source']=='fs'))")
DB_COUNT=$(echo "$LIST2" | python3 -c "import sys,json; m=json.loads(sys.stdin.read())['meta']; print(sum(1 for x in m if x['source']=='db'))")
say "  重新计数：builtin=${BUILTIN_COUNT} fs=${FS_COUNT} db=${DB_COUNT}"
[[ "$BUILTIN_COUNT" == "3" && "$FS_COUNT" == "2" && "$DB_COUNT" == "1" ]] || fail "期望 builtin=3 fs=2 db=1，实际 ${BUILTIN_COUNT}/${FS_COUNT}/${DB_COUNT}"
say "✓ skill 注册数正确：builtin=${BUILTIN_COUNT}, fs=${FS_COUNT}, db=${DB_COUNT}"

# ---- 6. 退款 happy path ---------------------------------------------
say "退款 happy path：调 apply_refund skill（不走 LLM）→ 拿 ticket → confirm → DB mutate"

TICKET_JSON=$(./bin/demo-apply-refund \
  --db "$DB_PATH" --user demo-user --order ORD-1001 \
  --amount 5000 --reason "演示退款")
TICKET_ID=$(printf '%s' "$TICKET_JSON" | python3 -c "import sys,json; print(json.loads(sys.stdin.read())['ticketId'])" 2>/dev/null) || fail "拿 ticket 失败：${TICKET_JSON}"
[[ -n "$TICKET_ID" ]] || fail "ticket id 为空"
TICKET_STATUS=$(printf '%s' "$TICKET_JSON" | python3 -c "import sys,json; print(json.loads(sys.stdin.read())['status'])" 2>/dev/null) || fail "拿 ticket status 失败"
[[ "$TICKET_STATUS" == "pending_human" ]] || fail "demo-apply-refund status 期望 pending_human，实际 ${TICKET_STATUS}"
say "✓ 拿到 ticket id: ${TICKET_ID} (status=${TICKET_STATUS})"

CONFIRM=$(curl -sS -X POST http://127.0.0.1:8080/api/skills/confirm \
  -H "Content-Type: application/json" \
  -d "{\"ticketId\":\"${TICKET_ID}\",\"userId\":\"demo-user\"}")
STATUS=$(printf '%s' "$CONFIRM" | python3 -c "import sys,json; print(json.loads(sys.stdin.read())['status'])" 2>/dev/null) || fail "confirm 失败：${CONFIRM}"
[[ "$STATUS" == "confirmed" ]] || fail "confirm 后 ticket status 期望 confirmed，实际 ${STATUS}"
say "✓ ticket 状态：${STATUS}"

ORDER_STATUS=$(python3 - "$DB_PATH" <<'PY'
import sys, sqlite3
con = sqlite3.connect(sys.argv[1])
row = con.execute("SELECT status FROM mock_orders WHERE order_id='ORD-1001'").fetchone()
print(row[0] if row else '')
con.close()
PY
)
[[ "$ORDER_STATUS" == "refunding" ]] || fail "ORD-1001 status 期望 refunding，实际 ${ORDER_STATUS}"
say "✓ mock_orders.ORD-1001.status = ${ORDER_STATUS}"

# ---- 7. cancel 路径 ------------------------------------------------
say "再开一个 ticket 然后 cancel..."
TICKET2_JSON=$(./bin/demo-apply-refund \
  --db "$DB_PATH" --user demo-user --order ORD-1002 \
  --amount 1000 --reason "演示取消")
TICKET2=$(printf '%s' "$TICKET2_JSON" | python3 -c "import sys,json; print(json.loads(sys.stdin.read())['ticketId'])")
TICKET2_STATUS=$(printf '%s' "$TICKET2_JSON" | python3 -c "import sys,json; print(json.loads(sys.stdin.read())['status'])")
[[ "$TICKET2_STATUS" == "pending_human" ]] || fail "第二个 ticket status 期望 pending_human，实际 ${TICKET2_STATUS}"
HTTP=$(curl -sS -o /dev/null -w "%{http_code}" -X POST http://127.0.0.1:8080/api/skills/cancel \
  -H "Content-Type: application/json" \
  -d "{\"ticketId\":\"${TICKET2}\",\"userId\":\"demo-user\"}")
[[ "$HTTP" == "204" ]] || fail "cancel http 期望 204，实际 ${HTTP}"
say "✓ cancel http=${HTTP}"

STATUS2=$(python3 - "$DB_PATH" "$TICKET2" <<'PY'
import sys, sqlite3
con = sqlite3.connect(sys.argv[1])
row = con.execute("SELECT status FROM skill_pending_tickets WHERE id=?", (sys.argv[2],)).fetchone()
print(row[0] if row else '')
con.close()
PY
)
[[ "$STATUS2" == "cancelled" ]] || fail "cancel 后 ticket status 期望 cancelled，实际 ${STATUS2}"
say "✓ ticket ${TICKET2} status = ${STATUS2}"

# ---- 8. 后台 sweeper 自动 expired（PRD REQ-007）---------------------
# TICKET_SWEEP_INTERVAL=500ms 在启动时已注入。手工注入一个 expires_at
# 已过期的 pending ticket，验证下次 sweep tick 后变成 expired。
say "注入一个过期 pending ticket，等后台 sweeper 处理..."
EXPIRED_ID="expired-$$"
python3 - "$DB_PATH" "$EXPIRED_ID" <<'PY'
import sys, sqlite3, time
db, tid = sys.argv[1], sys.argv[2]
con = sqlite3.connect(db)
con.execute("""
INSERT INTO skill_pending_tickets(id, conversation_id, user_id, skill_name, payload_json, summary, status, expires_at, created_at)
VALUES (?, NULL, 'demo-user', 'apply_refund', '{}', 'auto-expire test', 'pending', ?, ?)
""", (tid, int((time.time()-60)*1000), int(time.time()*1000)))
con.commit()
con.close()
PY
# 留出足够时间给 sweeper 跑（间隔 500ms，2 个 tick 足够）
sleep 1.5
SWEEP_STATUS=$(python3 - "$DB_PATH" "$EXPIRED_ID" <<'PY'
import sys, sqlite3
con = sqlite3.connect(sys.argv[1])
row = con.execute("SELECT status FROM skill_pending_tickets WHERE id=?", (sys.argv[2],)).fetchone()
print(row[0] if row else '')
con.close()
PY
)
[[ "$SWEEP_STATUS" == "expired" ]] || fail "后台 sweeper 未把过期 ticket 翻成 expired，实际 ${SWEEP_STATUS}"
say "✓ ticket ${EXPIRED_ID} 被后台 sweeper 自动 expired"

# ---- 8. 收尾 -------------------------------------------------------
say "===================="
say "✓ demo 全过"
say "覆盖的端到端路径："
say "  1. fs 热加载（data/skills/*.json → registry 启动时加载）"
say "  2. SQLite CRUD + 热加载（reload endpoint 让 in-memory 拿到）"
say "  3. apply_refund → pending_human ticket → confirm → mock_orders mutate"
say "  4. cancel ticket 路径"
say "  5. 后台 sweeper 自动 expired（PRD REQ-007）"
say "===================="