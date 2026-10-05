# Install AgentHub from a GitHub release on Windows: no Go needed.
#
#   irm https://raw.githubusercontent.com/its-ammu/agenthub/main/get.ps1 | iex
#
# To pass options, download the script and run it:
#   .\get.ps1 -Version v0.1.0 -Prefix C:\tools\agenthub -NoSkills
param(
  [string]$Version = "latest",
  [string]$Prefix = (Join-Path $env:LOCALAPPDATA "agenthub\bin"),
  [switch]$NoSkills
)
$ErrorActionPreference = "Stop"
$repo = if ($env:AGENTHUB_REPO) { $env:AGENTHUB_REPO } else { "its-ammu/agenthub" }

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$archive = "agenthub_windows_$arch.zip"
$base = if ($env:AGENTHUB_BASE_URL) { $env:AGENTHUB_BASE_URL } elseif ($Version -eq "latest") { "https://github.com/$repo/releases/latest/download" } else { "https://github.com/$repo/releases/download/$Version" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "downloading $archive ($Version)"
  Invoke-WebRequest "$base/$archive" -OutFile (Join-Path $tmp $archive)
  Invoke-WebRequest "$base/checksums.txt" -OutFile (Join-Path $tmp "checksums.txt")

  $line = Get-Content (Join-Path $tmp "checksums.txt") | Where-Object { $_ -match " $([regex]::Escape($archive))$" } | Select-Object -First 1
  $want = if ($line) { ($line -split "\s+")[0] } else { "" }
  $got = (Get-FileHash (Join-Path $tmp $archive) -Algorithm SHA256).Hash.ToLower()
  if (-not $want -or $want -ne $got) { throw "checksum mismatch for $archive, refusing to install" }

  Expand-Archive (Join-Path $tmp $archive) -DestinationPath $tmp -Force
  New-Item -ItemType Directory -Path $Prefix -Force | Out-Null
  Copy-Item (Join-Path $tmp "ah.exe") $Prefix -Force
  Copy-Item (Join-Path $tmp "agenthub-server.exe") $Prefix -Force
  Write-Host "installed ah.exe and agenthub-server.exe to $Prefix"

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if (($userPath -split ";") -notcontains $Prefix) {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$Prefix", "User")
    Write-Host "added $Prefix to your user PATH (open a new terminal to use it)"
  }

  if (-not $NoSkills) { & (Join-Path $Prefix "ah.exe") install --bin (Join-Path $Prefix "ah.exe") }
}
finally { Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue }

Write-Host ""
Write-Host "Done. Start the hub with: agenthub-server   (dashboard at http://localhost:8080)"
Write-Host "Restart your coding agent so it loads the instructions, then ask it to 'use the blackboard'."
