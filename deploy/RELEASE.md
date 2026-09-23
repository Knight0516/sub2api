# Sub2API Fork 发版流程

> 适用：`Knight0516/sub2api`（fork 自 `Wei-Shaw/sub2api`）
> 服务器部署模式：`curl install.sh | bash` 一键安装/升级
> 构建平台：GitHub Actions（CI 自动构建 tarball 并发布到 fork 的 GitHub Release）

## 架构总览

```
本地开发 ──git push──▶ GitHub fork (Knight0516/sub2api)
                              │
                              │ CI (.github/workflows/release.yml)
                              │ 构建 5 个平台的 tarball + checksums.txt
                              ▼
                      GitHub Releases
                              │
                              │ install.sh 从 releases 拉取
                              ▼
                  服务器 (/opt/sub2api/sub2api)
```

服务器每次升级**只换 `/opt/sub2api/sub2api` 这一个二进制**，配置 (`/etc/sub2api/config.yaml`)、数据库、`/opt/sub2api/data/` 全部保留。

---

## 日常发版流程（5 步）

### 1. 本地开发并推送

```powershell
cd "D:\product file\sub2api"

# 改代码...
git status
git add -A
git commit -m "feat: 描述你的改动"
git push origin main
```

### 2. 打 annotated tag

**版本号规则：纯数字（不要带 `-` 后缀，否则会变 Pre-release）。**

```powershell
# 上一版是 v0.2.7，这次发 v0.2.8
git tag v0.2.8 -m "v0.2.8 改动说明"
git push origin v0.2.8
```

**不能用 `v0.2.8-fork.1`、`v0.2.8-rc.1`** 这类带 `-` 的版本号 → 会被 GoReleaser 标为 Pre-release → `/releases/latest` API 找不到 → install.sh 装不上。

### 3. 等 CI 跑（约 10-15 分钟）

打开 https://github.com/Knight0516/sub2api/actions 看 `Release` workflow：

- ✅ `prepare` — 解析 tag
- ✅ `build-frontend` — 编译前端
- ✅ `build-binaries` — 编译 5 个平台的二进制（`linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`、`windows/amd64`）
- ✅ `release` — 上传到 GitHub Release
- ✅ `sync-version-file` — 同步 VERSION 到 main 分支

跑完后 https://github.com/Knight0516/sub2api/releases/tag/v0.2.8 应该有：

- **不是 Draft、不是 Pre-release**（正常 published 绿色）
- Assets 6 个文件：5 个 tarball/zip + `checksums.txt`

### 4. 服务器升级

```bash
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- upgrade
```

install.sh 会：
1. 拉取 fork 的 latest release（`/releases?per_page=1`，兼容 pre-release）
2. 下载 `sub2api_0.2.8_linux_amd64.tar.gz`
3. 校验 sha256
4. 备份当前二进制为 `/opt/sub2api/sub2api.backup.<时间戳>`
5. `systemctl stop` → 替换二进制 → `chown sub2api:sub2api` → `systemctl start`
6. 校验服务 active + 打印新版本

### 5. 验证

服务器上：

```bash
sudo systemctl status sub2api   # 应该 active (running)
sudo /opt/sub2api/sub2api --version   # 应该显示 0.2.8
```

---

## 紧急发版（不走 tag / CI）

如果 CI 出问题或不想打 tag，可以用本地一键脚本直接编译并上传到服务器。

文件：`deploy/publish-local.ps1`

```powershell
# 1) 编辑 deploy/publish-local.ps1 顶部配置
$RepoRoot  = 'd:/product file/sub2api'
$GoArch    = 'amd64'                  # 或 arm64
$SshHost   = 'root@your.server.ip'    # ← 改成你的
$SshPort   = 22
$RemoteBin = '/opt/sub2api/sub2api'

# 2) 干跑确认本地能打包
pwsh deploy/publish-local.ps1 -DryRun

# 3) 真上服务器
pwsh deploy/publish-local.ps1
# 前端没改可加 -SkipFrontend 加速
pwsh deploy/publish-local.ps1 -SkipFrontend
```

脚本流程：
1. `pnpm build` 前端
2. `go build -tags=embed -trimpath` 交叉编译 Linux 二进制
3. `scp` 上传到服务器的 `/tmp/sub2api.new`
4. `ssh` 调用 `deploy/upgrade-remote.sh`：备份 → 替换 → chown → restart → 校验

> ⚠️ **先在本地 PowerShell 7+ 装好 SSH key**：`ssh-copy-id root@your.server.ip`，否则脚本的 `BatchMode=yes` 会失败。

---

## 回滚

### 方式 A：服务器上有 backup 文件

`upgrade` 每次都会备份当前二进制到 `/opt/sub2api/sub2api.backup.<时间戳>`：

```bash
# 看有哪些备份
ls -lt /opt/sub2api/sub2api.backup.* | head -5

# 回滚到指定备份
sudo cp /opt/sub2api/sub2api.backup.20260101120000 /opt/sub2api/sub2api
sudo systemctl restart sub2api
sudo /opt/sub2api/sub2api --version   # 确认回到旧版
```

### 方式 B：用 install.sh rollback（如果有历史 release）

```bash
# 列出 fork 上所有已发布的版本
curl -s https://api.github.com/repos/Knight0516/sub2api/releases | grep tag_name

# 回滚到指定版本
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- rollback v0.2.7
```

