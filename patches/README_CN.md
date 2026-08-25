# sub2api-overdraft Patch 说明

## Patch 信息

- 文件：`sub2api-overdraft-v0.1.177-baeac1f3d.patch`
- 上游项目：<https://github.com/Wei-Shaw/sub2api>
- Sub2API 基础版本：`0.1.177`
- Git 描述：`v0.1.177-1-gbaeac1f3d`
- 基础提交：`baeac1f3de21d37b129405f092ef86c24b3f203d`
- Patch 生成基准 Fork 提交：`b3cbc091c9057c8dc2f088db0c2b8c8905ac1d34`
- 目标 Git tree（不含 `patches/`）：`aa2a2d6c50cf372f87bf363e45de86460d60b821`
- SHA-256：`673764989fe37078843b2eceaacbe48fe27dae8e9579c2b7db92c0a1e18a5fa9`
- 文件大小：265,887 字节

该 Patch 包含 Codex 5h / 7d 额度透支后端、前端显示、配置、源码构建 Compose、品牌和公开部署文档，并额外包含以下修复：

- OpenAI OAuth 常规文本“测试账号连接”使用透支请求形态，并接入额度观察及明确额度 429 探测。
- `passed/recovered` 后清理并发 429 遗留的账号级限流状态，避免账号实际可用但页面仍显示“限流中”。
- API Key、Shadow、图片和 Compact 测试保持原行为。
- 用量达到 95% 后开始为普通 OAuth 文本请求注入；100% 时用真实业务成功直接确认 `passed`，明确额度 429 直接确认 `failed` 并切号冷却。
- 没有业务证据时，每个额度周期仅补充 1 次独立探测且不自动重试；普通瞬时 429、网络错误和 5xx 不会误判为透支结束，401/403 仍交给原认证异常逻辑处理。
- 同周期 `failed` 为终态，晚到的成功结果不能覆盖；并发 claim 冲突会重新读取数据库状态后完成业务失败收敛。
- 扩充结构化额度证据解析并记录逐次探测日志，便于区分明确额度耗尽与临时上游故障。
- `failed` 状态、账号暂停和 scheduler outbox 在同一个数据库事务内提交，并对持久化失败进行有限重试和同周期幂等保护。
- 更新检查跟踪 `DeanZFC/sub2api-overdraft` 的 `codex-overdraft` 分支和 `FORK_VERSION`，不再把官方 Release 误报为 Fork 更新。
- 源码构建只提示 `git pull` 和重新构建，禁用可能覆盖 Fork 功能的二进制在线更新与回退。
- 源码 Compose 构建固定从仓库 `FORK_VERSION` 读取版本，避免宿主机遗留的 `BUILD_VERSION` 把页面版本覆盖成分支名。
- 根目录、部署目录和后端开发 Dockerfile 统一使用 Go `1.26.6`，与 `backend/go.mod` 及 CI 约束保持一致。
- 兼容 Sub2API `v0.1.177` 的原生 remote compaction v2，旧 Compact 与原生 v2 均不会误启用透支调度。
- 将透支 SQL、业务钩子、协调器接线和前端展示迁入独立文件，移除 Fork 专用 Wire Provider；功能行为不变，但后续合并官方更新时的热点文件冲突面显著缩小。
- 新增对应后端单元测试和故障排查日志。

Patch 共修改 65 个源码和文档文件。`patches/` 目录本身不包含在 Patch 中，避免 Patch 递归包含自身。

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
aa2a2d6c50cf372f87bf363e45de86460d60b821
```

它与生成 Patch 时本地源码快照排除 `patches/` 后的 Git tree 完全一致；该快照基于 Fork 提交 `b3cbc091c9057c8dc2f088db0c2b8c8905ac1d34` 生成。

## 历史 Patch

- Sub2API `v0.1.176`：`sub2api-overdraft-v0.1.176-fbfdcef81.patch`

历史 Patch 仅用于对应的旧官方基线；新部署应使用本文顶部列出的 `v0.1.177` Patch。
