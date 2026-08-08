#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo '==> 验证 Go 后端'
unformatted="$(cd "$repo_root/backend" && gofmt -l .)"
if [[ -n "$unformatted" ]]; then
  printf '以下 Go 文件尚未格式化：\n%s\n' "$unformatted" >&2
  exit 1
fi
(
  cd "$repo_root/backend"
  go mod tidy -diff
  go vet ./...
  go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
  go test -count=1 ./...
  go build ./...
  go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
)

echo '==> 验证 Vue 前端'
(
  cd "$repo_root/frontend"
  pnpm install --frozen-lockfile
  pnpm lint
  pnpm lint:stylelint
  pnpm test
  pnpm build
)

echo '==> 验证 Python Worker'
(
  cd "$repo_root/worker"
  uv sync --locked --all-groups
  uv run --frozen ruff check .
  uv run --frozen mypy coinsphere_worker tests
  uv run --frozen pytest
)

if command -v docker >/dev/null 2>&1; then
  echo '==> 验证 Docker Compose'
  (
    cd "$repo_root"
    COINSPHERE_AUTH__SECRET_KEY=local-compose-validation-only docker compose config --quiet
  )
else
  echo '警告：未找到 Docker，容器验证交由 GitHub Actions 执行。' >&2
fi

echo '全部本地验证通过。'
