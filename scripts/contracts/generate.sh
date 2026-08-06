#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
go_cmd="${GO_BIN:-go}"

cd "$repo_root/backend"
"$go_cmd" run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0 \
  -generate types \
  -package api \
  -o internal/api/openapi.gen.go \
  ../docs/contracts/openapi.yaml

cd "$repo_root/frontend"
pnpm dlx openapi-typescript@7.9.1 \
  ../docs/contracts/openapi.yaml \
  -o src/types/generated/openapi.d.ts
pnpm exec prettier --write src/types/generated/openapi.d.ts
