#!/usr/bin/env bash
# Agent + Skill 自动化评测脚本。
#
# 用法:
#   1. 先启动后端:  cd backend && bash scripts/run.sh
#   2. 在另一个终端跑评测:  bash backend/scripts/eval.sh
#
# 环境变量:
#   API_BASE   默认 http://localhost:8080
#   SIGN_SECRET 默认 dev-secret-please-change-in-production-must-be-32b
#   SKIP_SIGN=1 跳过 HMAC 签名（开发模式，SIGNATURE_REQUIRED=false 时有效）
#   EVAL_FILE   默认 backend/scripts/eval.jsonl
#
# 输出: 每条用例 PASS/FAIL 摘要，最后一行汇总。

set -o pipefail

API_BASE="${API_BASE:-http://localhost:8080}"
SIGN_SECRET="${SIGN_SECRET:-dev-secret-please-change-in-production-must-be-32b}"
EVAL_FILE="${EVAL_FILE:-$(cd "$(dirname "$0")" && pwd)/eval.jsonl}"

cd "$(dirname "$0")/.." || exit 1

if [[ ! -f "$EVAL_FILE" ]]; then
  echo "eval file not found: $EVAL_FILE" >&2
  exit 1
fi

# sign_to_tmp <tmpdir-prefix> <method> <path> <body>
# Writes three lines into <tmpdir-prefix>-headers.txt:
#   X-Timestamp: ...
#   X-Nonce: ...
#   X-Signature: ...
sign_to_tmp() {
  local prefix="$1" method="$2" url_path="$3" body="$4"
  local ts nonce body_hash canonical sig
  ts=$(date +%s%3N)
  nonce=$(openssl rand -hex 16)
  body_hash=$(printf '%s' "$body" | openssl dgst -sha256 -hex | awk '{print $2}')
  canonical=$(printf '%s\n%s\n%s\n%s\n%s' "$method" "$url_path" "$ts" "$nonce" "$body_hash")
  sig=$(printf '%s' "$canonical" | openssl dgst -sha256 -hmac "$SIGN_SECRET" -hex | awk '{print $2}')
  cat > "${prefix}.headers" <<EOF
X-Timestamp: ${ts}
X-Nonce: ${nonce}
X-Signature: ${sig}
EOF
}

run_case() {
  local line="$1"
  local id input
  id=$(printf '%s' "$line" | python3 -c 'import sys,json; print(json.loads(sys.stdin.read())["id"])')
  input=$(printf '%s' "$line" | python3 -c 'import sys,json; print(json.loads(sys.stdin.read())["input"])')

  local body
  body=$(printf '{"userId":"eval-user","content":%s}' "$(python3 -c "import json,sys; print(json.dumps(sys.argv[1]))" "$input")")

  local raw tmp
  tmp=$(mktemp -d)
  local headers_args=()
  if [[ "${SKIP_SIGN:-0}" == "1" ]]; then
    headers_args=(-H "Content-Type: application/json")
  else
    sign_to_tmp "$tmp/req" POST /api/chat "$body"
    headers_args=(
      -H "Content-Type: application/json"
      -H "$(sed -n 1p "${tmp}/req.headers")"
      -H "$(sed -n 2p "${tmp}/req.headers")"
      -H "$(sed -n 3p "${tmp}/req.headers")"
    )
  fi

  raw=$(curl -sS -X POST "${API_BASE}/api/chat" "${headers_args[@]}" --data "$body")
  rm -rf "$tmp"

  if [[ -z "$raw" ]]; then
    printf "  [FAIL] %s: empty response\n" "$id"
    return 1
  fi

  echo "$raw" | python3 -c '
import json, sys
case_id = sys.argv[1]
events = []
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        events.append(json.loads(line))
    except Exception:
        pass

ok = True
notes = []

tool_calls = [e for e in events if e.get("type") == "tool_call"]
handover = next((e for e in events if e.get("type") == "handover"), None)
final = next((e for e in events if e.get("type") == "final"), None)
pending = next((e for e in events if e.get("type") == "pending_human"), None)

# Smoke: at least one terminal event.
if not (handover or final or pending):
    ok = False
    notes.append("no terminal event")

steps = len([e for e in events if e.get("type") == "step"])
print(f"  [{'"'"'PASS'"'"' if ok else '"'"'FAIL'"'"'}] {case_id}: steps={steps} tools={[t.get('"'"'tool'"'"') for t in tool_calls]} handed={bool(handover)} pending={bool(pending)}")
for n in notes:
    print(f"      note: {n}")
sys.exit(0 if ok else 1)
' "$id"
}

total=0
pass=0
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  total=$((total + 1))
  if run_case "$line"; then
    pass=$((pass + 1))
  fi
done < "$EVAL_FILE"

echo "-----"
echo "Eval: $pass/$total passed"