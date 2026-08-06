[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go -and $env:GOROOT) {
    $goPath = Join-Path $env:GOROOT 'bin\go.exe'
    if (Test-Path -LiteralPath $goPath) {
        $go = $goPath
    }
}
if (-not $go -and $env:USERPROFILE) {
    $goPath = Join-Path $env:USERPROFILE 'go\go1.26.5\bin\go.exe'
    if (Test-Path -LiteralPath $goPath) {
        $go = $goPath
    }
}
if (-not $go) {
    throw 'Go 1.26 was not found.'
}

Push-Location (Join-Path $repoRoot 'backend')
try {
    & $go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0 `
        -generate types `
        -package api `
        -o internal/api/openapi.gen.go `
        ..\docs\contracts\openapi.yaml
    if ($LASTEXITCODE -ne 0) { throw 'oapi-codegen failed.' }
} finally {
    Pop-Location
}

Push-Location (Join-Path $repoRoot 'frontend')
try {
    & pnpm dlx openapi-typescript@7.9.1 ..\docs\contracts\openapi.yaml -o src/types/generated/openapi.d.ts
    if ($LASTEXITCODE -ne 0) { throw 'openapi-typescript failed.' }
    & pnpm exec prettier --write src/types/generated/openapi.d.ts
    if ($LASTEXITCODE -ne 0) { throw 'prettier failed.' }
} finally {
    Pop-Location
}
