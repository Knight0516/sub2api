<#
.SYNOPSIS
    Sub2API 一键发版脚本。

.DESCRIPTION
    流程：
      1. 拉取原仓库(upstream)最新代码
      2. 合并到本地 main
      3. 推送 main 到 origin
      4. 在 main 最新提交上打 tag（版本号由你输入）
      5. 推送 tag，触发 GitHub Actions 自动构建

    脚本跑完后，会打印服务器升级命令，你拿去服务器执行即可。

.PARAMETER Version
    要发布的版本号，例如 v0.2.17 或 v0.2.17-1。不传则交互输入。

.PARAMETER Yes
    所有确认提示自动回答 y（无人值守模式）。

.EXAMPLE
    pwsh deploy/release.ps1

.EXAMPLE
    pwsh deploy/release.ps1 -Version v0.2.17

.EXAMPLE
    pwsh deploy/release.ps1 -Version v0.2.17 -Yes
#>

[CmdletBinding()]
param(
    [string]$Version,
    [string]$Branch   = 'main',
    [string]$Remote   = 'origin',
    [string]$Upstream = 'upstream',
    [switch]$Yes
)

$ErrorActionPreference = 'Stop'

# 让 PowerShell 按 UTF-8 解码 git 等外部命令的输出。
# 中文 Windows 控制台默认 code page 是 936(GBK)，会把 git 输出的 UTF-8 字节解成乱码，
# 表现就是脚本里 "合并完成：$(git log ...)" 这类捕获来的中文变成 浼樺寲... 这种乱码。
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$OutputEncoding = [System.Text.Encoding]::UTF8

# ==================== 输出辅助 ====================
function Info { param([string]$m) Write-Host "[*]  $m" -ForegroundColor Cyan }
function Ok   { param([string]$m) Write-Host "[OK] $m" -ForegroundColor Green }
function Warn { param([string]$m) Write-Host "[!]  $m" -ForegroundColor Yellow }
function Die  { param([string]$m) Write-Host "[x]  $m" -ForegroundColor Red; exit 1 }

function Confirm {
    param([string]$Message)
    if ($Yes) { return $true }
    $answer = Read-Host "$Message [y/N]"
    return ($answer -match '^(y|yes)$')
}

# 每条 git 命令后调用，统一检查退出码
function Assert-GitOk {
    param([string]$What)
    if ($LASTEXITCODE -ne 0) { Die "$What 失败（exit $LASTEXITCODE）" }
}

# ==================== 0. 环境检查 ====================
Write-Host ''
Write-Host '===== Sub2API 一键发版 =====' -ForegroundColor Magenta
Write-Host ''

# 脚本放在仓库的子目录（如 安装\）里，可能从任意位置启动，
# 这里从脚本自身位置反查仓库根目录并切过去，保证 git 命令作用在正确的仓库上。
$repoRoot = (git -C $PSScriptRoot rev-parse --show-toplevel 2>$null)
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($repoRoot)) {
    Die "无法定位 git 仓库根目录（脚本位置：$PSScriptRoot）。请把本脚本放在仓库内部，例如 安装\ 目录下。"
}
$repoRoot = $repoRoot.Trim()
Set-Location $repoRoot
Info "仓库根目录：$repoRoot"
Write-Host ''

$remotes = @(git remote)
if ($remotes -notcontains $Upstream) {
    Die "找不到远程 '$Upstream'。请先执行： git remote add $Upstream https://github.com/Wei-Shaw/sub2api.git"
}
if ($remotes -notcontains $Remote) {
    Die "找不到远程 '$Remote'，请检查仓库配置。"
}

$pendingMerge = (git rev-parse --git-path MERGE_HEAD).Trim()
if (Test-Path $pendingMerge) {
    Die '当前仓库还停留在一次未完成的合并中。请先解决冲突并提交，或执行 git merge --abort 后再运行本脚本。'
}

# ==================== 1. 输入并校验版本号 ====================
while ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = Read-Host '请输入要发布的版本号（例如 v0.2.17）'
}
$Version = $Version.Trim()

# 规则与 CI 的 release_matrix.py 保持一致：v + 三段数字，可选 -后缀
if ($Version -notmatch '^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') {
    Die "版本号格式错误：$Version`n      正确写法：v0.2.17   或   v0.2.17-1`n      错误写法：0.2.17（缺 v）、v0.2.15.1（四段式，CI 会直接报错）"
}

$isPrerelease = $Version.Contains('-')
if ($isPrerelease) {
    Warn "版本号带 '-'，GitHub 会把它标记为【预发布 Pre-release】。"
    Warn '服务器上不带 -v 的 upgrade 拿不到它，升级时必须显式指定版本。'
}
Write-Host ""

# ==================== 2. 切换分支、检查工作区 ====================
$current = (git rev-parse --abbrev-ref HEAD).Trim()
if ($current -ne $Branch) {
    Info "当前分支为 $current，先切换到 $Branch"
    git checkout $Branch
    Assert-GitOk "git checkout $Branch"
}

