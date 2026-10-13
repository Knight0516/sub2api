# Sub2API 发版 / 更新指南

- **我的仓库**：`Knight0516/sub2api`（Git 里叫 `origin`）
- **原仓库**：`Wei-Shaw/sub2api`（Git 里叫 `upstream`）
- **服务器**：程序在 `/opt/sub2api`，配置在 `/etc/sub2api`

**一句话流程**：

```
原仓库更新 ──合并──▶ 我的 main ──打标签──▶ CI 自动构建 ──▶ GitHub Release
                                                                │
                                                       install.sh 拉取
                                                                ▼
                                                         服务器自动升级
```

升级只替换 `/opt/sub2api/sub2api` 一个文件，配置、数据、数据库全部保留。

---

## 一、同步原仓库的更新

在本地 PowerShell 执行：

```powershell
cd "D:\product file\sub2api"

git fetch upstream          # 拉取原仓库最新代码
git checkout main           # 切到 main
git merge upstream/main     # 合并原仓库
git push origin main        # 推送到我的仓库
```

如果提示有冲突（`Unmerged paths`）：手动改完冲突文件 → `git add -A` → `git commit` → 再 `git push origin main`。

---

## 二、发版

### 1. 版本号规则（必须遵守）

| 写法 | 能不能用 | 说明 |
|------|----------|------|
| `v0.2.16` | ✅ 推荐 | 三段式。服务器 `upgrade` 能自动找到 |
| `v0.3.0` | ✅ 推荐 | 同上 |
| `v0.2.15-1` | ⚠️ 可 | 带 `-` 后缀，会被标为 **Pre-release**，服务器升级要带 `-v` 指定版本 |
| `v0.2.15.1` | ❌ 禁止 | 四段式，CI 直接报错 `publishing requires a v-prefixed release version tag` |

**记住：版本号只能是 `v` + 三段数字，后面可选 `-后缀`。不要用四段。**

### 2. 打标签并推送

```powershell
git tag -a v0.2.16 -m "v0.2.16 改动说明"   # 必须带 -a 和 -m（annotated tag）
git push origin v0.2.16                     # 推送后自动触发 CI
```

> 标签要指向当前 HEAD（最新的 main 提交），否则 CI 会报 `checkout does not match the selected release tag`。

### 3. 等 CI 构建（约 10-15 分钟）

打开 https://github.com/Knight0516/sub2api/actions ，看 `Release` workflow 全部变绿。

然后到 https://github.com/Knight0516/sub2api/releases 确认：

- 不是 **Draft**
- 如果你用的是带 `-` 的版本号，会被自动标为 **Pre-release**。想让服务器默认 `upgrade` 也能拉到，就点 **Edit** 取消勾选 → Save

---

## 三、服务器升级

登录服务器执行：

```bash
# 普通版本（推荐，自动升级到最新）
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- upgrade

# 指定版本（版本号带 - 的预发布版本必须这样写）
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- upgrade -v v0.2.15-1
```

升级后检查：

```bash
sudo systemctl status sub2api          # 应为 active (running)
sudo /opt/sub2api/sub2api --version    # 显示新版本号
```

---

## 四、回滚

```bash
# 回滚到指定版本
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- rollback v0.2.15

# 查看有哪些版本可用
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- list-versions
```

---

## 五、报错速查

| 报错 | 原因 / 解决 |
|------|-------------|
| `publishing requires a v-prefixed release version tag` | 版本号格式不对。只能是 `vX.Y.Z` 或 `vX.Y.Z-后缀`，**不能四段** |
| `checkout does not match the selected release tag` | 标签没指向最新提交。删掉重打在最新 commit 上 |
| CI 根本没触发 | 标签没推成功，检查 `git ls-remote --tags origin` |
| 服务器提示「获取最新版本失败」 | Release 是 Draft 或 Pre-release。去 Releases 页面取消勾选，或升级时加 `-v 版本号` |
| 服务起不来 | `sudo journalctl -u sub2api -n 50 --no-pager` 看日志 |

**重打标签（已存在/打错了）：**

```powershell
git tag -d v0.2.16                        # 删本地
git push origin :refs/tags/v0.2.16        # 删远程
git tag -a v0.2.16 -m "v0.2.16 改动说明"  # 重打
git push origin v0.2.16                   # 重推
```

---

## 速查表

```powershell
# ===== 本地：同步原仓库 =====
git fetch upstream && git checkout main && git merge upstream/main && git push origin main

# ===== 本地：发版 =====
git tag -a v0.2.16 -m "v0.2.16 改动说明"
git push origin v0.2.16

# ===== 本地：删标签重打 =====
git tag -d v0.2.16
git push origin :refs/tags/v0.2.16
```

```bash
# ===== 服务器：升级 =====
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- upgrade
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- upgrade -v v0.2.15-1
curl -sSL https://raw.githubusercontent.com/Knight0516/sub2api/main/deploy/install.sh | sudo bash -s -- rollback v0.2.15
sudo systemctl status sub2api
sudo /opt/sub2api/sub2api --version
```
