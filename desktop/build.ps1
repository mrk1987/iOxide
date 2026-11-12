param([ValidateSet('amd64', 'arm64')][string]$Architecture = 'amd64')
$ErrorActionPreference = 'Stop'
$previousDesktopArch = $env:GOARCH
Push-Location $PSScriptRoot
try {
    $desktopWailsVersion = go list -m -f '{{.Version}}' github.com/wailsapp/wails/v3
    if ($LASTEXITCODE -ne 0) { throw 'Could not determine Wails version' }
    $desktopWailsTool = "github.com/wailsapp/wails/v3/cmd/wails3@$desktopWailsVersion"
    go run $desktopWailsTool generate icons -input frontend/assets/ioxide-icon.png -windowsfilename build/icon.ico -macfilename build/icons.icns
    if ($LASTEXITCODE -ne 0) { throw 'Icon generation failed' }
    go run $desktopWailsTool generate syso -manifest build/app.manifest -icon build/icon.ico -arch $Architecture -out "rsrc_windows_$Architecture.syso"
    if ($LASTEXITCODE -ne 0) { throw 'Windows resource generation failed' }
    $env:GOARCH = $Architecture
    $desktopOutput = "../dist/ioxide-desktop-windows-$Architecture/ioxide-desktop.exe"
    go build -trimpath -tags production -ldflags '-s -w -H windowsgui' -o $desktopOutput .
    if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed' }
    Write-Output "Built $desktopOutput"
} finally {
    $env:GOARCH = $previousDesktopArch
    Pop-Location
}
