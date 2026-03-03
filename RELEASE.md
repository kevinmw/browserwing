# 发版流程

## 前置条件

- 本地已安装 Go、make、jq、scp、ssh、pnpm
- Gitee token 存放于 `~/.config/gitee-release-cli-nodejs/config.json`
- 120.79.164.240 机器可 SSH 免密登录，已安装 `gh` CLI 并已认证
- npm 已登录（用于 `npm publish`）

## 1. 确定版本号

查看当前最新 tag：

```bash
git tag --sort=-v:refname | head -5
```

确定新版本号，例如 `v1.0.0` → `v1.0.1`。

## 2. 更新版本号 & Changelog

```bash
# 更新 Makefile 版本号
vim Makefile   # 修改 VERSION = "v1.0.1"

# 更新 npm 版本号
vim npm/package.json   # 修改 "version" 字段

# 更新 CHANGELOG.md，在顶部添加新版本的变更记录
vim CHANGELOG.md
```

查看自上个 tag 以来的所有提交，作为 changelog 素材：

```bash
git log v1.0.0..HEAD --oneline
```

## 3. 提交 & 打 Tag

```bash
git add -A
git commit -m "build: release v1.0.1 — 简要描述"
git tag v1.0.1
```

## 4. 构建 Release 包

```bash
make release
```

产物在 `build/release/` 目录，包含 6 个平台的二进制包（linux/darwin/windows × amd64/arm64）及对应的压缩包。

## 5. 推送到 Gitee

```bash
git push origin main --tags
```

> 旧 tag 的 `[rejected]` 提示可忽略，只要新 tag 推送成功即可。

## 6. 发布 Gitee Release

由于 `gitee-release` CLI 解析仓库 URL 有兼容问题，直接用 Gitee API：

```bash
GITEE_TOKEN="你的token"  # 从 ~/.config/gitee-release-cli-nodejs/config.json 读取
OWNER="browserwing"
REPO="browserwing"
TAG="v1.0.1"

# 创建 release（注意 prerelease: true 表示预发版）
BODY="release notes 内容..."
RELEASE_ID=$(curl -s -X POST "https://gitee.com/api/v5/repos/${OWNER}/${REPO}/releases" \
  -H "Content-Type: application/json" \
  -d "$(jq -n --arg tag "$TAG" --arg name "$TAG" --arg body "$BODY" --arg token "$GITEE_TOKEN" \
    '{access_token: $token, tag_name: $tag, name: $name, body: $body, prerelease: true, target_commitish: "main"}')" | jq -r '.id')

echo "Release ID: $RELEASE_ID"

# 上传资产文件
cd build/release
for f in *.tar.gz *.zip; do
  echo "Uploading $f ..."
  curl -s -X POST "https://gitee.com/api/v5/repos/${OWNER}/${REPO}/releases/${RELEASE_ID}/attach_files" \
    -F "access_token=${GITEE_TOKEN}" \
    -F "file=@${f}" | jq '{name: .name}'
done
```

## 7. 上传文件到 120 机器

```bash
scp build/release/*.tar.gz build/release/*.zip root@120.79.164.240:/tmp/browserwing-release/
```

> 如果 `/tmp/browserwing-release/` 目录不存在，先 `ssh root@120.79.164.240 "mkdir -p /tmp/browserwing-release"`。

## 8. 发布 GitHub Release（在 120 机器上）

本地无法直接推送到 GitHub（需要密码认证），需要通过 120 机器操作：

```bash
# 同步代码和 tag
ssh root@120.79.164.240 "cd /root/code/browserwing && git pull && git fetch --tags"

# 如果 tag 还没推送到 GitHub，在 120 机器上打 tag 并推送
ssh root@120.79.164.240 "cd /root/code/browserwing && git tag v1.0.1 2>/dev/null; git push origin v1.0.1"

# 创建 GitHub release 并上传资产（注意超时可能较长，耐心等待）
ssh root@120.79.164.240 'cd /root/code/browserwing && gh release create v1.0.1 \
  --prerelease \
  --title "v1.0.1" \
  --notes "release notes..." \
  /tmp/browserwing-release/*.tar.gz /tmp/browserwing-release/*.zip'
```

## 9. 发布 npm

在本地 Mac 上执行：

```bash
cd npm
npm publish --tag beta
```

> - beta 版必须加 `--tag beta`，否则 npm 会报错
> - 如果 token 过期，浏览器会弹出认证页面

验证安装：

```bash
npm install -g browserwing@beta
browserwing --version
```

## 快速检查清单

- [ ] `Makefile` VERSION 版本号已更新
- [ ] `npm/package.json` 版本号已更新
- [ ] `CHANGELOG.md` 已更新
- [ ] `git commit` + `git tag` 完成
- [ ] `make release` 构建成功（6 个包 + 压缩包）
- [ ] Gitee: 代码 + tag 已推送，release 已创建，资产已上传
- [ ] GitHub: tag 已推送（通过 120 机器），release 已创建，资产已上传
- [ ] npm: `npm publish --tag beta` 已执行
