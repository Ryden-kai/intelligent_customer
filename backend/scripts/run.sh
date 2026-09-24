#!/usr/bin/env bash
# Run the backend with backend/.env in cwd. If missing it is copied from
# .env.example. Frontend must be started separately.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [ ! -f ".env" ]; then
  echo ".env not found, copying from .env.example. Review and edit before production."
  cp .env.example .env
fi

exec ./bin/intelligent_customer