#Requires -RunAsAdministrator
<#
  Installs phleetd as a Windows scheduled task that starts at boot and runs as
  SYSTEM. Loopback-only by default; public traffic arrives through cloudflared.

  Usage:
    .\deploy\windows\install-service.ps1
    .\deploy\windows\install-service.ps1 -Exe C:\phleet\dist\phleetd.exe -DataDir D:\phleet-data
    .\deploy\windows\install-service.ps1 -Uninstall
#>
param(
  [string]$Exe = "",
  [string]$DataDir = "$env:ProgramData\phleet",
  [string]$Addr = "127.0.0.1:8080",
  [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'
$TaskName = 'phleetd'

if ($Uninstall) {
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
  Write-Host "removed scheduled task $TaskName (data in $DataDir was left untouched)" -ForegroundColor Yellow
  return
}

if (-not $Exe) {
  $candidate = Join-Path (Split-Path -Parent (Split-Path -Parent $PSScriptRoot)) 'dist\phleetd.exe'
  if (Test-Path $candidate) { $Exe = (Resolve-Path $candidate).Path }
}
if (-not $Exe -or -not (Test-Path $Exe)) { throw "phleetd.exe not found; build it first or pass -Exe" }

New-Item -ItemType Directory -Force -Path $DataDir | Out-Null

$action = New-ScheduledTaskAction -Execute $Exe -Argument "-addr $Addr -data `"$DataDir`""
$trigger = New-ScheduledTaskTrigger -AtStartup
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet `
  -AllowStartIfOnBatteries `
  -DontStopIfGoingOnBatteries `
  -StartWhenAvailable `
  -RestartCount 999 `
  -RestartInterval (New-TimeSpan -Minutes 1) `
  -ExecutionTimeLimit ([TimeSpan]::Zero) `
  -MultipleInstances IgnoreNew

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal `
  -Settings $settings -Description 'phleet control plane' -Force | Out-Null

Start-ScheduledTask -TaskName $TaskName

Write-Host ""
Write-Host "phleetd installed and started as scheduled task '$TaskName'" -ForegroundColor Green
Write-Host "  panel   http://$Addr"
Write-Host "  data    $DataDir"
Write-Host "  admin   $DataDir\admin_token"
Write-Host "  agent   $DataDir\agent_token"
Write-Host ""
Write-Host "logs: Task Scheduler > $TaskName, or run phleetd in a console for live output"
