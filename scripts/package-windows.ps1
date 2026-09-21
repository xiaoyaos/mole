param(
    [string]$Version = "dev",
    [string]$ISCC = ""
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
if (-not $ISCC) {
    $ISCC = Join-Path ([Environment]::GetEnvironmentVariable("ProgramFiles(x86)")) "Inno Setup 6\ISCC.exe"
}
if (-not (Test-Path "$root\bin\client\mole-client-windows-amd64.exe")) {
    throw "Build bin\client\mole-client-windows-amd64.exe first."
}
if (-not (Test-Path $ISCC)) { throw "Inno Setup 6 was not found at $ISCC." }
New-Item -ItemType Directory -Path "$root\dist" -Force | Out-Null
& $ISCC "/DMyAppVersion=$($Version.TrimStart('v'))" "/DSourceRoot=$root" "$root\packaging\windows\Mole.iss"
if ($LASTEXITCODE -ne 0) { throw "Inno Setup failed with exit code $LASTEXITCODE." }
Write-Output "$root\dist\mole-client-windows-amd64-setup.exe"
