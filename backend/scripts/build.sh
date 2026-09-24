#!/usr/bin/env bash
# Build the backend (Go) binaries only. Frontend is built separately from
# ../frontend and deployed independently (Cloudflare Pages, etc.).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

mkdir -p bin
go build -trimpath -o bin/intelligent_customer ./cmd/server
go build -trimpath -o bin/adminctl ./cmd/adminctl

echo ""
echo "✓ built: $ROOT/bin/intelligent_customer   (API server)"
echo "✓ built: $ROOT/bin/adminctl               (admin DB CLI)"
echo "  start server: cd backend && ./bin/intelligent_customer  (uses .env in cwd)"
echo "  manage admins: ./bin/adminctl -db \$DB_PATH <list|add|passwd|rename|del>"