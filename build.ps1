#!/usr/bin/env pwsh
# 构建 Windows 可执行文件（纯 Go，无 CGO）
$ErrorActionPreference = 'Stop'

$Version = if ($env:VERSION) { $env:VERSION } else { '1.0.14' }
$env:CGO_ENABLED = '0'

Write-Host "==> 正在构建 server-inspect.exe (version $Version) ..."
go build -trimpath -ldflags "-s -w -X main.version=$Version" -o server-inspect.exe .
if ($LASTEXITCODE -ne 0) { throw "构建失败" }

$file = Get-Item server-inspect.exe
Write-Host ("==> 构建完成: {0} ({1:N1} MB)" -f $file.FullName, ($file.Length / 1MB))
Write-Host "==> 运行: .\server-inspect.exe -addr :8080 -data .\data"
