# Сборка perfd: проверки, затем кросс-компиляция под linux/amd64 (на сервере Go не ставим).
# Запуск: pwsh -File tools\portal\build.ps1 [-SkipTest] [-Windows]
param(
    [switch]$SkipTest,
    [switch]$Windows
)

$ErrorActionPreference = 'Stop'

# Агентский шелл приходит с пустыми каталогами профиля — задаём явно.
$env:LOCALAPPDATA = "$env:USERPROFILE\AppData\Local"
$env:APPDATA = "$env:USERPROFILE\AppData\Roaming"
$env:TEMP = "$env:LOCALAPPDATA\Temp"
$env:GOPATH = 'D:\dev\gopath'
$env:GOCACHE = 'D:\dev\cache\go-build'
$env:GOTOOLCHAIN = 'local'
$env:PATH = "D:\dev\go\bin;$env:PATH"

Push-Location $PSScriptRoot
try {
    $version = 'dev'
    try {
        $short = (git rev-parse --short HEAD 2>$null)
        if ($short) { $version = $short.Trim() }
    } catch { }

    if (-not $SkipTest) {
        Write-Host "== go vet ==" -ForegroundColor Cyan
        go vet ./...
        if ($LASTEXITCODE -ne 0) { throw 'go vet нашёл проблемы' }

        Write-Host "== go test ==" -ForegroundColor Cyan
        go test ./...
        if ($LASTEXITCODE -ne 0) { throw 'тесты не прошли' }
    }

    New-Item -ItemType Directory -Force -Path dist | Out-Null

    Write-Host "== сборка linux/amd64 (версия $version) ==" -ForegroundColor Cyan
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'linux'
    $env:GOARCH = 'amd64'
    go build -trimpath -ldflags "-s -w -X perfd/internal/web.Version=$version" -o dist/perfd-linux-amd64 ./cmd/perfd
    if ($LASTEXITCODE -ne 0) { throw 'сборка linux не удалась' }

    if ($Windows) {
        Write-Host "== сборка windows/amd64 ==" -ForegroundColor Cyan
        $env:GOOS = 'windows'
        go build -trimpath -ldflags "-s -w -X perfd/internal/web.Version=$version" -o dist/perfd.exe ./cmd/perfd
        if ($LASTEXITCODE -ne 0) { throw 'сборка windows не удалась' }
    }
    Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue

    Get-ChildItem dist -File | ForEach-Object {
        "{0,-24} {1,8:N1} МБ" -f $_.Name, ($_.Length / 1MB)
    }
}
finally {
    Pop-Location
}