$changes = @(git status --porcelain)
$trackedChanges = @($changes | Where-Object { $_ -notmatch '^\?\?' })
if ($trackedChanges.Count -gt 0) {
    Warn '工作区有未提交的改动（不会被打进 tag）：'
    $trackedChanges | ForEach-Object { Write-Host "        $_" -ForegroundColor DarkGray }
    if (-not (Confirm '确定继续？')) { Die '已取消。' }
} elseif ($changes.Count -gt 0) {
    Info "工作区有 $($changes.Count) 项未跟踪文件（不影响 tag，已忽略）。"
}

# ==================== 3. 拉取上游并合并 ====================
# 只拉需要的分支，不加 --tags：
# upstream 的 v0.2.8/v0.2.10/v0.2.11/v0.2.14 和 fork 上的同名 tag 指向不同，
# --tags 会报 "would clobber existing tag" 并让整个 fetch 以非 0 退出。
Info "拉取原仓库 $Upstream 的 $Branch 分支 ..."
git fetch $Upstream "+refs/heads/${Branch}:refs/remotes/${Upstream}/${Branch}" --no-tags
Assert-GitOk "git fetch $Upstream"

Info "合并 $Upstream/$Branch 到本地 $Branch ..."
git merge "$Upstream/$Branch" --no-edit
$mergeExit = $LASTEXITCODE
$mergeHead = (git rev-parse --git-path MERGE_HEAD).Trim()
if (Test-Path $mergeHead) {
    Warn '合并存在冲突，请手动解决后重新运行本脚本。冲突文件：'
    git status --short
    Die '已中止，未推送任何内容。'
}
if ($mergeExit -ne 0) { Die "git merge 失败（exit $mergeExit）" }
Ok "合并完成：$(git log -1 --format='%h %s')"

# ==================== 4. 推送 main ====================
Info "推送 $Branch 到 $Remote ..."
git push $Remote $Branch
Assert-GitOk "git push $Remote $Branch"
Ok "$Branch 已推送（$(git rev-parse --short HEAD)）"

# ==================== 5. 处理 tag ====================
$localTagExists = @(git tag --list $Version).Count -gt 0
if ($localTagExists) {
    Info "删除本地同名 tag $Version"
    git tag -d $Version | Out-Null
    Assert-GitOk "git tag -d $Version"
}

$remoteTagExists = @(git ls-remote --tags $Remote "refs/tags/$Version").Count -gt 0
if ($remoteTagExists) {
    Warn "远程已存在 tag $Version"
    Warn '如果它对应的 GitHub Release 也已存在，CI 发布会失败（GoReleaser 不会覆盖已存在的 Release）。'
    Warn '稳妥做法：换一个新版本号；或先去 GitHub Releases 页面手动删除同名 Release。'
    if (-not (Confirm "仍然删除远程 tag $Version 并继续？")) { Die '已取消。' }
    git push $Remote ":refs/tags/$Version"
    Assert-GitOk "删除远程 tag $Version"
    Ok "远程旧 tag $Version 已删除"
}

Info "在最新提交上创建 tag $Version ..."
git tag -a $Version -m "$Version 改动说明"
Assert-GitOk "git tag -a $Version"

Info "推送 tag $Version（会触发 CI 自动构建）..."
git push $Remote $Version
Assert-GitOk "git push $Remote $Version"
Ok 'tag 已推送'

# ==================== 6. 输出结果 ====================
$originUrl = (git config --get "remote.$Remote.url")
$owner = ''; $repo = ''
if ($originUrl -match 'github\.com[:/](?<o>[^/]+)/(?<r>[^/]+?)(\.git)?$') {
    $owner = $Matches['o']
    $repo  = $Matches['r']
}

if ($owner) {
    $installUrl = "https://raw.githubusercontent.com/$owner/$repo/$Branch/deploy/install.sh"
} else {
    $installUrl = '<你的仓库地址>/deploy/install.sh'
}

$shortSha = (git rev-parse --short HEAD).Trim()

Write-Host ''
Write-Host '===== 发版完成 =====' -ForegroundColor Magenta
Write-Host "  版本号 : $Version"
Write-Host "  提交   : $shortSha"
Write-Host "  分支   : $Branch"
if ($owner) {
    Write-Host "  CI     : https://github.com/$owner/$repo/actions"
    Write-Host "  Release: https://github.com/$owner/$repo/releases"
}
Write-Host ''
Info 'CI 构建大约需要 10-15 分钟，等它全部变绿后再去服务器执行：'
Write-Host ''
if ($isPrerelease) {
    Write-Host "  curl -sSL $installUrl | sudo bash -s -- upgrade -v $Version" -ForegroundColor White
    Write-Host ''
    Warn "$Version 是预发布版本，必须带 -v，否则会装成旧的正式版。"
    Warn '若想让普通 upgrade 也能拿到：去 Releases 页面编辑该 Release，取消 Pre-release 勾选。'
} else {
    Write-Host "  curl -sSL $installUrl | sudo bash -s -- upgrade" -ForegroundColor White
}
Write-Host ''
Write-Host '  验证： sudo /opt/sub2api/sub2api --version' -ForegroundColor DarkGray
Write-Host ''