### 方式 C：本地紧急发版覆盖

如果新版本服务根本起不来：

```powershell
# 在本地快速切回旧版 commit，重新走 publish-local
git checkout v0.2.7  # 切到旧 tag
pwsh deploy/publish-local.ps1 -SkipFrontend
```

---

## 关键文件 & 配置

| 文件 | 作用 |
|------|------|
| `deploy/install.sh` | 官方安装脚本，已改 `GITHUB_REPO="Knight0516/sub2api"`、`get_latest_version` 用 `/releases?per_page=1` |
| `.github/workflows/release.yml` | CI workflow，已删 Docker/Telegram 相关步骤，`SIMPLE_RELEASE=false` |
| `deploy/publish-local.ps1` | 本地一键发版脚本（不走 CI，紧急通道） |
| `deploy/upgrade-remote.sh` | 上传到服务器执行的升级脚本（被 publish-local.ps1 调用） |
| `backend/cmd/server/VERSION` | 版本号文件，CI 会自动同步 |

---

## 故障排查

### 「获取最新版本失败」

服务器上跑：
```bash
curl -s https://api.github.com/repos/Knight0516/sub2api/releases/latest
```
- 返回 `"message": "Not Found"` → **没 release 或 release 是 Draft/Pre-release**
- 返回含 `tag_name` 的 JSON → API 没问题，看下面其他错误

### 「Make sure you have a valid tag」（GoReleaser 错误）

可能原因：
1. **Tag 没 push 到远端**：`git ls-remote --tags origin | grep <tag>`
2. **Tag 是 lightweight**：用 `git tag <tag> -m "msg"` 打 annotated
3. **CI 还没跑完** 或 **release 还没创建**

### Release 还是 Draft / Pre-release

- **Draft**：CI 跑成功了，但 `releases/latest` API 不返回 Draft。Edit release → 取消「Save as draft」
- **Pre-release**：tag 带了 `-` 后缀（如 `v0.2.8-rc.1`），GoReleaser 自动标 Pre-release。删 release + 重新打纯数字 tag

### 「template: failed to apply... DOCKERHUB_USERNAME」（GoReleaser 模板错）

CI 改过 release.yml 的人可能误删了 publish 步骤的 `DOCKERHUB_USERNAME` env。检查：
```yaml
- name: Publish existing archives and release notes
  env:
    DOCKERHUB_USERNAME: skip   # ← 必须在，值用 skip 让模板跳过 Docker Hub 段
```

### GitHub Actions 没跑

去 https://github.com/Knight0516/sub2api/settings/actions → 确认是 **「Allow all actions and reusable workflows」**（不是 Disable）。

### 服务起不来

```bash
sudo journalctl -u sub2api -n 50 --no-pager
```

最常见：
- **DB 迁移失败**：检查 `/opt/sub2api/data/` 权限 + PostgreSQL 是否在跑
- **端口被占**：`sudo ss -tlnp | grep 8080`
- **二进制架构不对**：`file /opt/sub2api/sub2api`（应该是 `x86-64` 或 `aarch64`）

---

## 注意事项

1. **VERSION 文件由 CI 自动维护**：`sync-version-file` job 在 release 成功后自动 commit 并 push 到 main，不要手动改。

2. **不要让 fork 自动 sync upstream**：去 Settings → Sync fork 关掉，否则 `install.sh` 的 `GITHUB_REPO` 改回 `Wei-Shaw/sub2api`。

3. **`/opt/sub2api/data/` 是运行时数据**：包含 DB 迁移记录、缓存。**不要 rm -rf**，否则重启会触发全量迁移。

4. **`/etc/sub2api/config.yaml` 是数据库连接配置**：setup wizard 首次启动写入，**不要 rm**，否则下次启动又让你走一遍初始化。

6. **Permissions**：服务以 `sub2api:sub2api` 用户运行（systemd unit `User=sub2api`），所有 `/opt/sub2api/` 写入操作需要 sudo + chown。

6. **服务器上的 backup 文件**：建议定期清理老的：
   ```bash
   sudo rm /opt/sub2api/sub2api.backup.*  # 或保留最近 5 个
   ls -lt /opt/sub2api/sub2api.backup.* | tail -n +6 | awk '{print $NF}' | xargs sudo rm
   ```

---

## 速查表

```powershell
# === 本地 ===
git status                                          # 看改动
git add -A && git commit -m "msg" && git push       # 推 main
git tag v0.2.8 -m "msg" && git push origin v0.2.8   # 发版
git tag -d v0.2.7-fork.1 && git push origin :refs/tags/v0.2.7-fork.1   # 删 tag
git log --oneline -5                                 # 看最近 commits
pwsh deploy/publish-local.ps1 -DryRun                # 本地干跑
pwsh deploy/publish-local.ps1 -SkipFrontend          # 本地紧急发版
```

```bash
# === 服务器 ===
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- upgrade   # 升级 latest
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- upgrade -v v0.2.7   # 升级指定版本
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- rollback v0.2.7   # 回滚
sudo systemctl status sub2api                          # 看状态
sudo journalctl -u sub2api -n 50 --no-pager            # 看日志
sudo /opt/sub2api/sub2api --version                    # 看版本
ls -lt /opt/sub2api/sub2api.backup.* | head -5         # 看历史备份
```