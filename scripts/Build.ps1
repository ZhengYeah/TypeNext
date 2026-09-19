param([switch]$Run, [switch]$RefreshIcon)
$ErrorActionPreference = "Stop"
Set-Location (Split-Path $PSScriptRoot -Parent)
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Install a supported Go toolchain, then run this script again."
}
if ($RefreshIcon) { & (Join-Path $PSScriptRoot "Build-Icon.ps1") }
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
& go test ./...
if ($LASTEXITCODE -ne 0) { throw "Tests failed." }
& go vet ./...
if ($LASTEXITCODE -ne 0) { throw "Static analysis failed." }
& go build -trimpath '-ldflags=-H=windowsgui -s -w' -o TypeNext.exe ./cmd/typenext
if ($LASTEXITCODE -ne 0) { throw "Build failed." }
Get-FileHash .\TypeNext.exe -Algorithm SHA256
Write-Host "Built TypeNext.exe. Live editor compatibility still needs the Windows test plan."
if ($Run) { Start-Process .\TypeNext.exe }
