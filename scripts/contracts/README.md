# Contract generation

`docs/contracts/openapi.yaml` is the only `/api/v1` contract source. Generated
files are committed and must be reproducible with the pinned tool versions:

- Go: `oapi-codegen v2.5.0` -> `backend/internal/api/openapi.gen.go`
- TypeScript: `openapi-typescript v7.9.1`, then the frontend-pinned Prettier ->
  `frontend/src/types/generated/openapi.d.ts`

Run `./scripts/contracts/generate.sh` on CI/Linux or
`./scripts/contracts/generate.ps1` in PowerShell, then require a clean diff for
the two generated files.
