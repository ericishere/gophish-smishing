#!/usr/bin/env pwsh
$ErrorActionPreference = "Stop"

$repoRoot = $PSScriptRoot
$outDir = Join-Path $repoRoot "deployment"
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$archive = Join-Path $outDir "gophish-deploy-$timestamp.tar.gz"

$excludes = @(
    "--exclude=.git",
    "--exclude=node_modules",
    "--exclude=deployment",
    "--exclude=.qoder",
    "--exclude=.claude",
    "--exclude=*.exe"
)

Push-Location $repoRoot
try {
    tar -czf $archive @excludes .
} finally {
    Pop-Location
}

Copy-Item -Path (Join-Path $repoRoot "deploy.sh") -Destination $outDir -Force

Write-Host "Created $archive"
