#Requires -Version 5.1
<#
  Builds phleet from source on Windows.

  Output:
    dist/phleetd.exe                   control plane (embeds the built UI)
    dist/phleet.exe                    operator CLI
    dist/bin/phleet-agent-linux-arm64  agent for Termux on arm64 phones
    dist/bin/phleet-agent-linux-amd64  agent for x86 Linux hosts
    dist/bin/bootstrap.sh              onboarding script served by phleetd

  Usage:
    .\scripts\build.ps1
    .\scripts\build.ps1 -SkipWeb     # reuse the existing UI build
#>
param(
  [switch]$SkipWeb,
  [switch]$SkipAgent
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

# npm and go write progress to stderr; PowerShell's Stop preference treats that
# as a terminating error. Run native steps with Continue and check exit codes.
function Invoke-Native {
  param([string]$Label, [scriptblock]$Script)
  Write-Host "==> $Label" -ForegroundColor Cyan
  $previous = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try {
    & $Script
    $code = $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $previous
  }
  if ($code -ne 0) { throw "$Label failed (exit $code)" }
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw "Go is not installed" }

if (-not $SkipWeb) {
  if (-not (Get-Command npm -ErrorAction SilentlyContinue)) { throw "Node/npm is required to build the UI" }
  Push-Location web
  try {
    if (Test-Path package-lock.json) {
      Invoke-Native "npm ci" { npm ci --no-audit --no-fund }
    } else {
      Invoke-Native "npm install" { npm install --no-audit --no-fund }
    }
    Invoke-Native "typecheck" { npm run typecheck }
    Invoke-Native "build UI" { npm run build }
  } finally {
    Pop-Location
  }
}

if (-not (Test-Path internal\webui\dist\index.html)) {
  throw "UI build is missing (internal/webui/dist). Run without -SkipWeb."
}

New-Item -ItemType Directory -Force -Path dist, dist\bin, dist\gotmp | Out-Null

$oldTmp = $env:GOTMPDIR
$env:GOTMPDIR = Join-Path $root 'dist\gotmp'
try {
  Invoke-Native "go vet" { go vet ./... }
  Invoke-Native "go test" { go test ./... }
} finally {
  $env:GOTMPDIR = $oldTmp
}

Invoke-Native "building control plane" { go build -trimpath -o dist\phleetd.exe ./cmd/phleetd }
Invoke-Native "building CLI" { go build -trimpath -o dist\phleet.exe ./cmd/phleet }

if (-not $SkipAgent) {
  $oldCGO = $env:CGO_ENABLED
  $oldGOOS = $env:GOOS
  $oldGOARCH = $env:GOARCH
  $oldGOARM = $env:GOARM
  $oldToolchain = $env:GOTOOLCHAIN
  $oldFlags = $env:GOFLAGS
  try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'linux'
    $env:GOARCH = 'arm64'
    # Go 1.23+ probes pidfd_open when starting any process, which Android 10's
    # seccomp policy kills with SIGSYS (terminals and deploys would crash the
    # agent). Build the arm64 agent with Go 1.22; the control plane and the
    # amd64 agent keep the current toolchain.
    $env:GOTOOLCHAIN = 'go1.22.12'
    $env:GOFLAGS = "-modfile=" + (Join-Path $root 'go122.mod')
    Invoke-Native "agent linux/arm64 (Go 1.22 for Android 10)" { go build -trimpath -ldflags "-s -w" -o dist\bin\phleet-agent-linux-arm64 ./cmd/phleet-agent }
    $env:GOARCH = 'arm'
    $env:GOARM = '7'
    Invoke-Native "agent linux/armv7 (Go 1.22 for older phones)" { go build -trimpath -ldflags "-s -w" -o dist\bin\phleet-agent-linux-arm ./cmd/phleet-agent }

    $env:GOTOOLCHAIN = $oldToolchain
    $env:GOFLAGS = $oldFlags
    $env:GOARCH = 'amd64'
    Invoke-Native "agent linux/amd64" { go build -trimpath -ldflags "-s -w" -o dist\bin\phleet-agent-linux-amd64 ./cmd/phleet-agent }
    Invoke-Native "control plane linux/amd64 (for e2e/WSL)" { go build -trimpath -o dist\bin\phleetd-linux-amd64 ./cmd/phleetd }
  } finally {
    $env:CGO_ENABLED = $oldCGO
    $env:GOOS = $oldGOOS
    $env:GOARCH = $oldGOARCH
    $env:GOARM = $oldGOARM
    $env:GOTOOLCHAIN = $oldToolchain
    $env:GOFLAGS = $oldFlags
  }
}

Copy-Item scripts\bootstrap-termux.sh dist\bin\bootstrap.sh -Force
Copy-Item scripts\bootstrap-android-root.sh dist\bin\bootstrap-root.sh -Force
Copy-Item scripts\bootstrap-debian-chroot.sh dist\bin\bootstrap-debian.sh -Force

$size = [math]::Round((Get-Item dist\phleetd.exe).Length / 1MB, 1)
Write-Host ""
Write-Host "built dist\phleetd.exe ($size MB) and dist\phleet.exe" -ForegroundColor Green
Write-Host "note: Windows binaries keep their symbols on purpose; some Application"
Write-Host "      Control / Smart App Control policies block stripped Go binaries."
Write-Host "next:"
Write-Host "  .\dist\phleetd.exe -addr 127.0.0.1:8080 -print-tokens"
Write-Host "  .\dist\phleet.exe setup-cloudflare -domain example.com"
