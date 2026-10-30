#Requires -Version 5.1
<#
.SYNOPSIS
构建包含最新前端的 Sub2API Linux 二进制。
.DESCRIPTION
需要 Node.js（建议 24）、pnpm 9 和 backend/go.mod 指定版本的 Go 工具链。
首次构建需要联网下载依赖，不需要启动数据库、Redis 或应用服务。
先构建前端，再用 embed 标签将页面嵌入后端。版本来自 cmd/server/VERSION，
不通过 ldflags 覆盖版本，不使用 Git 标签作为版本号。
默认输出到项目根目录 release，生成二进制和 SHA256 校验文件。
.PARAMETER Arch
目标 Linux 架构：amd64（默认，适用于 Intel/AMD x86_64）或 arm64。
.PARAMETER OutputDir
产物目录。相对路径以项目根目录为基准，默认 release。
.PARAMETER SkipInstall
跳过 pnpm install；仅在前端依赖已安装且与锁文件一致时使用。
仍会完整构建前端，不会直接使用旧页面。
.EXAMPLE
powershell -NoProfile -ExecutionPolicy Bypass -File .\打包二进制.ps1
.EXAMPLE
powershell -NoProfile -ExecutionPolicy Bypass -File .\打包二进制.ps1 -Arch arm64
.EXAMPLE
.\打包二进制.ps1 -SkipInstall -OutputDir 'D:\发布文件'
#>
[CmdletBinding()]
param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',

    [ValidateNotNullOrEmpty()]
    [string]$OutputDir = 'release',

    [switch]$SkipInstall
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Invoke-BuildCommand {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "命令执行失败（退出码 $LASTEXITCODE）：$Command $($Arguments -join ' ')"
    }
}

$projectDir = $PSScriptRoot
$frontendDir = Join-Path $projectDir 'frontend'
$backendDir = Join-Path $projectDir 'backend'
$versionPath = Join-Path $backendDir 'cmd/server/VERSION'
$goModPath = Join-Path $backendDir 'go.mod'
$embeddedIndexPath = Join-Path $backendDir 'internal/web/dist/index.html'

foreach ($requiredFile in @($versionPath, $goModPath, (Join-Path $frontendDir 'package.json'), (Join-Path $frontendDir 'pnpm-lock.yaml'))) {
    if (-not (Test-Path -LiteralPath $requiredFile -PathType Leaf)) {
        throw "缺少项目文件：$requiredFile。请将脚本放在完整项目的根目录。"
    }
}

foreach ($command in @('node', 'pnpm', 'go')) {
    if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
        throw "未找到 $command，请先安装并加入 PATH，再重新打开 PowerShell。pnpm 安装命令：npm install -g pnpm@9"
    }
}

$buildVersion = (Get-Content -LiteralPath $versionPath -Raw -Encoding UTF8).Trim()
if ($buildVersion -notmatch '^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$') {
    throw "VERSION 文件中的版本号无效：$buildVersion"
}

$nodeVersion = & node --version
if ($LASTEXITCODE -ne 0 -or "$nodeVersion" -notmatch '^v(\d+)\.') {
    throw '无法读取 Node.js 版本。'
}
if ([int]$Matches[1] -lt 20) {
    throw "Node.js 版本过旧：$nodeVersion，请安装 Node.js 24。"
}
$pnpmVersion = & pnpm --version
if ($LASTEXITCODE -ne 0 -or "$pnpmVersion" -notmatch '^9\.') {
    throw "需要 pnpm 9，当前为 $pnpmVersion。请执行：npm install -g pnpm@9"
}

$goRequirement = [regex]::Match((Get-Content -LiteralPath $goModPath -Raw), '(?m)^go\s+(\S+)').Groups[1].Value
if (-not $goRequirement) { throw '无法读取 go.mod 中要求的 Go 版本。' }

if (-not [IO.Path]::IsPathRooted($OutputDir)) {
    $OutputDir = Join-Path $projectDir $OutputDir
}
$artifactDir = [IO.Path]::GetFullPath($OutputDir)
$artifactName = 'sub2api'
$artifactPath = Join-Path $artifactDir $artifactName
$checksumPath = "$artifactPath.sha256"
$temporaryArtifact = Join-Path $artifactDir ('.' + $artifactName + '.' + [guid]::NewGuid().ToString('N') + '.tmp')

