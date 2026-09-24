#!/usr/bin/env bash
# Wrapper that generates signed headers and curls the API in one shot.
#   bash scripts/curl-signed.sh POST /api/chat '{"content":"hi"}'
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

METHOD=${1:-POST}
PATH_=${2:-/api/chat}
BODY=${3:-'{}'}

JSON=$(MSYS_NO_PATHCONV=1 node scripts/sign.js "$METHOD" "$PATH_" "$BODY")

TS=$(echo "$JSON"    | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>console.log(JSON.parse(s).headers["X-Timestamp"]))')
NONCE=$(echo "$JSON" | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>console.log(JSON.parse(s).headers["X-Nonce"]))')
SIG=$(echo "$JSON"   | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>console.log(JSON.parse(s).headers["X-Signature"]))')

curl -sS -m 30 -X "$METHOD" "http://localhost:8080$PATH_" \
  -H "Content-Type: application/json" \
  -H "X-Timestamp: $TS" \
  -H "X-Nonce: $NONCE" \
  -H "X-Signature: $SIG" \
  -d "$BODY"
echo