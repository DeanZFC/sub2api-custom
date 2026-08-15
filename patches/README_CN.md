# sub2api-overdraft Patch 说明

## Patch 信息

- 文件：`sub2api-overdraft-v0.1.177-baeac1f3d.patch`
- 上游项目：<https://github.com/Wei-Shaw/sub2api>
- Sub2API 基础版本：`0.1.177`
- Git 描述：`v0.1.177-1-gbaeac1f3d`
- 基础提交：`baeac1f3de21d37b129405f092ef86c24b3f203d`
- 目标源码提交：`f48d8ed7ed63cb7eee85306ae8f6f183a82eba91`
- 目标 Git tree（不含 `patches/`）：`9aca85e55803fa59698c3a57e5a632818d072754`
- SHA-256：`72076ac6ea09e9c5719c191dacec6e79e0b478309b8f92ae1d6deb1bb6e411de`
- 文件大小：202,451 字节

该 Patch 包含 Codex 5h / 7d 额度透支后端、前端显示、配置、源码构建 Compose、品牌和公开部署文档，并额外包含以下修复：

- OpenAI OAuth 常规文本“测试账号连接”使用透支请求形态，并接入额度观察及明确额度 429 探测。
- `passed/recovered` 后清理并发 429 遗留的账号级限流状态，避免账号实际可用但页面仍显示“限流中”。
- API Key、Shadow、图片和 Compact 测试保持原行为。
- 更新检查跟踪 `DeanZFC/sub2api-overdraft` 的 `codex-overdraft` 分支和 `FORK_VERSION`，不再把官方 Release 误报为 Fork 更新。
- 源码构建只提示 `git pull` 和重新构建，禁用可能覆盖 Fork 功能的二进制在线更新与回退。
- 兼容 Sub2API `v0.1.177` 的原生 remote compaction v2，旧 Compact 与原生 v2 均不会误启用透支调度。
- 新增对应后端单元测试和故障排查日志。

Patch 共修改 51 个源码和文档文件。`patches/` 目录本身不包含在 Patch 中，避免 Patch 递归包含自身。

## 在精确基线上应用

先把 Patch 下载到仓库外的临时位置：

```bash
curl -L \
  https://raw.githubusercontent.com/DeanZFC/sub2api-overdraft/codex-overdraft/patches/sub2api-overdraft-v0.1.177-baeac1f3d.patch \
  -o /tmp/sub2api-overdraft.patch
```

在 Sub2API 源码目录执行：

```bash
git switch -c codex-overdraft-patched \
  baeac1f3de21d37b129405f092ef86c24b3f203d
git apply --check /tmp/sub2api-overdraft.patch
git apply --3way /tmp/sub2api-overdraft.patch
```

`git apply --check` 没有输出且退出码为 `0`，表示可以干净应用。

## 应用到较新的官方版本

Patch 的精确基线是 `baeac1f3d`。应用到后续 Sub2API 版本时，先创建独立分支，再使用三方合并模式：

```bash
git switch -c codex-overdraft-reapply
git apply --3way /tmp/sub2api-overdraft.patch
```

如果上游修改了相同文件，Git 可能报告冲突。这时应按照根目录的 `CODEX_QUOTA_OVERDRAFT_CUSTOMIZATION.md` 逐项处理，并重新生成 Wire、运行后端和前端测试。不要对有运行数据或未提交修改的服务器目录直接强制应用。

## 完整性验证

本 Patch 已在临时工作树中从基础提交执行 `git apply --check` 和三方应用。应用后写入的 Git tree 为：

```text
9aca85e55803fa59698c3a57e5a632818d072754
```

它与目标源码提交 `f48d8ed7ed63cb7eee85306ae8f6f183a82eba91` 排除 `patches/` 后的 Git tree 完全一致。

## 历史 Patch

- Sub2API `v0.1.176`：`sub2api-overdraft-v0.1.176-fbfdcef81.patch`

历史 Patch 仅用于对应的旧官方基线；新部署应使用本文顶部列出的 `v0.1.177` Patch。