# 保存进程环境，结束后还原，避免影响当前终端后续的 Go 命令。
$savedEnvironment = @{}
foreach ($name in @('CGO_ENABLED', 'GOOS', 'GOARCH', 'GOCACHE', 'GOFLAGS')) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}

Push-Location -LiteralPath $projectDir
try {
    Write-Host "版本：$buildVersion（来自 backend/cmd/server/VERSION）" -ForegroundColor Cyan
    Write-Host "目标：linux/$Arch；Go 工具链要求：$goRequirement；Node.js：$nodeVersion；pnpm：$pnpmVersion"

    Set-Location -LiteralPath $frontendDir
    if ($SkipInstall) {
        if (-not (Test-Path -LiteralPath 'node_modules' -PathType Container)) {
            throw '未找到 frontend/node_modules，请去掉 -SkipInstall 后重试。'
        }
        Write-Host '[1/3] 跳过前端依赖安装。'
    } else {
        Write-Host '[1/3] 安装前端依赖……' -ForegroundColor Cyan
        Invoke-BuildCommand -Command 'pnpm' -Arguments @('install', '--frozen-lockfile')
    }

    Write-Host '[2/3] 构建前端（包含翻译键检查和类型检查）……' -ForegroundColor Cyan
    Invoke-BuildCommand -Command 'pnpm' -Arguments @('run', 'build')
    if (-not (Test-Path -LiteralPath $embeddedIndexPath -PathType Leaf)) {
        throw "前端构建未生成预期文件：$embeddedIndexPath"
    }

    Set-Location -LiteralPath $backendDir
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'linux'
    $env:GOARCH = $Arch
    $env:GOCACHE = Join-Path $backendDir '.gocache'
    # 使用明确的构建选项，避免继承 GOFLAGS 中可能存在的版本覆盖参数。
    $env:GOFLAGS = '-buildvcs=false'
    New-Item -ItemType Directory -Path $env:GOCACHE -Force | Out-Null
    New-Item -ItemType Directory -Path $artifactDir -Force | Out-Null

    Write-Host '[3/3] 构建包含前端的 Linux 二进制……' -ForegroundColor Cyan
    Invoke-BuildCommand -Command 'go' -Arguments @('version')
    $buildDate = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    # 不传 main.Version，让 main.go 读取编译期嵌入的 VERSION。
    $linkFlags = "-s -w -X main.BuildType=release -X main.Date=$buildDate"
    Invoke-BuildCommand -Command 'go' -Arguments @(
        'build', '-tags', 'embed', '-trimpath', '-ldflags', $linkFlags,
        '-o', $temporaryArtifact, './cmd/server'
    )
    if (-not (Test-Path -LiteralPath $temporaryArtifact -PathType Leaf)) {
        throw 'Go 构建结束，但没有生成二进制文件。'
    }
    if ((Get-Item -LiteralPath $temporaryArtifact).Length -eq 0) {
        throw '生成的二进制文件为空。'
    }

    # 完整构建成功后才替换输出目录中的已有产物。
    $checksum = (Get-FileHash -LiteralPath $temporaryArtifact -Algorithm SHA256).Hash.ToLowerInvariant()
    Move-Item -LiteralPath $temporaryArtifact -Destination $artifactPath -Force
    [IO.File]::WriteAllText($checksumPath, "$checksum  $artifactName`n", [Text.Encoding]::ASCII)

    Write-Host "打包完成：$artifactPath" -ForegroundColor Green
    Write-Host "校验文件：$checksumPath"
    Write-Host "SHA256：$checksum"
    Write-Host '上传两个文件到 Linux 同一目录后，可执行：'
    Write-Host "  sha256sum -c '$artifactName.sha256'"
    Write-Host "  chmod +x '$artifactName'"
    Write-Host "  ./$artifactName --version"
} finally {
    foreach ($name in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
    }
    # 仅清理本次生成的临时文件；不递归删除任何目录。
    if (Test-Path -LiteralPath $temporaryArtifact -PathType Leaf) {
        Remove-Item -LiteralPath $temporaryArtifact -Force -ErrorAction SilentlyContinue
    }
    Pop-Location
}
