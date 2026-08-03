[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot

function Invoke-Native {
    param(
        [Parameter(Mandatory)] [string] $FilePath,
        [Parameter()] [string[]] $Arguments = @()
    )

    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Command failed: $FilePath $($Arguments -join ' ')"
    }
}

function Resolve-Go {
    $command = Get-Command go -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }

    $candidates = @(
        $(if ($env:GOROOT) { Join-Path $env:GOROOT 'bin\go.exe' }),
        $(if ($env:USERPROFILE) { Join-Path $env:USERPROFILE 'go\go1.26.5\bin\go.exe' })
    ) | Where-Object { $_ }
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate) {
            return $candidate
        }
    }
    throw 'Go was not found. Install Go 1.26 or add it to PATH.'
}

$go = Resolve-Go
$gofmt = Join-Path (Split-Path -Parent $go) 'gofmt.exe'

Write-Host '==> Verify Go backend'
Push-Location (Join-Path $repoRoot 'backend')
try {
    Invoke-Native $go @('mod', 'tidy', '-diff')
    $unformatted = @(& $gofmt -l .)
    if ($LASTEXITCODE -ne 0) {
        throw 'gofmt check failed.'
    }
    if ($unformatted.Count -gt 0) {
        $message = 'The following Go files are not formatted:' + [Environment]::NewLine
        $message += $unformatted -join [Environment]::NewLine
        throw $message
    }
    Invoke-Native $go @('vet', './...')
    Invoke-Native $go @('run', 'honnef.co/go/tools/cmd/staticcheck@v0.7.0', './...')
    Write-Host '==> Verify database contracts'
    Invoke-Native $go @('test', '-count=1', './internal/db', './internal/migration', './internal/service', './cmd/migrate')
    Invoke-Native $go @('test', './...')
    Invoke-Native $go @('build', './...')
    Invoke-Native $go @('run', 'golang.org/x/vuln/cmd/govulncheck@v1.1.4', './...')
} finally {
    Pop-Location
}

Write-Host '==> Verify Vue frontend'
Push-Location (Join-Path $repoRoot 'frontend')
try {
    Invoke-Native 'pnpm' @('install', '--frozen-lockfile')
    Invoke-Native 'pnpm' @('lint')
    Invoke-Native 'pnpm' @('lint:stylelint')
    Invoke-Native 'pnpm' @('test')
    Invoke-Native 'pnpm' @('build')
} finally {
    Pop-Location
}

Write-Host '==> Verify Python worker'
Push-Location (Join-Path $repoRoot 'worker')
try {
    Invoke-Native 'uv' @('sync', '--locked', '--all-groups')
    Invoke-Native 'uv' @('run', '--frozen', 'ruff', 'check', '.')
    Invoke-Native 'uv' @('run', '--frozen', 'mypy', 'coinsphere_worker', 'tests')
    Invoke-Native 'uv' @('run', '--frozen', 'pytest')
} finally {
    Pop-Location
}

$docker = Get-Command docker -ErrorAction SilentlyContinue
if ($docker) {
    Write-Host '==> Verify Docker Compose'
    $previousSecret = $env:COINSPHERE_AUTH__SECRET_KEY
    $env:COINSPHERE_AUTH__SECRET_KEY = 'local-compose-validation-only'
    Push-Location $repoRoot
    try {
        Invoke-Native $docker.Source @('compose', 'config', '--quiet')
    } finally {
        Pop-Location
        if ($null -eq $previousSecret) {
            Remove-Item Env:COINSPHERE_AUTH__SECRET_KEY -ErrorAction SilentlyContinue
        } else {
            $env:COINSPHERE_AUTH__SECRET_KEY = $previousSecret
        }
    }
} else {
    Write-Warning 'Docker was not found. GitHub Actions must run container checks.'
}

Write-Host 'All local checks passed.'
